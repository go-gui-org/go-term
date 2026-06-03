package term

import (
	"io"
	"log"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mike-ward/go-gui/gui"
)

// cmdScheduler schedules callbacks on the GUI main thread. *gui.Window
// satisfies this via its QueueCommand method. Tests replace it with a
// synchronous executor so callbacks run inline and assertions work without
// a real window.
type cmdScheduler interface {
	QueueCommand(func(*gui.Window))
}

// notifier sends desktop notifications. The production implementation
// shells out to osascript (macOS) or notify-send (Linux). Tests replace it
// with a no-op or recorder.
type notifier interface {
	Notify(title, body string)
}

// desktopNotifier is the production notifier backed by osascript / notify-send.
type desktopNotifier struct{}

func (desktopNotifier) Notify(title, body string) { sendDesktopNotify(title, body) }

// Cfg configures a Term widget. All fields are optional.
type Cfg struct {
	// TextStyle overrides the default monospace text style. When set to
	// the zero value, the widget falls back to gui.CurrentTheme().M5.
	// To use a custom style you must set at least one field (typically
	// Size or Typeface) — a zero-value TextStyle is treated as "unset."
	TextStyle gui.TextStyle

	// OnTitle, if non-nil, receives OSC 0/1/2 window-title updates
	// on the main goroutine. When nil, the widget calls
	// win.SetTitle directly, which is appropriate for standalone
	// single-Term windows. Embedders that manage their own title bar
	// (or multiple Term instances) should set OnTitle to capture
	// per-terminal titles.
	OnTitle func(string)

	// OnNotify, if non-nil, is called for OSC 9 / OSC 777 desktop
	// notification requests. title may be empty (OSC 9). When nil,
	// the widget fires a native OS notification via osascript (macOS)
	// or notify-send (Linux). Called on a background goroutine — safe
	// to block.
	OnNotify func(title, body string)

	// CursorBlink, if non-nil, overrides the application's DECSCUSR
	// blink request. Use *true to force blinking on, *false to force
	// steady. Leave nil to honor whatever the shell asks for (steady
	// by default for a brand-new grid).
	CursorBlink *bool

	// OnExit, if non-nil, is called when the child process exits.
	// Runs on the reader goroutine — fire a goroutine for any slow
	// work (e.g. calling Term.Close on the main thread via QueueCommand).
	OnExit func()

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
	// the child inherits os.Environ() plus TERM=xterm-256color. Entries
	// are appended after the inherited environment, so they override
	// inherited values. Use "KEY=" (trailing equals) to unset.
	Env []string

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

	// BellFlashDuration overrides how long the visual-bell overlay stays
	// visible. Zero (default) uses the built-in 100 ms. Negative disables
	// the visual bell entirely.
	BellFlashDuration time.Duration

	// ScrollbarWidth overrides the pixel width of the scrollbar thumb.
	// Zero (default) uses the built-in 4 px. Negative hides the scrollbar.
	ScrollbarWidth float32

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
}

// NamedTheme pairs a display name with a Theme for use in menus.
type NamedTheme struct {
	Name  string
	Theme Theme
}

// cursorBlinkPeriod is the half-cycle duration: cursor visible for
// blinkPeriod, then hidden for blinkPeriod. 500 ms matches xterm
// defaults.
const cursorBlinkPeriod = 500 * time.Millisecond

// defaultScrollbackRows is the cap applied when Cfg.ScrollbackRows == 0.
const defaultScrollbackRows = 5000

// resizeDebounce is the minimum stable interval before a pending size
// change is actually applied to the grid + pty. Picked to be longer
// than a single 60Hz frame (16.7 ms) so a continuous mouse drag never
// triggers a reflow mid-gesture, but short enough that the post-drag
// apply feels instant.
const resizeDebounce = 50 * time.Millisecond

// bellFlashDuration is the default visual-bell flash duration.
// Override via Cfg.BellFlashDuration; see effectiveBellDuration().
const bellFlashDuration = 100 * time.Millisecond

// maxBellDuration caps the user-configurable BellFlashDuration so that
// arithmetic (d + time.Millisecond) cannot overflow time.Duration.
const maxBellDuration = 5 * time.Second

// scrollbarWidth is the pixel width of the scrollbar thumb.
const scrollbarWidth float32 = 4

// scrollbarDuration is how long the scrollbar stays visible after the last
// scroll event while the viewport is back at the live bottom.
const scrollbarDuration = 1500 * time.Millisecond

