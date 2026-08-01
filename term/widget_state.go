package term

import (
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/internal/recfmt"
)

// cursorBlinkPeriod is the half-cycle duration: cursor visible for
// blinkPeriod, then hidden for blinkPeriod. 500 ms matches xterm
// defaults.
const cursorBlinkPeriod = 500 * time.Millisecond

// resizeDebounce is the minimum stable interval before a pending size
// change is actually applied to the grid + pty. Picked to be longer
// than a single 60Hz frame (16.7 ms) so a continuous mouse drag never
// triggers a reflow mid-gesture, but short enough that the post-drag
// apply feels instant.
const resizeDebounce = 50 * time.Millisecond

// syncUpdateTimeout bounds a mode-2026 synchronized-update block. An
// application that begins a block (CSI ?2026h / DCS =1s) and stalls or dies
// before ending it would otherwise suppress repaints forever, freezing the
// pane on a stale frame. The watchdog clears the block and repaints after
// this long. 500 ms sits between alacritty (150 ms) and kitty (2 s): long
// enough to mask a GC pause or tool-call stall mid-frame, short enough
// that a wedged app doesn't look hung.
const syncUpdateTimeout = 500 * time.Millisecond

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

	// badgeUntil/badgeTimer drive the transient "COLS × ROWS" readout shown
	// while the window is being dragged. The badge tracks the *candidate*
	// dims, not the grid's, so it updates on every drag frame rather than
	// only when the debounce lets a reflow through — the feedback is the
	// whole point.
	badgeUntil time.Time
	badgeTimer *time.Timer
	badgeRows  int
	badgeCols  int

	pendingRows int
	pendingCols int

	// ptyLastRows/ptyLastCols are the dims the child was last told about.
	// Zero initially, so the first frame always publishes.
	ptyLastRows int
	ptyLastCols int

	// sized suppresses the badge for the window's initial sizing, which is
	// not a user gesture and would flash the readout at startup.
	sized bool
}

