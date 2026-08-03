package workspace

// Save → Restore round-trips against real Workspaces, plus the degradation
// paths. persist_test.go covers snapshot/marshalling in isolation; these
// drive restoreWorkspace and newTabFromPersisted, which spawn panes and are
// where a corrupt or unexpected file turns into lost work rather than a
// visible error.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// Applying a theme must reach every pane, including panes in tabs that are not
// focused — a pane whose theme is stale renders in the old palette the moment
// the user switches to it.
func TestApplyTheme_ReachesAllPanesInAllTabs(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.SplitPane(false)
	ws.AddTab()

	before := ws.ActivePane().Theme()
	ws.applyThemeImpl(ws.cfg.Themes[1])
	after := ws.ActivePane().Theme()

	if before == after {
		t.Fatal("applyThemeImpl did not change the active pane's theme")
	}
	for i, tab := range ws.tabs {
		for id, tm := range tab.terms {
			if tm.Theme() != after {
				t.Errorf("tab %d pane %s kept the old theme", i, id)
			}
		}
	}
}

// After a theme change, persistableThemeName must reflect the newly active
// theme so a workspace saved next writes the correct name into the file.
func TestApplyTheme_UpdatesPersistedThemeName(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
	}
	ws := newLiveWorkspaceCfg(t, cfg)

	ws.applyThemeImpl(ws.cfg.Themes[1])

	if name := ws.persistableThemeName(); name != "Dracula" {
		t.Errorf("persistableThemeName = %q, want Dracula", name)
	}
}

// SaveRestore_PreservesTheme writes a workspace with a non-default theme
// and verifies the restored workspace applies it to all panes.
func TestSaveRestore_PreservesTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.applyThemeImpl(ws.cfg.Themes[1]) // Dracula

	if err := ws.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"theme": "Dracula"`) {
		t.Errorf("snapshot JSON should include theme Dracula: %s", data)
	}

	restoreCfg := hermeticCfg(t)
	restoreCfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
	}
	restored, err := Restore(&gui.Window{}, restoreCfg, path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	if got := restored.ActivePane().Theme(); got != testTheme(t, "Dracula") {
		t.Errorf("restored theme = %+v, want Dracula", got)
	}
}

// Restore with a zero-tab workspace (last-shell-exit) still applies the
// persisted theme to its fresh single pane.
func TestRestore_ZeroTabAppliesTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	data := `{"version":1,"activeTab":0,"tabs":[],"theme":"Dracula"}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
	}
	restored, err := Restore(&gui.Window{}, cfg, path)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })

	if got := restored.ActivePane().Theme(); got != testTheme(t, "Dracula") {
		t.Errorf("zero-tab restored theme = %+v, want Dracula", got)
	}
	if name := restored.persistableThemeName(); name != "Dracula" {
		t.Errorf("zero-tab persistableThemeName = %q, want Dracula", name)
	}
}

// themeBrowserCfg configures three themes so wrap-around in both directions
// is distinguishable from a simple clamp.
func themeBrowserCfg(t *testing.T) Cfg {
	t.Helper()
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
		{Name: "Solarized", Theme: testTheme(t, "iTerm2 Solarized Dark")},
	}
	return cfg
}

// The browser opens on the active theme, and arrow navigation wraps at both
// ends rather than sticking.
func TestThemeBrowser_NavigationWraps(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	n := len(ws.cfg.Themes)

	ws.ToggleThemeBrowser()
	if !ws.browser.visible {
		t.Fatal("browser not visible after toggle")
	}
	if len(ws.browser.matches) != n {
		t.Fatalf("unfiltered matches = %d, want %d", len(ws.browser.matches), n)
	}

	start := ws.browser.idx
	for range n {
		ws.themeBrowserMove(1)
	}
	if ws.browser.idx != start {
		t.Errorf("after %d moves down idx = %d, want wrap to %d", n, ws.browser.idx, start)
	}

	for range n {
		ws.themeBrowserMove(-1)
	}
	if ws.browser.idx != start {
		t.Errorf("after %d moves up idx = %d, want wrap to %d", n, ws.browser.idx, start)
	}

	// Every intermediate position must stay in range — an out-of-range index
	// would panic the browser's view builder on the next frame.
	for i := range 2*n + 1 {
		ws.themeBrowserMove(-1)
		if ws.browser.idx < 0 || ws.browser.idx >= n {
			t.Fatalf("idx %d out of range after move up #%d", ws.browser.idx, i)
		}
	}
}

