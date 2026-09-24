package workspace

// Tests for the pane and tab lifecycle — the paths that build, focus, and
// tear down real Terms. The rest of the package's tests assemble Workspace
// literals with empty tab values, which covers the pure tree and config
// logic but leaves every user-facing command untested. These drive the real
// thing: workspace.New against a zero-value Window spawns actual shells
// headlessly, so nothing here needs a display.

import (
	"errors"
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
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
	var hookSnap persistedWorkspace
	calls := 0
	var ws *Workspace
	cfg.OnLastShellExit = func(w *gui.Window) {
		calls++
		hookWindow = w
		// What falcon's saveAndClose does: Save, then close.
		hookSnap = ws.snapshot()
		w.Close()
	}
	ws = newLiveWorkspaceCfg(t, cfg)

	tab := activeTabOf(t, ws)
	leaf := tab.focused
	ws.onPaneExit(leaf)

	if calls != 1 {
		t.Fatalf("OnLastShellExit calls = %d, want 1", calls)
	}
	if hookWindow != ws.w {
		t.Error("hook got a different window than the workspace's")
	}
	// A Save from the hook records the exiting pane's real tab (typing
	// "exit" must restore like Cmd+Q, not start fresh).
	if len(hookSnap.Tabs) != 1 || hookSnap.Tabs[0].Root.LeafID != leaf {
		t.Errorf("snapshot in hook = %+v, want the one live tab (leaf %q)", hookSnap.Tabs, leaf)
	}
	// The tab stays until Workspace.Close: the window closes a frame later
	// and commands queued for this frame still index the active tab.
	if len(ws.tabs) != 1 || ws.tabs[0] != tab {
		t.Errorf("tabs = %d after last shell exit, want the exiting tab kept", len(ws.tabs))
	}
}

// The exiting pane's Term is released as soon as the hook has run, not at
// Workspace.Close: its pty, capture tee and session recording must not stay
// open for the rest of the frame (or forever, for an embedder that never
// calls Workspace.Close). The shell here is still running — onPaneExit is
// called directly, as Cmd+W on a live last pane would — so Alive turning
// false proves Close ran and sent the hangup.
func TestOnPaneExit_LastShellReleasesDeadTerm(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	cfg.OnLastShellExit = func(w *gui.Window) { w.Close() }
	ws := newLiveWorkspaceCfg(t, cfg)

	tab := activeTabOf(t, ws)
	tm := tab.terms[tab.focused]
	ws.onPaneExit(tab.focused)

	if tm.Alive() {
		t.Error("exiting pane's Term still open after the window close was requested")
	}
	// Still in the map: the View and a Save read it until teardown.
	if tab.terms[tab.focused] != tm {
		t.Error("exiting pane's Term removed from its tab")
	}
}

// A tab the exit hook opens (a "keep the window open?" flow) must stay in the
// workspace, alive and active, and the dead tab must go. Keeping the dead tab
// left the window stuck on a frozen pane once the new tab's shell exited: the
// dead pane never exits again, so the last-shell path never fired.
func TestOnPaneExit_LastShellKeepsTabAddedByHook(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	var ws *Workspace
	var added *tab
	cfg.OnLastShellExit = func(*gui.Window) {
		var err error
		if added, err = ws.addTabIn(""); err != nil {
			t.Fatalf("addTabIn from hook: %v", err)
		}
	}
	ws = newLiveWorkspaceCfg(t, cfg)

	dead := activeTabOf(t, ws)
	deadTerm := dead.terms[dead.focused]
	ws.onPaneExit(dead.focused)

	if len(ws.tabs) != 1 || ws.tabs[0] != added {
		t.Fatalf("tabs = %d after hook added one, want only the added tab", len(ws.tabs))
	}
	if activeTabOf(t, ws) != added {
		t.Error("tab added by the exit hook is not active")
	}
	if tm := added.terms[added.focused]; tm == nil || !tm.Alive() {
		t.Error("tab added by the exit hook lost its live pane")
	}
	if deadTerm.Alive() {
		t.Error("dead tab's Term not closed")
	}
}