// resizeState coalesces live-resize requests so continuous mouse-drag
// frames don't trigger a full grid reflow (and its allocations) on every
// redraw. The actual resize is deferred until the target dims have been
// stable for resizeDebounce. Main-thread only (onDraw is the sole writer).
type resizeState struct {
	pendingSince time.Time
	timer        *time.Timer // wakes main thread to apply after debounce
	pendingRows  int
	pendingCols  int
}

// imeState holds transient IME composition and widget-position state.
// Updated from the main thread: onAmendLayout sets layoutX/Y during
// layout, View reads the window's IME state for change detection, and
// onDraw reads layoutX/Y for IMESetRect.
type imeState struct {
	compText         string  // cached composition text for change detection
	compCursor       int     // cached composition cursor position
	layoutX, layoutY float32 // widget absolute position (AmendLayout)
	composing        bool    // whether IME composition is active
}

// momentumState drives the two-phase friction deceleration after a trackpad
// scroll gesture ends. vel/coasting protected by mu; timer and kick owned
// by the GUI goroutine (onMouseScroll) except for the timer callback which
// only touches mu-protected fields.
type momentumState struct {
	kick     chan struct{} // buffered 1; wakes momentumLoop
	timer    *time.Timer   // reset on each scroll; fires kickMomentum
	vel      float64       // EMA of recent scroll deltas (pixels)
	mu       sync.Mutex
	cellH    float32 // cellH snapshot at last scroll event
	coasting bool    // true while goroutine is decelerating
}

// searchState holds the interactive search bar state. All fields accessed
// on the GUI goroutine only (onChar, onKeyDown, onDraw) — no lock required.
type searchState struct {
	reErr      error
	re         *regexp.Regexp
	query      string
	cacheQuery string
	matches    []searchMatch // viewport matches refreshed each onDraw
	idx        int           // index of last jump target in matches
	cacheVer   uint64        // last drawVersion for which matches was computed
	active     bool
	regex      bool // true: match via re instead of plain text
	cacheRegex bool
}

// bellState tracks visual-bell flash timing. flashUntil is written from
// the QueueCommand callback (main thread) and read in onDraw (main thread).
// readCount is only accessed from readLoop. flashTimer is managed from the
// QueueCommand callback and stopped in Close.
type bellState struct {
	flashUntil time.Time
	flashTimer *time.Timer // reused per-BEL clear timer; lazy init
	seenCount  uint64
	readCount  uint64
}

// scrollbarState manages the auto-hide scrollbar thumb timer. Main-thread
// only (created lazily in showScrollbar). The until field is read under
// grid.Mu in drawOverlays but written without Mu in showScrollbar; both
// call sites are main-thread-only so this is not a race, but future
// refactors should keep showScrollbar on the main thread or switch to
// an atomic.Time.
type scrollbarState struct {
	until time.Time
	timer *time.Timer
}

// mouseState tracks pointer state for selection drags, host-side mouse
// reports, and hyperlink hover highlighting. All fields on the GUI main
// thread. hoverR/hoverC are atomic for race-detector safety in case the
// framework ever dispatches callbacks concurrently.
type mouseState struct {
	dragging   bool
	dragButton gui.MouseButton
	dragReport bool // true when this drag is being reported to the pty
	lastR      int  // dedupe motion reports under ?1003
	lastC      int
	hoverR     atomic.Int32 // sentinel -1 = not yet set
	hoverC     atomic.Int32
}

// drawBufs holds per-frame scratch buffers reused across onDraw calls.
// All fields are main-thread only.
type drawBufs struct {
	runBuf      strings.Builder
	runeCache   map[rune]string // caches string(r) for non-ASCII runes
	vMatchBuf   [][]vMatch      // pre-allocated search-highlight rows
	selBuf      []rowBounds     // pre-allocated selection-bound rows
	bidiVisRows [][]cell        // visual-reordered rows for current frame
	bidiV2LRows [][]int         // visual→logical column maps
	bidiScratch []cell          // reused per-row cell buffer for BiDi pre-pass
}

