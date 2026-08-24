package term

import (
	"path/filepath"
	"testing"
	"time"
)

// The loops below are demand-driven: their tickers exist only while there is
// something to animate. These tests pin both halves of that contract — the
// loop stays quiet with no work pending, and a kick wakes it — because a
// regression in either direction is invisible on screen until someone
// measures wakeups (quiet loop that never wakes) or battery life (loop that
// never parks).

// idleTerm builds a bare Term with the channels the loops need. No window, so
// queueCommand drops its callbacks.
func idleTerm() *Term {
	tm := &Term{
		grid:           newGrid(24, 80),
		blinkDone:      make(chan struct{}),
		blinkKick:      make(chan struct{}, 1),
		autoScrollKick: make(chan struct{}, 1),
		momentum:       momentumState{kick: make(chan struct{}, 1)},
	}
	tm.focused.Store(true)
	tm.winFocused.Store(true)
	return tm
}

// fillScrollback gives the grid rows above the viewport so ScrollView and
// ScrollViewPx have somewhere to move to. A bare newGrid has ScrollbackCap 0.
func fillScrollback(tm *Term) {
	tm.grid.ScrollbackCap = 1000
	for i := 0; i < 100; i++ {
		tm.grid.Put('x')
		tm.grid.Newline()
	}
}

