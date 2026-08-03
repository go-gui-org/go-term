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
		{Name: "Dracula", Theme: term.DraculaTheme},
		{Name: "Solarized Light", Theme: term.SolarizedLightTheme},
	}

	t.Run("no_configured_theme_keeps_the_order", func(t *testing.T) {
		got := paneThemes(Cfg{Themes: themes})
		if len(got) != len(themes) || got[0].Name != "Default" {
			t.Errorf("paneThemes = %v, want the list unchanged", got)
		}
	})

	t.Run("configured_theme_moves_to_the_front", func(t *testing.T) {
		light := term.SolarizedLightTheme
		got := paneThemes(Cfg{Themes: themes, opts: termOpts{theme: &light}})
		if len(got) != len(themes) {
			t.Fatalf("paneThemes dropped entries: %d, want %d", len(got), len(themes))
		}
		if got[0].Name != "Solarized Light" {
			t.Errorf("paneThemes[0] = %q, want Solarized Light", got[0].Name)
		}
		if got[0].Theme.IsDark() {
			t.Error("the pane's startup theme reports dark; COLORFGBG would lie")
		}
		// Every original entry must survive: the list is the full set the
		// pane's own theme menu would show.
		for _, want := range themes {
			var found bool
			for _, nt := range got {
				if nt.Name == want.Name {
					found = true
				}
			}
			if !found {
				t.Errorf("paneThemes lost %q", want.Name)
			}
		}
	})

	t.Run("theme_not_in_the_list_is_left_alone", func(t *testing.T) {
		// opts.theme is a value, not an index, so nothing guarantees it
		// appears in Themes. applyPaneTheme is the backstop; this must not
		// panic or drop entries getting there.
		odd := term.MonokaiTheme
		got := paneThemes(Cfg{Themes: themes[:2], opts: termOpts{theme: &odd}})
		if len(got) != 2 || got[0].Name != "Default" {
			t.Errorf("paneThemes = %v, want the list unchanged", got)
		}
	})

	t.Run("no_themes_at_all", func(t *testing.T) {
		light := term.SolarizedLightTheme
		if got := paneThemes(Cfg{opts: termOpts{theme: &light}}); len(got) != 0 {
			t.Errorf("paneThemes = %v, want empty", got)
		}
	})
}
