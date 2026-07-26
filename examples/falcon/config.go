package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
	"github.com/go-gui-org/go-term/term/workspace"
)

// startCfg holds the CLI arguments for the normal (non-replay) startup path.
// Register flags via parseStartCfg before flag.Parse; fields are populated
// after Parse runs in main.
type startCfg struct {
	workspacePath     string
	saveWorkspacePath string
	recordPath        string
}

// parseStartCfg registers the --workspace, --save-workspace, and --record
// flags on the default flag set and returns a struct whose fields will
// hold the parsed values after flag.Parse executes.
func parseStartCfg() startCfg {
	var cfg startCfg
	flag.StringVar(&cfg.workspacePath, "workspace", "",
		"workspace JSON to load on startup")
	flag.StringVar(&cfg.saveWorkspacePath, "save-workspace", "",
		"workspace JSON to write on quit (defaults to --workspace path when set)")
	flag.StringVar(&cfg.recordPath, "record", "",
		"record the first pane's session to this .gtr file")
	return cfg
}

// resolvedWorkspacePath returns the workspace path with the default
// fallback applied when --workspace was not given. Must be called after
// flag.Parse.
func (c startCfg) resolvedWorkspacePath() string {
	if c.workspacePath != "" {
		return c.workspacePath
	}
	def, err := workspace.DefaultWorkspacePath()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(def); err != nil {
		return ""
	}
	return def
}

// effectiveSavePath returns the path to write the workspace on quit:
// --save-workspace if set, otherwise --workspace when set.
// Must be called after flag.Parse.
func (c startCfg) effectiveSavePath() string {
	if c.saveWorkspacePath != "" {
		return c.saveWorkspacePath
	}
	return c.workspacePath
}

// themeList returns the built-in color themes available in falcon.
func themeList() []term.NamedTheme {
	return []term.NamedTheme{
		{Name: "Default", Theme: term.DefaultTheme},
		{Name: "Dracula", Theme: term.DraculaTheme},
		{Name: "Catppuccin Mocha", Theme: term.CatppuccinMochaTheme},
		{Name: "Tokyo Night", Theme: term.TokyoNightTheme},
		{Name: "Monokai", Theme: term.MonokaiTheme},
		{Name: "One Dark", Theme: term.OneDarkTheme},
		{Name: "Rosé Pine", Theme: term.RosePineTheme},
		{Name: "Kanagawa", Theme: term.KanagawaTheme},
		{Name: "Ayu Dark", Theme: term.AyuDarkTheme},
		{Name: "Everforest", Theme: term.EverforestTheme},
		{Name: "GitHub Dark", Theme: term.GitHubDarkTheme},
		{Name: "Gruvbox", Theme: term.GruvboxTheme},
		{Name: "Nord", Theme: term.NordTheme},
		{Name: "Solarized Dark", Theme: term.SolarizedDarkTheme},
	}
}

// newLiveWindowCfg builds the WindowCfg for the normal (non-replay) path.
// sp points to the caller's *Workspace variable; OnInit assigns the
// created workspace through it, and OnCloseRequest / saveAndClose read
// the current value through it. This mirrors the closure-over-local
// pattern from the original single-function main, now explicit.
func newLiveWindowCfg(
	wc workspace.Cfg,
	workspacePath, savePath, recordPath string,
	sp **workspace.Workspace,
) gui.WindowCfg {
	saveAndClose := func(w *gui.Window) {
		s := *sp
		if savePath != "" && s != nil {
			if err := s.Save(savePath); err != nil {
				log.Printf("workspace save: %v", err)
			}
		}
		w.Close()
	}
	return gui.WindowCfg{
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
			s := *sp
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
					OnOkYes: func(w *gui.Window) { saveAndClose(w) },
				})
				return
			}
			saveAndClose(w)
		},
		OnInit: func(w *gui.Window) {
			var s *workspace.Workspace
			var err error
			if workspacePath != "" {
				s, err = workspace.Restore(w, wc, workspacePath)
			} else {
				s, err = workspace.New(w, wc)
			}
			if err != nil {
				log.Fatalf("workspace init: %v", err)
			}
			*sp = s
			// --record applies to the pane the user starts in. Other panes
			// are recorded on demand with Cmd+Shift+R.
			if recordPath != "" {
				if tm := s.ActivePane(); tm != nil {
					if err := tm.StartRecording(recordPath); err != nil {
						log.Printf("record: %v", err)
					} else {
						log.Printf("recording to %s", recordPath)
					}
				}
			}
			w.UpdateView(s.View)
		},
	}
}
