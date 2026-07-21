package term

import (
	"testing"
	"time"

	gui "github.com/go-gui-org/go-gui/gui"
)

// newSyncTestTerm builds a bare Term for applyChunk-level sync tests.
// cmd is a real (zero) window so the watchdog's queueCommand path works.
func newSyncTestTerm() *Term {
	g := newGrid(4, 20)
	return &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    &gui.Window{},
	}
}

// CSI ?2026h must begin a sync block (suppressing the redraw for content
// in the same and following chunks) and ?2026l must end it and flush the
// accumulated dirty rows. This is the modern form opencode and other TUI
// stacks emit; treating h as "enable only" rendered torn frames.
func TestApplyChunk_CSISyncSuppressesThenFlushes(t *testing.T) {
	tm := newSyncTestTerm()

	if tm.applyChunk([]byte("\x1b[?2026hX"), true) {
		t.Error("redraw during CSI sync block: want suppressed")
	}
	tm.grid.Mu.Lock()
	active := tm.grid.SyncActive
	tm.grid.Mu.Unlock()
	if !active {
		t.Fatal("SyncActive not set by CSI ?2026h")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d during sync block, want 0", v)
	}

	if !tm.applyChunk([]byte("\x1b[?2026l"), true) {
		t.Error("ESU should flush the dirty rows accumulated in the block")
	}
	if v := tm.drawVersion.Load(); v != 1 {
		t.Errorf("drawVersion = %d after ESU, want 1", v)
	}
}

