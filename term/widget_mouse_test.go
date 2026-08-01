package term

import (
	"math"
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// ---------------------------------------------------------------------------
// posToCell
// ---------------------------------------------------------------------------

func TestPosToCell_Basic(t *testing.T) {
	tm, _ := newMouseTerm(24, 80)
	r, c := tm.posToCell(15, 25)
	if r != 1 || c != 1 {
		t.Fatalf("got (%d,%d), want (1,1)", r, c)
	}
}

func TestPosToCell_ClampedToBounds(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	r, c := tm.posToCell(-5, -5)
	if r != 0 || c != 0 {
		t.Fatalf("negative input: got (%d,%d), want (0,0)", r, c)
	}
	r, c = tm.posToCell(9999, 9999)
	if r != 3 || c != 7 {
		t.Fatalf("overflow input: got (%d,%d), want (3,7)", r, c)
	}
}

// TestPosToCell_SubPixelScroll verifies the pixel→row inversion accounts for
// ViewSubPx. When smooth-scrolled the renderer shifts each visible row down by
// ViewSubPx, so row r spans [r*cellH+ViewSubPx, (r+1)*cellH+ViewSubPx). A click
// in the bottom sliver of a row (past the next cell boundary but before the
// shifted next row starts) must still map to that row, not the one below.
func TestPosToCell_SubPixelScroll(t *testing.T) {
	tm, _ := newMouseTerm(24, 80) // cellH = 20
	tm.grid.ViewSubPx = 8
	// Row 0 band is [8, 28). y=25 is past the cellH=20 boundary but still in
	// row 0's shifted band. Pre-fix int(25/20)=1 selected the wrong row.
	if r, _ := tm.posToCell(5, 25); r != 0 {
		t.Fatalf("y=25 with ViewSubPx=8: got row %d, want 0", r)
	}
	// Row 1 band is [28, 48). y=30 must map to row 1.
	if r, _ := tm.posToCell(5, 30); r != 1 {
		t.Fatalf("y=30 with ViewSubPx=8: got row %d, want 1", r)
	}
}

func TestPosToCell_ZeroCellMetrics(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8), cellW: 0, cellH: 0}
	r, c := tm.posToCell(50, 100)
	if r != 0 || c != 0 {
		t.Fatalf("zero metrics: got (%d,%d), want (0,0)", r, c)
	}
}

// ---------------------------------------------------------------------------
// posToSelCol
// ---------------------------------------------------------------------------

func TestPosToSelCol_Basic(t *testing.T) {
	tm, _ := newMouseTerm(4, 8) // cellW=10
	// x=5  → 5/10=0.5 → floor(1.0)=1 (boundary after cell 0)
	if got := tm.posToSelCol(5); got != 1 {
		t.Errorf("x=5: got %d, want 1", got)
	}
	// x=14 → 14/10=1.4 → floor(1.9)=1 (still in cell 1)
	if got := tm.posToSelCol(14); got != 1 {
		t.Errorf("x=14: got %d, want 1", got)
	}
	// x=15 → 15/10=1.5 → floor(2.0)=2 (tie rounds up)
	if got := tm.posToSelCol(15); got != 2 {
		t.Errorf("x=15: got %d, want 2", got)
	}
}

func TestPosToSelCol_ZeroCellW(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8), cellW: 0, cellH: 20}
	if got := tm.posToSelCol(50); got != 0 {
		t.Errorf("zero cellW: got %d, want 0", got)
	}
}

func TestPosToSelCol_NegativeCellW(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8), cellW: -1, cellH: 20}
	if got := tm.posToSelCol(50); got != 0 {
		t.Errorf("negative cellW: got %d, want 0", got)
	}
}

func TestPosToSelCol_ClampedToBounds(t *testing.T) {
	tm, _ := newMouseTerm(4, 8) // cellW=10, Cols=8
	if got := tm.posToSelCol(-50); got != 0 {
		t.Errorf("negative x: got %d, want 0", got)
	}
	if got := tm.posToSelCol(999); got != 8 {
		t.Errorf("overflow x: got %d, want 8", got)
	}
}

