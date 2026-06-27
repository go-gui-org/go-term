// Command demo runs the go-term widget with multi-tab, multi-pane support.
package main

import (
	"fmt"
	"log"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

// confirmOnQuit asks for confirmation before closing the window while
// terminal panes still have a live shell. Set to false to quit silently.
const confirmOnQuit = true

func main() {
	gui.SetTheme(gui.ThemeDark.WithBorders(true))

	var s *workspace.Workspace
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term",
		Width:  900,
		Height: 600,
		OnCloseRequest: func(w *gui.Window) {
			n := 0
			if s != nil {
				n = s.LiveTermCount()
			}
			if confirmOnQuit && n > 0 {
				w.NativeConfirmDialog(gui.NativeConfirmDialogCfg{
					Title: "Quit go-term?",
					Body: fmt.Sprintf(
						"%d active terminal(s) will be terminated. Quit anyway?", n),
					Level: gui.AlertWarning,
					OnDone: func(r gui.NativeAlertResult, w *gui.Window) {
						if r.Status == gui.DialogOK {
							w.Close()
						}
					},
				})
				return
			}
			w.Close()
		},
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
	// Use the multi-window app loop: only it honors an OnCloseRequest
	// veto (single-window backend.Run quits unconditionally on Cmd+Q).
	backend.RunApp(gui.NewApp(), w)
}
