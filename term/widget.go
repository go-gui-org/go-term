package term

import (
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

// Cfg configures a Term widget. All fields optional.
type Cfg struct {
	// TextStyle overrides the default monospace style. Zero value
	// uses gui.CurrentTheme().M5.
	TextStyle gui.TextStyle

	// ScrollbackRows caps the scrollback ring buffer. Zero uses the
	// default (defaultScrollbackRows). Negative disables scrollback.
	ScrollbackRows int

	// OnTitle, if non-nil, receives OSC 0/1/2 window-title updates on
	// the main goroutine (delivered via Window.QueueCommand). When
	// nil, the widget calls win.SetTitle directly. Embedders set this
	// to wrap the title in app-specific framing.
	OnTitle func(string)

	// OnNotify, if non-nil, is called for OSC 9 / OSC 777 desktop
	// notification requests. title may be empty (OSC 9). When nil,
	// the widget fires a native OS notification via osascript (macOS)
	// or notify-send (Linux). Called on a background goroutine — safe
	// to block.
	OnNotify func(title, body string)

	// AllowOSC52Write permits host applications to write the system clipboard
	// via OSC 52. Disabled by default so untrusted terminal output cannot
	// silently replace the user's clipboard.
	AllowOSC52Write bool

	// CursorBlink, if non-nil, overrides the application's DECSCUSR
	// blink request. Use *true to force blinking on, *false to force
	// steady. Leave nil to honor whatever the shell asks for (steady
	// by default for a brand-new grid).
	CursorBlink *bool

	// Themes, if non-empty, adds a right-click context menu for selecting
	// a color theme at runtime. The first entry is used as the initial theme.
	Themes []NamedTheme
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
// change is actually applied to the grid + PTY. Picked to be longer
// than a single 60Hz frame (16.7 ms) so a continuous mouse drag never
// triggers a reflow mid-gesture, but short enough that the post-drag
// apply feels instant.
const resizeDebounce = 50 * time.Millisecond

// bellFlashDuration is how long the visual-bell overlay remains visible.
const bellFlashDuration = 100 * time.Millisecond

// scrollbarWidth is the pixel width of the scrollbar thumb.
const scrollbarWidth float32 = 4

// scrollbarDuration is how long the scrollbar stays visible after the last
// scroll event while the viewport is back at the live bottom.
const scrollbarDuration = 1500 * time.Millisecond

// Term is a terminal-emulator widget bound to a single PTY-backed shell.
// Use New to construct, View to embed in a layout, Close to tear down.
type Term struct {
	cfg    Cfg
	grid   *Grid
	parser *Parser
	pty    *PTY
	win    *gui.Window

	// Cell metrics measured on first draw and reused thereafter. Both
	// zero until the first OnDraw.
	cellW, cellH float32

	// dragging tracks the button-held state set in onClick, extended
	// in onMouseMove, finalized in onMouseUp. Used both for local
	// selection drag and host-side drag reports — distinguished by
	// dragReport.
	dragging   bool
	dragButton gui.MouseButton
	dragReport bool // true when this drag is being reported to the PTY

	// lastMouseR/C dedupe motion reports under ?1003 so a still
	// pointer doesn't flood the PTY with identical coordinates each
	// frame. Set to (-1, -1) when no prior report.
	lastMouseR int
	lastMouseC int

	// hoverR/hoverC track the cell under the pointer for hyperlink hover
	// highlighting. Updated in onMouseMove and read in onDraw — both on
	// the GUI main thread. Atomics satisfy the race detector in case the
	// framework ever dispatches these callbacks concurrently.
	// Sentinel -1 means "not yet set"; initialized via Store in New.
	hoverR atomic.Int32
	hoverC atomic.Int32

	// closed guards Close so multiple calls are safe.
	closed atomic.Bool

	// loopWg tracks the three auxiliary goroutines (blink, autoScroll,
	// momentum) so Close can wait for them to exit before tearing down
	// state they may still reference.
	loopWg sync.WaitGroup

	// notifyBusy prevents goroutine pile-up from rapid OSC 9/777 sequences.
	// Only one notification runs at a time; extras are dropped.
	notifyBusy atomic.Bool

	// cursorEpoch is the reference time for blink-phase calculation.
	// Set in New so the cursor starts in the "on" half-cycle.
	cursorEpoch time.Time

	// blinkDone signals the blink ticker goroutine to exit. Closed by Close.
	blinkDone chan struct{}

	// readDone is closed by readLoop when it exits. Close waits on it so
	// no further win.QueueCommand calls can arrive after Close returns.
	readDone chan struct{}

	// autoScrollDir drives the selection auto-scroll goroutine during a
	// drag that extends outside the widget (-1 = toward live,
	// +1 = into scrollback, 0 = no scroll). Written on the main
	// thread; read in autoScrollLoop — atomic for safety.
	autoScrollDir atomic.Int32

	// drawVersion is incremented on every visual state change so that
	// go-gui's DrawCanvas tessellation cache can skip OnDraw on unchanged
	// frames. Reads happen on the main thread (View); writes happen on
	// both the main thread and the reader goroutine, hence atomic.
	drawVersion atomic.Uint64

	// writeHost forwards bytes to the PTY. Tests replace this with a
	// buffer sink so key/focus behavior can be asserted without a live PTY.
	writeHost func([]byte) error

	// Search state. All fields accessed on the GUI goroutine only (onChar,
	// onKeyDown, onDraw) — no lock required.
	searchActive  bool
	searchQuery   string
	searchMatches []SearchMatch // viewport matches refreshed each onDraw
	searchIdx     int           // index of last jump target in searchMatches
	searchRegex   bool          // true: match via re instead of plain text
	searchRE      *regexp.Regexp
	searchREErr   error

	// searchCacheVer/Query/Regex track the last drawVersion + query + mode
	// combination for which searchMatches was computed. When all three match
	// the current frame, the expensive ViewportMatches call is skipped.
	searchCacheVer   uint64
	searchCacheQuery string
	searchCacheRegex bool

	// Bell flash state. Both fields are main-thread only (written inside
	// QueueCommand callbacks and read in onDraw). bellSeenCount tracks
	// the last BellCount observed so new bells are detected exactly once.
	bellSeenCount  uint64
	bellFlashUntil time.Time

	// readBellCount tracks the BellCount seen by the readLoop goroutine so
	// bell events (which dirty no cells) still trigger a version bump.
	// Only accessed from readLoop; no synchronization needed.
	readBellCount uint64

	// scrollbarUntil is the deadline until which the scrollbar thumb is
	// rendered, even after ViewOffset returns to 0. Main-thread only.
	scrollbarUntil time.Time
	// scrollbarTimer is the single debounce timer that schedules the hide
	// redraw. Reset on each scroll event; avoids spawning a goroutine per
	// event. Main-thread only (created lazily in showScrollbar).
	scrollbarTimer *time.Timer

	// themeMenuItems is the precomputed ContextMenu item list for runtime
	// theme switching. Built once in New; nil when no themes are configured.
	themeMenuItems []gui.MenuItemCfg

	// gfxDir is a per-Term scratch directory for Sixel-decoded PNGs.
	// Created lazily in New; removed (best-effort) in Close so a long
	// session that prints many graphics doesn't pollute /tmp forever.
	gfxDir string

	// Momentum scroll state. momentumVel/Acc/CellH/Coasting protected by
	// momentumMu. momentumTimer and momentumKick owned by the GUI goroutine
	// (onMouseScroll) except for the timer callback, which only touches
	// momentumMu-protected fields.
	momentumMu       sync.Mutex
	momentumVel      float64       // EMA of recent scroll deltas (pixels)
	momentumCellH    float32       // cellH snapshot at last scroll event
	momentumCoasting bool          // true while goroutine is decelerating
	momentumKick     chan struct{} // buffered 1; wakes momentumLoop
	momentumTimer    *time.Timer   // reset on each scroll; fires kickMomentum

	// runBuf reused across onDraw calls; grows once, never freed.
	runBuf strings.Builder

	// runeStrCache caches string(r) for non-ASCII runes so wide-char
	// and cursor cells don't allocate per frame. Populated lazily;
	// bounded by the set of distinct runes rendered in a session.
	runeStrCache map[rune]string

	// vMatchBuf/selBuf are pre-allocated slices reused across onDraw
	// calls for search highlights and selection bounds, replacing per-
	// frame make([][]vMatch, rows) / make([]rowBounds, rows) calls.
	vMatchBuf [][]vMatch
	selBuf    []rowBounds

	// bidiVisRows/bidiV2LRows cache visual-reordered rows and their
	// visual→logical column maps for the current frame. Pre-allocated to
	// avoid per-frame heap pressure; grown on resize, never shrunk.
	// Main-thread only (onDraw).
	bidiVisRows [][]Cell
	bidiV2LRows [][]int
	// bidiScratch is a reused per-row Cell buffer for the BiDi pre-pass,
	// replacing the per-RTL-row make([]Cell, cols) allocation.
	bidiScratch []Cell

	// Pending-resize state. Live mouse drags fire onDraw at the display
	// refresh rate with continuously changing dims; running grid.Resize
	// (full scrollback reflow) on every frame allocates ~24 MB per call
	// at the default 5000-row scrollback and starves the main thread on
	// Grid.Mu, eventually appearing to hang. We coalesce by deferring
	// the actual reflow until the target dims have been stable for
	// resizeDebounce. Main-thread only (onDraw is the sole writer).
	pendingResizeRows  int
	pendingResizeCols  int
	pendingResizeSince time.Time
	// resizeTimer wakes the main thread to apply a pending resize after
	// the debounce window even if the mouse stops moving (no further
	// onDraw events would otherwise fire). Single shared timer reset
	// each pending frame to avoid spawning goroutines.
	resizeTimer *time.Timer

	// pendingReplies buffers parser-originated reply bytes (DA, DECRQSS,
	// XTGETTCAP, ...) emitted during parser.Feed. Drained by readLoop
	// after Grid.Mu is released so writeHost (which can block when the
	// PTY slave-side input buffer is full) cannot deadlock against
	// onDraw waiting for the same lock. Owned by the readLoop goroutine
	// — append (via onParserReply called from inside Feed) and drain
	// both happen there.
	pendingReplies [][]byte
}

// New starts a shell in a PTY and returns a Term widget. The reader
// goroutine is spawned immediately; subsequent PTY output schedules a
// redraw via win.QueueCommand.
func New(w *gui.Window, cfg Cfg) (*Term, error) {
	const initRows, initCols = 24, 80
	pty, err := Start(initRows, initCols)
	if err != nil {
		return nil, err
	}
	g := NewGrid(initRows, initCols)
	if len(cfg.Themes) > 0 {
		g.Theme = cfg.Themes[0].Theme
	}
	var themeMenuItems []gui.MenuItemCfg
	if len(cfg.Themes) > 0 {
		themeMenuItems = make([]gui.MenuItemCfg, 0, len(cfg.Themes)+1)
		themeMenuItems = append(themeMenuItems, gui.MenuSubtitle("Theme"))
		for i, nt := range cfg.Themes {
			themeMenuItems = append(themeMenuItems, gui.MenuItemCfg{ID: strconv.Itoa(i), Text: nt.Name})
		}
	}
	switch {
	case cfg.ScrollbackRows == 0:
		g.ScrollbackCap = defaultScrollbackRows
	case cfg.ScrollbackRows > 0:
		g.ScrollbackCap = clampScrollback(cfg.ScrollbackRows)
	default:
		// Negative: leave ScrollbackCap = 0 (scrollback disabled).
	}
	t := &Term{
		cfg:            cfg,
		grid:           g,
		parser:         NewParser(g),
		pty:            pty,
		win:            w,
		lastMouseR:     -1,
		lastMouseC:     -1,
		cursorEpoch:    time.Now(),
		blinkDone:      make(chan struct{}),
		readDone:       make(chan struct{}),
		momentumKick:   make(chan struct{}, 1),
		themeMenuItems: themeMenuItems,
	}
	t.hoverR.Store(-1)
	t.hoverC.Store(-1)
	// os.File.Write holds an internal mutex, so concurrent calls from
	// readLoop (parser replies) and the GUI goroutine (key/mouse input)
	// are safe without an extra lock here.
	t.writeHost = func(b []byte) error {
		_, err := t.pty.Write(b)
		return err
	}
	if dir, err := os.MkdirTemp("", "go-term-gfx-*"); err == nil {
		t.gfxDir = dir
		t.parser.SetGraphicsDir(dir)
	}
	t.parser.SetTitleHandler(t.onParserTitle)
	t.parser.SetReplyHandler(t.onParserReply)
	t.parser.SetClipboardWriteAllowed(cfg.AllowOSC52Write)
	if cfg.AllowOSC52Write {
		t.parser.SetClipboardHandler(func(data []byte) {
			text := string(data)
			t.win.QueueCommand(func(w *gui.Window) {
				w.SetClipboard(text)
			})
		})
	}
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
				sendDesktopNotify(title, body)
			}
		}()
	})
	prevOnEvent := w.OnEvent
	w.OnEvent = func(e *gui.Event, w *gui.Window) {
		t.onWindowEvent(e)
		if prevOnEvent != nil {
			prevOnEvent(e, w)
		}
	}
	w.SetIDFocus(focusID)
	go t.readLoop()
	t.loopWg.Add(3)
	go t.blinkLoop()
	go t.autoScrollLoop()
	go t.momentumLoop()
	return t, nil
}