func TestPosToSelCol_NaNInf(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	if got := tm.posToSelCol(float32(math.NaN())); got != 0 {
		t.Errorf("NaN: got %d, want 0", got)
	}
	if got := tm.posToSelCol(float32(math.Inf(1))); got != 0 {
		t.Errorf("+Inf: got %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// onClick
// ---------------------------------------------------------------------------

func TestOnClick_LeftButtonSelectionAnchor(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	// MouseX=15 is the center of cell 1 (cellW=10); nearest boundary is 2.
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(nil, e, &gui.Window{})
	if !tm.mouse.dragging || tm.mouse.dragReport {
		t.Error("expected local drag, not report drag")
	}
	if !e.IsHandled {
		t.Error("event should be handled")
	}
	if len(*buf) != 0 {
		t.Errorf("expected no pty output, got %q", *buf)
	}
	func() {
		tm.grid.Mu.Lock()
		defer tm.grid.Mu.Unlock()
		if tm.grid.SelAnchor != (contentPos{Row: 1, Col: 2}) {
			t.Errorf("SelAnchor = %v, want {Row:1 Col:2}", tm.grid.SelAnchor)
		}
	}()
}

func TestOnClick_RightButtonNoSelection(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseRight}
	tm.onClick(nil, e, &gui.Window{})
	if e.IsHandled {
		t.Error("right button without reporting should not be handled")
	}
	if len(*buf) != 0 {
		t.Errorf("expected no output, got %q", *buf)
	}
}

func TestOnClick_SGRLeftPress(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{
		MouseX: 15, MouseY: 25,
		MouseButton: gui.MouseLeft,
	}
	tm.onClick(nil, e, &gui.Window{})
	got := string(*buf)
	if !strings.HasPrefix(got, "\x1b[<0;2;2M") {
		t.Errorf("got %q, want \\x1b[<0;2;2M", got)
	}
	if !tm.mouse.dragging || !tm.mouse.dragReport {
		t.Error("expected SGR drag tracking")
	}
	if !e.IsHandled {
		t.Error("event should be handled")
	}
}

func TestOnClick_SGRWithModifiers(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{
		MouseX: 15, MouseY: 25,
		MouseButton: gui.MouseLeft,
		Modifiers:   gui.ModShift | gui.ModCtrl,
	}
	tm.onClick(nil, e, &gui.Window{})
	got := string(*buf)
	// shift=4, ctrl=16, base=0 → cb=20
	if !strings.HasPrefix(got, "\x1b[<20;2;2M") {
		t.Errorf("got %q, want \\x1b[<20;2;2M", got)
	}
}

func TestOnClick_UnsupportedButton(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{
		MouseX: 15, MouseY: 25,
		MouseButton: gui.MouseInvalid,
	}
	tm.onClick(nil, e, &gui.Window{})
	if len(*buf) != 0 {
		t.Errorf("expected no output for unsupported button, got %q", *buf)
	}
	if tm.mouse.dragging {
		t.Error("should not enter drag for unsupported button")
	}
}

func TestOnClick_OnClickFocusCallbackFires(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	called := false
	tm.cfg.OnClickFocus = func() { called = true }
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(nil, e, &gui.Window{})
	if !called {
		t.Error("OnClickFocus callback was not called")
	}
}

func TestOnClick_NilOnClickFocusNoPanic(t *testing.T) {
	// Every existing onClick test already exercises this path;
	// this test makes the nil-safety explicit.
	tm, _ := newMouseTerm(4, 8)
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	// Should not panic.
	tm.onClick(nil, e, &gui.Window{})
}

// ---------------------------------------------------------------------------
// onMouseMove
// ---------------------------------------------------------------------------

func TestOnMouseMove_SelectionExtend(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	// Set up anchor at (0,0), start dragging.
	tm.grid.Mu.Lock()
	tm.grid.SelAnchor = contentPos{Row: 0, Col: 0}
	tm.grid.SelHead = contentPos{Row: 0, Col: 0}
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = false
	// Move to row 3 (65/20=3); MouseX=55 is the center of cell 5, nearest
	// boundary 6.
	e := &gui.Event{MouseX: 55, MouseY: 65}
	tm.onMouseMove(nil, e, &gui.Window{})
	if len(*buf) != 0 {
		t.Errorf("local drag should not write to pty, got %q", *buf)
	}
	func() {
		tm.grid.Mu.Lock()
		defer tm.grid.Mu.Unlock()
		if tm.grid.SelHead != (contentPos{Row: 3, Col: 6}) {
			t.Errorf("SelHead = %v, want col=6", tm.grid.SelHead)
		}
		if !tm.grid.SelActive {
			t.Error("selection should be active after extend")
		}
	}()
}

// TestOnMouseMove_SingleCharSelect is the regression guard for the boundary
// selection model: pressing on a character and dragging one cell to the right
// must select exactly that one character, not two. With cell-index inclusive
// selection this returned "ab".
func TestOnMouseMove_SingleCharSelect(t *testing.T) {
	tm, _ := newMouseTerm(4, 8) // cellW=10, cellH=20
	tm.grid.Mu.Lock()
	tm.grid.Cells[0].Ch = 'a'
	tm.grid.Cells[1].Ch = 'b'
	tm.grid.Mu.Unlock()

	// Press near the left edge of cell 0 → boundary 0.
	down := &gui.Event{MouseX: 2, MouseY: 5, MouseButton: gui.MouseLeft}
	tm.onClick(nil, down, &gui.Window{})
	// Drag to the left part of cell 1 → boundary 1. Half-open [0,1) = cell 0.
	move := &gui.Event{MouseX: 12, MouseY: 5}
	tm.onMouseMove(nil, move, &gui.Window{})

	tm.grid.Mu.Lock()
	got := tm.grid.SelectedText()
	tm.grid.Mu.Unlock()
	if got != "a" {
		t.Errorf("one-cell drag selected %q, want %q", got, "a")
	}
}

func TestOnMouseMove_AutoScrollUp(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.SelAnchor = contentPos{Row: 0, Col: 0}
	tm.grid.SelHead = contentPos{Row: 0, Col: 0}
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	// Mouse above widget → negative Y triggers auto-scroll up.
	e := &gui.Event{MouseX: 10, MouseY: -10}
	tm.onMouseMove(nil, e, &gui.Window{})
	if tm.autoScrollDir.Load() != 1 {
		t.Errorf("autoScrollDir = %d, want 1", tm.autoScrollDir.Load())
	}
}

func TestOnMouseMove_CancelMomentum(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 100
	tm.momentum.mu.Unlock()
	e := &gui.Event{MouseX: 10, MouseY: 10}
	tm.onMouseMove(nil, e, &gui.Window{})
	tm.momentum.mu.Lock()
	if tm.momentum.coasting || tm.momentum.vel != 0 {
		t.Error("momentum should be cancelled on mouse move")
	}
	tm.momentum.mu.Unlock()
}

func TestOnMouseMove_SGRAnyMotionReport(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrackAny = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45}
	tm.onMouseMove(nil, e, &gui.Window{})
	got := string(*buf)
	// cb=35 (motion, no button), col=3+1=4, row=2+1=3
	if !strings.HasPrefix(got, "\x1b[<35;4;3M") {
		t.Errorf("got %q, want \\x1b[<35;4;3M", got)
	}
}

func TestOnMouseMove_SGRDragReport(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrackBtn = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR = 0
	tm.mouse.lastC = 0
	e := &gui.Event{MouseX: 35, MouseY: 45}
	tm.onMouseMove(nil, e, &gui.Window{})
	got := string(*buf)
	// base=0, +32 = 32, col=3+1=4, row=2+1=3
	if !strings.HasPrefix(got, "\x1b[<32;4;3M") {
		t.Errorf("got %q, want \\x1b[<32;4;3M", got)
	}
}

// ---------------------------------------------------------------------------
// onMouseUp
// ---------------------------------------------------------------------------

func TestOnMouseUp_SGRReleaseReport(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR = 2
	tm.mouse.lastC = 3
	e := &gui.Event{MouseX: 35, MouseY: 45}
	tm.onMouseUp(nil, e, &gui.Window{})
	got := string(*buf)
	if !strings.Contains(got, "m") {
		t.Errorf("release should end with 'm', got %q", got)
	}
	if tm.mouse.dragging || tm.mouse.dragReport {
		t.Error("drag should be cleared on release")
	}
	if !e.IsHandled {
		t.Error("event should be handled")
	}
}

func TestOnMouseUp_NotDraggingNoOp(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	e := &gui.Event{MouseX: 15, MouseY: 25}
	tm.onMouseUp(nil, e, &gui.Window{})
	if len(*buf) != 0 {
		t.Errorf("expected no output, got %q", *buf)
	}
	if e.IsHandled {
		t.Error("should not be handled when not dragging")
	}
}

func TestOnMouseUp_LocalReleaseClearsSelection(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.SelAnchor = contentPos{Row: 0, Col: 0}
	tm.grid.SelHead = contentPos{Row: 0, Col: 3}
	tm.grid.SelActive = true
	tm.grid.Cells[0].Ch = 'a'
	tm.grid.Cells[1].Ch = 'b'
	tm.grid.Cells[2].Ch = 'c'
	tm.grid.Cells[3].Ch = 'd'
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = false
	e := &gui.Event{MouseX: 35, MouseY: 5, MouseButton: gui.MouseLeft}
	tm.onMouseUp(nil, e, &gui.Window{})
	if !e.IsHandled {
		t.Error("event should be handled")
	}
}

// ---------------------------------------------------------------------------
// shift-click extend
// ---------------------------------------------------------------------------

// A plain click must leave the anchor behind (inactive) so a following
// Shift+click can extend from it. A full ClearSelection here would wipe the
// anchor and break click-then-Shift+click.
func TestOnMouseUp_PlainClickPreservesAnchor(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	// MouseX=15 → cell boundary 2; MouseY=25 → row 1.
	down := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(nil, down, &gui.Window{})
	up := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onMouseUp(nil, up, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelActive {
		t.Error("plain click should leave the selection inactive")
	}
	if !tm.grid.hasSelAnchor {
		t.Error("anchor must survive a plain-click release for later shift-extend")
	}
	if tm.grid.SelAnchor != (contentPos{Row: 1, Col: 2}) {
		t.Errorf("SelAnchor = %v, want {Row:1 Col:2} preserved", tm.grid.SelAnchor)
	}
}

// Click, release, then Shift+click a different cell: the anchor stays put and
// only the head moves, activating the selection — xterm/iTerm2 behavior.
func TestOnClick_ShiftExtendFromAnchor(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.onClick(nil, &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}, &gui.Window{})
	tm.onMouseUp(nil, &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}, &gui.Window{})

	// MouseX=55 → boundary 6; MouseY=65 → row 3.
	shift := &gui.Event{
		MouseX: 55, MouseY: 65, MouseButton: gui.MouseLeft, Modifiers: gui.ModShift,
	}
	tm.onClick(nil, shift, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelAnchor != (contentPos{Row: 1, Col: 2}) {
		t.Errorf("SelAnchor = %v, want {Row:1 Col:2} (anchor preserved)", tm.grid.SelAnchor)
	}
	if tm.grid.SelHead != (contentPos{Row: 3, Col: 6}) {
		t.Errorf("SelHead = %v, want {Row:3 Col:6}", tm.grid.SelHead)
	}
	if !tm.grid.SelActive {
		t.Error("shift-extend to a different cell should activate the selection")
	}
}

// Shift+click as the very first action has no prior anchor, so it anchors
// normally instead of extending from the zero-value contentPos{}.
func TestOnClick_ShiftClickNoPriorAnchorReanchors(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	e := &gui.Event{
		MouseX: 55, MouseY: 65, MouseButton: gui.MouseLeft, Modifiers: gui.ModShift,
	}
	tm.onClick(nil, e, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelAnchor != (contentPos{Row: 3, Col: 6}) {
		t.Errorf("SelAnchor = %v, want {Row:3 Col:6} (fresh anchor)", tm.grid.SelAnchor)
	}
	if tm.grid.SelActive {
		t.Error("fresh shift-click (head == anchor) should be inactive")
	}
	if !tm.grid.hasSelAnchor {
		t.Error("hasSelAnchor should be set after any click")
	}
}

// A selection-invalidating event (here ClearSelection, as scroll/reset emit)
// resets hasSelAnchor, so the next Shift+click starts a fresh anchor rather
// than extending from a now-meaningless content position.
func TestOnClick_ShiftExtendAfterClearReanchors(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.onClick(nil, &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}, &gui.Window{})
	tm.grid.Mu.Lock()
	tm.grid.ClearSelection()
	tm.grid.Mu.Unlock()

	tm.onClick(nil, &gui.Event{
		MouseX: 55, MouseY: 65, MouseButton: gui.MouseLeft, Modifiers: gui.ModShift,
	}, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelAnchor != (contentPos{Row: 3, Col: 6}) {
		t.Errorf("SelAnchor = %v, want {Row:3 Col:6}; ClearSelection must reset hasSelAnchor",
			tm.grid.SelAnchor)
	}
	if tm.grid.SelActive {
		t.Error("fresh anchor after clear should be inactive")
	}
}

// ---------------------------------------------------------------------------
// canvas offset — mouse-lock callbacks use absolute coords
// ---------------------------------------------------------------------------

func TestOnMouseMove_CanvasOffset(t *testing.T) {
	tm, _ := newMouseTerm(4, 8) // cellH=20
	tm.ime.layoutY = 20         // simulate tab-bar offset

	tm.grid.Mu.Lock()
	tm.grid.SelAnchor = contentPos{Row: 0, Col: 0}
	tm.grid.SelHead = contentPos{Row: 0, Col: 0}
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = false
	tm.mouse.locked = true // selection drag holds a mouse lock

	// Absolute Y=25 → canvas Y=5 → row 0. Without fix: r=1 (one row off).
	e := &gui.Event{MouseX: 10, MouseY: 25}
	tm.onMouseMove(nil, e, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelHead.Row != 0 {
		t.Errorf("offset canvas: SelHead.Row = %d, want 0", tm.grid.SelHead.Row)
	}
}

// A drag reported to the pty (?1000/?1002/?1003 — what full-screen apps such
// as Claude Code / opencode use) never takes a mouse lock, so its motion
// events arrive through the canvas config already relative. Translating them
// again would shift every report down by the tab-bar height.
func TestOnMouseMove_CanvasOffset_ReportingDragIsRelative(t *testing.T) {
	tm, buf := newMouseTerm(4, 8) // cellH=20, cellW=10
	tm.ime.layoutY = 20           // simulate tab-bar offset

	tm.grid.Mu.Lock()
	tm.grid.MouseSGR = true
	tm.grid.MouseTrackBtn = true // ?1002 — report motion while a button is held
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR, tm.mouse.lastC = -1, -1

	// Relative Y=25 → r=1 → SGR row=2. Pre-fix: absolute-coord translation
	// dropped it to r=0 → row=1, one line off.
	e := &gui.Event{MouseX: 10, MouseY: 25}
	tm.onMouseMove(nil, e, &gui.Window{})

	got := string(*buf)
	if !strings.Contains(got, ";2M") {
		t.Errorf("unlocked reporting drag: got %q, want row 2", got)
	}
}

func TestOnMouseUp_CanvasOffset(t *testing.T) {
	tm, buf := newMouseTerm(4, 8) // cellH=20, cellW=10
	tm.ime.layoutY = 20

	tm.grid.Mu.Lock()
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.locked = true

	// Absolute Y=25 → canvas Y=5 → r=0 → SGR row=1. Without fix: r=1 → row=2.
	e := &gui.Event{MouseX: 10, MouseY: 25}
	tm.onMouseUp(nil, e, &gui.Window{})

	got := string(*buf)
	if !strings.Contains(got, ";1m") {
		t.Errorf("offset canvas SGR: got %q, want row 1", got)
	}
}

// Release of an unlocked (reported) drag: coords are already relative.
func TestOnMouseUp_CanvasOffset_ReportingDragIsRelative(t *testing.T) {
	tm, buf := newMouseTerm(4, 8) // cellH=20, cellW=10
	tm.ime.layoutY = 20

	tm.grid.Mu.Lock()
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft

	// Relative Y=25 → r=1 → SGR row=2. Pre-fix: row=1.
	e := &gui.Event{MouseX: 10, MouseY: 25}
	tm.onMouseUp(nil, e, &gui.Window{})

	got := string(*buf)
	if !strings.Contains(got, ";2m") {
		t.Errorf("unlocked reporting release: got %q, want row 2", got)
	}
}

// ---------------------------------------------------------------------------
// lockMouse / unlockMouse / toCanvasRel helpers
// ---------------------------------------------------------------------------

func TestLockMouse_SetsLockedFlag(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.lockMouse(&gui.Window{})
	if !tm.mouse.locked {
		t.Error("lockMouse should set locked = true")
	}
}

func TestUnlockMouse_ClearsLockedFlag(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.mouse.locked = true
	tm.unlockMouse(&gui.Window{})
	if tm.mouse.locked {
		t.Error("unlockMouse should set locked = false")
	}
}

func TestLockMouse_NilWindowNoPanic(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.lockMouse(nil) // must not panic
	if tm.mouse.locked {
		t.Error("lockMouse with nil Window should not set locked flag")
	}
}

func TestUnlockMouse_NilWindowNoPanic(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.mouse.locked = true
	tm.unlockMouse(nil) // must not panic
	if tm.mouse.locked {
		t.Error("unlockMouse with nil Window should still clear locked flag")
	}
}

func TestToCanvasRel_LockedOffsetsCoords(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.ime.layoutX = 10
	tm.ime.layoutY = 20
	tm.mouse.locked = true

	e := &gui.Event{MouseX: 100, MouseY: 80}
	tm.toCanvasRel(e)

	if e.MouseX != 90 || e.MouseY != 60 {
		t.Errorf("toCanvasRel (locked): got (%0.f, %0.f), want (90, 60)",
			e.MouseX, e.MouseY)
	}
}

func TestToCanvasRel_NotLockedNoOp(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.ime.layoutX = 10
	tm.ime.layoutY = 20
	tm.mouse.locked = false

	e := &gui.Event{MouseX: 100, MouseY: 80}
	tm.toCanvasRel(e)

	if e.MouseX != 100 || e.MouseY != 80 {
		t.Errorf("toCanvasRel (not locked): got (%0.f, %0.f), want (100, 80)",
			e.MouseX, e.MouseY)
	}
}

// ---------------------------------------------------------------------------
// onMouseScroll
// ---------------------------------------------------------------------------

func TestOnMouseScroll_ZeroDeltaCancelsMomentum(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 50
	tm.momentum.mu.Unlock()
	e := &gui.Event{ScrollY: 0}
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.momentum.mu.Lock()
	if tm.momentum.coasting || tm.momentum.vel != 0 {
		t.Error("momentum should be cancelled on zero delta")
	}
	tm.momentum.mu.Unlock()
}

func TestOnMouseScroll_SGRWheelUp(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 1}
	tm.onMouseScroll(nil, e, &gui.Window{})
	got := string(*buf)
	// wheel up → base=64, col=3+1=4, row=2+1=3
	if !strings.HasPrefix(got, "\x1b[<64;4;3M") {
		t.Errorf("got %q, want \\x1b[<64;4;3M", got)
	}
	if !e.IsHandled {
		t.Error("event should be handled")
	}
}

func TestOnMouseScroll_SGRWheelDown(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: -1}
	tm.onMouseScroll(nil, e, &gui.Window{})
	got := string(*buf)
	// wheel down → base=65, col=3+1=4, row=2+1=3
	if !strings.HasPrefix(got, "\x1b[<65;4;3M") {
		t.Errorf("got %q, want \\x1b[<65;4;3M", got)
	}
}

func TestOnMouseScroll_LocalScrollBack(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(10, 8)
	row := make([]cell, 8)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 5 {
		tm.grid.Scrollback.Push(row, false) // 5 rows of scrollback
	}
	tm.grid.ViewOffset = 0
	tm.grid.ViewSubPx = 0
	tm.grid.Mu.Unlock()
	prevVer := tm.drawVersion.Load()
	e := &gui.Event{ScrollY: 1} // integer = mouse wheel
	tm.onMouseScroll(nil, e, &gui.Window{})
	func() {
		tm.grid.Mu.Lock()
		defer tm.grid.Mu.Unlock()
		// wheelSensitivity=5, 1*5=5px, cellH=20 → ViewSubPx=5.
		if tm.grid.ViewSubPx == 0 && tm.grid.ViewOffset == 0 {
			t.Error("expected viewport movement after scroll")
		}
	}()
	if tm.drawVersion.Load() <= prevVer {
		t.Error("version should be bumped after scroll")
	}
}

func TestOnMouseScroll_FractionalDeltaStartsMomentum(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	e := &gui.Event{ScrollY: 2.5, ScrollPrecise: true} // trackpad
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.momentum.mu.Lock()
	defer tm.momentum.mu.Unlock()
	if tm.momentum.vel <= 0 {
		t.Error("velocity should be set by precise (trackpad) scroll")
	}
	if tm.momentum.coasting {
		t.Error("coasting should not start during active scroll")
	}
}

// TestOnMouseScroll_NonPreciseIsMouseWheel verifies that a scroll delta
// without ScrollPrecise is classified as a mouse wheel and cancels
// momentum, regardless of the delta's fractional value.
func TestOnMouseScroll_NonPreciseIsMouseWheel(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(10, 8)
	row := make([]cell, 8)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 5 {
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.Mu.Unlock()
	// Pre-seed momentum state so we can observe cancellation.
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = 50
	tm.momentum.mu.Unlock()
	e := &gui.Event{ScrollY: 2.5} // fractional but non-precise = wheel
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	// wheelSensitivity=5, 2.5*5=12.5px, cellH=20 → ViewSubPx=12.5.
	if tm.grid.ViewSubPx == 0 && tm.grid.ViewOffset == 0 {
		t.Error("expected viewport movement from wheel scroll")
	}
	tm.momentum.mu.Lock()
	defer tm.momentum.mu.Unlock()
	if tm.momentum.coasting || tm.momentum.vel != 0 {
		t.Error("momentum should be cancelled for non-precise (mouse wheel) delta")
	}
}

// TestOnMouseScroll_NonPreciseReverse confirms that a small non-precise
// negative delta also cancels momentum.
func TestOnMouseScroll_NonPreciseReverse(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.momentum.mu.Lock()
	tm.momentum.coasting = true
	tm.momentum.vel = -50
	tm.momentum.mu.Unlock()
	e := &gui.Event{ScrollY: -1.0001}
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.momentum.mu.Lock()
	defer tm.momentum.mu.Unlock()
	if tm.momentum.coasting || tm.momentum.vel != 0 {
		t.Error("momentum should be cancelled for non-precise negative delta")
	}
}

// TestOnMouseScroll_TrackpadSensitivity verifies that precise deltas use
// the trackpad factor: ScrollY=0.5 precise → 0.5*10=5px at cellH=20 →
// ViewSubPx=5 (a wheel delta of 0.5 would only move 2.5px).
func TestOnMouseScroll_TrackpadSensitivity(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.Scrollback.SetGeom(10, 8)
	row := make([]cell, 8)
	for i := range row {
		row[i] = defaultCell()
	}
	for range 5 {
		tm.grid.Scrollback.Push(row, false)
	}
	tm.grid.Mu.Unlock()
	e := &gui.Event{ScrollY: 0.5, ScrollPrecise: true}
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got := tm.grid.ViewSubPx; got != 5 {
		t.Errorf("ViewSubPx = %v, want 5", got)
	}
}

// countReports returns the number of SGR reports in buf with the given
// prefix, e.g. "\x1b[<64;" for unmodified wheel-up.
func countReports(buf []byte, prefix string) int {
	return strings.Count(string(buf), prefix)
}

// TestOnMouseScroll_SGRWheelDeltaMultiplier verifies that a large wheel
// delta emits multiple SGR wheel reports — one per cellH of scaled scroll
// distance — instead of a single tick per event. cellH=20,
// wheelSensitivity=5: ScrollY=10 → 50px → 2 ticks.
func TestOnMouseScroll_SGRWheelDeltaMultiplier(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 10}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<64;4;3M"); got != 2 {
		t.Errorf("ticks = %d, want 2 (buf %q)", got, *buf)
	}
}

