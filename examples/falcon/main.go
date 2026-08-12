// Falcon is a full-featured terminal emulator built on go-term and the go-gui
// framework. It spawns a real shell over a PTY and renders through a
// GPU-accelerated DrawCanvas, covering the protocol surface expected by modern
// CLI tools and TUI frameworks (e.g. vim, less, htop). Supports multi-tab,
// multi-pane workspaces, workspace save/restore, and multiple color themes.
// Targets macOS, Linux, and Windows (ConPTY). Used as a daily-driver terminal
// on macOS.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term/workspace"
)

// main is a thin wrapper so run's deferred teardown isn't skipped: os.Exit
// (and log.Fatal) don't unwind defers, so the exit code has to come back
// as a return value.
func main() {
	os.Exit(run())
}

func run() int {
	start, replay, err := parseFlags(flag.CommandLine, os.Args[1:])
	if err != nil {
		// flag.CommandLine is ExitOnError, so Parse has already exited on
		// bad input; this only fires for a ContinueOnError FlagSet.
		return 2
	}

	// Replay is a viewer, not a multiplexer: one pane, no tabs, no
	// workspace persistence. Handled entirely separately from the normal
	// startup path below.
	if replay.path != "" {
		return runReplay(*replay)
	}

	themes := themeList()
	// Chrome for the window frame itself, which exists before the workspace
	// has read the config file. OnColorScheme corrects it a moment later if
	// `theme =` names a scheme of the other character. An empty list is not
	// reachable today but must not panic here: term falls back to its own dark
	// DefaultTheme in that case, so the chrome follows it.
	startupDark := true
	if len(themes) > 0 {
		startupDark = themes[0].Theme.IsDark()
	}
	applyChrome(startupDark)

	a := &app{
		wc: workspace.Cfg{
			TextStyle:              defaultTextStyle(),
			Identity:               "falcon",
			ExitWhenLastShellExits: true,
			DownloadDir:            defaultDownloadDir(),
			SavePath:               start.effectiveSavePath(),
			Themes:                 themes,
			OnColorScheme:          applyChrome,
		},
		loadPath:   start.resolvedWorkspacePath(),
		savePath:   start.effectiveSavePath(),
		recordPath: start.recordPath,
	}
	// Exiting the last shell closes the window without going through
	// OnCloseRequest, so route that path through saveAndClose too — otherwise
	// the workspace file keeps whatever it held at startup. Set after the
	// literal because it closes over a.
	a.wc.OnLastShellExit = a.saveAndClose
	defer a.close()

	// The App has to exist before the window: onInit installs the menubar on
	// it, and SetNativeMenubar is a no-op until the main window is registered
	// — which RunApp does before it dispatches OnInit.
	a.gapp = gui.NewApp()
	backendRunApp(a.gapp, gui.NewWindow(a.windowCfg()))
	if a.initErr != nil {
		log.Printf("workspace init: %v", a.initErr)
		return 1
	}
	return 0
}

// backendRunApp runs the multi-window app loop: only it honors an
// OnCloseRequest veto (single-window backend.Run quits unconditionally on
// Cmd+Q).
func backendRunApp(app *gui.App, w *gui.Window) {
	backend.RunApp(app, w)
}