// blinkLoop wakes every cursorBlinkPeriod and forces a redraw when the
// cursor is currently blinking + visible at the live viewport. Other
// states (steady cursor, scrolled-back view, hidden cursor) need no
// periodic redraw and the loop simply skips.
func (t *Term) blinkLoop() {
	defer t.loopWg.Done()
	tk := time.NewTicker(cursorBlinkPeriod)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-tk.C:
			t.grid.Mu.Lock()
			redraw := t.grid.CursorVisible &&
				t.grid.ViewOffset == 0 && t.grid.ViewSubPx == 0 &&
				t.cursorBlinks()
			t.grid.Mu.Unlock()
			if redraw {
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) {
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
			t.grid.Mu.Lock()
			t.grid.ScrollView(dir)
			t.grid.Mu.Unlock()
			t.bumpVersion()
			t.win.QueueCommand(func(w *gui.Window) {
				if t.closed.Load() {
					return
				}
				t.showScrollbar()
				w.UpdateWindow()
			})
		}
	}
}

// cursorBlinks reports whether the cursor should currently blink,
// honoring the Cfg.CursorBlink override over the grid's DECSCUSR
// state. Caller holds Grid.Mu.
func (t *Term) cursorBlinks() bool {
	if t.cfg.CursorBlink != nil {
		return *t.cfg.CursorBlink
	}
	return t.grid.CursorBlink
}

