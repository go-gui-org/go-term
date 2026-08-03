package workspace

import (
	"runtime"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// termCfg is the choke point that rejects a working directory it cannot
// trust. A relative dir would resolve against the process CWD — the shell
// would silently open somewhere the user never chose.
func TestTermCfg_RejectsRelativeDir(t *testing.T) {
	tab := &Tab{id: "tab-0"}
	hooks := paneHooks{
		onExit:  func(string) {},
		onFocus: func(string) {},
		onTitle: func(string, string) {},
		onInput: func(string, []byte, term.InputKind) {},
	}
	// What counts as absolute is platform-specific: Windows treats a rooted
	// but driveless "/abs/path" as relative to the *current drive*, so it is
	// correctly dropped there. A Windows shell reports OSC 7 as
	// file:///C:/... anyway, which arrives here with a drive letter.
	abs := "/abs/path"
	if runtime.GOOS == "windows" {
		abs = `C:\abs\path`
	}
	cases := []struct{ in, want string }{
		{"", ""},
		{"relative/path", ""},
		{"..", ""},
		{"~/src", ""}, // no shell expansion happens here
		{abs, abs},
	}
	for _, c := range cases {
		got := tab.termCfg(&gui.Window{}, Cfg{}, "tab-0-pane-0", c.in, hooks).Dir
		if got != c.want {
			t.Errorf("termCfg(dir=%q).Dir = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTermCfg_CarriesMinimumContrast covers the last link of the
// minimum-contrast chain: the config file resolves into termOpts, but a pane
// only sees it if termCfg copies it across. A break here is silent — the key
// parses, the reload path fires, and nothing on screen changes.
func TestTermCfg_CarriesMinimumContrast(t *testing.T) {
	tab := &Tab{id: "tab-0"}
	hooks := paneHooks{
		onExit:  func(string) {},
		onFocus: func(string) {},
		onTitle: func(string, string) {},
		onInput: func(string, []byte, term.InputKind) {},
	}
	cfg := Cfg{opts: termOpts{minContrast: 4.5}}
	if got := tab.termCfg(&gui.Window{}, cfg, "tab-0-pane-0", "", hooks).MinimumContrast; got != 4.5 {
		t.Errorf("termCfg().MinimumContrast = %v, want 4.5", got)
	}
	// Unset stays unset rather than becoming some floor of its own: term
	// treats anything at or below 1 as off, and 0 is what an absent key means.
	if got := tab.termCfg(&gui.Window{}, Cfg{}, "tab-0-pane-0", "", hooks).MinimumContrast; got != 0 {
		t.Errorf("termCfg().MinimumContrast = %v with no config, want 0", got)
	}
}

// TestPaneThemes covers the ordering that decides COLORFGBG. term reads
// Themes[0] twice — to seed the grid, which applyPaneTheme can correct, and to
// build the child's COLORFGBG, which nothing can correct once the process is
// running. So a user who sets `theme = Solarized Light` must get the light
// theme first here, or the child is told it is running on a dark background.
func TestPaneThemes(t *testing.T) {
	t.Parallel()

	themes := []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: testTheme(t, "Dracula")},
		{Name: "Solarized Light", Theme: testTheme(t, "iTerm2 Solarized Light")},
	}

	t.Run("no_configured_theme_uses_the_workspace_default", func(t *testing.T) {
		got := paneThemes(Cfg{Themes: themes})
		if len(got) != 1 || got[0].Name != "Default" {
			t.Errorf("paneThemes = %v, want just Default", got)
		}
	})

	t.Run("configured_theme_is_the_one_the_pane_gets", func(t *testing.T) {
		light := term.NamedTheme{
			Name:  "Solarized Light",
			Theme: testTheme(t, "iTerm2 Solarized Light"),
		}
		got := paneThemes(Cfg{Themes: themes, opts: themeOpts(light)})
		if len(got) != 1 {
			t.Fatalf("paneThemes returned %d entries, want 1", len(got))
		}
		if got[0].Name != "Solarized Light" {
			t.Errorf("paneThemes[0] = %q, want Solarized Light", got[0].Name)
		}
		if got[0].Theme.IsDark() {
			t.Error("the pane's startup theme reports dark; COLORFGBG would lie")
		}
	})

	t.Run("theme_not_in_the_list_still_reaches_the_pane", func(t *testing.T) {
		// opts.theme is a value, not an index, so nothing guarantees it
		// appears in Themes. The pane must still be built with it — that is
		// what fixes COLORFGBG, which applyPaneTheme cannot correct later.
		odd := term.NamedTheme{Name: "Monokai", Theme: testTheme(t, "Monokai Classic")}
		got := paneThemes(Cfg{Themes: themes[:2], opts: themeOpts(odd)})
		if len(got) != 1 || got[0].Name != "Monokai" {
			t.Errorf("paneThemes = %v, want just Monokai", got)
		}
	})

	t.Run("no_themes_and_none_selected", func(t *testing.T) {
		if got := paneThemes(Cfg{}); len(got) != 0 {
			t.Errorf("paneThemes = %v, want empty", got)
		}
	})

	t.Run("selected_theme_without_a_list", func(t *testing.T) {
		// Not reachable through the config file (findTheme searches the list),
		// but the pane should get the selected theme rather than none.
		light := term.NamedTheme{
			Name:  "Solarized Light",
			Theme: testTheme(t, "iTerm2 Solarized Light"),
		}
		got := paneThemes(Cfg{opts: themeOpts(light)})
		if len(got) != 1 || got[0].Name != "Solarized Light" {
			t.Errorf("paneThemes = %v, want just Solarized Light", got)
		}
	})

	t.Run("result_does_not_alias_the_workspace_list", func(t *testing.T) {
		// The pane's slice is handed to term.Cfg and outlives this call; an
		// append into it must not scribble over ws.cfg.Themes[1].
		got := paneThemes(Cfg{Themes: themes})
		got = append(got, term.NamedTheme{Name: "Injected"})
		if themes[1].Name != "Dracula" {
			t.Errorf("appending to the pane list overwrote Themes[1]: %q", themes[1].Name)
		}
		_ = got
	})
}