// TestOnMouseScroll_SGRWheelResidualAccumulates verifies that the
// fractional remainder carries across events: two ScrollY=10 events are
// 100px total → 5 ticks at cellH=20 (2 then 3).
func TestOnMouseScroll_SGRWheelResidualAccumulates(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 10}
	tm.onMouseScroll(nil, e, &gui.Window{})
	e = &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 10}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<64;4;3M"); got != 5 {
		t.Errorf("ticks = %d, want 5 (buf %q)", got, *buf)
	}
}

// TestOnMouseScroll_SGRWheelMinOneTick verifies that a small delta still
// emits exactly one report so a slow notch never feels dead.
func TestOnMouseScroll_SGRWheelMinOneTick(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 1}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<64;4;3M"); got != 1 {
		t.Errorf("ticks = %d, want 1 (buf %q)", got, *buf)
	}
}

// TestOnMouseScroll_SGRWheelDirectionResetsResidual verifies that
// reversing scroll direction discards the accumulated residual: up
// ScrollY=10 leaves 10px residual, then down ScrollY=10 must emit
// floor(50/20)=2 down ticks, not 3.
func TestOnMouseScroll_SGRWheelDirectionResetsResidual(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 10}
	tm.onMouseScroll(nil, e, &gui.Window{})
	e = &gui.Event{MouseX: 35, MouseY: 45, ScrollY: -10}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<65;4;3M"); got != 2 {
		t.Errorf("down ticks = %d, want 2 (buf %q)", got, *buf)
	}
}

