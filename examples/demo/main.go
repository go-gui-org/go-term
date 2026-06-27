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
			// A confirm dialog is already up (e.g. a repeated Cmd+Q or
			// a close-button click while confirming): don't stack a
			// second one. DialogIsVisible also drives the quit-request
			// dedup in go-gui, but the window-close path has no such
			// guard, so check here too.
			if w.DialogIsVisible() {
				return
			}
			n := 0
			if s != nil {
				n = s.LiveTermCount()
			}
			if confirmOnQuit && n > 0 {
				// Use go-gui's in-app dialog, not NativeConfirmDialog:
				// go-gui renders and keyboard-routes it itself (Enter,
				// Esc, Tab all work). The native NSAlert runModal path
				// loses keyboard focus under the metal backend's manual
				// event pump, and doesn't participate in the quit-request
				// dedup, so it could stack duplicate dialogs.
				w.Dialog(gui.DialogCfg{
					DialogType: gui.DialogConfirm,
					Title:      "Quit go-term?",
					Body: fmt.Sprintf(
						"%d active terminal(s) will be terminated. Quit anyway?", n),
					OnOkYes: func(w *gui.Window) { w.Close() },
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
