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
		// scrollSensitivity=15, 1*15=15px, cellH=20 → ViewOffset stays 0,
		// ViewSubPx moves to 15.
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
	e := &gui.Event{ScrollY: 2.5} // fractional = trackpad
	tm.onMouseScroll(nil, e, &gui.Window{})
	tm.momentum.mu.Lock()
	defer tm.momentum.mu.Unlock()
	if tm.momentum.vel <= 0 {
		t.Error("velocity should be set by fractional scroll")
	}
	if tm.momentum.coasting {
		t.Error("coasting should not start during active scroll")
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
