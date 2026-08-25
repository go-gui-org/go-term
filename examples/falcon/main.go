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
	"net"
	"net/http"
	_ "net/http/pprof" // registers handlers on http.DefaultServeMux at import
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/go-gui-org/go-term/term/workspace"
)

// memLimit is the soft heap limit in bytes, injected at link time by the
// Makefile (-X main.memLimit=...). Plain go run/go build leaves it empty and
// the default applies. Keep it a bare integer so the Makefile needs no size
// parser; the value is validated at startup.
var memLimit string

// defaultMemLimit is applied when -X main.memLimit is absent (dev builds).
// The limit value is the RSS governor after a burst: the runtime returns
// freed memory toward the live set instead of leaving clean pages resident,
// so a heavy session settles well below its peak. It must stay ABOVE the
// live working set of a heavy session (faces + layout cache + atlas +
// scrollback, ~300-400MB while sweeping Unicode) — a limit below the live
// set forces constant GC (thrash: lag, then crawl). 512MiB is that safe
// floor and still settles post-session RSS to ~230MB. The limit is soft —
// genuine pressure exceeds it rather than hard-capping.
const defaultMemLimit = 512 << 20

// main is a thin wrapper so run's deferred teardown isn't skipped: os.Exit
// (and log.Fatal) don't unwind defers, so the exit code has to come back
// as a return value.
func main() {
	applyMemoryLimit()
	os.Exit(run())
}

// applyMemoryLimit installs the soft heap limit before the app allocates.
// GOMEMLIMIT env wins when set: debug.SetMemoryLimit would otherwise
// override the runtime's own env handling, and the runtime accepts the
// friendlier "512MiB" spelling the -X value deliberately avoids.
func applyMemoryLimit() {
	if limit, ok := resolveMemLimit(); ok {
		debug.SetMemoryLimit(limit)
	}
}

// resolveMemLimit picks the effective soft heap limit in bytes. ok is false
// when a GOMEMLIMIT env var governs instead, in which case nothing should be
// installed. Otherwise the -X stamp wins over the in-code default, and a
// stamp that fails to parse (or is not positive) falls back to the default
// with a log line.
func resolveMemLimit() (int64, bool) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return 0, false
	}
	limit := int64(defaultMemLimit)
	if memLimit != "" {
		v, err := strconv.ParseInt(memLimit, 10, 64)
		if err != nil {
			log.Printf("falcon: ignoring invalid -X memLimit %q: %v", memLimit, err)
		} else if v <= 0 {
			log.Printf("falcon: ignoring non-positive -X memLimit %q", memLimit)
		} else {
			limit = v
		}
	}
	return limit, true
}

func run() int {
	serveProfiling()

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
			ConfigPath:             defaultConfigPath(),
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

// serveProfiling starts the runtime profiling server when GOTERM_PPROF is
// set. The value is a port number, or "1" for the default 6060. The server
// listens on 127.0.0.1 only, so the endpoints are never reachable from the
// network. Off by default and inert when unset. A bad value or a busy port
// is logged and the app continues unprofiled — this is a debug knob, not a
// startup dependency.
func serveProfiling() {
	port := strings.TrimSpace(os.Getenv("GOTERM_PPROF"))
	if port == "" {
		return
	}
	if port == "1" {
		port = "6060"
	}
	// Bind explicitly so the logged address is the one actually bound (port
	// "0" picks an ephemeral port; ListenAndServe's log would say :0).
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		log.Printf("falcon: profiling server: %v", err)
		return
	}
	go func() {
		log.Printf("falcon: profiling server on http://%s/debug/pprof/", ln.Addr())
		if err := http.Serve(ln, nil); err != nil {
			log.Printf("falcon: profiling server: %v", err)
		}
	}()
}

// backendRunApp runs the multi-window app loop: only it honors an
// OnCloseRequest veto (single-window backend.Run quits unconditionally on
// Cmd+Q).
func backendRunApp(app *gui.App, w *gui.Window) {
	backend.RunApp(app, w)
}
