package term

import (
	"log"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// resizeLoop is the dedicated goroutine that applies pty resizes latched by
// onDraw. The TIOCSWINSZ ioctl must not run on the main thread (deadlock with
// the SDL event queue during macOS live-resize — see onDraw), and running it
// on the reader goroutine (the previous design) delayed it until the child's
// next write: readLoop only re-checked the latch after its blocking Read
// returned, so resizing over an idle full-screen app left the app unaware of
// its new size — and stale on screen — until the next keystroke produced
// output.
//
// The same-size heal below closes a subtler race that design allowed: with
// the child quiet, the grid could resize A→B (clipping content) and back to
// A before the latch was ever applied. The coalesced latch then carried A —
// the size the kernel already had — so TIOCSWINSZ delivered no SIGWINCH and
// a diff-rendering app never repainted the cells the clip destroyed. When
// the latched size equals the last size this loop applied, bounce through a
// one-row-off size first so the child gets two real SIGWINCHes and repaints.
func (t *Term) resizeLoop() {
	defer t.loopWg.Done()
	defer recoverLoop("resizeLoop")
	lastRows, lastCols := initRows, initCols
	for {
		select {
		case <-t.blinkDone:
			return
		case <-t.ptyResizeKick:
		}
		if !t.ptyResizePending.Swap(false) {
			continue
		}
		rows := clampDim(int(t.ptyResizeRows.Load()))
		cols := clampDim(int(t.ptyResizeCols.Load()))
		if rows == lastRows && cols == lastCols {
			// Same-size re-apply: the grid diverged and returned while
			// no intermediate size reached the kernel. Bump one row off
			// and back to force SIGWINCH delivery (see doc comment).
			bump := rows - 1
			if bump < 1 {
				bump = rows + 1
			}
			if err := t.pty.Resize(bump, cols); err != nil && !t.closed.Load() {
				log.Printf("term: pty resize: %v", err)
			}
		}
		if err := t.pty.Resize(rows, cols); err != nil && !t.closed.Load() {
			log.Printf("term: pty resize: %v", err)
		}
		// Record the new geometry so a replay can report the size the
		// session actually ran at (recfmt.KindResize).
		t.rec.Load().Resize(rows, cols)
		lastRows, lastCols = rows, cols
	}
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
			// Blinking text needs the same periodic repaint, and unlike the
			// cursor it blinks in any viewport position. The recording
			// indicator rides the same tick to advance its elapsed timer,
			// which is why the timer shows whole seconds and no finer.
			redraw = redraw || t.blinkCells.Load() || t.Recording()
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

// scheduleResizeWake arms (or resets) a one-shot timer that bumps the
// draw version and asks go-gui for a redraw after d. Used by onDraw
// when a resize is being debounced — without this, no further onDraw
// would fire once the mouse stops moving and the debounced size would
// never be applied. Main-thread only (called from inside onDraw under
// grid.Mu, but only mutates resizeTimer which is main-thread owned).
func (t *Term) scheduleResizeWake(d time.Duration) {
	t.scheduleDelayedUpdate(d, &t.resize.timer)
}

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
	t.appendReplies(t.pendingReplies, n)
	t.pendingReplies = nil
}

// queueReply hands one reply to writeLoop. Unlike enqueueReplies it is safe
// from any goroutine including the main thread, because it only appends under
// replyMu — writeRaw would block the UI thread whenever the child's input
// buffer is full. Used by the emitters that originate outside parser.Feed:
// SetTheme's mode-2031 color-scheme notification.
func (t *Term) queueReply(b []byte) {
	if len(b) == 0 {
		return
	}
	t.appendReplies([][]byte{b}, len(b))
}

// appendReplies is the shared tail of both queueing paths: one lock, one
// append, one signal, and the same maxReplyQueueBytes drop rule — a child that
// has stopped reading must not be able to grow this queue without bound.
func (t *Term) appendReplies(batch [][]byte, n int) {
	t.replyMu.Lock()
	queued := t.replyBytes < maxReplyQueueBytes
	if queued {
		t.replyQueue = append(t.replyQueue, batch...)
		t.replyBytes += n
	}
	t.replyMu.Unlock()
	if queued {
		t.replyCond.Signal()
	}
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
		n, err := t.pty.Read(buf)
		if n > 0 {
			// Record the exact bytes delivered by the pty master before
			// parsing, so a rendering bug can be replayed against a
			// reference terminal, the EmulatorReplay harness, or go-term
			// itself. Two independent sinks: the GOTERM_CAPTURE debug tee
			// (raw bytes) and the user-facing .gtr session recording
			// (timed frames). Both self-disable on write failure rather
			// than costing pty throughput.
			t.capture.Output(buf[:n])
			t.rec.Load().Output(buf[:n])
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
	// Counted before the lock: a read that waits for grid.Mu is exactly the
	// delay this number exists to expose.
	t.lat.noteChunk()
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
	overlayVer := t.grid.OverlayVersion
	// A block still open at the end of this chunk normally suppresses the
	// repaint, since the grid may hold a half-written frame. But applications
	// commonly close one frame and open the next back to back (BSU … ESU BSU),
	// and a read can land in that gap: the grid then holds a *finished* frame
	// under a block that has written nothing. SyncFrameQuiescent detects
	// exactly that case, so the frame goes up now instead of waiting on the
	// next read or the 500 ms watchdog.
	redraw := !t.grid.SyncOutput || !t.grid.SyncActive || t.grid.SyncFrameQuiescent()
	// OverlayVersion covers state that repaints an overlay but marks no row
	// dirty (OSC 9;4 progress) — without it the frame is never scheduled and
	// the change sits in the grid unseen.
	dirty := t.grid.HasDirtyRows() || bellCount != t.bell.readCount ||
		overlayVer != t.overlayVersion
	needUpdate := false
	if redraw {
		t.grid.SyncFrameReady = false
	}
	if redraw && dirty {
		t.bell.readCount = bellCount
		t.overlayVersion = overlayVer
		t.bumpVersion()
		needUpdate = true
		// Screen-changing output while a keystroke is outstanding: this is the
		// echo that keystroke was waiting for. Stamped under grid.Mu with the
		// rest of the decision, so the timestamp cannot precede the parse that
		// justified it.
		t.lat.markEcho()
	}
	// This chunk left a sync block open: arm the watchdog so a stalled or
	// dead application cannot suppress repaints past syncUpdateTimeout.
	// armedAt keys on SyncBegan so each block arms exactly once.
	var syncDeadline time.Time
	if t.grid.SyncActive && t.grid.SyncBegan != t.sync.armedAt {
		t.sync.armedAt = t.grid.SyncBegan
		syncDeadline = t.grid.SyncBegan.Add(syncUpdateTimeout)
	}
	t.grid.Mu.Unlock()

	if !syncDeadline.IsZero() {
		d := time.Until(syncDeadline)
		if d < 0 {
			d = 0
		}
		if t.sync.timer == nil {
			t.sync.timer = time.AfterFunc(d, t.onSyncTimeout)
		} else {
			// Reset without Stop is safe: onSyncTimeout re-checks the
			// deadline under grid.Mu, so a stale fire is a no-op.
			t.sync.timer.Reset(d)
		}
	}

	// Replies are the latency-critical path: hand them to the writer
	// goroutine immediately, off both the render loop and this read loop.
	t.enqueueReplies()

	bellRang := bellCount > t.bell.seenCount
	if bellRang {
		t.bell.seenCount = bellCount
		t.ringBell()
	}

	// Report activity to a pane manager. Computed here rather than per cell:
	// needUpdate is already the answer to "did this read change the screen",
	// so the hook costs one nil check per PTY read.
	if fn := t.cfg.OnActivity; fn != nil && (needUpdate || bellRang) {
		kind := ActivityOutput
		if bellRang {
			kind = ActivityBell
		}
		t.reportActivity(fn, kind)
	}

	// Coalesce: queue at most one outstanding UpdateWindow so a burst of
	// reads between frames doesn't pile up redundant command closures.
	if needUpdate && !t.redrawPending.Swap(true) {
		t.queueCommand(func(w *gui.Window) {
			t.redrawPending.Store(false)
			// First thing on the main thread: the stamp must not include the
			// UpdateWindow it precedes, or the wake/paint split moves work from
			// one side to the other.
			t.lat.markWake()
			w.UpdateWindow()
		})
	}
	return needUpdate
}

// reportActivity hands one activity report to the embedder's OnActivity hook
// on the main thread. Dispatched through queueCommand because the hook runs
// there and grid.Mu must never be held across a go-gui call.
//
// Coalesced exactly like the redraw above it, and for the same reason: a child
// spewing output produces a read every few hundred microseconds, and one
// queued closure per read floods the command queue with calls whose answer is
// identical. At most one dispatch is outstanding; further reads fold into it.
// A bell arriving while one is pending upgrades the kind rather than queuing
// its own, so the signal the embedder must not miss cannot be dropped.
func (t *Term) reportActivity(fn func(ActivityKind), kind ActivityKind) {
	if kind == ActivityBell {
		t.activityBell.Store(true)
	}
	if t.activityPending.Swap(true) {
		return
	}
	t.queueCommand(func(*gui.Window) {
		// Cleared before the hook runs so activity produced during it queues a
		// fresh dispatch instead of being swallowed.
		t.activityPending.Store(false)
		k := ActivityOutput
		if t.activityBell.Swap(false) {
			k = ActivityBell
		}
		fn(k)
	})
}

// onSyncTimeout is the mode-2026 watchdog callback: a sync block was begun
// at least syncUpdateTimeout ago and never ended — the application stalled
// mid-frame or died. End the block and flush whatever arrived so the pane
// shows the partial frame instead of freezing on a stale one. The deadline
// re-check makes stale fires (a newer block re-armed the timer, or the
// block already ended) harmless no-ops.
func (t *Term) onSyncTimeout() {
	// Mirror scheduleDelayedUpdate's guard: after Close (or on a bare Term
	// with no scheduler) do nothing — the pane is going away, so flushing
	// a partial frame is pointless and the grid may be tearing down.
	if t.closed.Load() || t.cmd == nil {
		return
	}
	t.grid.Mu.Lock()
	expired := t.grid.SyncActive &&
		!t.grid.SyncBegan.IsZero() &&
		time.Since(t.grid.SyncBegan) >= syncUpdateTimeout
	if expired {
		t.grid.EndSync()
		// The forced flush below is the paint for whatever the grid holds, so
		// don't leave a frame marked pending for the next chunk to re-flush.
		t.grid.SyncFrameReady = false
	}
	dirty := expired && t.grid.HasDirtyRows()
	t.grid.Mu.Unlock()
	if !dirty {
		return
	}
	t.bumpVersion()
	if !t.redrawPending.Swap(true) {
		t.queueCommand(func(w *gui.Window) {
			t.redrawPending.Store(false)
			t.lat.markWake() // same split as applyChunk's repaint
			w.UpdateWindow()
		})
	}
}