// onParserTitle is the OSC 0/1/2 handler. Runs on the reader goroutine
// while Grid.Mu is held — must not touch *gui.Window state directly,
// hence the QueueCommand hop.
func (t *Term) onParserTitle(title string) {
	fn := t.cfg.OnTitle
	t.win.QueueCommand(func(w *gui.Window) {
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
// Grid.Mu, but only mutates resizeTimer which is main-thread owned).
func (t *Term) scheduleResizeWake(d time.Duration) {
	wake := func() {
		if t.closed.Load() {
			return
		}
		t.bumpVersion()
		t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
	}
	if t.resizeTimer == nil {
		t.resizeTimer = time.AfterFunc(d, wake)
		return
	}
	t.resizeTimer.Reset(d)
}

// onParserReply queues parser-originated bytes (e.g. DA1 reply) for
// writing back to the PTY after readLoop releases Grid.Mu. Called from
// inside parser.Feed which runs under Mu on the readLoop goroutine —
// writing to the PTY directly here would risk a deadlock when the
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

// cleanNotifyStr removes null bytes, which would truncate C-string args
// passed to subprocesses at the syscall boundary.
func cleanNotifyStr(s string) string {
	return strings.ReplaceAll(s, "\x00", "")
}

// sendDesktopNotify fires a native OS notification. Blocks briefly
// (subprocess exec), so always call from a goroutine.
func sendDesktopNotify(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		// Pass title/body as argv to avoid AppleScript string-literal injection.
		stmt := `display notification (item 1 of argv)`
		args := []string{"-e", "on run argv", "-e", stmt, "-e", "end run", cleanNotifyStr(body)}
		if title != "" {
			stmt = `display notification (item 1 of argv) with title (item 2 of argv)`
			args = []string{"-e", "on run argv", "-e", stmt, "-e", "end run", cleanNotifyStr(body), cleanNotifyStr(title)}
		}
		exec.Command("osascript", args...).Run() //nolint:errcheck
	case "linux":
		args := []string{cleanNotifyStr(body)}
		if title != "" {
			args = []string{cleanNotifyStr(title), cleanNotifyStr(body)}
		}
		exec.Command("notify-send", args...).Run() //nolint:errcheck
	}
}

// flushPendingReplies writes all queued parser replies to the PTY.
// Called by readLoop after Grid.Mu is released. Errors are logged and
// the queue is drained even on partial failure.
func (t *Term) flushPendingReplies() {
	if len(t.pendingReplies) == 0 {
		return
	}
	pending := t.pendingReplies
	t.pendingReplies = nil
	for _, b := range pending {
		if err := t.writeHost(b); err != nil {
			log.Printf("term: pty reply: %v", err)
		}
	}
}

func (t *Term) onWindowEvent(e *gui.Event) {
	if e == nil {
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
	if err := t.writeHost(report); err != nil {
		log.Printf("term: pty focus report: %v", err)
	}
}

// Cwd returns the most recent working directory reported via OSC 7,
// or "" if the shell has never emitted one. Typical payload format
// is "file://host/path"; embedders parse as needed.
func (t *Term) Cwd() string {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.grid.Cwd
}

// SetTheme replaces the active color theme and schedules a redraw.
// Safe to call from the main thread at any time.
func (t *Term) SetTheme(th Theme) {
	t.grid.Mu.Lock()
	t.grid.Theme = th
	t.grid.Mu.Unlock()
	t.bumpVersion()
}

// focusID is the IDFocus value claimed by the terminal container.
const focusID uint32 = 1

// View returns the go-gui view tree for this terminal. Usable as a
// gui.Window UpdateView generator: w.UpdateView(t.View).
func (t *Term) View(w *gui.Window) gui.View {
	ww, wh := w.WindowSize()
	canvas := gui.DrawCanvas(gui.DrawCanvasCfg{
		ID:            "term-canvas",
		Version:       t.drawVersion.Load(),
		Sizing:        gui.FillFill,
		OnDraw:        t.onDraw,
		OnMouseScroll: t.onMouseScroll,
		OnClick:       t.onClick,
		OnMouseMove:   t.onMouseMove,
		OnMouseUp:     t.onMouseUp,
	})
	colCfg := gui.ContainerCfg{
		Padding:   gui.Some(gui.Padding{}),
		Spacing:   gui.SomeF(0),
		Color:     t.grid.Theme.DefaultBG,
		IDFocus:   focusID,
		OnChar:    t.onChar,
		OnKeyDown: t.onKeyDown,
		OnKeyUp:   t.onKeyUp,
		Content:   []gui.View{canvas},
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
	colCfg.Width = float32(ww)
	colCfg.Height = float32(wh)
	colCfg.Sizing = gui.FixedFixed
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
	// Wait for readLoop to drain, but don't hang forever if the PTY fd is
	// in a degraded state where close doesn't unblock an in-progress read.
	readTimer := time.NewTimer(2 * time.Second)
	defer readTimer.Stop()
	select {
	case <-t.readDone:
	case <-readTimer.C:
	}
	// Wait for auxiliary goroutines to exit cleanly so they cannot
	// reference t.win or other state after we return.
	t.loopWg.Wait()
	if t.resizeTimer != nil {
		t.resizeTimer.Stop()
	}
	if t.gfxDir != "" {
		_ = os.RemoveAll(t.gfxDir)
	}
	return err
}

// readLoop forwards PTY output through the parser and schedules a
// render. Exits when the PTY is closed or returns EOF.
func (t *Term) readLoop() {
	defer close(t.readDone)
	buf := make([]byte, 4096)
	for {
		n, err := t.pty.Read(buf)
		if n > 0 {
			t.grid.Mu.Lock()
			t.parser.Feed(buf[:n])
			bellCount := t.grid.BellCount
			redraw := !t.grid.SyncOutput || !t.grid.SyncActive
			// Gate the version bump on actual visual changes: cell mutations
			// (HasDirtyRows) or a new BEL (which marks no cells but needs a
			// flash). Pure no-op sequences (swallowed queries, etc.) skip the
			// version bump so the tessellation cache stays valid.
			dirty := t.grid.HasDirtyRows() || bellCount != t.readBellCount
			if redraw && dirty {
				t.readBellCount = bellCount
				t.bumpVersion()
			}
			t.grid.Mu.Unlock()
			// Drain parser replies (DA, DECRQSS, ...) outside Mu so a
			// blocking pty.Write (slave-side input buffer full) cannot
			// stall onDraw waiting for the same lock.
			t.flushPendingReplies()
			if redraw && dirty {
				t.win.QueueCommand(func(w *gui.Window) {
					if bellCount > t.bellSeenCount {
						t.bellSeenCount = bellCount
						t.bellFlashUntil = time.Now().Add(bellFlashDuration)
						// Schedule a redraw to clear the flash overlay.
						// Use a select goroutine so blinkDone cancellation
						// prevents QueueCommand from firing after Close.
						blinkDone := t.blinkDone
						go func() {
							timer := time.NewTimer(bellFlashDuration + time.Millisecond)
							defer timer.Stop()
							select {
							case <-blinkDone:
								return
							case <-timer.C:
								if t.closed.Load() {
									return
								}
							}
							t.bumpVersion()
							t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
						}()
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
	if t.cfg.TextStyle.Size > 0 {
		return t.cfg.TextStyle
	}
	return gui.CurrentTheme().M5
}

// bumpVersion increments drawVersion so the next View call produces a
// new cache key, forcing go-gui to re-invoke OnDraw for this frame.
func (t *Term) bumpVersion() { t.drawVersion.Add(1) }

func (t *Term) writeBytes(out []byte) {
	if err := t.writeHost(out); err != nil {
		log.Printf("term: pty write: %v", err)
	}
}