// A hook that keeps the window open without adding a tab (a cancelled "quit?"
// dialog) must not leave the window on a dead pane: the workspace falls back
// to the no-hook-close behavior and replaces the tab with a fresh shell.
func TestOnPaneExit_LastShellHookDeclinesClose(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	cfg.OnLastShellExit = func(*gui.Window) {}
	ws := newLiveWorkspaceCfg(t, cfg)

	dead := activeTabOf(t, ws)
	ws.onPaneExit(dead.focused)

	if len(ws.tabs) != 1 || ws.tabs[0] == dead {
		t.Fatalf("tabs = %d, want one fresh tab in place of the dead one", len(ws.tabs))
	}
	tab := activeTabOf(t, ws)
	if tm := tab.terms[tab.focused]; tm == nil || !tm.Alive() {
		t.Error("replacement tab has no live pane")
	}
}

// gui.Window.Close only raises a flag, so commands queued in the same frame as
// the exit still run. They must not panic, must not run the exit hook a second
// time (falcon would Save twice), and must not spawn shells into a window that
// is closing.
func TestOnPaneExit_LastShellCommandsInSameFrame(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	calls := 0
	cfg.OnLastShellExit = func(w *gui.Window) {
		calls++
		w.Close()
	}
	ws := newLiveWorkspaceCfg(t, cfg)

	tab := activeTabOf(t, ws)
	ws.onPaneExit(tab.focused)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("command after last shell exit panicked: %v", r)
		}
	}()
	ws.nextPane()
	ws.splitPane(false)
	ws.addTab()
	ws.closePane()
	ws.closeTab()

	if calls != 1 {
		t.Errorf("OnLastShellExit calls = %d, want 1", calls)
	}
	if len(ws.tabs) != 1 || len(tab.terms) != 1 {
		t.Errorf("tabs = %d, panes = %d after same-frame commands, want 1 and 1",
			len(ws.tabs), len(tab.terms))
	}
}

