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

	// CursorBlink, if non-nil, overrides the application's DECSCUSR
	// blink request. Use *true to force blinking on, *false to force
	// steady. Leave nil to honor whatever the shell asks for (steady
	// by default for a brand-new grid).
	CursorBlink *bool

	// OnExit, if non-nil, is called when the child process exits.
	// Runs on the reader goroutine — fire a goroutine for any slow
	// work (e.g. calling Term.Close on the main thread via QueueCommand).
	OnExit func()

	// OnClickFocus, if non-nil, is called when the user clicks on the
	// terminal canvas. Multi-Term embedders use this to switch focus to
	// the clicked pane. Runs synchronously during the click handler.
	OnClickFocus func()

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

	// Dir sets the working directory for the child process. When non-empty
	// and the path exists on disk, the shell starts there. Empty inherits
	// the process CWD.
	Dir string
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

// maxReplyQueueBytes caps the cumulative size of parser replies waiting to be
// written to the pty. The reader goroutine enqueues without blocking, so if the
// writer stalls (slave input buffer full) while an application keeps emitting
// queries without reading their replies, the queue would otherwise grow without
// bound. Past the cap, further replies are dropped — an application that floods
// queries without draining the responses is already pathological.
const maxReplyQueueBytes = 4 << 20 // 4 MiB

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

// bellState tracks visual-bell flash timing. flashUntil holds the UnixNano
// instant the flash ends (0 = no flash); it is written by applyChunk on the
// reader goroutine and read by onDraw on the main thread, hence atomic.
// seenCount/readCount are touched only by applyChunk (reader goroutine).
// flashTimer is reset by scheduleBellClear (reader goroutine) and stopped in
// Close, which first waits for readLoop to exit so the two never overlap.
type bellState struct {
	flashUntil atomic.Int64
	flashTimer *time.Timer // reused per-BEL clear timer; lazy init
	seenCount  uint64
	readCount  uint64
}

// scrollbarState manages the auto-hide scrollbar thumb timer plus the
// hit-test geometry needed to click/drag the thumb. Main-thread only
// (until/timer created lazily in showScrollbar; the geometry fields are
// written by drawOverlays and read by the mouse handlers, all on the GUI
// main thread). The until field is read under grid.Mu in drawOverlays but
// written without Mu in showScrollbar; both call sites are main-thread-only
// so this is not a race, but future refactors should keep showScrollbar on
// the main thread or switch to an atomic.Time.
type scrollbarState struct {
	until time.Time
	timer *time.Timer

	// Geometry recorded each frame by drawOverlays so the mouse handlers
	// can hit-test without a DrawContext. hitX0 is the left edge of the
	// clickable region (extends inward from the thumb, so the grabbable
	// area is wider than the drawn thumb while the thumb itself stays clear
	// of the OS window-resize band); viewH is the canvas height. active is
	// true when the thumb is interactive this frame (scrollback present and
	// scrollbar not hidden).
	hitX0  float32
	viewH  float32
	active bool

	// hovered is true when the pointer is over the scrollbar hit region
	// (track or thumb). Set by onMouseMove, consumed by drawOverlays to
	// brighten the thumb.
	hovered bool

	// dragging is true while the user holds the thumb; grabDy is the
	// pointer offset from the thumb top captured at grab time so the thumb
	// tracks the cursor without snapping its center to the pointer.
	dragging bool
	grabDy   float32
}

