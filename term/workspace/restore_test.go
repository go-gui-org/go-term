package workspace

// Save → Restore round-trips against real Workspaces, plus the degradation
// paths. persist_test.go covers snapshot/marshalling in isolation; these
// drive restoreWorkspace and newTabFromPersisted, which spawn panes and are
// where a corrupt or unexpected file turns into lost work rather than a
// visible error.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// shapeOf renders a split tree as a parenthesised string — "(V a (H b c))" —
// so two trees can be compared structurally without depending on leaf IDs,
// which restore deliberately regenerates.
func shapeOf(n *splitNode) string {
	if n == nil {
		return "."
	}
	if n.isLeaf() {
		return "L"
	}
	d := "V"
	if n.Dir == SplitHorizontal {
		d = "H"
	}
	return "(" + d + " " + shapeOf(n.First) + " " + shapeOf(n.Second) + ")"
}

// A workspace with several tabs and a nested split restores to the same
// structure: same tab count, same tree shape per tab, same active tab.
func TestSaveRestore_PreservesStructure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")

	ws := newLiveWorkspace(t)
	ws.SplitPane(false) // tab 0: two panes side by side
	ws.SplitPane(true)  // tab 0: nested split in the focused pane
	ws.AddTab()         // tab 1: single pane
	wantTabs := len(ws.tabs)
	wantActive := ws.activeTab
	wantShapes := make([]string, 0, len(ws.tabs))
	for _, tab := range ws.tabs {
		wantShapes = append(wantShapes, shapeOf(tab.root))
	}

	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	if len(restored.tabs) != wantTabs {
		t.Fatalf("tabs = %d, want %d", len(restored.tabs), wantTabs)
	}
	if restored.activeTab != wantActive {
		t.Errorf("activeTab = %d, want %d", restored.activeTab, wantActive)
	}
	for i, tab := range restored.tabs {
		if got := shapeOf(tab.root); got != wantShapes[i] {
			t.Errorf("tab %d shape = %s, want %s", i, got, wantShapes[i])
		}
	}
}

// Every restored leaf must have a live Term behind it. A tree that restores
// structurally but leaves panes unbacked would render empty rectangles.
func TestSaveRestore_EveryLeafHasATerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")

	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	for i, tab := range restored.tabs {
		leaves := leavesOf(tab.root)
		for _, id := range leaves {
			if _, ok := tab.terms[id]; !ok {
				t.Errorf("tab %d: leaf %q has no Term", i, id)
			}
		}
		if len(tab.terms) != len(leaves) {
			t.Errorf("tab %d: %d Terms for %d leaves", i, len(tab.terms), len(leaves))
		}
		if _, ok := tab.terms[tab.focused]; !ok {
			t.Errorf("tab %d: focused leaf %q has no Term", i, tab.focused)
		}
	}
}

// The focused pane survives a round trip. Leaf IDs are regenerated, so this
// exercises the persisted-ID → new-ID mapping rather than a literal match.
func TestSaveRestore_PreservesFocusedPanePosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")

	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	tab := activeTabOf(t, ws)
	// Focus the first leaf, which is not the one SplitPane just focused.
	want := leavesOf(tab.root)[0]
	ws.FocusPane(want)
	wantIdx := 0
	for i, id := range leavesOf(tab.root) {
		if id == tab.focused {
			wantIdx = i
		}
	}

	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restored, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	rt := restored.tabs[restored.activeTab]
	gotIdx := -1
	for i, id := range leavesOf(rt.root) {
		if id == rt.focused {
			gotIdx = i
		}
	}
	if gotIdx != wantIdx {
		t.Errorf("focused leaf at index %d, want %d", gotIdx, wantIdx)
	}
}