// Term is a terminal-emulator widget bound to a single pty-backed shell.
// Use New to construct, View to embed in a layout, Close to tear down.
type Term struct {
	cfg Cfg

	// embedded grouped state — see each struct's doc comment.
	resize    resizeState
	bell      bellState
	momentum  momentumState
	scrollbar scrollbarState

	// cursorEpoch is the reference time for blink-phase calculation.
	// Set in New so the cursor starts in the "on" half-cycle.
	cursorEpoch time.Time

	// pw writes bytes to the pty slave. In production this is the *ptyDev
	// itself (*os.File satisfies io.Writer). Tests replace it with a buffer
	// sink so key/focus behavior can be asserted without a live pty.
	pw io.Writer

	// cmd schedules callbacks on the GUI main thread. In production this is
	// the *gui.Window itself. Tests replace it with a synchronous executor.
	cmd cmdScheduler

	// notif sends desktop notifications (OSC 9 / OSC 777). Production uses
	// desktopNotifier (osascript / notify-send). Tests replace with a no-op.
	notif notifier

	grid   *grid
	parser *parser
	pty    *ptyDev

	// win is the *gui.Window this Term is bound to. Stored so Close can
	// restore the original OnEvent handler and prevent the handler chain
	// from leaking closed-Term closures. Only accessed on the main thread.
	win *gui.Window

	// prevOnEvent is the original Window.OnEvent handler before this Term
	// wrapped it. Restored in Close so that creating and destroying Terms
	// (e.g. closing a pane) does not leak closures in the dispatch chain.
	// Correct for LIFO close order; a pane manager managing multiple live
	// Terms should set NoWindowHandler and install its own
	// dispatcher rather than rely on chaining.
	prevOnEvent func(*gui.Event, *gui.Window)

	// blinkDone signals the blink ticker goroutine to exit. Closed by Close.
	blinkDone chan struct{}

	// readDone is closed by readLoop when it exits. Close waits on it so
	// no further cmd.QueueCommand calls can arrive after Close returns.
	readDone chan struct{}

	// canvasID is a unique per-Term identifier used as the DrawCanvas ID
	// so multiple terminals in the same window don't collide in go-gui's
	// tessellation cache.
	canvasID string

	// gfxDir is a per-Term scratch directory for Sixel-decoded PNGs.
	// Created lazily in New; removed (best-effort) in Close so a long
	// session that prints many graphics doesn't pollute /tmp forever.
	gfxDir string

	draw drawBufs

	search searchState

	// ime tracks IME composition state and widget position for
	// candidate-window placement. See imeState doc.
	ime imeState

	// themeMenuItems is the precomputed ContextMenu item list for runtime
	// theme switching. Built once in New; nil when no themes are configured.
	themeMenuItems []gui.MenuItemCfg

	// pendingReplies buffers parser-originated reply bytes (DA, DECRQSS,
	// XTGETTCAP, ...) emitted during parser.Feed. Drained by readLoop
	// after grid.Mu is released so pw.Write (which can block when the
	// pty slave-side input buffer is full) cannot deadlock against
	// onDraw waiting for the same lock. Owned by the readLoop goroutine
	// — append (via onParserReply called from inside Feed) and drain
	// both happen there.
	pendingReplies [][]byte
	mouse          mouseState

	// loopWg tracks the three auxiliary goroutines (blink, autoScroll,
	// momentum) so Close can wait for them to exit before tearing down
	// state they may still reference.
	loopWg sync.WaitGroup

	// drawVersion is incremented on every visual state change so that
	// go-gui's DrawCanvas tessellation cache can skip OnDraw on unchanged
	// frames. Reads happen on the main thread (View); writes happen on
	// both the main thread and the reader goroutine, hence atomic.
	drawVersion atomic.Uint64

	// cell metrics measured on first draw and reused thereafter. Both
	// zero until the first OnDraw.
	cellW, cellH float32

	// focused is set by a pane manager via SetFocused to control whether
	// this terminal claims IDFocus. Defaults to true in New so a
	// standalone Term (no pane manager) works without extra setup.
	focused atomic.Bool

	// focusID is a unique per-Term IDFocus value so multiple terminals
	// in the same window don't compete for the same focus slot.
	focusID uint32

	// closed guards Close so multiple calls are safe.
	closed atomic.Bool

	// notifyBusy prevents goroutine pile-up from rapid OSC 9/777 sequences.
	// Only one notification runs at a time; extras are dropped.
	notifyBusy atomic.Bool

	// autoScrollDir drives the selection auto-scroll goroutine during a
	// drag that extends outside the widget (-1 = toward live,
	// +1 = into scrollback, 0 = no scroll). Written on the main
	// thread; read in autoScrollLoop — atomic for safety.
	autoScrollDir atomic.Int32
}

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

// buildThemeMenu builds a right-click context menu from cfg.Themes.
// Returns nil when no themes are configured.
func buildThemeMenu(cfg Cfg) []gui.MenuItemCfg {
	if len(cfg.Themes) == 0 {
		return nil
	}
	items := make([]gui.MenuItemCfg, 0, len(cfg.Themes)+1)
	items = append(items, gui.MenuSubtitle("Theme"))
	for i, nt := range cfg.Themes {
		items = append(items, gui.MenuItemCfg{ID: strconv.Itoa(i), Text: nt.Name})
	}
	return items
}

