package workspace

// Tests for the pane and tab lifecycle — the paths that build, focus, and
// tear down real Terms. The rest of the package's tests assemble Workspace
// literals with empty tab values, which covers the pure tree and config
// logic but leaves every user-facing command untested. These drive the real
// thing: workspace.New against a zero-value Window spawns actual shells
// headlessly, so nothing here needs a display.

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// hermeticCfg points ConfigPath at a file that does not exist inside the
// test's temp dir. Without it New falls back to the real user config, so a
// developer with ~/.config/go-term/config would run different tests than CI.
func hermeticCfg(t *testing.T) Cfg {
	t.Helper()
	return Cfg{ConfigPath: filepath.Join(t.TempDir(), "absent.config")}
}

// newLiveWorkspace builds a Workspace with one shell-backed pane and
// guarantees teardown. Each pane is a real child process, so tests should
// stay modest about how many they create.
func newLiveWorkspace(t *testing.T) *Workspace {
	t.Helper()
	return newLiveWorkspaceCfg(t, hermeticCfg(t))
}

// newLiveWorkspaceCfg is newLiveWorkspace with caller-supplied settings.
func newLiveWorkspaceCfg(t *testing.T, cfg Cfg) *Workspace {
	t.Helper()
	ws, err := New(&gui.Window{}, cfg)
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

// leavesOf collects leaf IDs depth-first, for structural assertions about
// the split tree.
func leavesOf(n *splitNode) []string {
	if n == nil {
		return nil
	}
	if n.isLeaf() {
		return []string{n.LeafID}
	}
	return append(leavesOf(n.First), leavesOf(n.Second)...)
}

// activeTabOf returns the active tab, failing the test if the index is out
// of range — a bad index is a bug worth reporting precisely rather than a
// panic in the assertion itself.
func activeTabOf(t *testing.T, ws *Workspace) *tab {
	t.Helper()
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
		t.Fatalf("activeTab %d out of range (%d tabs)", ws.activeTab, len(ws.tabs))
	}
	return ws.tabs[ws.activeTab]
}

// A fresh workspace has exactly one tab holding one focused, live pane.
func TestNew_SingleTabSinglePane(t *testing.T) {
	ws := newLiveWorkspace(t)

	if len(ws.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(ws.tabs))
	}
	tab := activeTabOf(t, ws)
	if got := leavesOf(tab.root); len(got) != 1 {
		t.Errorf("leaves = %v, want exactly 1", got)
	}
	if tab.focused == "" {
		t.Error("no focused leaf")
	}
	if _, ok := tab.terms[tab.focused]; !ok {
		t.Errorf("focused leaf %q has no Term", tab.focused)
	}
	if n := ws.LiveTermCount(); n != 1 {
		t.Errorf("LiveTermCount = %d, want 1", n)
	}
	if ws.ActivePane() == nil {
		t.Error("ActivePane = nil")
	}
}

// splitPane adds a second leaf to the active tab and moves focus to it.
func TestSplitPane_AddsLeafAndFocusesIt(t *testing.T) {
	ws := newLiveWorkspace(t)
	tab := activeTabOf(t, ws)
	before := tab.focused

	ws.splitPane(false)

	leaves := leavesOf(tab.root)
	if len(leaves) != 2 {
		t.Fatalf("leaves = %v, want 2", leaves)
	}
	if tab.focused == before {
		t.Errorf("focus stayed on %q; the new pane should take focus", before)
	}
	if _, ok := tab.terms[tab.focused]; !ok {
		t.Errorf("focused leaf %q has no Term", tab.focused)
	}
	if n := ws.LiveTermCount(); n != 2 {
		t.Errorf("LiveTermCount = %d, want 2", n)
	}
}

// Splitting horizontally and vertically both work and are distinguishable in
// the resulting tree.
func TestSplitPane_Direction(t *testing.T) {
	for _, tc := range []struct {
		name       string
		horizontal bool
		want       splitDir
	}{
		{"vertical", false, splitVertical},
		{"horizontal", true, splitHorizontal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newLiveWorkspace(t)
			tab := activeTabOf(t, ws)

			ws.splitPane(tc.horizontal)

			if tab.root.isLeaf() {
				t.Fatal("root is still a leaf after split")
			}
			if tab.root.Dir != tc.want {
				t.Errorf("Dir = %v, want %v", tab.root.Dir, tc.want)
			}
		})
	}
}

