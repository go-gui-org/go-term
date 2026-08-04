package term

import (
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/internal/recfmt"
)

// scrollbarWidth is the pixel width of the scrollbar thumb.
const scrollbarWidth float32 = 4

// scrollbarDuration is how long the scrollbar stays visible after the last
// scroll event while the viewport is back at the live bottom.
const scrollbarDuration = 1500 * time.Millisecond

// New starts a shell in a pty and returns a Term widget. The reader
// goroutine and auxiliary loops (blink, auto-scroll, momentum) are
// spawned before New returns. Call Close to tear down.
func New(w *gui.Window, cfg Cfg) (*Term, error) {
	pty, err := startPTY(initRows, initCols, cfg)
	if err != nil {
		return nil, err
	}
	return newWithPTY(w, cfg, pty)
}

// newWithPTY builds a Term around an already-created byte source. New wraps
// a real shell pty; NewReplay wraps a recording. Everything above the ptyIO
// boundary — parser, grid, rendering, input, scrollback — is identical for
// both, which is the whole reason replay needs no separate viewer.
func newWithPTY(w *gui.Window, cfg Cfg, pty ptyIO) (*Term, error) {
	g := newGrid(initRows, initCols)
	applyTheme(g, cfg)
	applyScrollbackConfig(g, cfg)
	applyContrastConfig(g, cfg)
	seqID := termSeq.Add(1)
	t := &Term{
		cfg:         cfg,
		bindings:    mergeBindings(cfg.KeyBindings),
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
		capture:     openCapture(seqID),
	}
	t.ptyResizeKick = make(chan struct{}, 1)
	t.bellMode.Store(int32(cfg.BellMode))
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
	t.registerCommandHandler()
	t.registerDownloadHandler()
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
	// A Term that never receives a focus event is assumed to be on screen, so
	// the long-running-command notification stays quiet until a real
	// EventUnfocused says otherwise.
	t.winFocused.Store(true)
	t.notifyAfter.Store(int64(clampNotifyAfter(cfg.NotifyAfter)))
	w.SetFocus(t.focusID)
	t.replyCond = sync.NewCond(&t.replyMu)
	// Record-from-startup. Started before readLoop so the recording cannot
	// miss the shell's first bytes. A failure is logged, not fatal — see
	// Cfg.RecordPath.
	if cfg.RecordPath != "" {
		if err := t.StartRecording(cfg.RecordPath); err != nil {
			log.Printf("term: record %s: %v", cfg.RecordPath, err)
		}
	}
	go t.readLoop()
	t.loopWg.Add(5)
	go t.blinkLoop()
	go t.autoScrollLoop()
	go t.momentumLoop()
	go t.writeLoop()
	go t.resizeLoop()
	return t, nil
}

