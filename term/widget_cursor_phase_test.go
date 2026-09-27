package term

import (
	"testing"
	"time"
)

// A moving cursor must be solid. xterm, VTE, Windows Terminal and iTerm2 all
// restart the blink timer whenever the cursor moves, so the cursor never
// vanishes under the character being typed. Without the re-phase the 500 ms
// cycle free-runs and hides the cursor for half of it regardless of activity,
// which reads as the cursor failing to advance with typing.
func TestRephaseCursorBlink(t *testing.T) {
	base := time.Unix(100, 0)
	tm := &Term{cursorEpoch: base}
	tm.cursorPhaseR, tm.cursorPhaseC = 3, 7

	// Halfway into the hidden half-cycle, with the cursor parked.
	off := base.Add(cursorBlinkPeriod + cursorBlinkPeriod/2)
	tm.rephaseCursorBlink(3, 7, off)
	if !tm.cursorEpoch.Equal(base) {
		t.Fatal("a parked cursor re-phased; blink must keep free-running")
	}

	// The same instant, but the cursor just moved one column.
	tm.rephaseCursorBlink(3, 8, off)
	if !tm.cursorEpoch.Equal(off) {
		t.Fatalf("cursorEpoch = %v, want %v (move must restart the cycle)",
			tm.cursorEpoch, off)
	}
	if tm.cursorPhaseR != 3 || tm.cursorPhaseC != 8 {
		t.Fatalf("tracked position = %d,%d, want 3,8", tm.cursorPhaseR, tm.cursorPhaseC)
	}
}

// The re-phase is what makes the cursor visible again mid-hidden-half.
func TestRephaseMakesCursorVisible(t *testing.T) {
	base := time.Unix(100, 0)
	tm := &Term{cursorEpoch: base, grid: newGrid(4, 10)}
	tm.grid.CursorBlink = true
	tm.focused.Store(true)
	tm.winFocused.Store(true)

	off := base.Add(cursorBlinkPeriod + cursorBlinkPeriod/2)
	if !tm.cursorBlinkOff(off) {
		t.Fatal("precondition: expected the hidden half-cycle")
	}
	tm.rephaseCursorBlink(0, 1, off)
	if tm.cursorBlinkOff(off) {
		t.Fatal("cursor still hidden on the frame that follows a move")
	}
}