// nextPane and prevPane cycle focus across the tab's leaves and wrap.
func TestNextPrevPane_CyclesAndWraps(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)

	leaves := leavesOf(tab.root)
	if len(leaves) != 2 {
		t.Fatalf("setup: leaves = %v, want 2", leaves)
	}
	start := tab.focused

	ws.nextPane()
	if tab.focused == start {
		t.Fatalf("nextPane did not move focus off %q", start)
	}
	ws.nextPane()
	if tab.focused != start {
		t.Errorf("nextPane twice = %q, want wrap back to %q", tab.focused, start)
	}

	ws.prevPane()
	if tab.focused == start {
		t.Fatalf("prevPane did not move focus off %q", start)
	}
	ws.prevPane()
	if tab.focused != start {
		t.Errorf("prevPane twice = %q, want wrap back to %q", tab.focused, start)
	}
}

// focusPane targets a leaf by ID, wherever it lives, and ignores unknown IDs.
func TestFocusPane_ByID(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)

	leaves := leavesOf(tab.root)
	target := leaves[0]
	if tab.focused == target {
		target = leaves[1]
	}

	ws.focusPane(target)
	if tab.focused != target {
		t.Errorf("focused = %q, want %q", tab.focused, target)
	}

	// An unknown ID must not change focus or panic.
	ws.focusPane("no-such-leaf")
	if tab.focused != target {
		t.Errorf("unknown ID changed focus to %q", tab.focused)
	}
}

// Closing one pane of a split leaves the survivor focused and collapses the
// tree back to a single leaf.
func TestClosePane_CollapsesToSurvivor(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)

	leaves := leavesOf(tab.root)
	closing := tab.focused
	var survivor string
	for _, id := range leaves {
		if id != closing {
			survivor = id
		}
	}

	ws.closePane()

	if got := leavesOf(tab.root); len(got) != 1 || got[0] != survivor {
		t.Errorf("leaves = %v, want exactly [%s]", got, survivor)
	}
	if tab.focused != survivor {
		t.Errorf("focused = %q, want survivor %q", tab.focused, survivor)
	}
	if _, ok := tab.terms[closing]; ok {
		t.Errorf("closed pane %q still has a Term", closing)
	}
	if n := ws.LiveTermCount(); n != 1 {
		t.Errorf("LiveTermCount = %d, want 1", n)
	}
}

// addTab appends a tab, makes it active, and gives it its own live pane.
func TestAddTab_AppendsAndActivates(t *testing.T) {
	ws := newLiveWorkspace(t)
	firstTab := ws.tabs[0]

	ws.addTab()

	if len(ws.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2", len(ws.tabs))
	}
	if ws.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1", ws.activeTab)
	}
	newTab := activeTabOf(t, ws)
	if newTab == firstTab {
		t.Fatal("addTab reused the existing tab")
	}
	if newTab.id == firstTab.id {
		t.Errorf("tab IDs collide: %q", newTab.id)
	}
	if _, ok := newTab.terms[newTab.focused]; !ok {
		t.Errorf("new tab's focused leaf %q has no Term", newTab.focused)
	}
	if n := ws.LiveTermCount(); n != 2 {
		t.Errorf("LiveTermCount = %d, want 2", n)
	}
}

// Closing a tab when others remain drops it and keeps a valid active index.
func TestCloseTab_WithOthersRemaining(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.addTab()
	closing := activeTabOf(t, ws)

	ws.closeTab()

	if len(ws.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1", len(ws.tabs))
	}
	if ws.tabs[0] == closing {
		t.Error("the closed tab is still present")
	}
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
	if ws.ActivePane() == nil {
		t.Error("ActivePane = nil after closing a tab")
	}
}

// Closing the only tab replaces it with a fresh one rather than leaving the
// workspace empty — otherwise the window would be left with nothing to show.
func TestCloseTab_LastTabReplacedWithFresh(t *testing.T) {
	ws := newLiveWorkspace(t)
	original := ws.tabs[0]

	ws.closeTab()

	if len(ws.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1 (a replacement)", len(ws.tabs))
	}
	if ws.tabs[0] == original {
		t.Error("the original tab survived; want a fresh replacement")
	}
	if ws.ActivePane() == nil {
		t.Error("replacement tab has no active pane")
	}
}

