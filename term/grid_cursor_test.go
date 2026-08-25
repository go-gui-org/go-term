package term

import "testing"

func TestGrid_MoveCursorClamps(t *testing.T) {
	g := newGrid(3, 4)
	g.MoveCursor(-1, -1)
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("clamp low: %d %d", g.CursorR, g.CursorC)
	}
	g.MoveCursor(99, 99)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("clamp high: %d %d", g.CursorR, g.CursorC)
	}
}

func TestGrid_CursorMoveByLargeNClamps(t *testing.T) {
	g := newGrid(5, 5)
	g.MoveCursor(2, 2)
	g.CursorUp(100)
	if g.CursorR != 0 {
		t.Errorf("up: %d", g.CursorR)
	}
	g.CursorDown(100)
	if g.CursorR != 4 {
		t.Errorf("down: %d", g.CursorR)
	}
	g.CursorBack(100)
	if g.CursorC != 0 {
		t.Errorf("back: %d", g.CursorC)
	}
	g.CursorForward(100)
	if g.CursorC != 4 {
		t.Errorf("forward: %d", g.CursorC)
	}
}

func TestGrid_DECSCUSRParam_RoundTrip(t *testing.T) {
	cases := []struct{ ps, want int }{
		{1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6},
	}
	for _, c := range cases {
		g := newGrid(1, 5)
		g.ApplyDECSCUSR(c.ps)
		if got := g.DECSCUSRParam(); got != c.want {
			t.Errorf("ApplyDECSCUSR(%d) → DECSCUSRParam() = %d, want %d", c.ps, got, c.want)
		}
	}
}

func TestGrid_MoveCursorOrigin_WhenOriginModeOff(t *testing.T) {
	g := newGrid(5, 8)
	g.SetScrollRegion(1, 3)

	g.MoveCursorOrigin(2, 3)
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("cursor = %d,%d, want 2,3", g.CursorR, g.CursorC)
	}
}

func TestGrid_SaveRestoreCursor_ULState(t *testing.T) {
	g := newGrid(2, 10)
	g.CurULStyle = ulDouble
	g.CurULColor = rgbColor(0, 128, 255)
	g.SaveCursor()

	g.CurULStyle = ulDotted
	g.CurULColor = defaultColor

	g.RestoreCursor()
	if g.CurULStyle != ulDouble {
		t.Errorf("RestoreCursor: CurULStyle = %d, want ulDouble (%d)", g.CurULStyle, ulDouble)
	}
	if g.CurULColor != rgbColor(0, 128, 255) {
		t.Errorf("RestoreCursor: CurULColor = %#x, want %#x", g.CurULColor, rgbColor(0, 128, 255))
	}
}

func TestGrid_SaveRestoreCursor_CharsetState(t *testing.T) {
	g := newGrid(2, 10)
	g.CharsetG0 = 'B'
	g.CharsetG1 = '0'
	g.ActiveG = 1
	g.SaveCursor()

	g.CharsetG0 = '0'
	g.CharsetG1 = 'B'
	g.ActiveG = 0

	g.RestoreCursor()
	if g.CharsetG0 != 'B' || g.CharsetG1 != '0' || g.ActiveG != 1 {
		t.Fatalf("RestoreCursor charsets: G0=%q G1=%q active=%d", g.CharsetG0, g.CharsetG1, g.ActiveG)
	}
}

func TestGrid_CursorMovementMarksDirty(t *testing.T) {
	g := newGrid(10, 10)
	g.ClearDirty()

	g.MoveCursor(5, 5)
	if !g.Dirty[0] {
		t.Errorf("MoveCursor: old row 0 not marked dirty")
	}
	if !g.Dirty[5] {
		t.Errorf("MoveCursor: new row 5 not marked dirty")
	}
	if !g.HasDirtyRows() {
		t.Errorf("MoveCursor: HasDirtyRows should be true")
	}

	g.ClearDirty()
	g.CursorForward(2)
	if !g.Dirty[5] {
		t.Errorf("CursorForward: row 5 not marked dirty")
	}

	g.ClearDirty()
	g.CursorDown(1)
	if !g.Dirty[5] || !g.Dirty[6] {
		t.Errorf("CursorDown: rows 5 and 6 not marked dirty")
	}

	g.ClearDirty()
	g.Newline()
	if !g.Dirty[6] || !g.Dirty[7] {
		t.Errorf("Newline: rows 6 and 7 not marked dirty")
	}

	g.ClearDirty()
	g.ReverseIndex()
	if !g.Dirty[6] || !g.Dirty[7] {
		t.Errorf("ReverseIndex: rows 6 and 7 not marked dirty")
	}
}

// A locked cursor drops DECSCUSR outright. The sequence is not remembered for
// later either: unlocking must not suddenly apply a shape the child asked for
// while the user had the cursor pinned.
func TestApplyDECSCUSR_LockDropsRequest(t *testing.T) {
	g := newGrid(4, 8)
	g.setCursorDefaults(CursorStyleBar, true, true)

	g.ApplyDECSCUSR(2) // steady block
	if g.cursorShape != CursorStyleBar || !g.CursorBlink {
		t.Errorf("locked cursor = %v/blink %v, want bar/true", g.cursorShape, g.CursorBlink)
	}

	g.cursorLocked = false
	if got := g.DECSCUSRParam(); got != 5 {
		t.Errorf("DECSCUSRParam = %d, want 5 (blinking bar) — the lock must not queue requests", got)
	}
	g.ApplyDECSCUSR(2)
	if g.cursorShape != CursorStyleBlock || g.CursorBlink {
		t.Errorf("unlocked cursor = %v/blink %v, want block/false", g.cursorShape, g.CursorBlink)
	}
}

// setCursorDefaults moves the live cursor and the reset target together.
func TestSetCursorDefaults(t *testing.T) {
	g := newGrid(4, 8)
	g.setCursorDefaults(CursorStyleUnderline, true, false)
	if g.cursorShape != CursorStyleUnderline || !g.CursorBlink {
		t.Errorf("live cursor = %v/blink %v, want underline/true", g.cursorShape, g.CursorBlink)
	}
	if g.defaultShape != CursorStyleUnderline || !g.defaultBlink || g.cursorLocked {
		t.Errorf("defaults = %v/%v locked %v, want underline/true/false",
			g.defaultShape, g.defaultBlink, g.cursorLocked)
	}
}

func TestCursorStyle_String(t *testing.T) {
	cases := map[CursorStyle]string{
		CursorStyleBlock:     "block",
		CursorStyleUnderline: "underline",
		CursorStyleBar:       "bar",
		CursorStyle(99):      "block",
	}
	for style, want := range cases {
		if got := style.String(); got != want {
			t.Errorf("CursorStyle(%d).String() = %q, want %q", style, got, want)
		}
	}
}