// applyTheme sets the initial grid theme from cfg.Themes. When no
// themes are configured the grid keeps its zero-value Theme.
func applyTheme(g *grid, cfg Cfg) {
	if len(cfg.Themes) > 0 {
		g.Theme = cfg.Themes[0].Theme
	}
}

// New starts a shell in a pty and returns a Term widget. The reader
// goroutine and auxiliary loops (blink, auto-scroll, momentum) are
// spawned before New returns. Call Close to tear down.
func New(w *gui.Window, cfg Cfg) (*Term, error) {
	const initRows, initCols = 24, 80
	pty, err := startPTY(initRows, initCols, cfg)
	if err != nil {
		return nil, err
	}
	g := newGrid(initRows, initCols)
	applyTheme(g, cfg)
	applyScrollbackConfig(g, cfg)
	themeMenuItems := buildThemeMenu(cfg)
	seqID := termSeq.Add(1)
	t := &Term{
		cfg:            cfg,
		grid:           g,
		parser:         newParser(g),
		pty:            pty,
		pw:             pty,
		cmd:            w,
		notif:          desktopNotifier{},
		cursorEpoch:    time.Now(),
		blinkDone:      make(chan struct{}),
		readDone:       make(chan struct{}),
		focusID:        uint32(seqID),
		canvasID:       "term-canvas-" + strconv.FormatUint(seqID, 10),
		themeMenuItems: themeMenuItems,
	}
	t.win = w
	t.mouse.lastR = -1
	t.mouse.lastC = -1
	t.momentum.kick = make(chan struct{}, 1)
	t.mouse.hoverR.Store(-1)
	t.mouse.hoverC.Store(-1)
	if !cfg.DisableGraphics {
		if dir, err := os.MkdirTemp("", "go-term-gfx-*"); err == nil {
			t.gfxDir = dir
			t.parser.SetGraphicsDir(dir)
		}
	}
	t.parser.SetTitleHandler(t.onParserTitle)
	t.parser.SetReplyHandler(t.onParserReply)
	t.parser.SetClipboardWriteAllowed(cfg.AllowOSC52Write)
	if cfg.AllowOSC52Write {
		t.parser.SetClipboardHandler(func(data []byte) {
			text := string(data)
			t.queueCommand(func(w *gui.Window) {
				w.SetClipboard(text)
			})
		})
	}
	t.registerNotifyHandler()
	if !cfg.NoWindowHandler {
		t.prevOnEvent = w.OnEvent
		w.OnEvent = func(e *gui.Event, w *gui.Window) {
			t.HandleWindowEvent(e)
			if t.prevOnEvent != nil {
				t.prevOnEvent(e, w)
			}
		}
	}
	t.focused.Store(true)
	w.SetIDFocus(t.focusID)
	go t.readLoop()
	t.loopWg.Add(3)
	go t.blinkLoop()
	go t.autoScrollLoop()
	go t.momentumLoop()
	return t, nil
}

// recoverLoop logs and suppresses panics in background goroutines so a
// single goroutine crash does not take down the whole process.
func recoverLoop(name string) {
	if r := recover(); r != nil {
		log.Printf("term: panic in %s: %v", name, r)
	}
}

// blinkLoop wakes every cursorBlinkPeriod and forces a redraw when the
// cursor is currently blinking + visible at the live viewport. Other
// states (steady cursor, scrolled-back view, hidden cursor) need no
// periodic redraw and the loop simply skips.
func (t *Term) blinkLoop() {
	defer t.loopWg.Done()
	defer recoverLoop("blinkLoop")
	tk := time.NewTicker(cursorBlinkPeriod)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-tk.C:
			redraw := func() bool {
				t.grid.Mu.Lock()
				defer t.grid.Mu.Unlock()
				return t.grid.CursorVisible &&
					t.grid.ViewOffset == 0 && t.grid.ViewSubPx == 0 &&
					t.cursorBlinks()
			}()
			if redraw {
				t.bumpVersion()
				t.queueCommand(func(w *gui.Window) {
					w.UpdateWindow()
				})
			}
		}
	}
}

// autoScrollLoop scrolls the viewport while autoScrollDir is non-zero.
// Handles the case where onMouseMove stops firing when the mouse leaves
// the window (e.g. above the title bar). Exits when blinkDone is closed.
func (t *Term) autoScrollLoop() {
	defer t.loopWg.Done()
	defer recoverLoop("autoScrollLoop")
	const rate = 80 * time.Millisecond
	tk := time.NewTicker(rate)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-tk.C:
			dir := int(t.autoScrollDir.Load())
			if dir == 0 {
				continue
			}
			func() {
				t.grid.Mu.Lock()
				defer t.grid.Mu.Unlock()
				t.grid.ScrollView(dir)
			}()
			t.bumpVersion()
			t.queueCommand(func(w *gui.Window) {
				t.showScrollbar()
				w.UpdateWindow()
			})
		}
	}
}

