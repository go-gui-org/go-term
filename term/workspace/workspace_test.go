package workspace

import (
	"slices"
	"testing"

	"github.com/go-gui-org/go-term/term"
)

// ---------------------------------------------------------------------------
// truncateTitle
// ---------------------------------------------------------------------------

func TestTruncateTitle_ShortPassthrough(t *testing.T) {
	if got := truncateTitle("hello", 10); got != "hello" {
		t.Errorf("got %q, want \"hello\"", got)
	}
}

func TestTruncateTitle_ExactlyMax(t *testing.T) {
	title := "1234567890" // 10 runes
	if got := truncateTitle(title, 10); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
}

func TestTruncateTitle_LongerThanMax(t *testing.T) {
	if got := truncateTitle("hello world", 8); got != "hello..." {
		t.Errorf("got %q, want \"hello...\"", got)
	}
}

func TestTruncateTitle_MultiByteRuneAtBoundary(t *testing.T) {
	// "café" is 4 runes: c a f é. Truncating to max=4 leaves "café".
	// Truncating to max=3 should give "..." (keep = 0 runes + ellipsis).
	title := "café"
	if got := truncateTitle(title, 4); got != title {
		t.Errorf("got %q, want %q", got, title)
	}
	if got := truncateTitle(title, 3); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

func TestTruncateTitle_Empty(t *testing.T) {
	if got := truncateTitle("", 5); got != "" {
		t.Errorf("got %q, want \"\"", got)
	}
}

func TestTruncateTitle_MaxLessThanThree(t *testing.T) {
	// max=2: keep = max-3 = -1 → clamped to 0 → "..." (3 runes,
	// longer than max, but ellipsis is non-negotiable).
	if got := truncateTitle("abcdef", 2); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

func TestTruncateTitle_MaxZero(t *testing.T) {
	// max=0: keep = max-3 = -3 → clamped to 0 → "..."
	if got := truncateTitle("abcdef", 0); got != "..." {
		t.Errorf("got %q, want \"...\"", got)
	}
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_NilWindowReturnsError(t *testing.T) {
	ws, err := New(nil, Cfg{})
	if err == nil {
		t.Fatal("expected error for nil window, got nil")
	}
	if ws != nil {
		t.Errorf("expected nil Workspace on error, got %v", ws)
	}
}

// ---------------------------------------------------------------------------
// Tab navigation no-op paths
//
// These exercise the early-return guards that do not touch the window, so a
// Workspace can be hand-built with a nil window. Index changes that would
// reach refresh()/activateTab's switch path need a live *gui.Window and are
// covered visually via examples/falcon.
// ---------------------------------------------------------------------------

func TestGoToTab_OutOfRangeNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}, {}}, activeTab: 1}
	ws.GoToTab(-1)
	if ws.activeTab != 1 {
		t.Errorf("negative index changed activeTab to %d, want 1", ws.activeTab)
	}
	ws.GoToTab(5)
	if ws.activeTab != 1 {
		t.Errorf("too-large index changed activeTab to %d, want 1", ws.activeTab)
	}
}

func TestGoToTab_SameIndexNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}, {}}, activeTab: 1}
	ws.GoToTab(1) // activateTab returns early when idx == activeTab
	if ws.activeTab != 1 {
		t.Errorf("same-index GoToTab changed activeTab to %d, want 1", ws.activeTab)
	}
}

func TestNextTab_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}}, activeTab: 0}
	ws.NextTab()
	if ws.activeTab != 0 {
		t.Errorf("NextTab with one tab changed activeTab to %d, want 0", ws.activeTab)
	}
}

func TestPrevTab_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{}}, activeTab: 0}
	ws.PrevTab()
	if ws.activeTab != 0 {
		t.Errorf("PrevTab with one tab changed activeTab to %d, want 0", ws.activeTab)
	}
}

// ---------------------------------------------------------------------------
// MoveTabLeft / MoveTabRight — no-op guard paths
//
// The active paths (swapping then calling refresh()) need a live *gui.Window
// and are covered visually via examples/falcon. These exercise the early-return
// guards: single/empty tabs, edge positions, and negative activeTab.
// ---------------------------------------------------------------------------

func TestMoveTabLeft_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}}, activeTab: 0}
	ws.MoveTabLeft()
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
	if ws.tabs[0].id != "a" {
		t.Errorf("tab order changed for single-tab workspace")
	}
}

func TestMoveTabLeft_FirstTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}, {id: "b"}}, activeTab: 0}
	ws.MoveTabLeft()
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
	if ws.tabs[0].id != "a" || ws.tabs[1].id != "b" {
		t.Errorf("tab order changed when moving left from first tab")
	}
}

func TestMoveTabLeft_EmptyTabsNoop(t *testing.T) {
	ws := &Workspace{tabs: nil, activeTab: 0}
	ws.MoveTabLeft()
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
}