// TestOnMouseScroll_SGRWheelTickCap verifies a huge delta is capped so a
// single event cannot flood the pty.
func TestOnMouseScroll_SGRWheelTickCap(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 1e6}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<64;4;3M"); got != maxWheelTicks {
		t.Errorf("ticks = %d, want cap %d", got, maxWheelTicks)
	}
}

// TestOnMouseScroll_SGRTrackpadTicks verifies that precise deltas use the
// trackpad factor in the report path: ScrollY=4 precise → 4*10=40px at
// cellH=20 → 2 ticks (a wheel delta of 4 would emit only 1).
func TestOnMouseScroll_SGRTrackpadTicks(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 35, MouseY: 45, ScrollY: 4, ScrollPrecise: true}
	tm.onMouseScroll(nil, e, &gui.Window{})
	if got := countReports(*buf, "\x1b[<64;4;3M"); got != 2 {
		t.Errorf("ticks = %d, want 2 (buf %q)", got, *buf)
	}
}

// TestWheelReportTicks_NaNReturnsOne verifies that NaN scrollY returns 1
// tick rather than propagating NaN or panicking.
func TestWheelReportTicks_NaNReturnsOne(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	got := tm.wheelReportTicks(float32(math.NaN()), false)
	if got != 1 {
		t.Errorf("ticks = %d, want 1", got)
	}
}