// cursorBlinks reports whether the cursor should currently blink,
// honoring the Cfg.CursorBlink override over the grid's DECSCUSR
// state. Caller holds grid.Mu.
func (t *Term) cursorBlinks() bool {
	if t.cfg.CursorBlink != nil {
		return *t.cfg.CursorBlink
	}
	return t.grid.CursorBlink
}

// registerNotifyHandler wires the OSC 9 / OSC 777 notification path.
// Extracted so tests can reuse the same handler without copy-paste drift.
func (t *Term) registerNotifyHandler() {
	t.parser.SetNotifyHandler(func(title, body string) {
		if !t.notifyBusy.CompareAndSwap(false, true) {
			return
		}
		fn := t.cfg.OnNotify
		go func() {
			defer t.notifyBusy.Store(false)
			if fn != nil {
				fn(title, body)
			} else {
				t.notif.Notify(title, body)
			}
		}()
	})
}

// onParserTitle is the OSC 0/1/2 handler. Runs on the reader goroutine
// while grid.Mu is held — must not touch *gui.Window state directly,
// hence the QueueCommand hop.
func (t *Term) onParserTitle(title string) {
	fn := t.cfg.OnTitle
	t.queueCommand(func(w *gui.Window) {
		if fn != nil {
			fn(title)
			return
		}
		w.SetTitle(title)
	})
}

// scheduleResizeWake arms (or resets) a one-shot timer that bumps the
// draw version and asks go-gui for a redraw after d. Used by onDraw
// when a resize is being debounced — without this, no further onDraw
// would fire once the mouse stops moving and the debounced size would
// never be applied. Main-thread only (called from inside onDraw under
// grid.Mu, but only mutates resizeTimer which is main-thread owned).
func (t *Term) scheduleResizeWake(d time.Duration) {
	t.scheduleDelayedUpdate(d, &t.resize.timer)
}

// onParserReply queues parser-originated bytes (e.g. DA1 reply) for
// writing back to the pty after readLoop releases grid.Mu. Called from
// inside parser.Feed which runs under Mu on the readLoop goroutine —
// writing to the pty directly here would risk a deadlock when the
// slave-side input buffer is full (shell not reading), since onDraw on
// the main thread would be blocked waiting for the same Mu.
func (t *Term) onParserReply(b []byte) {
	if len(b) == 0 {
		return
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	t.pendingReplies = append(t.pendingReplies, cp)
}

// sendDesktopNotify fires a native OS notification. Blocks briefly
// (subprocess exec), so always call from a goroutine. Null bytes are
// removed to prevent truncation of C-string args at the syscall boundary.
func sendDesktopNotify(title, body string) {
	clean := func(s string) string { return strings.ReplaceAll(s, "\x00", "") }
	switch runtime.GOOS {
	case "darwin":
		// Pass title/body as argv to avoid AppleScript string-literal injection.
		stmt := `display notification (item 1 of argv)`
		args := []string{"-e", "on run argv", "-e", stmt, "-e", "end run", clean(body)}
		if title != "" {
			stmt = `display notification (item 1 of argv) with title (item 2 of argv)`
			args = []string{"-e", "on run argv", "-e", stmt, "-e", "end run", clean(body), clean(title)}
		}
		exec.Command("osascript", args...).Run() //nolint:errcheck
	case "linux":
		args := []string{clean(body)}
		if title != "" {
			args = []string{clean(title), clean(body)}
		}
		exec.Command("notify-send", args...).Run() //nolint:errcheck
	}
}

// flushPendingReplies writes all queued parser replies to the pty.
// Called by readLoop after grid.Mu is released. Errors are logged and
// the queue is drained even on partial failure.
func (t *Term) flushPendingReplies() {
	if len(t.pendingReplies) == 0 {
		return
	}
	pending := t.pendingReplies
	t.pendingReplies = nil
	for _, b := range pending {
		if _, err := t.pw.Write(b); err != nil {
			log.Printf("term: pty reply: %v", err)
		}
	}
}

// HandleWindowEvent processes window-level events that the Term needs to
// see: momentum cancellation on mouse-down/trackpad-touch, and focus-
// reporting sequences (CSI I / CSI O) when the shell has enabled focus
// reporting (DECSET ?1004). A pane manager calls this on the focused Term
// when the window dispatches an event. When [Cfg.NoWindowHandler] is false
// (the standalone default), New installs a wrapper that calls this
// automatically via w.OnEvent chaining.
func (t *Term) HandleWindowEvent(e *gui.Event) {
	if e == nil || t.closed.Load() {
		return
	}
	// Cancel momentum on mouse press or trackpad touch. EventScrollBegan
	// fires when a finger first contacts the trackpad (zero-delta phase),
	// giving immediate cancellation before any scroll delta arrives.
	if e.Type == gui.EventMouseDown || e.Type == gui.EventScrollBegan {
		t.cancelMomentum()
	}
	var report []byte
	t.grid.Mu.Lock()
	focus := t.grid.FocusReporting
	t.grid.Mu.Unlock()
	if !focus {
		return
	}
	switch e.Type {
	case gui.EventFocused:
		report = []byte("\x1b[I")
	case gui.EventUnfocused:
		report = []byte("\x1b[O")
	default:
		return
	}
	if _, err := t.pw.Write(report); err != nil {
		log.Printf("term: pty focus report: %v", err)
	}
}

// queueCommand wraps t.cmd.QueueCommand with a closed-Term guard: if
// Close has already been called the callback is silently dropped. All
// background goroutines that schedule work on the GUI thread should
// use this instead of calling t.cmd.QueueCommand directly.
func (t *Term) queueCommand(fn func(*gui.Window)) {
	t.cmd.QueueCommand(func(w *gui.Window) {
		if t.closed.Load() {
			return
		}
		fn(w)
	})
}

// Rows returns the current grid row count. Safe to call from any
// goroutine.
func (t *Term) Rows() int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Rows
}

