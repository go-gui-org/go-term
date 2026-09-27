package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// ---------------------------------------------------------------------------
// encodeMouseX10 — legacy byte encoding (no ?1006/?1015)
// ---------------------------------------------------------------------------

func TestEncodeMouseX10_Press(t *testing.T) {
	got, ok := encodeMouseX10(nil, 0, 4, 9, true)
	if !ok {
		t.Fatal("in-range coordinates should encode")
	}
	// 32+0, 32+5, 32+10
	if want := "\x1b[M\x20\x25\x2a"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeMouseX10_ReleaseUsesButton3(t *testing.T) {
	got, ok := encodeMouseX10(nil, 0, 0, 0, false)
	if !ok {
		t.Fatal("in-range coordinates should encode")
	}
	// There is no 'm' final in the legacy encoding: release is button 3.
	if want := "\x1b[M\x23\x21\x21"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeMouseX10_ReleaseKeepsModifiers(t *testing.T) {
	// shift=4, ctrl=16, button bits replaced by 3 → 23.
	got, _ := encodeMouseX10(nil, 20, 0, 0, false)
	if want := byte(32 + 23); got[3] != want {
		t.Errorf("button byte %d, want %d", got[3], want)
	}
}

func TestEncodeMouseX10_WheelKeepsHighBits(t *testing.T) {
	got, _ := encodeMouseX10(nil, 64, 10, 20, true)
	if want := byte(32 + 64); got[3] != want {
		t.Errorf("button byte %d, want %d", got[3], want)
	}
}

func TestEncodeMouseX10_OutOfRangeDropped(t *testing.T) {
	if _, ok := encodeMouseX10(nil, 0, maxX10Coord, 0, true); ok {
		t.Error("column past the single-byte range should not encode")
	}
	if _, ok := encodeMouseX10(nil, 0, 0, maxX10Coord, true); ok {
		t.Error("row past the single-byte range should not encode")
	}
	if _, ok := encodeMouseX10(nil, 0, maxX10Coord-1, maxX10Coord-1, true); !ok {
		t.Error("last representable cell should encode")
	}
}

// ---------------------------------------------------------------------------
// encodeMouseURXVT — ?1015
// ---------------------------------------------------------------------------

func TestEncodeMouseURXVT_Press(t *testing.T) {
	got := encodeMouseURXVT(nil, 0, 4, 9, true)
	if want := "\x1b[32;5;10M"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeMouseURXVT_ReleaseUsesButton3(t *testing.T) {
	got := encodeMouseURXVT(nil, 0, 4, 9, false)
	if want := "\x1b[35;5;10M"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeMouseURXVT_NoCoordinateLimit(t *testing.T) {
	got := encodeMouseURXVT(nil, 64, 400, 300, true)
	if want := "\x1b[96;401;301M"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// encoding selection
// ---------------------------------------------------------------------------

func TestMouseSnapEncoding_Precedence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sgr, urxv bool
		want      mouseEncoding
	}{
		{"default", false, false, mouseEncX10},
		{"urxvt", false, true, mouseEncURXVT},
		{"sgr", true, false, mouseEncSGR},
		{"sgr wins over urxvt", true, true, mouseEncSGR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := mouseSnap{sgr: tc.sgr, urxvt: tc.urxv}
			if got := m.encoding(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldReport_NoEncodingModeStillReports(t *testing.T) {
	// The bug this fixes: ?1000 alone used to be silently dropped.
	m := mouseSnap{report: true, live: true}
	if !m.shouldReport() {
		t.Error("?1000 without ?1006 must still report")
	}
}

// ---------------------------------------------------------------------------
// end-to-end through the widget
// ---------------------------------------------------------------------------

func TestOnClick_X10LeftPress(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true // ?1000 only — no ?1006
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	// cell (1,1) → 1-based (2,2) → bytes 32, 34, 34
	if want := "\x1b[M\x20\x22\x22"; !strings.HasPrefix(string(*buf), want) {
		t.Errorf("got %q, want prefix %q", *buf, want)
	}
	if !tm.mouse.dragging || !tm.mouse.dragReport {
		t.Error("expected drag tracking")
	}
	if !e.IsHandled {
		t.Error("event should be handled")
	}
}

func TestOnClick_URXVTLeftPress(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseURXVT = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if want := "\x1b[32;2;2M"; !strings.HasPrefix(string(*buf), want) {
		t.Errorf("got %q, want prefix %q", *buf, want)
	}
}

func TestOnClick_X10PixelModeIgnored(t *testing.T) {
	// ?1016 is an SGR-only refinement; the legacy encoding cannot carry
	// pixels, so it must keep reporting cells rather than emit garbage.
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.MouseSGRPixels = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 15, MouseY: 25, MouseButton: gui.MouseLeft}
	tm.onClick(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if want := "\x1b[M\x20\x22\x22"; !strings.HasPrefix(string(*buf), want) {
		t.Errorf("got %q, want prefix %q", *buf, want)
	}
}

func TestOnMouseScroll_X10WheelUp(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.Mu.Unlock()
	e := &gui.Event{MouseX: 15, MouseY: 25, ScrollY: 1}
	tm.onMouseScroll(gui.EventCtx{Layout: nil, Event: e, Window: &gui.Window{}})
	if want := "\x1b[M\x60\x22\x22"; !strings.Contains(string(*buf), want) {
		t.Errorf("got %q, want %q", *buf, want)
	}
}

func TestReportLostRelease_X10(t *testing.T) {
	tm, buf := newMouseTerm(4, 8)
	tm.grid.Mu.Lock()
	tm.grid.MouseTrack = true
	tm.grid.Mu.Unlock()
	tm.mouse.dragReport = true
	tm.mouse.dragButton = gui.MouseLeft
	tm.mouse.lastR, tm.mouse.lastC = 1, 1
	tm.reportLostRelease()
	if want := "\x1b[M\x23\x22\x22"; string(*buf) != want {
		t.Errorf("got %q, want %q", *buf, want)
	}
}

// ---------------------------------------------------------------------------
// ?1015 mode plumbing
// ---------------------------------------------------------------------------

func TestPrivateMode1015SetAndReset(t *testing.T) {
	g := newGrid(4, 8)
	p := newParser(g)
	p.Feed([]byte("\x1b[?1015h"))
	if !g.MouseURXVT {
		t.Fatal("?1015h should enable urxvt encoding")
	}
	p.Feed([]byte("\x1b[?1015l"))
	if g.MouseURXVT {
		t.Error("?1015l should disable urxvt encoding")
	}
}

func TestHardResetClearsMouseURXVT(t *testing.T) {
	g := newGrid(4, 8)
	g.MouseURXVT = true
	g.HardReset()
	if g.MouseURXVT {
		t.Error("HardReset should clear ?1015")
	}
}

func TestDECRQM1015(t *testing.T) {
	g := newGrid(4, 8)
	g.MouseURXVT = true
	p := newParser(g)
	if got := p.decModeState(1015); got != 1 {
		t.Errorf("DECRQM ?1015 reported %d, want 1 (set)", got)
	}
}