// Moving the cursor changes only the preview. The panes are not in the view
// tree while the browser is open, so applying to them per keystroke would emit
// mode-2031 reports and OnColorScheme calls for something nobody can see.
func TestThemeBrowser_NavigationDoesNotTouchPanes(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()

	before := ws.ActivePane().Theme()
	ws.themeBrowserMove(1)

	if got := ws.ActivePane().Theme(); got != before {
		t.Error("moving the cursor changed a live pane's theme before Enter")
	}
	if sel, ok := ws.browserSelected(); !ok || sel.Theme == before {
		t.Error("the cursor did not move to a different theme")
	}
}

// Enter applies the highlighted theme to the panes and closes the browser.
func TestThemeBrowser_ConfirmAppliesTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()
	ws.themeBrowserMove(1)
	want, _ := ws.browserSelected()

	ws.themeBrowserConfirm()

	if ws.browser.visible {
		t.Error("browser still visible after confirm")
	}
	if got := ws.ActivePane().Theme(); got != want.Theme {
		t.Error("confirm did not apply the highlighted theme")
	}
}

// Escape on an unfiltered browser reverts to whatever was active when it
// opened. This is the cancel the old picker never had: it applied on every
// arrow press, so Escape simply stopped and left the last previewed theme.
func TestThemeBrowser_EscapeRevertsAppliedTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))

	// Establish a non-default starting theme, so a revert to it is
	// distinguishable from "never changed anything".
	ws.ToggleThemeBrowser()
	ws.themeBrowserMove(1)
	ws.themeBrowserConfirm()
	settled := ws.ActivePane().Theme()

	// Reopen, commit something else, then cancel the same session. The
	// browser reopens on the settled theme, so one move lands elsewhere.
	ws.ToggleThemeBrowser()
	ws.themeBrowserMove(1)
	ws.themeBrowserConfirm()
	if ws.ActivePane().Theme() == settled {
		t.Fatal("the second confirm did not change the theme; test proves nothing")
	}

	ws.ToggleThemeBrowser()
	opened := ws.ActivePane().Theme()
	ws.themeBrowserMove(1)
	ws.themeBrowserConfirm() // applies, and closes
	changed := ws.ActivePane().Theme()
	if changed == opened {
		t.Fatal("confirm did not change the theme")
	}

	// Now cancel a session that applied something.
	ws.ToggleThemeBrowser()
	before := ws.ActivePane().Theme()
	ws.themeBrowserMove(1)
	if sel, ok := ws.browserSelected(); ok {
		ws.applyThemeImpl(sel)
		ws.browser.applied = true
	}
	ws.themeBrowserDismiss()

	if ws.browser.visible {
		t.Error("browser still visible after Escape")
	}
	if got := ws.ActivePane().Theme(); got != before {
		t.Error("Escape did not revert to the theme active when the browser opened")
	}
}

// Escape on a session that changed nothing must leave the theme alone rather
// than re-applying and emitting a spurious mode-2031 report.
func TestThemeBrowser_EscapeWithoutChangesLeavesTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()
	before := ws.ActivePane().Theme()

	ws.themeBrowserMove(1) // moves the cursor only
	ws.themeBrowserDismiss()

	if got := ws.ActivePane().Theme(); got != before {
		t.Error("Escape changed the theme on a session that applied nothing")
	}
}

// The browser opens with the cursor on the theme that is currently active, not
// at the top of the list — otherwise Enter straight after opening would swap
// the theme out from under the user.
func TestThemeBrowser_OpensOnActiveTheme(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()
	ws.themeBrowserMove(1)
	ws.themeBrowserConfirm()
	settled := ws.ActivePane().Theme()

	ws.ToggleThemeBrowser()

	sel, ok := ws.browserSelected()
	if !ok {
		t.Fatal("nothing selected after opening the browser")
	}
	if sel.Theme != settled {
		t.Errorf("browser opened on %q, want the active theme", sel.Name)
	}
}

// Escape with a filter typed clears the filter instead of closing: at that
// point Escape reads as "undo my search".
func TestThemeBrowser_EscapeClearsFilterFirst(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()
	ws.setThemeFilter("dracula")

	if len(ws.browser.matches) != 1 {
		t.Fatalf("filter matched %d themes, want 1", len(ws.browser.matches))
	}

	ws.themeBrowserDismiss()
	if !ws.browser.visible {
		t.Fatal("Escape closed the browser instead of clearing the filter")
	}
	if ws.browser.filter != "" {
		t.Errorf("filter = %q, want cleared", ws.browser.filter)
	}
	if len(ws.browser.matches) != len(ws.cfg.Themes) {
		t.Errorf("matches = %d after clearing, want all %d",
			len(ws.browser.matches), len(ws.cfg.Themes))
	}

	ws.themeBrowserDismiss()
	if ws.browser.visible {
		t.Error("second Escape did not close the browser")
	}
}

