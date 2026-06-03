package term_test

import (
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

func ExampleNew() {
	// In a real app, win comes from gui.NewWindow.
	var win *gui.Window

	t, err := term.New(win, term.Cfg{})
	if err != nil {
		panic(err)
	}
	defer func() { _ = t.Close() }()

	win.UpdateView(t.View)
}

func ExampleCfg() {
	cfg := term.Cfg{
		ScrollbackRows:  10000,
		AllowOSC52Write: true, // trusted environment
		Themes: []term.NamedTheme{
			{Name: "Default", Theme: term.DefaultTheme},
			{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
		},
	}
	_ = cfg // cfg is passed to term.New
}

func ExampleTerm_Close() {
	var win *gui.Window

	t, err := term.New(win, term.Cfg{})
	if err != nil {
		panic(err)
	}

	// Always close to clean up the PTY and goroutines.
	_ = t.Close()
}

func ExampleTerm_Cwd() {
	var win *gui.Window

	t, _ := term.New(win, term.Cfg{})

	// Cwd returns the shell's last-reported working directory.
	cwd := t.Cwd()
	_ = cwd
}