// TestWheelReportTicks_InfReturnsOne verifies that Inf scrollY returns 1
// tick (capped, not infinite).
func TestWheelReportTicks_InfReturnsOne(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	got := tm.wheelReportTicks(float32(math.Inf(1)), false)
	if got != 1 {
		t.Errorf("ticks = %d, want 1", got)
	}
}

// TestWheelReportTicks_ZeroCellHReturnsOne verifies that a zero cell
// height (before first measure) returns 1 tick instead of dividing.
func TestWheelReportTicks_ZeroCellHReturnsOne(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.cellH = 0
	got := tm.wheelReportTicks(10, false)
	if got != 1 {
		t.Errorf("ticks = %d, want 1", got)
	}
}

// TestScrollSensitivityFor_PreciseVsWheel verifies that the precise flag
// selects the trackpad factor (20) while non-precise gets the wheel
// factor (5).
func TestScrollSensitivityFor_PreciseVsWheel(t *testing.T) {
	if got := scrollSensitivityFor(true); got != trackpadSensitivity {
		t.Errorf("precise factor = %v, want %v", got, trackpadSensitivity)
	}
	if got := scrollSensitivityFor(false); got != wheelSensitivity {
		t.Errorf("wheel factor = %v, want %v", got, wheelSensitivity)
	}
}