func TestMoveTabLeft_NegativeActiveTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}, {id: "b"}}, activeTab: -1}
	ws.MoveTabLeft()
	if ws.activeTab != -1 {
		t.Errorf("activeTab = %d, want -1", ws.activeTab)
	}
}

func TestMoveTabRight_SingleTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}}, activeTab: 0}
	ws.MoveTabRight()
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
	if ws.tabs[0].id != "a" {
		t.Errorf("tab order changed for single-tab workspace")
	}
}

func TestMoveTabRight_LastTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}, {id: "b"}}, activeTab: 1}
	ws.MoveTabRight()
	if ws.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1", ws.activeTab)
	}
	if ws.tabs[0].id != "a" || ws.tabs[1].id != "b" {
		t.Errorf("tab order changed when moving right from last tab")
	}
}

func TestMoveTabRight_EmptyTabsNoop(t *testing.T) {
	ws := &Workspace{tabs: nil, activeTab: 0}
	ws.MoveTabRight()
	if ws.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", ws.activeTab)
	}
}

func TestMoveTabRight_NegativeActiveTabNoop(t *testing.T) {
	ws := &Workspace{tabs: []*Tab{{id: "a"}, {id: "b"}}, activeTab: -1}
	ws.MoveTabRight()
	if ws.activeTab != -1 {
		t.Errorf("activeTab = %d, want -1", ws.activeTab)
	}
}

// ---------------------------------------------------------------------------
// LiveTermCount — zero/empty paths
//
// The counting branch (tm.Alive() → n++) needs real *term.Term values with a
// live PTY and is covered visually via examples/falcon. These exercise the
// panic-safety guards: nil tabs slice and empty/nil terms maps return 0.
// ---------------------------------------------------------------------------