// syncState is the mode-2026 synchronized-update watchdog. applyChunk
// (reader goroutine) arms the timer when a chunk leaves a newly begun sync
// block open; onSyncTimeout runs on the timer's goroutine and touches only
// grid.Mu-guarded state plus the thread-safe queueCommand path. armedAt
// tracks which BeginSync the timer was armed for so re-arming happens once
// per block, not once per chunk. Both fields are written only by the
// reader goroutine (Close stops the timer after readLoop has exited).
type syncState struct {
	timer   *time.Timer
	armedAt time.Time
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

// copySelMode is copy mode's selection state: none yet, character-wise (v), or
// line-wise (V).
type copySelMode uint8

const (
	copySelNone copySelMode = iota
	copySelChar
	copySelLine
)

// copyState holds keyboard copy-mode state. Like searchState, every field is
// touched on the GUI goroutine only (onKeyDown, onChar, onDraw), so no lock is
// needed — the grid coordinates it derives are written under grid.Mu by
// syncSelection.
//
// cursor and anchor are *cell* positions, whereas grid.SelAnchor/SelHead are
// cell boundaries; syncSelection converts between the two.
type copyState struct {
	cursor contentPos // copy cursor
	anchor contentPos // selection anchor cell; meaningful when sel != copySelNone
	active bool
	sel    copySelMode

	// searching is set while '/' or '?' has the search bar open on copy mode's
	// behalf, so the search handlers keep their normal behavior and control
	// returns here on Enter/Escape instead of to the plain search path.
	searching bool
	backward  bool // direction of the last search, for n/N
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
	locked  bool
	lastR   int // dedupe motion reports under ?1003
	lastC   int
	hoverR  atomic.Int32 // sentinel -1 = not yet set
	hoverC  atomic.Int32
	cmdHeld atomic.Bool // true when Super (Cmd) is held

	// Implicit URL detected under the pointer while Cmd is held (issue 72).
	// Main-thread only: written by updateHover, read by prepareHoverURL and
	// onMouseUp. hoverURL is "" when no auto-detected URL is under the cursor;
	// hoverSpans are its per-content-row grid-column ranges for highlighting.
	hoverURL   string
	hoverSpans []urlSpan

	// Wheel-report accumulator (see wheelReportTicks): pixels of scroll
	// distance not yet emitted as an SGR wheel tick, and the direction
	// (+1 up / -1 down / 0 initial) it was accumulated in.
	wheelResidual float32
	wheelDir      int

	// Multi-click gesture state. clickCount is 1 for a plain click, 2 for a
	// double, 3 for a triple, wrapping back to 1 on a fourth so a user who
	// keeps clicking cycles through the granularities rather than sticking
	// at line-wise. A click counts as a repeat only when it lands within
	// multiClickInterval of the previous one *and* within multiClickSlop of
	// its position — the position test is what stops a fast typist clicking
	// two different words from getting one word-extended selection.
	clickCount  int
	lastClickAt time.Time
	lastClickX  float32
	lastClickY  float32

	// Bounds of the unit (word or line) the current gesture anchored on,
	// in content coordinates with selUnitEnd being an inclusive cell. A
	// word/line drag pivots around this so dragging back past the origin
	// extends the other way instead of collapsing. Meaningless unless the
	// grid's SelMode is selWord or selLine.
	selUnitStart contentPos
	selUnitEnd   contentPos
}

// drawBufs holds per-frame scratch buffers reused across onDraw calls.
// All fields are main-thread only.
type drawBufs struct {
	runBuf      strings.Builder
	runeCache   map[rune]string // caches string(r) for non-ASCII runes
	vMatchBuf   [][]vMatch      // pre-allocated search-highlight rows
	selBuf      []rowBounds     // pre-allocated selection-bound rows
	urlBuf      []rowBounds     // pre-allocated hover-URL highlight rows
	bidiVisRows [][]cell        // visual-reordered rows for current frame
	bidiV2LRows [][]int         // visual→logical column maps
	bidiScratch []cell          // reused per-row cell buffer for BiDi pre-pass
	// phRects accumulates the Kitty Unicode-placeholder rectangles found in
	// the viewport (see drawPlaceholders). Kept here so a screen full of
	// placeholder cells does not allocate every frame.
	phRects []placeholderRect
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

	// blinkCells records whether the last painted frame contained any SGR 5/6
	// (blink) text. Written by drawFgPass on the main thread, read by
	// blinkLoop, so periodic repaints happen only while blinking text is
	// actually visible.
	blinkCells atomic.Bool

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
	sync   syncState

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

	copy copyState

	mouse mouseState

	// loopWg tracks the auxiliary goroutines (blink, autoScroll, momentum,
	// reply writer, pty resizer) so Close can wait for them to exit before
	// tearing down state they may still reference.
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

	// dlQueue hands OSC 1337 File= transfers from the reader goroutine (which
	// holds grid.Mu and must never block on disk) to downloadWorker. Nil when
	// downloads are not configured. Never closed — the reader goroutine may
	// outlive Close on the stuck-read path, and a send on a closed channel
	// panics; the worker exits on dlDone instead.
	//
	// dlPending tracks queued payload bytes so a burst of large transfers
	// can't pin the whole queue's worth of memory.
	dlQueue   chan downloadJob
	dlDone    chan struct{}
	dlPending atomic.Int64

	// autoScrollDir drives the selection auto-scroll goroutine during a
	// drag that extends outside the widget (-1 = toward live,
	// +1 = into scrollback, 0 = no scroll). Written on the main
	// thread; read in autoScrollLoop — atomic for safety.
	autoScrollDir atomic.Int32

	// ptyResizeRows/Cols carry a pending TIOCSWINSZ from onDraw to
	// resizeLoop, the dedicated goroutine that applies pty resizes.
	// onDraw writes them under grid.Mu, sets pending, then kicks the
	// channel. Keeping the ioctl off the main thread avoids deadlock
	// with the SDL event queue during macOS live-resize; keeping it off
	// the reader goroutine means an idle child (no pty output) still
	// receives SIGWINCH promptly instead of on its next write.
	ptyResizePending atomic.Bool
	ptyResizeRows    atomic.Int32
	ptyResizeCols    atomic.Int32
	ptyResizeKick    chan struct{} // buffered(1); nil in bare test Terms

	// capture is the opt-in raw pty-output tee (GOTERM_CAPTURE env var):
	// a recorder in raw mode, i.e. bare bytes with no header or framing.
	// Set once in New, written only by the reader goroutine, closed in
	// Close after readLoop exits. Nil when capture is disabled.
	capture *recfmt.Recorder

	// rec is the user-facing session recording (.gtr — header, timing,
	// resize frames). Swapped at runtime by StartRecording/StopRecording on
	// the main thread while the reader goroutine reads it, hence atomic.
	// Independent of capture: the debug tee and a session recording may run
	// at the same time.
	rec atomic.Pointer[recfmt.Recorder]

	// bindings is the effective Term-level shortcut table: defaults merged
	// with Cfg.KeyBindings at construction, replaced wholesale by
	// SetKeyBindings. Read by binds() on the main thread only, so it needs no
	// lock — onKeyDown and SetKeyBindings both run there.
	bindings map[Action]binding

	// bellMode is the live BellMode, seeded from Cfg.BellMode and replaced by
	// SetBellMode. Atomic rather than a plain cfg read because ringBell runs
	// on the reader goroutine while the setter runs on the main thread.
	bellMode atomic.Int32

	// redrawPending coalesces UpdateWindow requests from the reader
	// goroutine: applyChunk only queues a redraw command when one is not
	// already in flight, so a burst of PTY reads between frames does not
	// pile up redundant closures on the command queue. Cleared by the
	// queued callback before it asks for the repaint.
	redrawPending atomic.Bool
}