// openCapture opens the raw pty-output capture file when the GOTERM_CAPTURE
// environment variable is set. The value is a path prefix; each Term appends
// "-<seq>.bin" so multi-terminal windows produce one stream per pty. The file
// receives the exact bytes the pty master delivered, so a rendering bug can be
// replayed byte-for-byte: `cat <file>` inside a reference terminal (kitty,
// Terminal.app) to compare visuals, or feed it to CaptureFixture /
// script2fixture for the EmulatorReplay harness. Returns nil (capture
// disabled) when the variable is unset or the file cannot be created.
//
// For a timed, self-describing recording — one that can be replayed inside
// go-term — use Cfg.RecordPath or Term.StartRecording instead.
func openCapture(seq uint64) *recfmt.Recorder {
	prefix := os.Getenv("GOTERM_CAPTURE")
	if prefix == "" {
		return nil
	}
	path := prefix + "-" + strconv.FormatUint(seq, 10) + ".bin"
	f, err := os.Create(path)
	if err != nil {
		log.Printf("term: GOTERM_CAPTURE: %v", err)
		return nil
	}
	log.Printf("term: capturing pty output to %s", path)
	return recfmt.NewRawRecorder(f)
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
	// When the window resizes during a drag, the host platform (notably
	// macOS) takes over mouse tracking and swallows the mouse-up. On the
	// next pointer move the terminal would still see a stale drag flag
	// and extend the selection from the now-lost anchor. Cancel any
	// active drag on every resize so it can't leak across the boundary.
	if e.Type == gui.EventResized && t.mouse.dragging {
		t.mouse.dragging = false
		t.mouse.dragReport = false
		t.autoScrollDir.Store(0)
		t.unlockMouse(t.win)
	}
	// A release the widget never saw also ends the multi-click run, so the
	// next press starts a fresh gesture rather than reading as a double click
	// on whatever the pointer has since moved over. Same for a resize, which
	// reflows content out from under the recorded unit bounds.
	if e.Type == gui.EventMouseUp || e.Type == gui.EventResized {
		t.resetClickCount()
	}
	// Window focus is tracked regardless of whether the child asked for focus
	// reports: the long-running-command notification needs to know the window
	// is unattended, and that has nothing to do with what the child enabled.
	// This must stay ahead of the FocusReporting early-return below, which
	// otherwise discards these events entirely for the common child.
	switch e.Type {
	case gui.EventFocused:
		t.winFocused.Store(true)
	case gui.EventUnfocused:
		t.winFocused.Store(false)
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
	t.rec.Load().Input(p)
	return t.pw.Write(p)
}

// SendInput injects user input into this pane's child as if it had been typed
// or pasted here. It is the receiving counterpart of Cfg.OnInput: what the tap
// hands out, SendInput takes back in, which is how a pane manager mirrors
// input to sibling panes (term/workspace broadcast mode).
//
// kind is not decoration. InputKey bytes are already encoded and go through
// verbatim; InputPaste text is re-wrapped according to *this* pane's own
// bracketed-paste (DEC ?2004) state, because that is a mode each child enables
// for itself — copying the source pane's wrapper would feed a literal
// ESC[200~ to a pane that has it off.
//
// Both kinds snap this pane back to the live view first, exactly as local
// typing does. A pane the user had scrolled into its scrollback must not sit
// frozen while its shell runs what was just sent to it.
//
// Main-thread only, unlike Write. It deliberately does not fire Cfg.OnInput,
// so a mirrored write cannot re-enter the tap and loop between panes. p is not
// retained past the call.
func (t *Term) SendInput(p []byte, kind InputKind) {
	if kind == InputPaste {
		// The conversion is also the copy that keeps p un-retained;
		// cleanPaste returns it unchanged when no marker is present.
		t.pasteText(cleanPaste(string(p)))
		return
	}
	if len(p) == 0 {
		return
	}
	t.snapToLive()
	t.writeRaw(p)
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
//
// A child that subscribed with DECSET ?2031 is told when the theme's light/dark
// character flips, which is what lets a running neovim or delta re-pick its
// syntax palette instead of staying unreadable until it is restarted. Only on a
// flip: cycling between two dark themes changes nothing the notification can
// describe, and one report per keystroke of Cmd+Shift+T is noise in the child's
// input stream.
func (t *Term) SetTheme(th Theme) {
	var report []byte
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		was := t.grid.colorSchemeDark()
		t.grid.setTheme(th)
		if now := t.grid.colorSchemeDark(); now != was && t.grid.ColorSchemeUpdates {
			report = colorSchemeReport(now)
		}
		t.bumpVersion()
	}()
	// Queued outside the lock: appendReplies takes replyMu, and grid.Mu is the
	// single lock this widget holds across subsystems.
	t.queueReply(report)
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
	// Composition state is window-global, but only the focused pane may
	// render it — otherwise every Term in a split paints the same preedit
	// strip at its own cursor. An unfocused pane must also *clear* state it
	// cached before losing focus (focus can change mid-composition), so read
	// zero values rather than returning early: the change detection below
	// then clears the cache and repaints exactly once.
	var (
		composing  bool
		compText   string
		compCursor int
	)
	if t.focused.Load() {
		composing = w.IMEComposing()
		compText = w.IMECompText()
		compCursor = w.IMECompCursor()
	}
	if composing != t.ime.composing || compText != t.ime.compText || compCursor != t.ime.compCursor {
		t.ime.composing = composing
		t.ime.compText = compText
		t.ime.compCursor = compCursor
		t.bumpVersion()
	}

	// Snapshot theme default-bg under the lock so a concurrent SetTheme
	// does not race with this read. The rest of View() is lock-free.
	// defaultBG (not Theme.DefaultBG) so DECSCNM flips the canvas fill too —
	// fillRun skips runs matching it, so the two must agree.
	t.grid.Mu.Lock()
	bgColor := t.grid.defaultBG()
	t.grid.Mu.Unlock()
	canvas := gui.DrawCanvas(gui.DrawCanvasCfg{
		ID:      t.canvasID,
		Version: t.drawVersion.Load(),
		Sizing:  gui.FillFill,
		// Clip is mandatory, not cosmetic. Smooth scrolling draws the
		// partial top row at y = -cellH + ViewSubPx — i.e. *above* the
		// canvas — and shifts the bottom row past dc.Height by ViewSubPx.
		// go-gui only scissors a DrawCanvas when Clip is set, so without
		// this those rows paint over whatever is adjacent: the workspace
		// tab bar above, or the neighboring pane in a split.
		Clip:          true,
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
	// Theme selection is the embedder's business (e.g. Workspace's theme
	// browser) — Term.View returns a plain Column so the embedder controls
	// the wrapping.
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
	// readLoop has exited (or is deemed stuck); the capture tee and the
	// session recording have no writer left, so both files can be closed.
	// Skipped on the stuck path only if readLoop later revives — its writes
	// then land on a closed recorder, which is a no-op.
	_ = t.capture.Close()
	t.capture = nil
	if r := t.rec.Swap(nil); r != nil {
		if err := r.Close(); err != nil {
			log.Printf("term: recording close: %v", err)
		}
	}
	// Stop the reply writer. The pty is already closed, so any pty.Write in
	// flight returns an error and writeLoop loops back to observe replyDone.
	t.replyMu.Lock()
	t.replyDone = true
	t.replyMu.Unlock()
	if t.replyCond != nil {
		t.replyCond.Signal()
	}
	// Stop the download worker. dlQueue is deliberately left open — the
	// reader goroutine may still be alive on the stuck-read path above and a
	// send on a closed channel panics. Anything still queued is dropped; a
	// transfer already being written finishes before the worker returns.
	if t.dlDone != nil {
		close(t.dlDone)
	}
	// Wait for auxiliary goroutines to exit cleanly so they cannot
	// reference t.cmd or other state after we return.
	t.loopWg.Wait()
	if t.resize.timer != nil {
		t.resize.timer.Stop()
	}
	if t.resize.badgeTimer != nil {
		t.resize.badgeTimer.Stop()
	}
	if t.scrollbar.timer != nil {
		t.scrollbar.timer.Stop()
	}
	if t.bell.flashTimer != nil {
		t.bell.flashTimer.Stop()
	}
	if t.bell.fadeTimer != nil {
		t.bell.fadeTimer.Stop()
	}
	// Reader goroutine has exited (readDone above), so the sync watchdog
	// can no longer be re-armed; a fire that already started is a no-op
	// via the closed guard in queueCommand.
	if t.sync.timer != nil {
		t.sync.timer.Stop()
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

// writeBytes sends keyboard-derived bytes to the child. This is the single
// choke point for onChar/onKeyDown output, which is why the OnInput tap hangs
// here rather than at each call site.
func (t *Term) writeBytes(out []byte) {
	t.writeRaw(out)
	// Fired after the local write so the pane the user is looking at never
	// lags behind the panes a broadcasting embedder mirrors to.
	if t.cfg.OnInput != nil {
		t.cfg.OnInput(out, InputKey)
	}
}

// writeRaw records out and pushes it to the pty. Split out of writeBytes so
// SendInput can reuse the write without re-firing the tap it came from.
func (t *Term) writeRaw(out []byte) {
	t.rec.Load().Input(out)
	if _, err := t.pw.Write(out); err != nil {
		log.Printf("term: pty write: %v", err)
	}
}
