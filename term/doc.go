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
// Use [Cfg.OnExit] to detect child-process death. Use [Term.Alive] to
// poll liveness without a callback.
//
// # Multi-Term windows (pane manager)
//
// Set [Cfg.NoWindowHandler] when embedding multiple Term instances in one
// window. A pane manager should install its own window-level event handler
// that routes events to the focused Term via [Term.HandleWindowEvent] and
// keyboard input to the focused Term's [Term.View] container. Use
// [Term.SetFocused] to switch focus between panes.
//
// The [Term.Rows], [Term.Cols], [Term.Write], [Term.PID], and [Term.Alive]
// methods support pane-manager introspection without reaching into internal
// state.
//
// # Thread safety
//
// The widget's exported methods (Cwd, SetTheme, View, Close, Rows, Cols,
// Write, PID, Alive, SetFocused, HandleWindowEvent) are safe to call from
// any goroutine. Internal grid state is protected by a single mutex.
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
// macOS, Linux, and Windows. The PTY boundary uses creack/pty on Unix and
// the ConPTY API on Windows; everything above it is platform-agnostic.
//
// # Stability
//
// go-term is pre-1.0. The public API is deliberately small so embedders
// have a narrow, well-defined contract to code against.
//
// Stable: [NamedTheme], [Theme], and the built-in theme variables
// ([DefaultTheme], [GruvboxTheme], [NordTheme], [SolarizedDarkTheme]) —
// their names won't change and their color values won't shift in ways
// that break contrast; the [MaxGridDim] and [MaxScrollbackCap] constants;
// the [New] constructor; every exported [Term] method ([Term.View],
// [Term.Close] — idempotent, [Term.Cwd], [Term.Theme], [Term.SetTheme],
// [Term.Rows], [Term.Cols], [Term.Write], [Term.PID], [Term.Alive],
// [Term.SetFocused], [Term.HandleWindowEvent]); and [Shortcuts] /
// [ShortcutInfo]. Term is an opaque handle: all fields are unexported,
// so embedders interact only through methods.
//
// What may change before 1.0:
//   - Cfg fields: new fields may be added. Renames and removals go
//     through a deprecation cycle (at least one minor version with the
//     old name still accepted). New fields are always zero-value-safe,
//     so untouched configs keep working across minor bumps.
//   - Term methods: new methods may appear; existing signatures stay.
//   - The gui.View tree returned by [Term.View] is an implementation
//     detail and may gain new widgets; embedders only pass the result
//     to UpdateView, and that contract holds.
//   - Internal layout: import only this package; the source-file
//     organisation within it is not a contract.
//   - Go version: the go directive in go.mod reflects the oldest Go
//     release tested against and may advance on minor version bumps.
//
// Concurrency details, render-pass structure, canvas IDs and draw
// versions, and parser dispatch sites are internal and not part of the
// contract.
//
// # Versioning
//
// Semantic Versioning with a pre-1.0 interpretation:
//   - Patch (0.x.Y): bug fix; no new API surface. Safe to upgrade.
//   - Minor (0.X.0): new feature; may add Cfg fields or Term methods,
//     but existing signatures stay backwards compatible. Read the
//     changelog before upgrading.
//   - Major (1.0.0): first stable release; standard semver afterwards.
//
// Guidance for embedders: pin a minor version in go.mod; use Cfg zero
// values for everything you don't explicitly set; stick to the
// documented methods and open an issue rather than depending on
// internal state. The term/workspace package wires the multi-Term
// methods together for split-pane and tab embedding.
//
// [go-gui]: https://github.com/go-gui-org/go-gui
package term
