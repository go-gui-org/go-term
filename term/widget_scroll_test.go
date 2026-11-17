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

// ---------------------------------------------------------------------------
// jumpScrollbarTo — maps a thumb-top pixel back to a viewport offset
// ---------------------------------------------------------------------------

func TestJumpScrollbarTo(t *testing.T) {
	tm := newScrollTerm(24, 80)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(200, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 100 {
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.ViewOffset = 0
	tm.grid.Mu.Unlock()
	tm.scrollbar.viewH = 480

	// Dragging the thumb to the very top pins the viewport at the oldest row.
	tm.jumpScrollbarTo(0)
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != tm.grid.Scrollback.Len() {
		t.Errorf("thumb top: ViewOffset=%d, want %d", tm.grid.ViewOffset, tm.grid.Scrollback.Len())
	}
	tm.grid.Mu.Unlock()

	// Dragging past the bottom snaps back to the live grid.
	tm.jumpScrollbarTo(tm.scrollbar.viewH)
	tm.grid.Mu.Lock()
	if tm.grid.ViewOffset != 0 || tm.grid.ViewSubPx != 0 {
		t.Errorf("thumb bottom: got (%d,%v), want (0,0)", tm.grid.ViewOffset, tm.grid.ViewSubPx)
	}
	tm.grid.Mu.Unlock()

	// A mid-track position lands somewhere in between.
	tm.jumpScrollbarTo(tm.scrollbar.viewH / 2)
	tm.grid.Mu.Lock()
	off := tm.grid.ViewOffset
	tm.grid.Mu.Unlock()
	if off <= 0 || off >= tm.grid.Scrollback.Len() {
		t.Errorf("mid track: ViewOffset=%d, want strictly between 0 and %d", off, tm.grid.Scrollback.Len())
	}
}

// dragTerm returns a Term whose grid has sb rows of scrollback, a 480 px
// track, and a scrollbar that takes clicks anywhere.
func dragTerm(sb int) *Term {
	tm := newScrollTerm(24, 80)
	tm.grid.ScrollbackCap = 10_000
	for range sb {
		tm.grid.scrollUpRegion(1)
	}
	tm.scrollbar.viewH = 480
	tm.scrollbar.active = true
	tm.scrollbar.hitX0 = 0
	return tm
}

func viewPos(tm *Term) float32 {
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	return float32(tm.grid.ViewOffset) + tm.grid.ViewSubPx/tm.cellH
}

// Output that arrives during a thumb drag pins the view (scrollUpRegion bumps
// ViewOffset). The next pointer move must keep that pin. It must not
// map the pointer to an absolute offset again, because that moves the text
// by up to n rows on each move while the pointer stays still. Before the pin
// change the same jump happened, in the other direction.
func TestScrollbarDrag_KeepsPinFromStreamingOutput(t *testing.T) {
	tm := dragTerm(100)
	const y = 240
	if !tm.scrollbarClick(&gui.Event{MouseX: 1, MouseY: y}, nil) {
		t.Fatal("click on track not taken")
	}
	tm.scrollbarDrag(&gui.Event{MouseY: y}, nil)
	before := viewPos(tm)

	// Output streams in while the pointer is held still.
	tm.grid.Mu.Lock()
	for range 10 {
		tm.grid.scrollUpRegion(1)
	}
	tm.grid.Mu.Unlock()
	if got := viewPos(tm); got != before+10 {
		t.Fatalf("setup: pin gave %v, want %v", got, before+10)
	}

	// A move with no pointer change must not move the view.
	tm.scrollbarDrag(&gui.Event{MouseY: y}, nil)
	if got := viewPos(tm); got != before+10 {
		t.Errorf("still pointer moved the view: %v, want %v", got, before+10)
	}

	// A real move scrolls by the pointer change, starting from the pinned
	// position. Moving down 48 px (a tenth of the track) goes toward live by
	// a tenth of the content (110 scrollback + 24 screen rows).
	tm.scrollbarDrag(&gui.Event{MouseY: y + 48}, nil)
	want := before + 10 - 13.4
	if got := viewPos(tm); got < want-0.01 || got > want+0.01 {
		t.Errorf("relative move: %v, want %v", got, want)
	}
}

// The ends of the track still map absolutely. Dragging past the bottom
// reaches live, and past the top reaches the oldest row, even after the pin
// moved the thumb away from the pointer.
func TestScrollbarDrag_EndsStillReachable(t *testing.T) {
	tm := dragTerm(100)
	tm.scrollbarClick(&gui.Event{MouseX: 1, MouseY: 240}, nil)
	tm.grid.Mu.Lock()
	for range 30 {
		tm.grid.scrollUpRegion(1)
	}
	tm.grid.Mu.Unlock()

	tm.scrollbarDrag(&gui.Event{MouseY: 480}, nil)
	if got := viewPos(tm); got != 0 {
		t.Errorf("past bottom: view %v, want live (0)", got)
	}
	tm.scrollbarDrag(&gui.Event{MouseY: -50}, nil)
	tm.grid.Mu.Lock()
	sb := tm.grid.Scrollback.Len()
	tm.grid.Mu.Unlock()
	if got := viewPos(tm); got != float32(sb) {
		t.Errorf("past top: view %v, want %d", got, sb)
	}
}