// Cols returns the current grid column count. Safe to call from any
// goroutine.
func (t *Term) Cols() int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Cols
}

// Write sends p to the PTY as if typed by the user. Useful for
// restoring CWD, running startup commands, or scripting input.
// Safe to call from any goroutine.
func (t *Term) Write(p []byte) (int, error) {
	return t.pw.Write(p)
}

// Cwd returns the most recent working directory reported via OSC 7,
// or "" if the shell has never emitted one. Typical payload format
// is "file://host/path"; embedders parse as needed.
func (t *Term) Cwd() string {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Cwd
}

// PID returns the child process ID, or 0 if the PTY is not started.
func (t *Term) PID() int {
	if t.pty == nil || t.pty.cmd == nil || t.pty.cmd.Process == nil {
		return 0
	}
	return t.pty.cmd.Process.Pid
}

// Alive reports whether the child process is still running. Returns
// false after the PTY reader goroutine exits (process death or Close).
func (t *Term) Alive() bool {
	if t.readDone == nil {
		return false
	}
	select {
	case <-t.readDone:
		return false
	default:
		return true
	}
}

// Theme returns the active color theme. Safe to call from any goroutine.
func (t *Term) Theme() Theme {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Theme
}

// SetTheme replaces the active color theme and schedules a redraw.
// Safe to call from the main thread at any time.
func (t *Term) SetTheme(th Theme) {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	t.grid.Theme = th
	t.bumpVersion()
}

// SetFocused sets whether this terminal has pane focus. The pane
// manager calls this when the user switches between panes. When
// focused, the container claims IDFocus (so go-gui routes keystrokes
// here) and the cursor renders normally. When unfocused, the cursor
// is dimmed. New defaults to focused=true for standalone use.
func (t *Term) SetFocused(v bool) {
	if t.focused.Swap(v) == v {
		return // no change
	}
	if v && t.cmd != nil {
		t.queueCommand(func(w *gui.Window) {
			w.SetIDFocus(t.focusID)
		})
	}
	t.bumpVersion()
}

// termSeq provides unique per-Term identifiers (canvas IDs etc.).
var termSeq atomic.Uint64

// onAmendLayout updates the Term's recorded absolute position when layout changes.
func (t *Term) onAmendLayout(l *gui.Layout, _ *gui.Window) {
	if l == nil {
		return
	}
	var x, y float32
	if len(l.Children) > 0 && l.Children[0].Shape != nil {
		x = l.Children[0].Shape.X
		y = l.Children[0].Shape.Y
	} else if l.Shape != nil {
		x = l.Shape.X
		y = l.Shape.Y
	}
	if realNumber(x) {
		t.ime.layoutX = x
	}
	if realNumber(y) {
		t.ime.layoutY = y
	}
}

