// Command demo runs the go-term widget with multi-tab, multi-pane support.
package main

import (
	"log"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

func main() {
	gui.SetTheme(gui.ThemeDark.WithBorders(true))

	var s *workspace.Workspace
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term",
		Width:  900,
		Height: 600,
		OnInit: func(w *gui.Window) {
			var err error
			s, err = workspace.New(w, workspace.Cfg{
				TextStyle: gui.TextStyle{Family: "JetBrainsMono Nerd Font", Size: 12},
				Themes: []term.NamedTheme{
					{Name: "Default", Theme: term.DefaultTheme},
					{Name: "Gruvbox", Theme: term.GruvboxTheme},
					{Name: "Nord", Theme: term.NordTheme},
					{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
				},
			})
			if err != nil {
				log.Fatalf("workspace.New: %v", err)
			}
			w.UpdateView(s.View)
		},
	})
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	backend.Run(w)
}
