package term

import (
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// cmdScheduler schedules callbacks on the GUI main thread. *gui.Window
// satisfies this via its QueueCommand method. Tests replace it with a
// synchronous executor so callbacks run inline and assertions work without
// a real window.
type cmdScheduler interface {
	QueueCommand(func(*gui.Window))
}

// notifier sends desktop notifications. The production implementation
// shells out to osascript (macOS), notify-send (Linux), or a WinRT toast
// via PowerShell (Windows). Tests replace it with a no-op or recorder.
type notifier interface {
	Notify(title, body string)
}

// desktopNotifier is the production notifier backed by osascript /
// notify-send / WinRT toast.
type desktopNotifier struct{}

func (desktopNotifier) Notify(title, body string) { sendDesktopNotify(title, body) }

// InputKind labels the user-input path a Cfg.OnInput callback observed, and
// selects how Term.SendInput replays it. The two are distinguished because a
// paste cannot simply be copied to another pane: bracketed paste (DEC ?2004)
// is a mode each child enables for itself, so the markers have to be applied
// per receiver.
type InputKind int

const (
	// InputKey is a typed key, already encoded for the pane it came from.
	InputKey InputKind = iota
	// InputPaste is clipboard text with the paste-end marker stripped and no
	// bracketed-paste wrapper applied.
	InputPaste
)

// ActivityKind labels what a Cfg.OnActivity callback observed. Every kind is
// an event the child asserted explicitly — a BEL, or an OSC 133 command end.
//
// Screen output is deliberately not one of them. An application that repaints
// on a timer (a spinner, a status-line clock, an animated prompt) dirties
// cells forever whether or not anything happened, so "the screen changed"
// cannot distinguish a finished build from an idle full-screen app, and an
// embedder marking panes from it would mark every pane permanently.
type ActivityKind int

const (
	// ActivityBell is a BEL the child emitted, whatever BellMode does with it.
	ActivityBell ActivityKind = iota
	// ActivityCommandDone is an OSC 133 D mark for a command that succeeded,
	// or whose exit status the shell did not report. Requires shell
	// integration — see scripts/shell-integration/.
	ActivityCommandDone
	// ActivityCommandFailed is an OSC 133 D mark carrying a non-zero exit.
	ActivityCommandFailed
)

// Cfg configures a Term widget. All fields are optional.
type Cfg struct {

	// OnTitle, if non-nil, receives OSC 0/1/2 window-title updates
	// on the main goroutine. When nil, the widget calls
	// win.SetTitle directly, which is appropriate for standalone
	// single-Term windows. Embedders that manage their own title bar
	// (or multiple Term instances) should set OnTitle to capture
	// per-terminal titles.
	OnTitle func(string)

	// OnNotify, if non-nil, is called for OSC 9 / OSC 777 desktop
	// notification requests. title may be empty (OSC 9). When nil,
	// the widget fires a native OS notification via osascript (macOS),
	// notify-send (Linux), or a WinRT toast (Windows). Called on a
	// background goroutine — safe to block.
	OnNotify func(title, body string)

	// OnActivity, if non-nil, is called on the main thread when the child
	// rings the bell or a command ends. A pane manager uses it to mark
	// background tabs — see term/workspace, which draws its bell and
	// command-result indicators from this one hook.
	//
	// One call per event, at the rate the child produces them (a bell, a
	// prompt), not at the rate it produces bytes. The command kinds need a
	// shell that emits OSC 133; without one, only bells arrive. This is not a
	// change feed — callers that need the screen contents should read the
	// grid on the next draw instead.
	OnActivity func(kind ActivityKind)

	// OnExit, if non-nil, is called when the child process exits.
	// Runs on the reader goroutine — fire a goroutine for any slow
	// work (e.g. calling Term.Close on the main thread via QueueCommand).
	OnExit func()

	// OnClickFocus, if non-nil, is called when the user clicks on the
	// terminal canvas. Multi-Term embedders use this to switch focus to
	// the clicked pane. Runs synchronously during the click handler.
	OnClickFocus func()

	// OnInput, if non-nil, is called on the main thread with every byte
	// sequence this pane sends to its child as a direct result of user
	// input. It runs alongside the local write and cannot suppress it — it
	// exists so a pane manager can mirror input to sibling panes (broadcast
	// mode). Mouse reporting, focus reports and pty replies are deliberately
	// excluded: those describe *this* pane's viewport and would be wrong
	// anywhere else.
	//
	// p is owned by the widget and is only valid for the duration of the
	// call; copy it if it must outlive the callback. Neither Term.Write nor
	// Term.SendInput — the method that replays what this tap hands out —
	// fires the hook, so a mirrored write cannot re-enter it.
	OnInput func(p []byte, kind InputKind)

	// Command overrides the shell command. When empty (default), $SHELL
	// from the environment is used (with /bin/sh as fallback). Set this
	// to spawn a custom binary in the pty instead of a shell.
	Command string

	// Themes, if non-empty, adds a right-click context menu for selecting
	// a color theme at runtime. The first entry is used as the initial theme.
	Themes []NamedTheme

	// Args supplies arguments when Command is set. When Command is empty,
	// Args are passed to the default shell (e.g. []string{"-c", "htop"}).
	Args []string

	// Env appends to the child process environment. When nil or empty,
	// the child inherits os.Environ() plus TERM=xterm-256color, and — on
	// unix, only when the inherited environment sets no LC_ALL/LC_CTYPE/LANG
	// — a UTF-8 LANG so wide characters survive. Entries are appended after
	// the inherited environment, so they override inherited values. Use
	// "KEY=" (trailing equals) to unset.
	Env []string

	// Identity names this terminal to children via TERM_PROGRAM, replacing
	// whatever emulator the host ran under (see setTerminalIdentity). Empty
	// (default) advertises "go-term". Embedders that implement the same
	// capability profile as a known emulator can set that name here so
	// children that key their behavior off TERM_PROGRAM — yazi and superfile
	// pick their image protocol from it — get the right one. cfg.Env is
	// applied after, so an Env entry still wins.
	Identity string

	// TextStyle overrides the default monospace text style. When set to
	// the zero value, the widget falls back to gui.CurrentTheme().M5.
	// To use a custom style you must set at least one field (typically
	// Size or Typeface) — a zero-value TextStyle is treated as "unset."
	TextStyle gui.TextStyle

	// ScrollbackRows caps the number of scrollback rows. The meaning
	// depends on the sign:
	//
	//   - Zero (the default): use defaultScrollbackRows (5000).
	//   - Positive: use this many rows, clamped to [1, MaxScrollbackCap].
	//   - Negative: disable scrollback entirely (ScrollbackCap = 0).
	//
	// Disabling scrollback saves memory for short-lived embedded
	// widgets that never need history.
	ScrollbackRows int

	// BellMode selects how a BEL (0x07) is signalled to the user. The
	// zero value plays the system alert sound, falling back to the
	// visual flash only where no such sound exists.
	BellMode BellMode

	// BellFlashDuration overrides how long the visual-bell overlay stays
	// visible. Zero (default) uses the built-in 100 ms. Negative disables
	// the visual bell entirely. Only consulted when BellMode actually
	// flashes.
	BellFlashDuration time.Duration

	// ScrollbarWidth overrides the pixel width of the scrollbar thumb.
	// Zero (default) uses the built-in 4 px. Negative hides the scrollbar.
	ScrollbarWidth float32

	// NotifyAfter fires a desktop notification when a command that ran at
	// least this long finishes while the user is looking elsewhere — the
	// window is in the background, or (under a pane manager) this pane is not
	// the active one. Zero (the default) or negative disables it; a positive
	// value below one second is raised to one second.
	//
	// Commands are delimited by the OSC 133 marks a shell emits only once its
	// integration hooks are installed (scripts/shell-integration/), so this
	// does nothing under an unconfigured shell. Term.SetNotifyAfter changes
	// it on a live terminal.
	NotifyAfter time.Duration

	// MinimumContrast is the WCAG contrast ratio (1.0–21.0) a cell's
	// foreground is forced to reach against its background at render time. Any
	// value at or below 1 (the default) disables the clamp.
	//
	// It exists because a truecolor SGR is not themeable: an app that emits
	// colors chosen for a dark background — eza, starship, most `ls` themes —
	// hands a light theme text at 1.5:1 that no palette setting can fix. The
	// grid keeps the color the child sent, so copy, search and recording are
	// unaffected; only what is painted changes. 3.0 is a reasonable setting,
	// 4.5 is the WCAG floor for body text.
	MinimumContrast float64

	// CursorStyle is the shape the cursor starts in, and the one `reset`
	// returns to. The zero value is a block; CursorStyleUnderline and
	// CursorStyleBar select the other two.
	//
	// It is a default, not an override: an application is free to change the
	// shape with DECSCUSR (vim does, for insert mode) unless CursorLocked
	// says otherwise.
	CursorStyle CursorStyle

	// CursorBlink is whether the cursor starts blinking, with the same
	// default-not-override meaning as CursorStyle. The zero value is steady.
	CursorBlink bool

	// CursorLocked ignores every DECSCUSR (CSI Ps SP q) the child sends, so
	// CursorStyle and CursorBlink hold for the life of the pane. Off by
	// default: shells and editors legitimately move the cursor, and a
	// terminal that silently dropped those requests would be the surprise.
	CursorLocked bool

	// MiddleClickPaste enables pasting with the middle mouse button: the X11
	// PRIMARY selection where one exists, the clipboard otherwise. Off by
	// default because it is a Unix convention rather than a universal one —
	// term/workspace turns it on for Linux when the config file says nothing,
	// keeping the platform policy out of the widget.
	//
	// Only consulted when the application has not enabled mouse reporting; a
	// child that asked for mouse events always receives the middle button.
	MiddleClickPaste bool

	// AllowOSC52Write permits host applications to write the system clipboard
	// via OSC 52. Disabled by default so untrusted terminal output cannot
	// silently replace the user's clipboard.
	AllowOSC52Write bool

	// DisableGraphics, when true, skips Sixel, Kitty, and iTerm2 inline
	// image decoding and rendering. Use to reduce memory/CPU in panes
	// that don't need image support.
	DisableGraphics bool

	// NoWindowHandler, when true, prevents New from installing this Term
	// as a handler on w.OnEvent. Set this when a pane manager or other
	// container owns the window-level event dispatch and will route
	// events to individual Terms via HandleWindowEvent. The standalone
	// (false) default is correct for single-Term windows.
	NoWindowHandler bool

	// OnDownload receives OSC 1337 File= transfers that are not inline
	// images (iTerm2's imgcat -d, it2dl). name is a sanitized bare filename,
	// never a path; data is the decoded payload. Runs on a background
	// goroutine, so it may block on disk or network. When nil and DownloadDir
	// is set, the built-in writer saves to that directory instead.
	//
	// Leaving both unset disables file transfers entirely, which is the
	// default: untrusted terminal output must not create files on its own
	// authority.
	OnDownload func(name string, data []byte)

	// DownloadDir is where OSC 1337 File= transfers are saved when
	// OnDownload is nil. Created on first use. Empty (default) disables the
	// built-in writer. Files land with 0600 permissions and a " (N)" suffix
	// on name collisions; a transfer never overwrites an existing file.
	DownloadDir string

	// Dir sets the working directory for the child process. When non-empty
	// and the path exists on disk, the shell starts there. Empty inherits
	// the process CWD.
	Dir string

	// RecordPath, when non-empty, starts a session recording at that path
	// as soon as the terminal opens (see Term.StartRecording). The file is
	// overwritten. A failure to open it is logged, not fatal — a terminal
	// that refuses to start because a debug artefact could not be written
	// would be a poor trade.
	RecordPath string

	// RecordInput adds keystrokes and pastes to session recordings as 'i'
	// frames. Off by default: input capture records whatever the user
	// types, including into a password prompt, so it must be asked for.
	// Replay ignores 'i' frames; they are context for a human reader.
	RecordInput bool

	// KeyBindings overrides the default chords for Term-level actions (copy,
	// paste, find, scrollback, font zoom). A nil or empty map leaves every
	// built-in binding in place; entries override only the actions they name.
	// A gui.Shortcut with Key == 0 unbinds its action so the key reaches the
	// child process instead.
	//
	// This seeds the initial table only. Term.SetKeyBindings changes bindings
	// on a live terminal — a settings UI or config reload must use that, since
	// Cfg is never re-read after New.
	KeyBindings KeyMap
}

// NamedTheme pairs a display name with a Theme for use in menus.
type NamedTheme struct {
	Name  string
	Theme Theme
}

// defaultScrollbackRows is the cap applied when Cfg.ScrollbackRows == 0.
const defaultScrollbackRows = 5000

// initRows/initCols size the pty and grid before the first draw has
// measured real cell metrics. resizeLoop also seeds its last-applied
// size from these so it can detect a same-size re-apply (see resizeLoop).
const initRows, initCols = 24, 80

// applyScrollbackConfig sets ScrollbackCap based on cfg.ScrollbackRows.
// Zero uses the default; positive clamps within bounds; negative disables.
func applyScrollbackConfig(g *grid, cfg Cfg) {
	switch {
	case cfg.ScrollbackRows == 0:
		g.ScrollbackCap = defaultScrollbackRows
	case cfg.ScrollbackRows > 0:
		g.ScrollbackCap = clampScrollback(cfg.ScrollbackRows)
	default:
		// Negative: leave ScrollbackCap = 0 (scrollback disabled).
	}
}

// applyTheme sets the initial grid theme from cfg.Themes. When no
// themes are configured the grid keeps its zero-value Theme.
func applyTheme(g *grid, cfg Cfg) {
	if len(cfg.Themes) > 0 {
		g.setTheme(cfg.Themes[0].Theme)
	}
}

// applyContrastConfig seeds the render-time contrast floor. A non-finite value
// is treated as unset rather than propagated: NaN would compare false against
// every threshold and silently disable the clamp in a way no caller could
// debug from the config file.
func applyContrastConfig(g *grid, cfg Cfg) {
	if r := cfg.MinimumContrast; realNumber(r) && r > contrastDisabled {
		g.MinContrast = r
	}
}

// applyCursorConfig seeds the grid's cursor defaults from Cfg. Called before
// the reader goroutine starts, so no lock is needed.
func applyCursorConfig(g *grid, cfg Cfg) {
	g.setCursorDefaults(validCursorStyle(cfg.CursorStyle), cfg.CursorBlink, cfg.CursorLocked)
}

// validCursorStyle maps an out-of-range CursorStyle onto the block default.
// The type is a bare uint8, so an embedder (or a future config value) can hand
// over a number no shape corresponds to; drawing would silently fall through
// to the block branch anyway, and this makes DECSCUSRParam agree with it.
func validCursorStyle(s CursorStyle) CursorStyle {
	switch s {
	case CursorStyleUnderline, CursorStyleBar:
		return s
	default:
		return CursorStyleBlock
	}
}