func TestLiveTermCount_NoTabsAndEmptyTermsReturnsZero(t *testing.T) {
	if n := (&Workspace{}).LiveTermCount(); n != 0 {
		t.Errorf("empty workspace: got %d, want 0", n)
	}
	ws := &Workspace{tabs: []*Tab{
		{terms: map[string]*term.Term{}}, // non-nil empty map
		{},                               // nil terms map
	}}
	if n := ws.LiveTermCount(); n != 0 {
		t.Errorf("empty terms maps: got %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// Theme browser — no-op guard paths
//
// Active paths (open, navigation, filter, apply, cancel) need a live
// *gui.Window + *term.Term and live in restore_test.go. This exercises the
// early-return guard: zero configured themes.
// ---------------------------------------------------------------------------

func TestToggleThemeBrowser_ZeroThemesNoop(t *testing.T) {
	ws := &Workspace{cfg: Cfg{}}
	// Must not panic, and it must not open — the guard runs before ws.w is
	// touched, which matters because ws.w is nil here.
	ws.ToggleThemeBrowser()
	if ws.browser.visible {
		t.Error("browser unexpectedly visible with zero themes")
	}
}

// ---------------------------------------------------------------------------
// applyThemeByName
// ---------------------------------------------------------------------------

func TestApplyThemeByName_EmptyNameReturnsFalse(t *testing.T) {
	ws := &Workspace{
		cfg: Cfg{Themes: []term.NamedTheme{{Name: "a", Theme: term.DefaultTheme}}},
	}
	if ws.applyThemeByName("") {
		t.Error("empty name should return false")
	}
}

func TestApplyThemeByName_NoThemesReturnsFalse(t *testing.T) {
	ws := &Workspace{cfg: Cfg{}}
	if ws.applyThemeByName("a") {
		t.Error("no themes should return false")
	}
}

func TestApplyThemeByName_UnknownNameReturnsFalse(t *testing.T) {
	ws := &Workspace{
		cfg: Cfg{Themes: []term.NamedTheme{{Name: "a", Theme: term.DefaultTheme}}},
	}
	if ws.applyThemeByName("unknown") {
		t.Error("unknown name should return false")
	}
}

func TestApplyThemeByName_CaseInsensitiveMatch(t *testing.T) {
	th := testTheme(t, "Dracula")
	ws := &Workspace{
		cfg: Cfg{
			Themes: []term.NamedTheme{
				{Name: "Dracula", Theme: th},
			},
			opts: themeOpts(term.NamedTheme{Name: "Default", Theme: term.DefaultTheme}),
		},
	}
	if !ws.applyThemeByName("dRaCuLa") {
		t.Error("case-insensitive match should return true")
	}
	if ws.cfg.opts.theme == nil || *ws.cfg.opts.theme != th {
		t.Error("opts.theme not updated after applyThemeByName")
	}
}

func TestApplyThemeByName_SetsTheme(t *testing.T) {
	ws := &Workspace{
		tabs: []*Tab{{terms: make(map[string]*term.Term)}},
		cfg: Cfg{
			Themes: []term.NamedTheme{
				{Name: "Default", Theme: term.DefaultTheme},
				{Name: "Dracula", Theme: testTheme(t, "Dracula")},
			},
		},
	}
	if !ws.applyThemeByName("Dracula") {
		t.Fatal("applyThemeByName returned false")
	}
	if ws.persistableThemeName() != "Dracula" {
		t.Errorf("persistableThemeName = %q, want Dracula", ws.persistableThemeName())
	}
}

// ---------------------------------------------------------------------------
// OnColorScheme
// ---------------------------------------------------------------------------

// TestNotifyColorScheme covers the callback an embedder uses to keep its own
// chrome in step with the panes: it must fire once when the scheme is first
// established, again only when the character actually flips, and never for a
// swap between two themes of the same character.
func TestNotifyColorScheme(t *testing.T) {
	var got []bool
	ws := &Workspace{
		tabs: []*Tab{{terms: make(map[string]*term.Term)}},
		cfg: Cfg{
			Themes: []term.NamedTheme{
				{Name: "Default", Theme: term.DefaultTheme},
				{Name: "Dracula", Theme: testTheme(t, "Dracula")},
				{Name: "Solarized Light", Theme: testTheme(t, "iTerm2 Solarized Light")},
			},
			OnColorScheme: func(dark bool) { got = append(got, dark) },
		},
	}

	// First call: nothing reported yet, so the initial dark theme is news.
	ws.notifyColorScheme()
	// Same character, different theme: not news.
	ws.applyThemeImpl(ws.cfg.Themes[1])
	// Flip to light: news.
	ws.applyThemeImpl(ws.cfg.Themes[2])
	// Back to dark: news again.
	ws.applyThemeImpl(ws.cfg.Themes[0])

	if want := []bool{true, false, true}; !slices.Equal(got, want) {
		t.Fatalf("OnColorScheme calls = %v, want %v", got, want)
	}
}

// TestSelectThemeByName covers the restore path's pre-spawn theme resolution.
// It has to settle opts.theme before any pane is built, because paneThemes
// feeds Themes[0] to COLORFGBG and a child's environment cannot be corrected
// after exec — a user who picked a light theme, quit, and relaunched would
// otherwise get light cells and a shell told it was running on dark.
func TestSelectThemeByName(t *testing.T) {
	newWS := func(seen *[]bool) *Workspace {
		return &Workspace{cfg: Cfg{
			Themes: []term.NamedTheme{
				{Name: "Default", Theme: term.DefaultTheme},
				{Name: "Solarized Light", Theme: testTheme(t, "iTerm2 Solarized Light")},
			},
			OnColorScheme: func(dark bool) { *seen = append(*seen, dark) },
		}}
	}

	t.Run("sets_the_effective_theme_and_reports_the_scheme", func(t *testing.T) {
		var seen []bool
		ws := newWS(&seen)
		if !ws.selectThemeByName("solarized light") { // matched case-insensitively
			t.Fatal("selectThemeByName returned false for a listed theme")
		}
		if ws.cfg.opts.theme == nil || ws.cfg.opts.theme.IsDark() {
			t.Fatalf("opts.theme = %v, want the light theme", ws.cfg.opts.theme)
		}
		// paneThemes is what the pane's term.Cfg is built from; it must now
		// front the light theme, which is what fixes COLORFGBG.
		if got := paneThemes(ws.cfg); len(got) == 0 || got[0].Name != "Solarized Light" {
			t.Errorf("paneThemes[0] = %v, want Solarized Light", got)
		}
		if want := []bool{false}; !slices.Equal(seen, want) {
			t.Errorf("OnColorScheme calls = %v, want %v", seen, want)
		}
	})

	t.Run("unknown_and_empty_names_change_nothing", func(t *testing.T) {
		for _, name := range []string{"", "No Such Theme"} {
			var seen []bool
			ws := newWS(&seen)
			if ws.selectThemeByName(name) {
				t.Errorf("selectThemeByName(%q) returned true", name)
			}
			if ws.cfg.opts.theme != nil {
				t.Errorf("selectThemeByName(%q) set opts.theme", name)
			}
			if len(seen) != 0 {
				t.Errorf("selectThemeByName(%q) fired OnColorScheme", name)
			}
		}
	})

	t.Run("no_themes_configured", func(t *testing.T) {
		ws := &Workspace{}
		if ws.selectThemeByName("Default") {
			t.Error("selectThemeByName matched against an empty theme list")
		}
	})
}

func TestNotifyColorScheme_NoThemesStaysSilent(t *testing.T) {
	called := false
	ws := &Workspace{
		cfg: Cfg{OnColorScheme: func(bool) { called = true }},
	}
	// With no themes configured the panes keep term's own default and the
	// embedder's chrome choice stands; guessing at a character here would
	// override a host that never asked for one.
	ws.notifyColorScheme()
	if called {
		t.Error("OnColorScheme fired with no themes configured")
	}
}