// ---------------------------------------------------------------------------
// updateHover
// ---------------------------------------------------------------------------

func TestUpdateHover_SameCellNoOp(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.mouse.hoverR.Store(5)
	tm.mouse.hoverC.Store(5)
	prevVer := tm.drawVersion.Load()
	tm.updateHover(5, 5, &gui.Window{})
	if tm.drawVersion.Load() != prevVer {
		t.Error("version should not change for same cell")
	}
}

func TestUpdateHover_NewCellUpdatesCoords(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)
	prevVer := tm.drawVersion.Load()
	tm.updateHover(2, 3, &gui.Window{})
	// Moving to a new cell should update hover state.
	if tm.mouse.hoverR.Load() != 2 || tm.mouse.hoverC.Load() != 3 {
		t.Error("hover state should be updated")
	}
	// Version only bumps when entering/leaving a hyperlinked cell run.
	// For a plain cell, no version bump is expected.
	_ = prevVer
}

func TestUpdateHover_CmdHeldDetectsImplicitURL(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	putRow(tm.grid, "see https://go.dev now")
	// Simulate Cmd being held.
	tm.mouse.cmdHeld.Store(true)
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)
	prevVer := tm.drawVersion.Load()
	// Hover over the URL text (col 4).
	tm.updateHover(0, 4, &gui.Window{})
	if tm.mouse.hoverURL == "" {
		t.Error("Cmd+hover over URL: expected detected URL, got empty")
	}
	if len(tm.mouse.hoverSpans) == 0 {
		t.Error("Cmd+hover over URL: expected hoverSpans, got none")
	}
	if tm.drawVersion.Load() <= prevVer {
		t.Error("Cmd+hover over URL: version should bump for highlight")
	}
}

func TestUpdateHover_CmdHeldPlainTextNoURL(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	putRow(tm.grid, "just plain text, no urls here")
	tm.mouse.cmdHeld.Store(true)
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)
	prevVer := tm.drawVersion.Load()
	tm.updateHover(0, 3, &gui.Window{})
	if tm.mouse.hoverURL != "" {
		t.Errorf("Cmd+hover over plain text: expected no URL, got %q", tm.mouse.hoverURL)
	}
	if len(tm.mouse.hoverSpans) != 0 {
		t.Error("Cmd+hover over plain text: expected empty spans")
	}
	// Moving from (-1,-1) to (0,3) always bumps (new cell).
	_ = prevVer
}

func TestUpdateHover_CmdReleasedClearsURLHighlight(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	putRow(tm.grid, "see https://go.dev now")
	// First, hover with Cmd to detect a URL.
	tm.mouse.cmdHeld.Store(true)
	tm.mouse.hoverR.Store(0)
	tm.mouse.hoverC.Store(4)
	tm.updateHover(0, 4, &gui.Window{})
	if tm.mouse.hoverURL == "" {
		t.Fatal("setup failed: URL not detected with Cmd held")
	}
	prevURL := tm.mouse.hoverURL

	// Release Cmd: hovering the same cell should clear the URL.
	tm.mouse.cmdHeld.Store(false)
	prevVer := tm.drawVersion.Load()
	tm.updateHover(0, 4, &gui.Window{})
	if tm.mouse.hoverURL != "" {
		t.Errorf("Cmd released: expected hoverURL to clear, got %q", tm.mouse.hoverURL)
	}
	if len(tm.mouse.hoverSpans) != 0 {
		t.Error("Cmd released: expected hoverSpans to clear")
	}
	if prevURL != "" && tm.drawVersion.Load() <= prevVer {
		t.Error("Cmd released: version should bump to clear the highlight")
	}
}

// ---------------------------------------------------------------------------
// Multi-click gestures (Phase 48)
// ---------------------------------------------------------------------------

// writeRowText writes s into row r of an existing grid, one rune per cell.
// Distinct from fillRow (floods a row with one marker rune) and putRow
// (writes at the cursor), both in grid_test.go.
func writeRowText(g *grid, r int, s string) {
	for c, ch := range s {
		if c >= g.Cols {
			break
		}
		g.At(r, c).Ch = ch
	}
}

// clickAt presses the left button at the given pixel position. Cells are
// 10x20 in newMouseTerm, so column n spans x = [10n, 10n+10).
func clickAt(tm *Term, x, y float32, mods gui.Modifier) {
	tm.onClick(nil, &gui.Event{
		MouseX: x, MouseY: y,
		MouseButton: gui.MouseLeft,
		Modifiers:   mods,
	}, &gui.Window{})
}

func TestNextClickCount_Cycles(t *testing.T) {
	tm, _ := newMouseTerm(4, 20)
	want := []int{1, 2, 3, 1, 2}
	for i, w := range want {
		if got := tm.nextClickCount(15, 25); got != w {
			t.Errorf("click %d: count = %d, want %d", i+1, got, w)
		}
	}
}

// A click that arrives after the interval starts a fresh gesture. Driven by
// back-dating lastClickAt rather than sleeping.
func TestNextClickCount_TimeoutResets(t *testing.T) {
	tm, _ := newMouseTerm(4, 20)
	if got := tm.nextClickCount(15, 25); got != 1 {
		t.Fatalf("first click = %d, want 1", got)
	}
	tm.mouse.lastClickAt = tm.mouse.lastClickAt.Add(-2 * multiClickInterval)
	if got := tm.nextClickCount(15, 25); got != 1 {
		t.Errorf("click after the interval = %d, want 1", got)
	}
}