// Restore must degrade to a working workspace rather than failing or
// panicking when the file on disk is unusable. Each case is a file a user
// could plausibly end up with: truncated by a crash, hand-edited, or from a
// future version.
func TestRestore_DegradesGracefully(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty file", ""},
		{"truncated json", `{"version":1,"tabs":[{"root":{`},
		{"not json at all", "this is not json"},
		{"null tabs", `{"version":1,"activeTab":0,"tabs":null}`},
		{"empty tab list", `{"version":1,"activeTab":0,"tabs":[]}`},
		{"active tab out of range", `{"version":1,"activeTab":99,"tabs":[{"activeLeaf":"x","root":{"leafID":"x"}}]}`},
		{"negative active tab", `{"version":1,"activeTab":-5,"tabs":[{"activeLeaf":"x","root":{"leafID":"x"}}]}`},
		{"malformed split node", `{"version":1,"activeTab":0,"tabs":[{"activeLeaf":"x","root":{"ratio":0.5,"first":null,"second":null,"leafID":""}}]}`},
		{"unknown future version", `{"version":9999,"activeTab":0,"tabs":[{"activeLeaf":"x","root":{"leafID":"x"}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "workspace.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			ws, err := Restore(&gui.Window{}, hermeticCfg(t), path)
			if err != nil {
				t.Fatalf("Restore returned an error instead of degrading: %v", err)
			}
			t.Cleanup(func() { _ = ws.Close() })

			// Whatever the input, the result must be usable: at least one
			// tab, a valid active index, and a live focused pane.
			if len(ws.tabs) == 0 {
				t.Fatal("no tabs after restore")
			}
			if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
				t.Errorf("activeTab %d out of range (%d tabs)", ws.activeTab, len(ws.tabs))
			}
			if ws.ActivePane() == nil {
				t.Error("ActivePane = nil")
			}
		})
	}
}

// A missing file is the first-run case, not an error.
func TestRestore_MissingFileStartsFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	ws, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = ws.Close() })

	if len(ws.tabs) != 1 {
		t.Errorf("tabs = %d, want 1 fresh tab", len(ws.tabs))
	}
	if ws.ActivePane() == nil {
		t.Error("ActivePane = nil")
	}
}

// Save writes a file that Restore accepts — guards against a serialization
// change that silently produces something unreadable.
func TestSave_ProducesRestorableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "workspace.json")

	ws := newLiveWorkspace(t)
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file: %v", err)
	}

	restored, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore of freshly saved file: %v", err)
	}
	_ = restored.Close()
}

// ToggleRecording starts a recording on the focused pane and stops it again,
// leaving a file behind.
func TestToggleRecording_StartsAndStops(t *testing.T) {
	dir := t.TempDir()
	cfg := hermeticCfg(t)
	cfg.RecordDir = dir
	ws := newLiveWorkspaceCfg(t, cfg)

	pane := ws.ActivePane()
	if pane == nil {
		t.Fatal("no active pane")
	}
	if pane.Recording() {
		t.Fatal("pane is recording before ToggleRecording")
	}

	ws.ToggleRecording()
	if !pane.Recording() {
		t.Fatal("pane is not recording after ToggleRecording")
	}

	ws.ToggleRecording()
	if pane.Recording() {
		t.Error("pane still recording after second ToggleRecording")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var found bool
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".gtr" {
			found = true
		}
	}
	if !found {
		t.Errorf("no .gtr file in %s; got %v", dir, entries)
	}
}

// Recording is per-pane: toggling records the focused pane only, so a split
// workspace does not start writing every pane's bytes into one file.
func TestToggleRecording_OnlyFocusedPane(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.RecordDir = t.TempDir()
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.SplitPane(false)
	tab := activeTabOf(t, ws)

	ws.ToggleRecording()

	recording := 0
	for _, tm := range tab.terms {
		if tm.Recording() {
			recording++
		}
	}
	if recording != 1 {
		t.Errorf("%d panes recording, want exactly 1", recording)
	}
	if !ws.ActivePane().Recording() {
		t.Error("the focused pane is not the one recording")
	}
}

// CycleTheme advances to the next configured theme and applies it to every
// pane, including panes in other tabs.
func TestCycleTheme_AppliesToAllPanes(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: term.DraculaTheme},
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.SplitPane(false)
	ws.AddTab()

	before := ws.ActivePane().Theme()
	ws.CycleTheme()
	after := ws.ActivePane().Theme()

	if before == after {
		t.Fatal("CycleTheme did not change the active pane's theme")
	}
	for i, tab := range ws.tabs {
		for id, tm := range tab.terms {
			if tm.Theme() != after {
				t.Errorf("tab %d pane %s kept the old theme", i, id)
			}
		}
	}
}

// With no themes configured there is nothing to cycle to; CycleTheme must
// not panic or leave the workspace in a strange state.
func TestCycleTheme_NoThemesIsNoOp(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, hermeticCfg(t)) // Themes unset
	before := ws.ActivePane().Theme()

	ws.CycleTheme()

	if ws.ActivePane().Theme() != before {
		t.Error("theme changed despite no configured themes")
	}
}

// themePickerCfg configures three themes so wrap-around in both directions
// is distinguishable from a simple clamp.
func themePickerCfg(t *testing.T) Cfg {
	t.Helper()
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: term.DraculaTheme},
		{Name: "Solarized", Theme: term.SolarizedDarkTheme},
	}
	return cfg
}

// The picker opens on the active theme, and arrow navigation wraps at both
// ends rather than sticking.
func TestThemePicker_NavigationWraps(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themePickerCfg(t))
	n := len(ws.cfg.Themes)

	ws.ToggleThemePicker()
	if !ws.themePickerVisible {
		t.Fatal("picker not visible after toggle")
	}
	if ws.themePickerIdx < 0 || ws.themePickerIdx >= n {
		t.Fatalf("initial idx %d out of range", ws.themePickerIdx)
	}

	start := ws.themePickerIdx
	for i := 0; i < n; i++ {
		ws.themePickerMoveDown()
	}
	if ws.themePickerIdx != start {
		t.Errorf("after %d moves down idx = %d, want wrap to %d", n, ws.themePickerIdx, start)
	}

	for i := 0; i < n; i++ {
		ws.themePickerMoveUp()
	}
	if ws.themePickerIdx != start {
		t.Errorf("after %d moves up idx = %d, want wrap to %d", n, ws.themePickerIdx, start)
	}

	// Every intermediate position must stay in range — an out-of-range index
	// would panic the picker's view builder on the next frame.
	for i := 0; i < 2*n+1; i++ {
		ws.themePickerMoveUp()
		if ws.themePickerIdx < 0 || ws.themePickerIdx >= n {
			t.Fatalf("idx %d out of range after move up #%d", ws.themePickerIdx, i)
		}
	}
}

// Navigating the picker previews the highlighted theme on the live panes.
func TestThemePicker_NavigationPreviewsTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themePickerCfg(t))
	ws.ToggleThemePicker()

	before := ws.ActivePane().Theme()
	ws.themePickerMoveDown()
	if ws.ActivePane().Theme() == before {
		t.Error("moving the highlight did not preview the new theme")
	}
}

// Confirm dismisses the picker and keeps the previewed theme.
func TestThemePicker_ConfirmKeepsPreviewedTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themePickerCfg(t))
	ws.ToggleThemePicker()
	ws.themePickerMoveDown()
	previewed := ws.ActivePane().Theme()

	ws.themePickerConfirm()

	if ws.themePickerVisible {
		t.Error("picker still visible after confirm")
	}
	if got := ws.ActivePane().Theme(); got != previewed {
		t.Error("confirm reverted the previewed theme")
	}
}

// The picker is a no-op with no themes — there is nothing to pick.
func TestThemePicker_NoThemesStaysHidden(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, hermeticCfg(t))

	ws.ToggleThemePicker()

	if ws.themePickerVisible {
		t.Error("picker opened with no configured themes")
	}
}

// Window focus events reach the active pane, and a nil event is ignored
// rather than panicking.
func TestOnWindowEvent_RoutesAndTolerates(t *testing.T) {
	ws := newLiveWorkspace(t)

	// Must not panic on any of these.
	ws.onWindowEvent(nil, ws.w)
	ws.onWindowEvent(&gui.Event{Type: gui.EventFocused}, ws.w)
	ws.onWindowEvent(&gui.Event{Type: gui.EventUnfocused}, ws.w)
	ws.onWindowEvent(&gui.Event{Type: gui.EventResized}, ws.w)

	if ws.ActivePane() == nil {
		t.Error("active pane lost after window events")
	}
}

// onWindowEvent chains to the handler that was installed before the
// workspace took over, so an embedder's own handler keeps running.
func TestOnWindowEvent_ChainsToPrevious(t *testing.T) {
	ws := newLiveWorkspace(t)
	var called int
	ws.prevOnEvent = func(e *gui.Event, w *gui.Window) { called++ }

	ws.onWindowEvent(&gui.Event{Type: gui.EventFocused}, ws.w)
	ws.onWindowEvent(&gui.Event{Type: gui.EventResized}, ws.w)

	if called != 2 {
		t.Errorf("previous handler called %d times, want 2", called)
	}
}

// A pane's OSC title update must not disturb workspace state.
func TestOnPaneTitle_KeepsStateIntact(t *testing.T) {
	ws := newLiveWorkspace(t)
	tab := activeTabOf(t, ws)
	focused := tab.focused

	ws.onPaneTitle(focused, "some title")

	if tab.focused != focused {
		t.Errorf("focus moved to %q on title update", tab.focused)
	}
	if ws.ActivePane() == nil {
		t.Error("ActivePane = nil after title update")
	}
}

// Broadcast is transient state, never persisted: a workspace restored hours
// later must not start silently typing into every pane.
func TestSaveRestore_BroadcastStartsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")

	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	if !ws.Broadcasting() {
		t.Fatal("precondition: broadcast should be on before saving")
	}
	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored, err := Restore(&gui.Window{}, hermeticCfg(t), path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	for i, tab := range restored.tabs {
		if tab.broadcast {
			t.Errorf("restored tab %d came back broadcasting", i)
		}
	}
}