// removeTab's failure path (the replacement tab cannot spawn) closes the
// window with an empty tab list. Commands queued for the same frame must
// not index it.
func TestCommands_EmptyWorkspaceNoPanic(t *testing.T) {
	ws := &Workspace{w: &gui.Window{}, activeTab: -1}
	ws.w.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("command on empty workspace panicked: %v", r)
		}
	}()
	ws.nextPane()
	ws.prevPane()
	ws.splitPane(true)
	ws.closePane()
	ws.closeTab()
	ws.addTab()
	ws.resizeActivePane(resizeLeft)
	if len(ws.tabs) != 0 {
		t.Errorf("tabs = %d, want none spawned into a closing window", len(ws.tabs))
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

// Closing a tab to the left of the active one shifts the active tab down one
// slot. removeTab must follow it: keeping the old index made the tab to its
// right active while the real active tab's pane still believed it had focus.
func TestCloseTabAt_LeftOfActiveKeepsActiveTab(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.addTab()
	ws.addTab()
	ws.goToTab(1)
	active := activeTabOf(t, ws)

	ws.closeTabAt(0)

	if got := activeTabOf(t, ws); got != active {
		t.Fatalf("active tab changed after closing a tab to its left (got index %d)", ws.activeTab)
	}
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
}

// recordFocus replaces setTermFocused for one test and returns the focus state
// the workspace last set on each Term.
func recordFocus(t *testing.T) map[*term.Term]bool {
	t.Helper()
	got := make(map[*term.Term]bool)
	orig := setTermFocused
	setTermFocused = func(tm *term.Term, v bool) {
		got[tm] = v
		orig(tm, v)
	}
	t.Cleanup(func() { setTermFocused = orig })
	return got
}

// failSpawns makes every later pane spawn fail, the way a missing shell or an
// exhausted pty pool would.
func failSpawns(t *testing.T) {
	t.Helper()
	orig := newTerm
	newTerm = func(*gui.Window, term.Cfg) (*term.Term, error) {
		return nil, errors.New("spawn failed")
	}
	t.Cleanup(func() { newTerm = orig })
}

// A new tab whose shell cannot start must leave the current pane focused.
// addTab used to unfocus it before the spawn, so a failure left no pane
// taking keys.
func TestAddTab_SpawnFailureKeepsFocus(t *testing.T) {
	ws := newLiveWorkspace(t)
	tab := activeTabOf(t, ws)
	pane := tab.terms[tab.focused]
	focus := recordFocus(t)
	failSpawns(t)

	ws.addTab()

	if len(ws.tabs) != 1 {
		t.Fatalf("tabs = %d, want 1 after a failed spawn", len(ws.tabs))
	}
	if v, set := focus[pane]; set && !v {
		t.Error("addTab unfocused the current pane although no tab was added")
	}
}

// Same for a split: a failed spawn must not leave the source pane unfocused.
func TestSplitPane_SpawnFailureKeepsFocus(t *testing.T) {
	ws := newLiveWorkspace(t)
	tab := activeTabOf(t, ws)
	pane := tab.terms[tab.focused]
	focus := recordFocus(t)
	failSpawns(t)

	ws.splitPane(false)

	if !tab.root.isLeaf() {
		t.Fatal("split tree changed after a failed spawn")
	}
	if v, set := focus[pane]; set && !v {
		t.Error("splitPane unfocused the source pane although no pane was added")
	}
}

// A pane whose shell exits after the window close was requested keeps its
// place in the tree (the View and a Save still read it), but its Term must be
// released right away: its pty, capture tee and recording would otherwise stay
// open until Workspace.Close, which an embedder is not required to call.
func TestOnPaneExit_AfterCloseRequestedReleasesTerm(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.splitPane(false)
	tab := activeTabOf(t, ws)
	leaf := tab.focused
	tm := tab.terms[leaf]

	ws.w.Close()
	ws.onPaneExit(leaf)

	if tm.Alive() {
		t.Error("exited pane's Term still open after the window close was requested")
	}
	if tab.terms[leaf] != tm {
		t.Error("exited pane removed from its tab during teardown")
	}
}

// An exit hook that asks "quit?" with an in-app dialog returns before the user
// answers. The workspace must not decide in the meantime: replacing the dead
// tab with a fresh shell behind the dialog made a Save from its Yes button
// record that shell instead of the real tab.
func TestOnPaneExit_LastShellWaitsForHookDialog(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.ExitWhenLastShellExits = true
	cfg.OnLastShellExit = func(w *gui.Window) {
		w.Dialog(gui.DialogCfg{DialogType: gui.DialogConfirm, Title: "Quit?"})
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	dead := activeTabOf(t, ws)
	leaf := dead.focused
	deadTerm := dead.terms[leaf]

	ws.onPaneExit(leaf)

	if len(ws.tabs) != 1 || ws.tabs[0] != dead {
		t.Fatal("dead tab replaced while the hook's dialog is still open")
	}
	ws.View(ws.w)
	if ws.pendingExit == nil {
		t.Fatal("exit settled while the hook's dialog is still open")
	}

	// The user answers Yes: the dialog goes and the window closes.
	ws.w.DialogDismiss()
	ws.w.Close()
	ws.View(ws.w)
	if ws.pendingExit != nil {
		t.Fatal("View did not pick up the exit once the dialog closed")
	}
	// The queued settle step, as the next frame runs it.
	ws.finishLastShellExit(dead, leaf)

	if len(ws.tabs) != 1 || ws.tabs[0] != dead {
		t.Error("dead tab replaced although the window is closing")
	}
	if deadTerm.Alive() {
		t.Error("dead pane's Term not released once the window closed")
	}
}