// mouseState tracks pointer state for selection drags, host-side mouse
// reports, and hyperlink hover highlighting. All fields on the GUI main
// thread. hoverR/hoverC are atomic for race-detector safety in case the
// framework ever dispatches callbacks concurrently.
type mouseState struct {
	dragging   bool
	dragButton gui.MouseButton
	dragReport bool // true when this drag is being reported to the pty
	// locked is true while a gui MouseLock is engaged. Lock callbacks
	// deliver absolute window coordinates; the same handlers registered on
	// the canvas deliver canvas-relative ones. This flag is what tells the
	// two apart — see toCanvasRel. Not every drag locks: a drag that is
	// being reported to the pty (?1000/?1002/?1003) leaves the pointer
	// unlocked, so keying off dragging would mis-translate those events.
	locked bool
	lastR  int // dedupe motion reports under ?1003
	lastC  int
	hoverR atomic.Int32 // sentinel -1 = not yet set
	hoverC atomic.Int32

	// Wheel-report accumulator (see wheelReportTicks): pixels of scroll
	// distance not yet emitted as an SGR wheel tick, and the direction
	// (+1 up / -1 down / 0 initial) it was accumulated in.
	wheelResidual float32
	wheelDir      int
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
	scrollbar scrollbarState

	// cursorEpoch is the reference time for blink-phase calculation.
	// Set in New so the cursor starts in the "on" half-cycle.
	cursorEpoch time.Time

	// pw writes bytes to the pty master. In production this is the ptyDev
	// itself (*ptyDev satisfies io.Writer). Tests replace it with a buffer
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
	pty    ptyIO

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

	// embedded grouped state — see each struct's doc comment.
	resize resizeState
	bell   bellState

	// pendingReplies buffers parser-originated reply bytes (DA, DECRQSS,
	// XTGETTCAP, ...) emitted during parser.Feed. Reader-goroutine local:
	// onParserReply appends during Feed, applyChunk hands the batch to the
	// reply writer (enqueueReplies) after grid.Mu is released, so no
	// synchronization is needed here.
	pendingReplies [][]byte

	// reply* form a single-producer/single-consumer queue feeding writeLoop,
	// the dedicated goroutine that writes parser replies to the pty. Replies
	// must NOT be written on the reader goroutine: a pty.Write blocks when
	// the slave-side input buffer fills (e.g. an application mid-write of a
	// large query batch that has not started reading replies yet). If the
	// reader blocked there it would stop draining the master, deadlocking
	// against the application's own blocked write. The reader enqueues under
	// replyMu without doing I/O and returns to draining; writeLoop performs
	// the blocking writes off both the reader and the render loop, so a
	// query/response round-trip still costs microseconds, not a vsync frame.
	replyMu    sync.Mutex
	replyCond  *sync.Cond // signaled when replyQueue grows or replyDone is set
	replyQueue [][]byte   // guarded by replyMu
	replyBytes int        // guarded by replyMu; cumulative len of replyQueue, capped at maxReplyQueueBytes
	replyDone  bool       // guarded by replyMu; set by Close to stop writeLoop

	momentum momentumState

	// ime tracks IME composition state and widget position for
	// candidate-window placement. See imeState doc.
	ime imeState

	cfg Cfg

	search searchState

	mouse mouseState

	// loopWg tracks the auxiliary goroutines (blink, autoScroll, momentum,
	// reply writer) so Close can wait for them to exit before tearing down
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

	// fontSize is the effective font size in points. Zero means "use the
	// configured TextStyle.Size." Adjusted via Cmd+=/Cmd+- and clamped to
	// [4, 72] pt.
	fontSize float32

	// focused is set by a pane manager via SetFocused to control whether
	// this terminal claims keyboard focus. Defaults to true in New so a
	// standalone Term (no pane manager) works without extra setup.
	focused atomic.Bool

	// focusID is a unique per-Term focus ID so multiple terminals
	// in the same window don't compete for the same focus slot.
	focusID string

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

	// ptyResizeRows/Cols carry a pending TIOCSWINSZ from onDraw to
	// readLoop. onDraw writes them under grid.Mu and sets pending.
	// readLoop consumes them before its next Read call so the ioctl
	// never runs on the main thread — avoids deadlock with the SDL
	// event queue during macOS live-resize.
	ptyResizePending atomic.Bool
	ptyResizeRows    atomic.Int32
	ptyResizeCols    atomic.Int32

	// redrawPending coalesces UpdateWindow requests from the reader
	// goroutine: applyChunk only queues a redraw command when one is not
	// already in flight, so a burst of PTY reads between frames does not
	// pile up redundant closures on the command queue. Cleared by the
	// queued callback before it asks for the repaint.
	redrawPending atomic.Bool
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
// ThemeMenuItems returns a ContextMenu item list for the given themes.
// Returns nil when themes is empty. Multi-Term embedders use this to
// attach a theme menu at the appropriate level of their view tree.
func ThemeMenuItems(themes []NamedTheme) []gui.MenuItemCfg {
	if len(themes) == 0 {
		return nil
	}
	items := make([]gui.MenuItemCfg, 0, len(themes)+1)
	items = append(items, gui.MenuSubtitle("Theme"))
	for i, nt := range themes {
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
	seqID := termSeq.Add(1)
	t := &Term{
		cfg:         cfg,
		grid:        g,
		parser:      newParser(g),
		pty:         pty,
		pw:          pty,
		cmd:         w,
		notif:       desktopNotifier{},
		cursorEpoch: time.Now(),
		blinkDone:   make(chan struct{}),
		readDone:    make(chan struct{}),
		focusID:     "term-" + strconv.FormatUint(seqID, 10),
		canvasID:    "term-canvas-" + strconv.FormatUint(seqID, 10),
	}
	if s := t.style(); s.Size > 0 {
		t.fontSize = s.Size
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
	w.SetFocus(t.focusID)
	t.replyCond = sync.NewCond(&t.replyMu)
	go t.readLoop()
	t.loopWg.Add(4)
	go t.blinkLoop()
	go t.autoScrollLoop()
	go t.momentumLoop()
	go t.writeLoop()
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

// onParserReply buffers parser-originated bytes (e.g. DA1 reply) in
// pendingReplies; it does not write them. Called from inside parser.Feed,
// which runs under grid.Mu on the reader goroutine. applyChunk hands the batch
// to writeLoop (via enqueueReplies) once grid.Mu is released, and writeLoop
// does the blocking pty.Write on its own goroutine — so a full slave-side input
// buffer (shell not reading) never stalls the reader, which must keep draining
// the master, nor onDraw, which waits on grid.Mu.
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
	case "windows":
		// WinRT toast via PowerShell — no extra dependency. Title and body
		// reach the script only through environment variables, never as
		// interpolated source, so hostile content cannot inject PowerShell.
		// The PowerShell AppUserModelID is borrowed so the toast surfaces
		// in Action Center without registering our own AUMID.
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive",
			"-ExecutionPolicy", "Bypass", "-Command", winToastScript)
		cmd.Env = append(os.Environ(),
			"GOTERM_NOTIFY_TITLE="+clean(title),
			"GOTERM_NOTIFY_BODY="+clean(body))
		cmd.Run() //nolint:errcheck
	}
}

// winToastScript builds and shows a Windows toast from GOTERM_NOTIFY_TITLE /
// GOTERM_NOTIFY_BODY. Show() hands the toast to the platform and returns, so
// no sleep is needed to keep it alive. Errors are swallowed by the caller.
const winToastScript = `
$ErrorActionPreference = 'Stop'
$null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
$null = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
$tpl = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$texts = $tpl.GetElementsByTagName('text')
$null = $texts.Item(0).AppendChild($tpl.CreateTextNode([string]$env:GOTERM_NOTIFY_TITLE))
$null = $texts.Item(1).AppendChild($tpl.CreateTextNode([string]$env:GOTERM_NOTIFY_BODY))
$toast = [Windows.UI.Notifications.ToastNotification]::new($tpl)
$aumid = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($aumid).Show($toast)
`

// enqueueReplies hands this chunk's parser replies to writeLoop and returns
// immediately. Runs on the reader goroutine after grid.Mu is released; it only
// appends under replyMu (no I/O), so the reader goes straight back to draining
// the pty and can never deadlock against an application blocked writing a query
// batch. writeLoop performs the actual blocking pty.Write. Replies are dropped
// once the queue exceeds maxReplyQueueBytes (see that const).
func (t *Term) enqueueReplies() {
	if len(t.pendingReplies) == 0 {
		return
	}
	n := 0
	for _, b := range t.pendingReplies {
		n += len(b)
	}
	t.replyMu.Lock()
	queued := t.replyBytes < maxReplyQueueBytes
	if queued {
		t.replyQueue = append(t.replyQueue, t.pendingReplies...)
		t.replyBytes += n
	}
	t.replyMu.Unlock()
	if queued {
		t.replyCond.Signal()
	}
	t.pendingReplies = nil
}

// writeLoop is the dedicated reply-writer goroutine: it drains replyQueue and
// writes each reply to the pty, blocking only this goroutine when the slave
// input buffer is full. Exits once Close sets replyDone and the queue is
// empty. Errors are logged; the queue is drained even on partial failure.
func (t *Term) writeLoop() {
	defer t.loopWg.Done()
	defer recoverLoop("writeLoop")
	for {
		t.replyMu.Lock()
		for len(t.replyQueue) == 0 && !t.replyDone {
			t.replyCond.Wait()
		}
		if len(t.replyQueue) == 0 {
			t.replyMu.Unlock()
			return // replyDone with nothing left to write
		}
		batch := t.replyQueue
		t.replyQueue = nil
		t.replyBytes = 0
		t.replyMu.Unlock()
		for _, b := range batch {
			if _, err := t.pw.Write(b); err != nil {
				log.Printf("term: pty reply: %v", err)
			}
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
	// Safety net: when a window-resize drag consumes the mouse-up event,
	// the locked onMouseUp callback never fires and t.mouse.dragging gets
	// stuck true. Any subsequent pointer motion then spuriously extends the
	// selection. Reset drag state on every window-level mouse-up so a
	// "lost" release doesn't leave the terminal in a permanent drag.
	if e.Type == gui.EventMouseUp && t.mouse.dragging {
		t.mouse.dragging = false
		t.mouse.dragReport = false
		t.autoScrollDir.Store(0)
		t.unlockMouse(t.win)
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
	if t.pty == nil {
		return 0
	}
	return t.pty.PID()
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
// focused, the container claims keyboard focus (so go-gui routes
// keystrokes here) and the cursor renders normally. When unfocused,
// the cursor is dimmed. New defaults to focused=true for standalone use.
func (t *Term) SetFocused(v bool) {
	if t.focused.Swap(v) == v {
		return // no change
	}
	if v && t.cmd != nil {
		t.queueCommand(func(w *gui.Window) {
			w.SetFocus(t.focusID)
		})
	}
	t.bumpVersion()
}

// FocusID returns the go-gui focus ID for this terminal.
// Multi-Term embedders use this to detect which pane has focus
// after a mouse click.
func (t *Term) FocusID() string { return t.focusID }

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
		colCfg.ID = t.focusID
		colCfg.Focusable = true
		// UpdateView → clearViewStateLocked clears the window's
		// focus ID. Reassert after every full layout rebuild so
		// keystrokes reach onChar/onKeyDown without requiring a
		// prior click. Skip while a modal dialog is up: go-gui routes
		// keys to the dialog layer, and re-asserting here would steal
		// focus back to the terminal, breaking Tab/Esc/Enter in the
		// dialog. DialogDismiss restores focus to this pane on close.
		if !w.DialogIsVisible() {
			w.SetFocus(t.focusID)
		}
	}
	// FillFill without explicit Width/Height: the Term may be embedded
	// in a multi-pane layout where the parent container dictates
	// dimensions. Using w.WindowSize() here would overflow the pane.
	// Theme menus are handled by the embedder (e.g. Workspace) via
	// ThemeMenuItems and gui.ContextMenu — Term.View returns a plain
	// Column so the embedder controls the wrapping.
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
	// Stop the reply writer. The pty is already closed, so any pty.Write in
	// flight returns an error and writeLoop loops back to observe replyDone.
	t.replyMu.Lock()
	t.replyDone = true
	t.replyMu.Unlock()
	if t.replyCond != nil {
		t.replyCond.Signal()
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

// readLoop reads raw PTY output and feeds it straight to the parser on
// this goroutine. Parser replies (DSR/CPR, DA, XTVERSION, ...) are written
// back to the pty here, decoupled from the vsync-throttled render loop — so
// a query/response round-trip costs microseconds instead of a full display
// frame. Only the resulting repaint is handed to the main thread, via a
// coalesced queueCommand(UpdateWindow). Exits when the pty is closed or
// returns EOF.
func (t *Term) readLoop() {
	defer recoverLoop("readLoop")
	defer func() {
		close(t.readDone)
		if fn := t.cfg.OnExit; fn != nil {
			func() {
				defer recoverLoop("OnExit")
				fn()
			}()
		}
	}()
	buf := make([]byte, 4096)
	for {
		// Apply a pending pty resize before blocking in Read so
		// the TIOCSWINSZ ioctl runs on this goroutine, not the
		// main thread. Avoids deadlock with the SDL event queue
		// during macOS live-resize (see onDraw).
		if t.ptyResizePending.Swap(false) {
			rows := int(t.ptyResizeRows.Load())
			cols := int(t.ptyResizeCols.Load())
			if err := t.pty.Resize(rows, cols); err != nil {
				log.Printf("term: pty resize: %v", err)
			}
		}
		n, err := t.pty.Read(buf)
		if n > 0 {
			// Feed buf directly: parser.feedChunk consumes the slice
			// synchronously (carry-over and reply bytes are copied
			// internally), so reusing buf next iteration is safe.
			//
			// Defer the trailing-grapheme flush when the read filled buf:
			// more bytes are likely already queued and a grapheme cluster
			// may straddle the read boundary. Flushing now would commit a
			// half-assembled cluster as broken pieces (e.g. a ZWJ emoji
			// split at the 4096-byte edge). A short or final read means the
			// burst has drained, so flush to render the trailing cluster.
			flush := n < len(buf) || err != nil
			t.applyChunk(buf[:n], flush)
		}
		if err != nil {
			return
		}
	}
}

// applyChunk feeds a single PTY read to the parser, writes any parser
// replies back to the pty, handles bell flash, and requests a repaint.
// Runs on the reader goroutine. Returns true when a redraw was requested
// (used by tests). grid.Mu is held only for the parse + dirty check, never
// across the pty.Write below, so a full slave-input buffer stalls just this
// goroutine and never blocks onDraw. flush commits a trailing grapheme
// cluster; the reader passes false while the input burst is still draining
// so a cluster split across reads is not committed half-assembled.
func (t *Term) applyChunk(data []byte, flush bool) bool {
	t.grid.Mu.Lock()
	if flush {
		t.parser.Feed(data)
	} else {
		// Burst still draining: feed without committing a trailing grapheme
		// cluster, so one straddling this read boundary is completed by the
		// next chunk rather than written as broken pieces.
		t.parser.feedChunk(data)
	}
	bellCount := t.grid.BellCount
	redraw := !t.grid.SyncOutput || !t.grid.SyncActive
	dirty := t.grid.HasDirtyRows() || bellCount != t.bell.readCount
	needUpdate := false
	if redraw && dirty {
		t.bell.readCount = bellCount
		t.bumpVersion()
		needUpdate = true
	}
	t.grid.Mu.Unlock()

	// Replies are the latency-critical path: hand them to the writer
	// goroutine immediately, off both the render loop and this read loop.
	t.enqueueReplies()

	if bellCount > t.bell.seenCount {
		t.bell.seenCount = bellCount
		if d := t.effectiveBellDuration(); d > 0 {
			t.bell.flashUntil.Store(time.Now().Add(d).UnixNano())
			t.scheduleBellClear(d)
		}
	}

	// Coalesce: queue at most one outstanding UpdateWindow so a burst of
	// reads between frames doesn't pile up redundant command closures.
	if needUpdate && !t.redrawPending.Swap(true) {
		t.queueCommand(func(w *gui.Window) {
			t.redrawPending.Store(false)
			w.UpdateWindow()
		})
	}
	return needUpdate
}

// style returns the resolved text style for this terminal. When fontSize
// is non-zero it overrides the configured Size.
func (t *Term) style() gui.TextStyle {
	ts := t.cfg.TextStyle
	if ts == (gui.TextStyle{}) {
		ts = gui.CurrentTheme().M5
	}
	if t.fontSize > 0 {
		ts.Size = t.fontSize
	}
	return ts
}

// AdjustFontSize shifts the terminal font size by delta points and
// triggers a full remeasure + redraw. Clamps the result to [4, 72] pt.
// Main-thread only (called from onKeyDown, which writes cellW/runeCache
// without grid.Mu — onDraw is the only concurrent reader and also runs
// on the main thread).
func (t *Term) AdjustFontSize(delta float32) {
	if !realNumber(delta) {
		return
	}
	if t.fontSize == 0 {
		if s := t.style(); s.Size > 0 {
			t.fontSize = s.Size
		}
		if t.fontSize == 0 {
			return
		}
	}
	t.fontSize += delta
	const minSize, maxSize float32 = 4, 72
	if t.fontSize < minSize {
		t.fontSize = minSize
	}
	if t.fontSize > maxSize {
		t.fontSize = maxSize
	}
	t.cellW = 0
	t.draw.runeCache = nil
	t.bumpVersion()
	t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
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