// A pane whose shell exits is reaped through onPaneExit, which collapses the
// split exactly like an explicit close.
func TestOnPaneExit_CollapsesSplit(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)

	leaves := leavesOf(tab.root)
	exiting := leaves[0]
	survivor := leaves[1]

	ws.onPaneExit(exiting)

	if got := leavesOf(tab.root); len(got) != 1 || got[0] != survivor {
		t.Errorf("leaves = %v, want exactly [%s]", got, survivor)
	}
	if _, ok := tab.terms[exiting]; ok {
		t.Errorf("exited pane %q still has a Term", exiting)
	}
}

// onPaneExit for an unknown leaf is a no-op, not a panic — a late callback
// from an already-reaped pane must not take the workspace down.
func TestOnPaneExit_UnknownLeafIsNoOp(t *testing.T) {
	ws := newLiveWorkspace(t)
	before := len(ws.tabs)

	ws.onPaneExit("no-such-leaf")

	if len(ws.tabs) != before {
		t.Errorf("tabs = %d, want unchanged %d", len(ws.tabs), before)
	}
}

// onPaneFocus routes a click in an unfocused pane to that pane.
func TestOnPaneFocus_MovesFocus(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)

	leaves := leavesOf(tab.root)
	target := leaves[0]
	if tab.focused == target {
		target = leaves[1]
	}

	ws.onPaneFocus(target)

	if tab.focused != target {
		t.Errorf("focused = %q, want %q", tab.focused, target)
	}
}

// Close tears down every pane across every tab.
func TestClose_ClosesAllPanes(t *testing.T) {
	// Not newLiveWorkspace: this test closes explicitly and asserts on the
	// result, so it must not also be closed by a Cleanup beforehand.
	ws, err := New(&gui.Window{}, hermeticCfg(t))
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	ws.splitPane(false)
	ws.addTab()
	if n := ws.LiveTermCount(); n != 3 {
		t.Fatalf("setup: LiveTermCount = %d, want 3", n)
	}

	if err := ws.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if n := ws.LiveTermCount(); n != 0 {
		t.Errorf("LiveTermCount = %d after Close, want 0", n)
	}
}

// Leaf IDs stay unique as panes come and go: allocLeafID must not hand out an
// ID that a live pane already holds, or the terms map would silently alias.
func TestAllocLeafID_StaysUniqueAcrossChurn(t *testing.T) {
	ws := newLiveWorkspace(t)
	tab := activeTabOf(t, ws)

	seen := map[string]bool{tab.focused: true}
	for i := 0; i < 3; i++ {
		ws.splitPane(i%2 == 0)
		for _, id := range leavesOf(tab.root) {
			seen[id] = true
		}
	}

	ids := leavesOf(tab.root)
	sort.Strings(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			t.Fatalf("duplicate leaf ID %q in %v", ids[i], ids)
		}
	}
	if len(ids) != 4 {
		t.Errorf("leaves = %v, want 4 after three splits", ids)
	}
}

// ExitWhenLastShellExits closes the window directly, which only raises
// gui.Window's close flag — OnCloseRequest never runs. An embedder that
// persists state on quit therefore needs OnLastShellExit; without it the
// workspace file would never be updated on this path.
func TestOnPaneExit_LastShellRunsExitHook(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	var hookWindow *gui.Window
	calls := 0
	cfg.OnLastShellExit = func(w *gui.Window) {
		calls++
		hookWindow = w
	}
	ws := newLiveWorkspaceCfg(t, cfg)

	tab := activeTabOf(t, ws)
	ws.onPaneExit(tab.focused)

	if calls != 1 {
		t.Fatalf("OnLastShellExit calls = %d, want 1", calls)
	}
	if hookWindow != ws.w {
		t.Error("hook got a different window than the workspace's")
	}
	// The hook owns the close, so the workspace must not have requested it.
	if ws.w.CloseRequested() {
		t.Error("window closed by the workspace despite the hook owning it")
	}
	// State is torn down before the hook runs, so a Save from inside it
	// records an empty session rather than a tab with a dead pane.
	if len(ws.tabs) != 0 {
		t.Errorf("tabs = %d after last shell exit, want 0", len(ws.tabs))
	}
	if snap := ws.snapshot(); len(snap.Tabs) != 0 {
		t.Errorf("snapshot tabs = %d, want 0", len(snap.Tabs))
	}
}

// With no hook installed the workspace still closes the window itself.
func TestOnPaneExit_LastShellClosesWindowWithoutHook(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	ws := newLiveWorkspaceCfg(t, cfg)

	tab := activeTabOf(t, ws)
	ws.onPaneExit(tab.focused)

	if !ws.w.CloseRequested() {
		t.Error("window not closed after the last shell exited")
	}
}
