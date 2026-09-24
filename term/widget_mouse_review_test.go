package term

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// setMouseModes turns on the given reporting modes under grid.Mu.
func setMouseModes(tm *Term, pixels bool) {
	tm.grid.Mu.Lock()
	tm.grid.MouseTrackBtn = true
	tm.grid.MouseSGR = true
	tm.grid.MouseSGRPixels = pixels
	tm.grid.Mu.Unlock()
}

// A press the child sees locks the mouse, so its release reaches onMouseUp even
// when it lands outside the pane. Regression: only selection drags locked, and
// a vim or tmux drag released over the tab bar never sent its release.
func TestOnClick_ReportedPressLocksMouse(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	setMouseModes(tm, false)
	w := &gui.Window{}
	tm.onClick(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 35, MouseY: 45}, Window: w})
	if !tm.mouse.locked {
		t.Fatal("reported press did not lock the mouse")
	}
	// The release arrives through the lock with window coordinates. The pane
	// sits 30 px below a tab bar and the pointer is over that tab bar.
	tm.ime.layoutY = 30
	*buf = (*buf)[:0]
	tm.onMouseUp(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 35, MouseY: 5}, Window: w})
	if got := string(*buf); got != "\x1b[<0;4;1m" {
		t.Errorf("release = %q, want \\x1b[<0;4;1m (clamped to row 1)", got)
	}
	if tm.mouse.locked || tm.mouse.dragging {
		t.Errorf("after release: locked %v, dragging %v; want both false", tm.mouse.locked, tm.mouse.dragging)
	}
}

// A reported drag cancelled because its release was lost (a window resize
// took the mouse-up) still gets a release, so the child leaves its drag mode.
func TestCancelSelectDrag_ReportsLostRelease(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	setMouseModes(tm, false)
	tm.mouse.dragging = true
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR, tm.mouse.lastC = 2, 3
	tm.HandleWindowEvent(&gui.Event{Type: gui.EventResized})
	if got := string(*buf); got != "\x1b[<0;4;3m" {
		t.Errorf("lost release = %q, want \\x1b[<0;4;3m", got)
	}
	if tm.mouse.dragReport {
		t.Error("drag still marked as reported after cancel")
	}
}

// ?1016 reports use device pixels, the unit CSI 14t and CSI 16t report sizes
// in. Regression: on a 2x display a click at point (35,45) was reported at
// pixel (35,45), half its real position.
func TestWriteMouse_PixelsAreDevicePixels(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	setMouseModes(tm, true)
	tm.draw.pxScale = 2
	tm.onClick(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 35, MouseY: 45}, Window: &gui.Window{}})
	if got := string(*buf); got != "\x1b[<0;71;91M" {
		t.Errorf("pixel press = %q, want \\x1b[<0;71;91M", got)
	}
}

// Under ?1016 motion inside one cell is still reported. Regression: motion was
// deduped by cell, so pixel mode lost the sub-cell precision it exists for.
func TestMotionReport_PixelModeReportsSubCellMotion(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	setMouseModes(tm, true)
	tm.draw.pxScale = 1
	w := &gui.Window{}
	tm.onClick(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 31, MouseY: 41}, Window: w})
	tm.mouse.locked = false // coordinates below are canvas-relative
	*buf = (*buf)[:0]
	for _, x := range []float32{32, 33, 33} { // same cell throughout
		tm.onMouseMove(gui.EventCtx{Event: &gui.Event{MouseX: x, MouseY: 41}, Window: w})
	}
	if n := strings.Count(string(*buf), "M"); n != 2 {
		t.Errorf("sub-cell drag sent %d reports (%q), want 2 (the repeat at 33 deduped)", n, *buf)
	}
}

// onMouseUp reads SelActive while the reader goroutine may clear the selection.
// Regression: the read was unlocked; go test -race flags it.
func TestOnMouseUp_SelActiveReadRacesReader(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	w := &gui.Window{}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			tm.grid.Mu.Lock()
			tm.grid.ClearSelection()
			tm.grid.Mu.Unlock()
		}
	}()
	for range 1000 {
		tm.mouse.dragging = true
		tm.onMouseUp(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 5, MouseY: 5}, Window: w})
	}
	close(stop)
	wg.Wait()
}

// A timer callback that already fired cannot be stopped. When it runs after a
// cancel, or after a newer scroll re-armed the timer, it must not start a
// coast. Regression: kickMomentum set coasting unconditionally.
func TestKickMomentum_StaleKickStartsNothing(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.momentum.kick = make(chan struct{}, 1)

	// Armed and due, then cancelled before the late callback ran.
	tm.momentum.kickAt = time.Now().Add(-time.Millisecond)
	tm.momentum.vel = 300
	tm.cancelMomentum()
	tm.kickMomentum()
	if tm.momentum.coasting {
		t.Error("stale kick after cancel started a coast")
	}

	// A newer scroll re-armed the deadline; the old callback runs early.
	tm.momentum.kickAt = time.Now().Add(time.Hour)
	tm.momentum.vel = 300
	tm.kickMomentum()
	if tm.momentum.coasting {
		t.Error("stale kick before the re-armed deadline started a coast")
	}

	// The real kick, at or after the deadline, still coasts.
	tm.momentum.kickAt = time.Now().Add(-time.Millisecond)
	tm.kickMomentum()
	if !tm.momentum.coasting {
		t.Error("due kick did not start the coast")
	}
}

// A reported drag holds the mouse lock, so its motion and release arrive even
// outside the pane, where the position is negative. Regression: ?1016 sent it
// as is, and "CSI < 32;-9;-4 M" is not a report any parser accepts.
func TestMotionReport_PixelModeClampsOutsidePane(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	setMouseModes(tm, true)
	tm.draw.pxScale = 1
	w := &gui.Window{}
	tm.onClick(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: 31, MouseY: 41}, Window: w})
	tm.mouse.locked = false // coordinates below are canvas-relative
	*buf = (*buf)[:0]
	tm.onMouseMove(gui.EventCtx{Event: &gui.Event{MouseX: -10, MouseY: -5}, Window: w})
	tm.onMouseUp(gui.EventCtx{Event: &gui.Event{MouseButton: gui.MouseLeft, MouseX: -10, MouseY: -5}, Window: w})
	if strings.Contains(string(*buf), "-") {
		t.Errorf("reports outside the pane = %q, want no negative coordinates", *buf)
	}
	if got := string(*buf); got != "\x1b[<32;1;1M\x1b[<0;1;1m" {
		t.Errorf("reports = %q, want motion and release clamped to 1;1", got)
	}
}

// A huge backend position must not overflow the int conversion into a
// negative or arbitrary coordinate.
func TestDevicePx_CapsHugePositions(t *testing.T) {
	tm, _ := newMouseTerm(4, 8)
	tm.draw.pxScale = 2
	x, y := tm.devicePx(3e38, 1e30)
	if x != maxDevicePx || y != maxDevicePx {
		t.Errorf("devicePx(huge) = %d,%d, want %d,%d", x, y, maxDevicePx, maxDevicePx)
	}
}