// Filtering is case- and accent-insensitive, and the cursor never points at a
// row that was filtered out.
func TestThemeBrowser_FilterMatching(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()

	ws.setThemeFilter("DRAC")
	if len(ws.browser.matches) != 1 {
		t.Fatalf("case-insensitive filter matched %d, want 1", len(ws.browser.matches))
	}
	if sel, ok := ws.browserSelected(); !ok || sel.Name != "Dracula" {
		t.Errorf("selection = %v, want Dracula", sel)
	}

	ws.setThemeFilter("zzzz")
	if len(ws.browser.matches) != 0 {
		t.Errorf("nonsense filter matched %d themes", len(ws.browser.matches))
	}
	if _, ok := ws.browserSelected(); ok {
		t.Error("something is selected despite no matches")
	}
}

// Enter with no matches must not commit an arbitrary theme.
func TestThemeBrowser_ConfirmWithNoMatchesIsNoOp(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()
	before := ws.ActivePane().Theme()
	ws.setThemeFilter("no such theme")

	ws.themeBrowserConfirm()

	if !ws.browser.visible {
		t.Error("browser closed on a confirm that had nothing to commit")
	}
	if ws.ActivePane().Theme() != before {
		t.Error("confirm applied a theme despite no matches")
	}
}

// The dark:/light: prefix narrows by the theme's character.
func TestThemeBrowser_CharacterFilter(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Latte", Theme: testTheme(t, "Catppuccin Latte")},
		{Name: "Mocha", Theme: testTheme(t, "Catppuccin Mocha")},
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.ToggleThemeBrowser()

	ws.setThemeFilter("light:")
	for _, ti := range ws.browser.matches {
		if ws.cfg.Themes[ti].Theme.IsDark() {
			t.Errorf("light: filter kept dark theme %q", ws.cfg.Themes[ti].Name)
		}
	}
	if len(ws.browser.matches) == 0 {
		t.Error("light: filter matched nothing")
	}

	ws.setThemeFilter("dark:moc")
	if len(ws.browser.matches) != 1 {
		t.Fatalf("dark:moc matched %d, want 1 (Mocha)", len(ws.browser.matches))
	}
	if sel, _ := ws.browserSelected(); sel.Name != "Mocha" {
		t.Errorf("dark:moc selected %q, want Mocha", sel.Name)
	}
}

// The browser is a no-op with no themes — there is nothing to browse.
func TestThemeBrowser_NoThemesStaysHidden(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, hermeticCfg(t))

	ws.ToggleThemeBrowser()

	if ws.browser.visible {
		t.Error("browser opened with no configured themes")
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

// The filter box takes whatever the user pastes into it. The text is folded
// and matched against every configured theme on each change, so it is capped
// before anything scans with it.
func TestThemeBrowser_FilterTextIsCapped(t *testing.T) {
	ws := newLiveWorkspaceCfg(t, themeBrowserCfg(t))
	ws.ToggleThemeBrowser()

	ws.setThemeFilter(strings.Repeat("x", 64*1024))
	if got := len(ws.browser.filter); got > browserFilterMax {
		t.Errorf("filter kept %d bytes, want at most %d", got, browserFilterMax)
	}
	if len(ws.browser.matches) != 0 {
		t.Errorf("a nonsense filter matched %d themes", len(ws.browser.matches))
	}

	// The cut must land on a rune boundary: a half-encoded rune would fold to
	// U+FFFD and quietly change what the query matches.
	ws.setThemeFilter(strings.Repeat("é", browserFilterMax))
	if !utf8.ValidString(ws.browser.filter) {
		t.Errorf("truncated filter is not valid UTF-8: %q", ws.browser.filter)
	}
}

// Filtering folds case and accents, so the bundled names carrying diacritics
// are reachable from a US keyboard.
func TestThemeBrowser_FilterFoldsAccents(t *testing.T) {
	cfg := hermeticCfg(t)
	cfg.Themes = []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Rosé Pine", Theme: testTheme(t, "Rose Pine")},
		{Name: "Café Noir", Theme: testTheme(t, "Dracula")},
	}
	ws := newLiveWorkspaceCfg(t, cfg)
	ws.ToggleThemeBrowser()

	for _, tc := range []struct{ query, want string }{
		{"rose", "Rosé Pine"},
		{"ROSÉ", "Rosé Pine"},
		{"cafe", "Café Noir"},
		{"café", "Café Noir"},
	} {
		ws.setThemeFilter(tc.query)
		if len(ws.browser.matches) != 1 {
			t.Errorf("%q matched %d themes, want 1", tc.query, len(ws.browser.matches))
			continue
		}
		if sel, _ := ws.browserSelected(); sel.Name != tc.want {
			t.Errorf("%q selected %q, want %q", tc.query, sel.Name, tc.want)
		}
	}
}
