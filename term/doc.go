// Package term is a full-featured terminal-emulator widget for the
// [go-gui] framework. It spawns a real shell via PTY and renders the
// cell grid through GPU-accelerated canvas drawing.
//
// # Quick start
//
//	t, err := term.New(win, term.Cfg{})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	win.SetView(t.View(win))
//
// # Lifecycle
//
// New starts the PTY and a background reader goroutine. Close kills
// the child process, closes the PTY, and shuts down the reader.
// Always call Close when the window is closed to avoid leaking file
// descriptors and goroutines.
//
// # Thread safety
//
// The widget's exported methods (Cwd, SetTheme, View, Close) are safe
// to call from any goroutine. Internal grid state is protected by a
// single mutex.
//
// # Security: OSC 52 clipboard
//
// OSC 52 clipboard write is disabled by default. Set
// [Cfg.AllowOSC52Write] to true only in trusted environments —
// untrusted terminal output can silently replace the clipboard.
//
// # Theme configuration
//
// Use [Cfg.Themes] to provide a list of named themes for runtime
// switching. The first entry is the initial theme. Built-in themes
// include [DefaultTheme], [GruvboxTheme], [NordTheme], and
// [SolarizedDarkTheme].
//
// # Cwd
//
// [Term.Cwd] returns the current working directory reported by the
// shell via OSC 7. Empty if the shell has not emitted a CWD escape
// sequence.
//
// # Supported platforms
//
// macOS and Linux. Windows / ConPTY is not supported.
//
// [go-gui]: https://github.com/mike-ward/go-gui
package term