// Two fast clicks on different words are two separate clicks, not a double.
func TestNextClickCount_MovementResets(t *testing.T) {
	tm, _ := newMouseTerm(4, 20)
	tm.nextClickCount(15, 25)
	if got := tm.nextClickCount(95, 25); got != 1 {
		t.Errorf("click a cell away = %d, want 1", got)
	}
	// Within half a cell still counts as the same spot.
	tm.resetClickCount()
	tm.nextClickCount(15, 25)
	if got := tm.nextClickCount(18, 27); got != 2 {
		t.Errorf("click within the slop = %d, want 2", got)
	}
}

func TestOnClick_DoubleClickSelectsWord(t *testing.T) {
	tm, _ := newMouseTerm(4, 30)
	writeRowText(tm.grid, 0, "ls /usr/local/bin --all")

	// Two clicks inside the path (column 8 → x = 85).
	clickAt(tm, 85, 10, 0)
	clickAt(tm, 85, 10, 0)

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelMode != selWord {
		t.Errorf("SelMode = %v, want selWord", tm.grid.SelMode)
	}
	if got, want := tm.grid.SelectedText(), "/usr/local/bin"; got != want {
		t.Errorf("double-click selected %q, want %q", got, want)
	}
}

func TestOnClick_TripleClickSelectsLine(t *testing.T) {
	tm, _ := newMouseTerm(4, 10)
	writeRowText(tm.grid, 0, "first")
	writeRowText(tm.grid, 1, "second")

	clickAt(tm, 15, 30, 0) // row 1
	clickAt(tm, 15, 30, 0)
	clickAt(tm, 15, 30, 0)

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelMode != selLine {
		t.Errorf("SelMode = %v, want selLine", tm.grid.SelMode)
	}
	if got, want := tm.grid.SelectedText(), "second"; got != want {
		t.Errorf("triple-click selected %q, want %q", got, want)
	}
}

// A triple-click on a soft-wrapped logical line selects all of it, not just
// the screen row under the pointer.
func TestOnClick_TripleClickSpansWrappedRows(t *testing.T) {
	tm, _ := newMouseTerm(4, 5)
	writeRowText(tm.grid, 0, "abcde")
	writeRowText(tm.grid, 1, "fghij")
	tm.grid.RowWrapped[0] = true

	clickAt(tm, 15, 30, 0) // row 1, the continuation
	clickAt(tm, 15, 30, 0)
	clickAt(tm, 15, 30, 0)

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got, want := tm.grid.SelectedText(), "abcde\nfghij"; got != want {
		t.Errorf("triple-click on a wrapped line selected %q, want %q", got, want)
	}
}

// Dragging after a double click extends by whole words in both directions.
func TestOnMouseMove_WordDragExtends(t *testing.T) {
	tm, _ := newMouseTerm(4, 30)
	writeRowText(tm.grid, 0, "alpha beta gamma delta")

	clickAt(tm, 15, 10, 0) // inside "alpha"
	clickAt(tm, 15, 10, 0)
	tm.onMouseMove(nil, &gui.Event{MouseX: 125, MouseY: 10}, &gui.Window{}) // "gamma"

	tm.grid.Mu.Lock()
	if got, want := tm.grid.SelectedText(), "alpha beta gamma"; got != want {
		t.Errorf("forward word drag selected %q, want %q", got, want)
	}
	tm.grid.Mu.Unlock()

	// Drag back onto the origin word: the selection collapses to it rather
	// than inverting.
	tm.onMouseMove(nil, &gui.Event{MouseX: 15, MouseY: 10}, &gui.Window{})
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got, want := tm.grid.SelectedText(), "alpha"; got != want {
		t.Errorf("drag back to the origin selected %q, want %q", got, want)
	}
}

// Dragging left of the origin word pivots the anchor to the origin's far
// edge, so the selection still covers whole words.
func TestOnMouseMove_WordDragBackwards(t *testing.T) {
	tm, _ := newMouseTerm(4, 30)
	writeRowText(tm.grid, 0, "alpha beta gamma")

	clickAt(tm, 125, 10, 0) // inside "gamma"
	clickAt(tm, 125, 10, 0)
	tm.onMouseMove(nil, &gui.Event{MouseX: 15, MouseY: 10}, &gui.Window{}) // "alpha"

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got, want := tm.grid.SelectedText(), "alpha beta gamma"; got != want {
		t.Errorf("backward word drag selected %q, want %q", got, want)
	}
}

// Dragging after a triple click extends by whole lines in both directions,
// mirroring the word-pivot logic with lineBoundsAt instead of wordBoundsAt.
func TestOnMouseMove_LineDragExtends(t *testing.T) {
	tm, _ := newMouseTerm(4, 10)
	writeRowText(tm.grid, 0, "line one")
	writeRowText(tm.grid, 1, "line two")
	writeRowText(tm.grid, 2, "line three")
	writeRowText(tm.grid, 3, "line four")

	// Triple-click on row 1 ("line two"), then drag down to row 3.
	clickAt(tm, 15, 30, 0) // row 1, inside "line two"
	clickAt(tm, 15, 30, 0)
	clickAt(tm, 15, 30, 0)
	tm.onMouseMove(nil, &gui.Event{MouseX: 15, MouseY: 70}, &gui.Window{}) // row 3

	tm.grid.Mu.Lock()
	if got, want := tm.grid.SelectedText(), "line two\nline three\nline four"; got != want {
		t.Errorf("forward line drag selected %q, want %q", got, want)
	}
	tm.grid.Mu.Unlock()

	// Drag back to the origin line: collapses to the origin line alone.
	tm.onMouseMove(nil, &gui.Event{MouseX: 15, MouseY: 30}, &gui.Window{})
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got, want := tm.grid.SelectedText(), "line two"; got != want {
		t.Errorf("drag back to the origin line selected %q, want %q", got, want)
	}
}

