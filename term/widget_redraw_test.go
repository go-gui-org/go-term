package term

import (
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// Why applyChunk asks for InvalidateLayout (a full layout refresh) rather than the
// cheaper InvalidateRender (render-only) after every screen-changing PTY read.
//
// The terminal repaints by bumping drawVersion, which reaches go-gui as the
// DrawCanvas Version — the canvas tessellation cache re-invokes OnDraw only when
// that value differs from the cached one. But Version is carried on the *layout
// node*, and only view generation rebuilds those. A render-only refresh reuses
// the existing tree, so the canvas still advertises the old Version, the cache
// hits, and OnDraw never runs: the grid would change with nothing repainting it.
//
// This test pins that asymmetry against go-gui, because the swap is an obvious
// optimization to reach for (a terminal's layout does not change between
// keystrokes) and it fails by freezing the screen rather than by not compiling.
//
// go-gui has since added DrawCanvasCfg.VersionFn, which reads the version at
// render time and so closes this gap; the Term's canvas sets it, and the blink
// tick uses it (TestBlinkTickRepaintsWithoutLayout). This probe sets Version
// only, so it still pins the plain-Version behavior. applyChunk keeps the full
// refresh for now.
func TestRenderOnlyRefreshSkipsCanvasVersionBump(t *testing.T) {
	var version uint64 = 1
	draws := 0
	w := gui.NewWindow(gui.WindowCfg{Title: "redraw-probe", Width: 400, Height: 300})
	w.SetView(func(*gui.Window) gui.View {
		return gui.DrawCanvas(gui.DrawCanvasCfg{
			ID:      "redraw-probe-canvas",
			Version: version,
			Width:   200,
			Height:  100,
			OnDraw:  func(*gui.DrawContext) { draws++ },
		})
	})

	w.FrameFn()
	if draws != 1 {
		t.Fatalf("initial frame: draws = %d, want 1", draws)
	}

	// What InvalidateRender would buy — and why it cannot be used here.
	version++
	w.InvalidateRender()
	w.FrameFn()
	if draws != 1 {
		t.Fatalf("render-only refresh: draws = %d, want 1 (see comment above)", draws)
	}

	// What applyChunk actually does.
	version++
	w.InvalidateLayout()
	w.FrameFn()
	if draws != 2 {
		t.Fatalf("full refresh: draws = %d, want 2", draws)
	}
}

// chanScheduler hands queued commands to the test goroutine instead of running
// them, so a command the Term queues can run against a real window on the
// goroutine that owns it.
type chanScheduler chan func(*gui.Window)

func (c chanScheduler) QueueCommand(fn func(*gui.Window)) { c <- fn }

// A blink tick must repaint the canvas without rebuilding the view. The tick
// bumps drawVersion and asks for a render-only frame; the canvas's VersionFn
// reads drawVersion at render time, so the cache sees the new value and OnDraw
// runs even though the layout shape still carries the old Version.
//
// Two probes. OnDraw remeasures the cell when cellW is 0, so a cellW reset
// before the tick shows whether OnDraw ran: without VersionFn the cache hits
// and it stays 0. A counter around View shows whether the frame rebuilt the
// view: before VersionFn the tick had to ask for a full layout to get the
// repaint, and this test failed on that count.
func TestBlinkTickRepaintsWithoutLayout(t *testing.T) {
	const cellW, cellH = 8, 16
	sched := make(chanScheduler, 16)
	tm := idleTerm()
	tm.cmd = sched
	tm.canvasID = "blink-probe-canvas"
	tm.focusID = "blink-probe"
	tm.cfg.TextStyle = gui.TextStyle{Size: 12}

	w := gui.NewWindow(gui.WindowCfg{Title: "blink-probe", Width: 400, Height: 300})
	w.SetTextMeasurer(testTextMeasurer{cellW: cellW, cellH: cellH})
	views := 0
	w.SetView(func(w *gui.Window) gui.View {
		views++
		return tm.View(w)
	})
	runQueued := func() {
		for len(sched) > 0 {
			(<-sched)(w)
		}
	}

	// The first frame sizes the grid through the resize debounce, whose wake
	// timer queues a full-layout command of its own. Let that resize land,
	// then stop the timers, so the only command left is the one the tick
	// queues.
	w.FrameFn()
	for deadline := time.Now().Add(2 * time.Second); ; {
		runQueued()
		w.FrameFn()
		if tm.resize.pendingSince.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial resize never applied")
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, tmr := range []*time.Timer{tm.resize.timer, tm.resize.badgeTimer, tm.scrollbar.timer} {
		if tmr != nil {
			tmr.Stop()
		}
	}
	runQueued()
	w.FrameFn()
	if tm.cellW == 0 {
		t.Fatal("setup frames did not run OnDraw")
	}

	// A blinking cursor at the live viewport is what the tick repaints.
	tm.grid.Mu.Lock()
	tm.grid.CursorVisible = true
	tm.grid.CursorBlink = true
	tm.grid.Mu.Unlock()
	tm.cellW = 0 // probe: OnDraw sets it again
	viewsBefore := views

	if !tm.blinkTick() {
		t.Fatal("blinkTick found nothing to animate")
	}
	if n := len(sched); n != 1 {
		t.Fatalf("blinkTick queued %d commands, want 1", n)
	}
	runQueued()
	w.FrameFn()

	if tm.cellW == 0 {
		t.Error("blink frame did not run OnDraw: canvas cache hit on a stale version")
	}
	if views != viewsBefore {
		t.Errorf("blink frame rebuilt the view %d time(s); want a render-only frame",
			views-viewsBefore)
	}
}

// blinkTick's decision table: what counts as "something blinking", and that a
// false answer leaves drawVersion alone and queues nothing — the loop parks on
// it, so a wrong true keeps an idle pane waking twice a second, and a wrong
// false freezes a blink until some other event repaints.
func TestBlinkTickDecision(t *testing.T) {
	cases := []struct {
		name  string
		setup func(tm *Term)
		want  bool
	}{
		{"hidden cursor", func(tm *Term) {}, false},
		{"steady cursor", func(tm *Term) { tm.grid.CursorVisible = true }, false},
		{"blinking cursor", func(tm *Term) {
			tm.grid.CursorVisible, tm.grid.CursorBlink = true, true
		}, true},
		// The cursor is off screen while scrolled back, so it has no blink to show.
		{"blinking cursor, scrolled back", func(tm *Term) {
			tm.grid.CursorVisible, tm.grid.CursorBlink = true, true
			tm.grid.ViewOffset = 1
		}, false},
		{"blinking cursor, mid smooth scroll", func(tm *Term) {
			tm.grid.CursorVisible, tm.grid.CursorBlink = true, true
			tm.grid.ViewSubPx = 3
		}, false},
		{"blinking cursor, pane unfocused", func(tm *Term) {
			tm.grid.CursorVisible, tm.grid.CursorBlink = true, true
			tm.focused.Store(false)
		}, false},
		// SGR 5 text blinks in any viewport position.
		{"blinking cells, scrolled back", func(tm *Term) {
			tm.blinkCells.Store(true)
			tm.grid.ViewOffset = 1
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched := make(chanScheduler, 4)
			tm := idleTerm()
			tm.cmd = sched
			tm.grid.CursorVisible = false
			tc.setup(tm)
			before := tm.drawVersion.Load()

			if got := tm.blinkTick(); got != tc.want {
				t.Fatalf("blinkTick() = %v, want %v", got, tc.want)
			}
			wantBump, wantCmds := uint64(0), 0
			if tc.want {
				wantBump, wantCmds = 1, 1
			}
			if d := tm.drawVersion.Load() - before; d != wantBump {
				t.Errorf("drawVersion moved by %d, want %d", d, wantBump)
			}
			if n := len(sched); n != wantCmds {
				t.Errorf("queued %d commands, want %d", n, wantCmds)
			}
		})
	}
}