// A sync block whose end never arrives must be force-ended by the
// watchdog after syncUpdateTimeout, flushing the partial frame instead of
// freezing the pane on a stale one.
func TestSyncWatchdog_TimesOutAndRepaints(t *testing.T) {
	tm := newSyncTestTerm()

	if tm.applyChunk([]byte("\x1b[?2026hX"), true) {
		t.Fatal("redraw during sync block: want suppressed")
	}

	deadline := time.Now().Add(syncUpdateTimeout + 2*time.Second)
	for time.Now().Before(deadline) {
		tm.grid.Mu.Lock()
		active := tm.grid.SyncActive
		tm.grid.Mu.Unlock()
		if !active && tm.drawVersion.Load() >= 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	tm.grid.Mu.Lock()
	active := tm.grid.SyncActive
	tm.grid.Mu.Unlock()
	t.Fatalf("watchdog did not fire: SyncActive=%v drawVersion=%d",
		active, tm.drawVersion.Load())
}

// A watchdog fire after the block already ended must be a no-op — no
// spurious version bump.
func TestSyncWatchdog_StaleFireIsNoop(t *testing.T) {
	tm := newSyncTestTerm()

	tm.applyChunk([]byte("\x1b[?2026hX"), true)
	if !tm.applyChunk([]byte("\x1b[?2026l"), true) {
		t.Fatal("ESU should flush")
	}
	v := tm.drawVersion.Load()

	// Let the armed timer fire; the deadline re-check must reject it.
	time.Sleep(syncUpdateTimeout + 200*time.Millisecond)
	if got := tm.drawVersion.Load(); got != v {
		t.Errorf("drawVersion changed %d → %d after stale watchdog fire", v, got)
	}
}

// The legacy iTerm2 DCS form (=1s/=2s) must keep working and also be
// covered by the watchdog (it shares BeginSync/EndSync with the CSI form).
func TestApplyChunk_LegacyDCSSyncStillGates(t *testing.T) {
	tm := newSyncTestTerm()

	// Legacy form requires the mode enabled first; DECSET opens the block
	// and the DCS =1s inside it is an idempotent no-op (see BeginSync).
	if tm.applyChunk([]byte("\x1b[?2026h\x1bP=1s\x1b\\Y"), true) {
		t.Error("redraw during DCS sync block: want suppressed")
	}
	if !tm.applyChunk([]byte("\x1bP=2s\x1b\\"), true) {
		t.Error("DCS ESU should flush")
	}
}

// A begin while a block is already open must NOT refresh SyncBegan —
// otherwise an app spamming BSU could extend repaint suppression forever,
// defeating the watchdog. After EndSync a new begin gets a fresh window.
func TestBeginSync_IdempotentWhileActive(t *testing.T) {
	g := newGrid(4, 20)

	g.BeginSync()
	first := g.SyncBegan
	if first.IsZero() {
		t.Fatal("BeginSync did not record a start time")
	}
	g.BeginSync()
	if g.SyncBegan != first {
		t.Error("nested BeginSync refreshed SyncBegan: watchdog window extended")
	}
	g.EndSync()
	time.Sleep(time.Millisecond) // ensure a distinct clock reading
	g.BeginSync()
	if g.SyncBegan.Equal(first) {
		t.Error("BeginSync after EndSync should start a fresh timeout window")
	}
}

// The watchdog arms once per block: a second chunk arriving while the same
// block is still open must not re-key armedAt (which would reset the timer
// and extend suppression past syncUpdateTimeout).
func TestApplyChunk_WatchdogArmsOncePerBlock(t *testing.T) {
	tm := newSyncTestTerm()

	tm.applyChunk([]byte("\x1b[?2026hX"), true)
	armed := tm.sync.armedAt
	if armed.IsZero() {
		t.Fatal("watchdog not armed by chunk that opened a sync block")
	}
	tm.applyChunk([]byte("Y"), true)
	if tm.sync.armedAt != armed {
		t.Error("second chunk in same block re-armed the watchdog")
	}
}

// An expired block with no dirty rows must still be force-ended, but with
// no version bump — there is nothing to repaint.
func TestSyncWatchdog_ExpiredCleanGridNoRepaint(t *testing.T) {
	tm := newSyncTestTerm()

	tm.applyChunk([]byte("\x1b[?2026h"), true)
	tm.grid.Mu.Lock()
	tm.grid.ClearDirty() // simulate a frame already drawn (OnDraw ran)
	tm.grid.SyncBegan = time.Now().Add(-2 * syncUpdateTimeout)
	tm.grid.Mu.Unlock()

	tm.onSyncTimeout()

	tm.grid.Mu.Lock()
	active := tm.grid.SyncActive
	tm.grid.Mu.Unlock()
	if active {
		t.Error("expired block not force-ended")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d, want 0 (clean grid, nothing to repaint)", v)
	}
}

// A watchdog fire after Close must be a total no-op — no grid mutation,
// no version bump, no queued command.
func TestSyncWatchdog_ClosedTermNoop(t *testing.T) {
	tm := newSyncTestTerm()

	tm.applyChunk([]byte("\x1b[?2026hX"), true)
	tm.grid.Mu.Lock()
	tm.grid.SyncBegan = time.Now().Add(-2 * syncUpdateTimeout)
	tm.grid.Mu.Unlock()
	tm.closed.Store(true)

	tm.onSyncTimeout()

	tm.grid.Mu.Lock()
	active := tm.grid.SyncActive
	tm.grid.Mu.Unlock()
	if !active {
		t.Error("closed Term's watchdog fire mutated grid sync state")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d after closed fire, want 0", v)
	}
}

// A chunk that closes one frame and immediately opens the next (the common
// "…ESU BSU" boundary a pty read can land on) must still paint the finished
// frame: the newly opened block has written nothing, so the grid holds
// exactly the completed frame. Before this, the frame waited for the next
// read or the 500 ms watchdog.
func TestApplyChunk_FrameFlushesWhenNextBlockOpenedButEmpty(t *testing.T) {
	tm := newSyncTestTerm()

	if !tm.applyChunk([]byte("\x1b[?2026hX\x1b[?2026l\x1b[?2026h"), true) {
		t.Error("completed frame not flushed when the next block is still empty")
	}
	if v := tm.drawVersion.Load(); v != 1 {
		t.Errorf("drawVersion = %d, want 1", v)
	}
	tm.grid.Mu.Lock()
	active, ready := tm.grid.SyncActive, tm.grid.SyncFrameReady
	tm.grid.Mu.Unlock()
	if !active {
		t.Error("trailing BSU should leave a block open")
	}
	if ready {
		t.Error("SyncFrameReady should be cleared once the frame is painted")
	}
}

// The counterpart: once the next block writes a cell, the grid holds a
// half-drawn mix of two frames. Painting it would tear, so suppression
// stands and the watchdog is the only release.
func TestApplyChunk_PartialNextFrameStaysSuppressed(t *testing.T) {
	tm := newSyncTestTerm()

	if tm.applyChunk([]byte("\x1b[?2026hX\x1b[?2026l\x1b[?2026hY"), true) {
		t.Error("painted a torn frame: next block has already written")
	}
	if v := tm.drawVersion.Load(); v != 0 {
		t.Errorf("drawVersion = %d, want 0 while a partial frame is pending", v)
	}
	tm.grid.Mu.Lock()
	ready := tm.grid.SyncFrameReady
	tm.grid.Mu.Unlock()
	if !ready {
		t.Error("the completed frame should stay pending for the watchdog")
	}
}

// A block reopened after a frame, with writes arriving in a later chunk,
// must not be flushed early by that later chunk either.
func TestApplyChunk_QuiescenceIsPerChunk(t *testing.T) {
	tm := newSyncTestTerm()

	// Frame 1 completes, frame 2 opens empty: painted.
	if !tm.applyChunk([]byte("\x1b[?2026hA\x1b[?2026l\x1b[?2026h"), true) {
		t.Fatal("setup: first frame should flush")
	}
	// Frame 2 starts writing in the next chunk: no repaint until its ESU.
	if tm.applyChunk([]byte("B"), true) {
		t.Error("mid-frame write repainted inside an open block")
	}
	if !tm.applyChunk([]byte("\x1b[?2026l"), true) {
		t.Error("ESU should flush frame 2")
	}
	if v := tm.drawVersion.Load(); v != 2 {
		t.Errorf("drawVersion = %d, want 2 (one paint per frame)", v)
	}
}