// Dragging left of the origin line pivots the anchor to the origin's far
// edge, so the selection still covers whole lines.
func TestOnMouseMove_LineDragBackwards(t *testing.T) {
	tm, _ := newMouseTerm(4, 10)
	writeRowText(tm.grid, 0, "line one")
	writeRowText(tm.grid, 1, "line two")
	writeRowText(tm.grid, 2, "line three")
	writeRowText(tm.grid, 3, "line four")

	clickAt(tm, 15, 70, 0) // inside "line four" (row 3)
	clickAt(tm, 15, 70, 0)
	clickAt(tm, 15, 70, 0)
	tm.onMouseMove(nil, &gui.Event{MouseX: 15, MouseY: 30}, &gui.Window{}) // row 1

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if got, want := tm.grid.SelectedText(), "line two\nline three\nline four"; got != want {
		t.Errorf("backward line drag selected %q, want %q", got, want)
	}
}

func TestOnClick_AltDragSelectsBlock(t *testing.T) {
	tm, _ := newMouseTerm(4, 10)
	writeRowText(tm.grid, 0, "abcdefghij")
	writeRowText(tm.grid, 1, "klmnopqrst")
	writeRowText(tm.grid, 2, "uvwxyzABCD")

	clickAt(tm, 20, 10, gui.ModAlt) // boundary col 2, row 0
	tm.onMouseMove(nil, &gui.Event{MouseX: 50, MouseY: 50}, &gui.Window{})

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelMode != selBlock {
		t.Fatalf("SelMode = %v, want selBlock", tm.grid.SelMode)
	}
	if got, want := tm.grid.SelectedText(), "cde\nmno\nwxy"; got != want {
		t.Errorf("Alt+drag selected %q, want %q", got, want)
	}
}

// Shift+click extends the previous selection; it must not be read as the
// second click of a double.
func TestOnClick_ShiftClickIsNotADouble(t *testing.T) {
	tm, _ := newMouseTerm(4, 20)
	writeRowText(tm.grid, 0, "alpha beta")

	clickAt(tm, 15, 10, 0)
	clickAt(tm, 15, 10, gui.ModShift)

	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	if tm.grid.SelMode == selWord {
		t.Error("Shift+click was treated as a double click")
	}
}

// ---------------------------------------------------------------------------
// Alt-screen wheel (Phase 48)
// ---------------------------------------------------------------------------

// On the alt screen with no mouse reporting the wheel is otherwise dead, so
// it synthesizes cursor keys — the pager behavior kitty/iTerm2/Ghostty have.
func TestOnMouseScroll_AltScreenSynthesizesArrows(t *testing.T) {
	cases := []struct {
		name    string
		scrollY float32
		want    string
	}{
		{"wheel up sends Up", 4, "\x1b[A"},
		{"wheel down sends Down", -4, "\x1b[B"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tm, buf := newMouseTerm(24, 80)
			tm.grid.AltActive = true
			tm.onMouseScroll(nil, &gui.Event{ScrollY: tc.scrollY}, &gui.Window{})
			if got := string(*buf); !strings.HasPrefix(got, tc.want) {
				t.Errorf("wrote %q, want a prefix of %q", got, tc.want)
			}
			if n := countReports(*buf, tc.want); n < 1 {
				t.Errorf("emitted %d arrows, want at least 1", n)
			}
		})
	}
}

// Under DECCKM the synthesized keys must use the SS3 form the application
// asked for, exactly as a real arrow keypress would.
func TestOnMouseScroll_AltScreenAppCursorKeys(t *testing.T) {
	tm, buf := newMouseTerm(24, 80)
	tm.grid.AltActive = true
	tm.grid.AppCursorKeys = true
	tm.onMouseScroll(nil, &gui.Event{ScrollY: -4}, &gui.Window{})
	if got := string(*buf); !strings.HasPrefix(got, "\x1bOB") {
		t.Errorf("wrote %q, want the SS3 form \\x1bOB", got)
	}
}

// An app that enabled mouse reporting gets real wheel reports, not arrows.
func TestOnMouseScroll_AltScreenReportingWins(t *testing.T) {
	tm, buf := newMouseTerm(24, 80)
	tm.grid.AltActive = true
	tm.grid.MouseTrack = true
	tm.grid.MouseSGR = true
	tm.onMouseScroll(nil, &gui.Event{ScrollY: -4}, &gui.Window{})
	got := string(*buf)
	if strings.Contains(got, "\x1b[B") || strings.Contains(got, "\x1bOB") {
		t.Errorf("wrote arrows %q while mouse reporting is on", got)
	}
	if !strings.Contains(got, "\x1b[<65") {
		t.Errorf("wrote %q, want an SGR wheel-down report", got)
	}
}

// The main screen keeps local scrollback scrolling; arrows there would be
// sent to a shell that never asked for them.
func TestOnMouseScroll_MainScreenDoesNotSynthesize(t *testing.T) {
	tm, buf := newMouseTerm(24, 80)
	tm.onMouseScroll(nil, &gui.Event{ScrollY: -4}, &gui.Window{})
	if got := string(*buf); got != "" {
		t.Errorf("main screen wrote %q, want nothing", got)
	}
}

// TestOSC22AppliesCursor covers the widget half of OSC 22: the shape the
// application asked for is applied on every hover update, and a hovered
// hyperlink still outranks it.
func TestOSC22AppliesCursor(t *testing.T) {
	tm, _ := newDrawTerm(4, 8, 10, 20)
	w := &gui.Window{}

	tm.grid.PointerShape = pointerIBeam
	tm.updateHover(1, 1, w)
	if got := w.MouseCursorState(); got != gui.CursorIBeam {
		t.Fatalf("cursor = %v, want CursorIBeam", got)
	}

	// go-gui resets the cursor to Arrow at the top of every mouse-move event,
	// so a shape held across a motion that changes no cell must be re-applied
	// rather than assumed sticky.
	w.SetMouseCursorArrow()
	tm.updateHover(1, 1, w)
	if got := w.MouseCursorState(); got != gui.CursorIBeam {
		t.Fatalf("shape not re-applied on a same-cell move: %v", got)
	}

	// A hyperlink under the pointer wins: it describes the cell the user is
	// actually on, while OSC 22 is a pane-wide default set at some earlier time.
	tm.grid.CurLinkID = tm.grid.internLink("https://example.com")
	tm.grid.At(2, 2).LinkID = tm.grid.CurLinkID
	tm.mouse.cmdHeld.Store(true)
	tm.updateHover(2, 2, w)
	if got := w.MouseCursorState(); got != gui.CursorPointingHand {
		t.Fatalf("link hover cursor = %v, want CursorPointingHand", got)
	}
}