func TestBlinkLoop_ParksWhenNothingAnimates(t *testing.T) {
	tm := idleTerm()
	tm.grid.CursorVisible = false // nothing to blink
	tm.loopWg.Add(1)
	go tm.blinkLoop()
	defer close(tm.blinkDone)

	// Two full periods with the ticker parked must not bump the version.
	time.Sleep(3 * cursorBlinkPeriod)
	quiet := tm.drawVersion.Load()

	// Now give it something to blink and kick it, as bumpVersion does.
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = true
	tm.grid.CursorBlink = true
	tm.grid.Mu.Unlock()
	kick(tm.blinkKick)

	deadline := time.Now().Add(3 * cursorBlinkPeriod)
	for time.Now().Before(deadline) {
		if tm.drawVersion.Load() > quiet {
			return // resumed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("blinkLoop did not resume after kick (version stuck at %d)", quiet)
}

func TestBlinkLoop_ParkedLoopMakesNoVersionBumps(t *testing.T) {
	tm := idleTerm()
	tm.grid.CursorVisible = false
	tm.loopWg.Add(1)
	go tm.blinkLoop()
	defer close(tm.blinkDone)

	time.Sleep(cursorBlinkPeriod + 100*time.Millisecond)
	first := tm.drawVersion.Load()
	time.Sleep(3 * cursorBlinkPeriod)
	if got := tm.drawVersion.Load(); got != first {
		t.Errorf("idle blinkLoop bumped version %d→%d; ticker did not park",
			first, got)
	}
}

// A blinking cursor rests visible while the pane or window is unfocused, so
// the ticker can park for a background window.
func TestCursorBlinkActive_FocusGated(t *testing.T) {
	tests := []struct {
		name              string
		focused, winFocus bool
		want              bool
	}{
		{"both focused", true, true, true},
		{"pane unfocused", false, true, false},
		{"window unfocused", true, false, false},
		{"neither", false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tm := idleTerm()
			tm.grid.CursorBlink = true
			tm.focused.Store(tc.focused)
			tm.winFocused.Store(tc.winFocus)
			if got := tm.cursorBlinkActive(); got != tc.want {
				t.Errorf("cursorBlinkActive() = %v, want %v", got, tc.want)
			}
			// The draw path must agree: an unfocused caret is never in the
			// hidden half of the cycle, whatever the wall clock says.
			off := tm.cursorBlinkOff(tm.cursorEpoch.Add(cursorBlinkPeriod))
			if !tc.want && off {
				t.Error("unfocused cursor reported blink-off; it must rest visible")
			}
		})
	}
}

func TestSetAutoScrollDir_KicksOnlyOnTransitionToNonZero(t *testing.T) {
	tm := idleTerm()

	tm.setAutoScrollDir(0) // no change from the zero value
	if len(tm.autoScrollKick) != 0 {
		t.Fatal("zero direction kicked the loop")
	}
	tm.setAutoScrollDir(1)
	if len(tm.autoScrollKick) != 1 {
		t.Fatal("start of a drag did not kick the loop")
	}
	<-tm.autoScrollKick
	tm.setAutoScrollDir(1) // same direction again
	if len(tm.autoScrollKick) != 0 {
		t.Error("repeated same-direction write kicked the loop again")
	}
	tm.setAutoScrollDir(0) // drag ended
	if len(tm.autoScrollKick) != 0 {
		t.Error("end of a drag kicked the loop")
	}
}

func TestAutoScrollLoop_ParksUntilKicked(t *testing.T) {
	tm := idleTerm()
	fillScrollback(tm)
	tm.loopWg.Add(1)
	go tm.autoScrollLoop()
	defer close(tm.blinkDone)

	// Direction set behind the setter's back: the parked loop must not see it.
	tm.autoScrollDir.Store(1)
	time.Sleep(300 * time.Millisecond) // several 80ms ticks' worth
	tm.grid.Mu.Lock()
	off := tm.grid.ViewOffset
	tm.grid.Mu.Unlock()
	if off != 0 {
		t.Fatalf("parked autoScrollLoop scrolled to offset %d", off)
	}

	kick(tm.autoScrollKick)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tm.grid.Mu.Lock()
		off = tm.grid.ViewOffset
		tm.grid.Mu.Unlock()
		if off > 0 {
			return // resumed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("autoScrollLoop did not scroll after kick")
}

func TestMomentumLoop_ParksUntilKicked(t *testing.T) {
	tm := idleTerm()
	fillScrollback(tm)
	tm.loopWg.Add(1)
	go tm.momentumLoop()
	defer close(tm.blinkDone)

	// Coast state set without kickMomentum: the parked ticker must ignore it.
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 200
	tm.momentum.cellH = 16
	tm.momentum.mu.Unlock()
	time.Sleep(150 * time.Millisecond) // ~9 ticks at 16ms
	tm.grid.Mu.Lock()
	off := tm.grid.ViewOffset
	tm.grid.Mu.Unlock()
	if off != 0 {
		t.Fatalf("parked momentumLoop scrolled to offset %d", off)
	}

	kick(tm.momentum.kick)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tm.grid.Mu.Lock()
		off = tm.grid.ViewOffset
		tm.grid.Mu.Unlock()
		if off > 0 {
			return // resumed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("momentumLoop did not coast after kick")
}

// A coast below minVel ends on its very first tick; the loop must park then,
// not keep a dead ticker running until some later cancellation.
func TestMomentumLoop_ParksAfterCoastEnds(t *testing.T) {
	tm := idleTerm()
	fillScrollback(tm)
	tm.loopWg.Add(1)
	go tm.momentumLoop()
	defer close(tm.blinkDone)

	// One friction application drops 1.5 below minVel (2.0), so the first
	// tick ends the coast without scrolling.
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 1.5
	tm.momentum.cellH = 16
	tm.momentum.mu.Unlock()
	kick(tm.momentum.kick)
	time.Sleep(150 * time.Millisecond)

	// Parked again: a live coast set behind kickMomentum's back must not
	// scroll, proving the dead ticker was really stopped.
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 200
	tm.momentum.mu.Unlock()
	time.Sleep(150 * time.Millisecond)
	tm.grid.Mu.Lock()
	off := tm.grid.ViewOffset
	tm.grid.Mu.Unlock()
	if off != 0 {
		t.Fatalf("parked momentumLoop scrolled to offset %d", off)
	}

	// A fresh kick still resumes the coast.
	kick(tm.momentum.kick)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		tm.grid.Mu.Lock()
		off = tm.grid.ViewOffset
		tm.grid.Mu.Unlock()
		if off > 0 {
			return // resumed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("momentumLoop did not coast after re-kick")
}

// The drag-end transition (direction back to zero) must park autoScrollLoop
// on the next tick — the setter deliberately does not kick on zero, so the
// running ticker is the only thing that can notice.
func TestAutoScrollLoop_ParksOnZeroDir(t *testing.T) {
	tm := idleTerm()
	fillScrollback(tm)
	tm.loopWg.Add(1)
	go tm.autoScrollLoop()
	defer close(tm.blinkDone)

	tm.setAutoScrollDir(1)
	deadline := time.Now().Add(time.Second)
	var off int
	for time.Now().Before(deadline) {
		tm.grid.Mu.Lock()
		off = tm.grid.ViewOffset
		tm.grid.Mu.Unlock()
		if off > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if off == 0 {
		t.Fatal("autoScrollLoop never scrolled after setAutoScrollDir(1)")
	}

	tm.setAutoScrollDir(0) // drag ended
	time.Sleep(300 * time.Millisecond)

	// Parked: direction set behind the setter's back must not scroll.
	tm.autoScrollDir.Store(1)
	time.Sleep(300 * time.Millisecond)
	tm.grid.Mu.Lock()
	after := tm.grid.ViewOffset
	tm.grid.Mu.Unlock()
	if after != off {
		t.Fatalf("parked autoScrollLoop scrolled %d → %d", off, after)
	}
}

// Recording never touches grid state, so neither start nor stop passes
// through bumpVersion — yet both must wake blinkLoop: start so the elapsed
// timer ticks, stop so the indicator gets repainted away.
func TestRecording_KicksBlinkLoop(t *testing.T) {
	tm := idleTerm()
	path := filepath.Join(t.TempDir(), "rec.gtr")

	if err := tm.StartRecording(path); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if len(tm.blinkKick) != 1 {
		t.Fatal("StartRecording did not kick blinkLoop")
	}
	if err := tm.StartRecording(path); err != errAlreadyRecording {
		t.Fatalf("second StartRecording: got %v, want errAlreadyRecording", err)
	}
	<-tm.blinkKick

	quiet := tm.drawVersion.Load()
	if err := tm.StopRecording(); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if got := tm.drawVersion.Load(); got == quiet {
		t.Error("StopRecording did not bump drawVersion; indicator stays painted")
	}
	if len(tm.blinkKick) != 1 {
		t.Error("StopRecording did not kick blinkLoop")
	}
}
