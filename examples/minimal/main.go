// Minimal go-term example: single window with live shell.
// go run examples/minimal
package main

import (
	"log"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term"
)

func main() {
	gui.SetTheme(gui.ThemeDark.WithBorders(true))

	var tm *term.Term
	w := gui.NewWindow(gui.WindowCfg{
		Title:  "go-term minimal",
		Width:  800,
		Height: 500,
		OnInit: func(w *gui.Window) {
			var err error
			tm, err = term.New(w, term.Cfg{
				TextStyle: gui.TextStyle{Family: "monospace", Size: 14},
			})
			if err != nil {
				log.Fatalf("term.New: %v", err)
			}
			w.UpdateView(tm.View)
		},
	})
	defer func() {
		if tm != nil {
			_ = tm.Close()
		}
	}()

	backend.Run(w)
}