// View returns the go-gui view tree for this terminal. Usable as a
// gui.Window UpdateView generator: w.UpdateView(t.View).
func (t *Term) View(w *gui.Window) gui.View {
	// Detect IME composition state changes and bump version to redraw.
	composing := w.IMEComposing()
	compText := w.IMECompText()
	compCursor := w.IMECompCursor()
	if composing != t.ime.composing || compText != t.ime.compText || compCursor != t.ime.compCursor {
		t.ime.composing = composing
		t.ime.compText = compText
		t.ime.compCursor = compCursor
		t.bumpVersion()
	}

	ww, wh := w.WindowSize()
	// Snapshot theme default-bg under the lock so a concurrent SetTheme
	// does not race with this read. The rest of View() is lock-free.
	t.grid.Mu.Lock()
	bgColor := t.grid.Theme.DefaultBG
	t.grid.Mu.Unlock()
	canvas := gui.DrawCanvas(gui.DrawCanvasCfg{
		ID:            t.canvasID,
		Version:       t.drawVersion.Load(),
		Sizing:        gui.FillFill,
		OnDraw:        t.onDraw,
		OnMouseScroll: t.onMouseScroll,
		OnClick:       t.onClick,
		OnMouseMove:   t.onMouseMove,
		OnMouseUp:     t.onMouseUp,
	})
	colCfg := gui.ContainerCfg{
		Padding:     gui.Some(gui.Padding{}),
		Spacing:     gui.SomeF(0),
		Color:       bgColor,
		OnChar:      t.onChar,
		OnKeyDown:   t.onKeyDown,
		OnKeyUp:     t.onKeyUp,
		AmendLayout: t.onAmendLayout,
		Content:     []gui.View{canvas},
	}
	if t.focused.Load() {
		colCfg.IDFocus = t.focusID
	}
	if len(t.themeMenuItems) > 0 {
		colCfg.Width = float32(ww)
		colCfg.Height = float32(wh)
		colCfg.Sizing = gui.FillFill
		return gui.ContextMenu(w, gui.ContextMenuCfg{
			ID:      "term-theme-menu",
			Width:   float32(ww),
			Height:  float32(wh),
			Sizing:  gui.FixedFixed,
			Padding: gui.NoPadding,
			Items:   t.themeMenuItems,
			Action: func(id string, _ *gui.Event, w *gui.Window) {
				i, err := strconv.Atoi(id)
				if err != nil || i < 0 || i >= len(t.cfg.Themes) {
					return
				}
				t.SetTheme(t.cfg.Themes[i].Theme)
				w.UpdateWindow()
			},
			Content: []gui.View{gui.Column(colCfg)},
		})
	}
	colCfg.Sizing = gui.FillFill
	return gui.Column(colCfg)
}

// Close stops the shell, reader, and blink goroutine. Safe to call once;
// subsequent calls are no-ops. Must be called from the GUI main thread so
// that pending QueueCommand callbacks and resizeTimer fire on the same
// goroutine that owns them.
func (t *Term) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	close(t.blinkDone)
	err := t.pty.Close() // signals readLoop to exit via read error
	// Wait for readLoop to drain, but don't hang forever if the pty fd
	// is in a degraded state where close doesn't unblock an in-progress
	// read. When this timeout fires, readLoop may still be alive and
	// could call cmd.QueueCommand after we return. Callers must ensure
	// the window outlives any such late callback, or call Close only
	// from the main thread immediately before window teardown.
	readTimer := time.NewTimer(2 * time.Second)
	defer readTimer.Stop()
	select {
	case <-t.readDone:
	case <-readTimer.C:
	}
	// Wait for auxiliary goroutines to exit cleanly so they cannot
	// reference t.cmd or other state after we return.
	t.loopWg.Wait()
	if t.resize.timer != nil {
		t.resize.timer.Stop()
	}
	if t.scrollbar.timer != nil {
		t.scrollbar.timer.Stop()
	}
	if t.bell.flashTimer != nil {
		t.bell.flashTimer.Stop()
	}
	if t.gfxDir != "" {
		if err := os.RemoveAll(t.gfxDir); err != nil {
			log.Printf("term: gfx dir cleanup: %v", err)
		}
	}
	// Restore the window's original OnEvent handler so this Term's
	// closure does not leak in the dispatch chain. Skip when
	// NoWindowHandler was set (prevOnEvent is nil) — the pane
	// manager owns the dispatch in that case.
	if t.prevOnEvent != nil && t.win != nil {
		t.win.OnEvent = t.prevOnEvent
	}
	return err
}

