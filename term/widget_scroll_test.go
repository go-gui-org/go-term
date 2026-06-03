package term

import (
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// ---------------------------------------------------------------------------
// scrollByPage
// ---------------------------------------------------------------------------

func TestScrollByPage_Forward(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(50, 80)
	// Fill scrollback so we can scroll.
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 30 {
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.ViewOffset = 0
	tm.grid.Mu.Unlock()
	prevOff := tm.grid.ViewOffset
	tm.scrollByPage(1, &gui.Window{})
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset <= prevOff {
		t.Errorf("ViewOffset should increase, got %d", tm.grid.ViewOffset)
	}
	tm.grid.Mu.Unlock()
}

func TestScrollByPage_Backward(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(50, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 30 {
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.ViewOffset = 24
	tm.grid.Mu.Unlock()
	prevOff := tm.grid.ViewOffset
	tm.scrollByPage(-1, &gui.Window{})
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset >= prevOff {
		t.Errorf("ViewOffset should decrease, got %d", tm.grid.ViewOffset)
	}
	tm.grid.Mu.Unlock()
}

// ---------------------------------------------------------------------------
// scrollToTop / scrollToBottom
// ---------------------------------------------------------------------------

func TestScrollToTop(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(50, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 30 {
		tm.grid.Scrollback.Push(row, false)
	}
	sb := tm.grid.Scrollback.Len()
	tm.grid.ViewOffset = 0
	tm.grid.Mu.Unlock()
	tm.scrollToTop(&gui.Window{})
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != sb {
		t.Errorf("scrollToTop: ViewOffset=%d, want %d", tm.grid.ViewOffset, sb)
	}
	tm.grid.Mu.Unlock()
}

func TestScrollToBottom(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(50, 80)
	tm.grid.ViewOffset = 10
	tm.grid.Mu.Unlock()
	tm.scrollToBottom(&gui.Window{})
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != 0 || tm.grid.ViewSubPx != 0 {
		t.Errorf("scrollToBottom: got (%d, %f), want (0,0)",
			tm.grid.ViewOffset, tm.grid.ViewSubPx)
	}
	tm.grid.Mu.Unlock()
}

// ---------------------------------------------------------------------------
// snapToLive
// ---------------------------------------------------------------------------

func TestSnapToLive_ScrolledBack(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.ViewOffset = 50
	tm.grid.SelActive = true
	tm.grid.SelAnchor = contentPos{Row: 10, Col: 0}
	tm.grid.SelHead = contentPos{Row: 10, Col: 5}
	tm.grid.Mu.Unlock()
	tm.snapToLive()
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != 0 || tm.grid.ViewSubPx != 0 {
		t.Error("snapToLive should reset view offset")
	}
	if tm.grid.SelActive {
		t.Error("snapToLive should clear selection")
	}
	tm.grid.Mu.Unlock()
}

func TestSnapToLive_AlreadyLive(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.ViewOffset = 0
	tm.grid.ViewSubPx = 0
	tm.grid.Mu.Unlock()
	tm.snapToLive()
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != 0 {
		t.Error("already-live should stay at live")
	}
	tm.grid.Mu.Unlock()
}

// ---------------------------------------------------------------------------
// jumpToMark
// ---------------------------------------------------------------------------

func TestJumpToMark_NoMarks(t *testing.T) {
	tm := newScrollTerm(24, 80)
	prevVer := tm.drawVersion.Load()
	tm.jumpToMark(true, &gui.Window{})
	if tm.drawVersion.Load() != prevVer {
		t.Error("no marks: version should not change")
	}
}

func TestJumpToMark_AltScreenSuppressed(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.AltActive = true
	prevVer := tm.drawVersion.Load()
	tm.jumpToMark(true, &gui.Window{})
	if tm.drawVersion.Load() != prevVer {
		t.Error("alt screen: should be suppressed")
	}
}

func TestJumpToMark_BackwardFound(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(50, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	// Push 5 rows, add mark (at content row ~5), push 10 more (mark now
	// at content row 15 in scrollback, since live grid rows shift up).
	tm.grid.Scrollback.Push(row, false) // sb=1
	tm.grid.Scrollback.Push(row, false) // sb=2
	tm.grid.AddMark(markPromptStart)    // mark at content row 2+CursorR=2
	for range 10 {
		tm.grid.Scrollback.Push(row, false) // sb=12
	}
	tm.grid.ViewOffset = 0
	tm.grid.Mu.Unlock()
	tm.jumpToMark(true, &gui.Window{})
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset == 0 {
		t.Error("expected viewport to move to mark")
	}
	tm.grid.Mu.Unlock()
}

// ---------------------------------------------------------------------------
// showScrollbar
// ---------------------------------------------------------------------------

func TestShowScrollbar_SetsTimer(t *testing.T) {
	tm := newScrollTerm(24, 80)
	if tm.scrollbar.timer != nil {
		t.Fatal("timer should start nil")
	}
	tm.showScrollbar()
	if tm.scrollbar.timer == nil {
		t.Fatal("showScrollbar should create timer")
	}
	if tm.scrollbar.until.Before(time.Now()) {
		t.Error("until should be in the future")
	}
	// Second call should reset, not create new.
	oldTimer := tm.scrollbar.timer
	tm.showScrollbar()
	if tm.scrollbar.timer != oldTimer {
		t.Error("second showScrollbar should reuse existing timer")
	}
	tm.scrollbar.timer.Stop()
}