// readLoop forwards pty output through the parser and schedules a
// render. Exits when the pty is closed or returns EOF.
func (t *Term) readLoop() {
	defer close(t.readDone)
	defer recoverLoop("readLoop")
	defer func() {
		if fn := t.cfg.OnExit; fn != nil {
			func() {
				defer recoverLoop("OnExit")
				fn()
			}()
		}
	}()
	buf := make([]byte, 4096)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			bellCount, redraw, dirty := func() (uint64, bool, bool) {
				t.grid.Mu.Lock()
				defer t.grid.Mu.Unlock()
				t.parser.Feed(buf[:n])
				bellCount := t.grid.BellCount
				redraw := !t.grid.SyncOutput || !t.grid.SyncActive
				// Gate the version bump on actual visual changes: cell mutations
				// (HasDirtyRows) or a new BEL (which marks no cells but needs a
				// flash). Pure no-op sequences (swallowed queries, etc.) skip the
				// version bump so the tessellation cache stays valid.
				dirty := t.grid.HasDirtyRows() || bellCount != t.bell.readCount
				if redraw && dirty {
					t.bell.readCount = bellCount
					t.bumpVersion()
				}
				return bellCount, redraw, dirty
			}()
			// Drain parser replies (DA, DECRQSS, ...) outside Mu so a
			// blocking pty.Write (slave-side input buffer full) cannot
			// stall onDraw waiting for the same lock.
			t.flushPendingReplies()
			if redraw && dirty {
				t.queueCommand(func(w *gui.Window) {
					if bellCount > t.bell.seenCount {
						t.bell.seenCount = bellCount
						if d := t.effectiveBellDuration(); d > 0 {
							t.bell.flashUntil = time.Now().Add(d)
							t.scheduleBellClear(d)
						}
					}
					w.UpdateWindow()
				})
			}
		}
		if err != nil {
			return
		}
	}
}

// style returns the resolved text style for this terminal.
func (t *Term) style() gui.TextStyle {
	if t.cfg.TextStyle != (gui.TextStyle{}) {
		return t.cfg.TextStyle
	}
	return gui.CurrentTheme().M5
}

// effectiveBellDuration returns the configured visual-bell duration,
// falling back to the default when unset. Negative disables the flash.
func (t *Term) effectiveBellDuration() time.Duration {
	if t.cfg.BellFlashDuration < 0 {
		return 0
	}
	if t.cfg.BellFlashDuration > 0 {
		return min(t.cfg.BellFlashDuration, maxBellDuration)
	}
	return bellFlashDuration
}

// scheduleDelayedUpdate lazily creates or resets a *time.Timer field so
// that tmr fires after d, bumps the draw version, and schedules a window
// repaint. Used by scheduleBellClear, showScrollbar, and scheduleResizeWake
// to avoid duplicating the after-func / guard / bump / queue pattern.
// Safe to call from any goroutine; the callback checks closed before
// queueing work.
func (t *Term) scheduleDelayedUpdate(d time.Duration, tmr **time.Timer) {
	if *tmr == nil {
		*tmr = time.AfterFunc(d, func() {
			if t.closed.Load() || t.cmd == nil {
				return
			}
			t.bumpVersion()
			t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
		})
	} else {
		(*tmr).Reset(d)
	}
}

// scheduleBellClear schedules a redraw to clear the visual-bell flash
// overlay after d. Uses a single reusable timer so rapid BEL sequences
// don't accumulate goroutines. Safe to call from the QueueCommand
// callback (main thread).
func (t *Term) scheduleBellClear(d time.Duration) {
	// Guard against overflow from a misconfigured (or malicious) duration.
	// effectiveBellDuration already clamps to maxBellDuration, so this is
	// defense-in-depth — the arithmetic is safe even if called directly.
	if d > maxBellDuration {
		d = maxBellDuration
	}
	t.scheduleDelayedUpdate(d+time.Millisecond, &t.bell.flashTimer)
}

// effectiveScrollbarWidth returns the configured scrollbar pixel width,
// falling back to the default when unset. Negative or NaN hides the
// scrollbar; +Inf is clamped to 0 (hidden) so it doesn't propagate
// into draw calls.
func (t *Term) effectiveScrollbarWidth() float32 {
	if !realNumber(t.cfg.ScrollbarWidth) {
		return 0 // NaN, Inf → hidden
	}
	if t.cfg.ScrollbarWidth < 0 {
		return 0
	}
	if t.cfg.ScrollbarWidth > 0 {
		return t.cfg.ScrollbarWidth
	}
	return scrollbarWidth
}

// bumpVersion increments drawVersion so the next View call produces a
// new cache key, forcing go-gui to re-invoke OnDraw for this frame.
func (t *Term) bumpVersion() { t.drawVersion.Add(1) }

func (t *Term) writeBytes(out []byte) {
	if _, err := t.pw.Write(out); err != nil {
		log.Printf("term: pty write: %v", err)
	}
}
