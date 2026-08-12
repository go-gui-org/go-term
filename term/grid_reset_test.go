package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

func TestGrid_RepeatLast(t *testing.T) {
	g := newGrid(2, 8)
	g.Put('x')
	g.RepeatLast(3)
	if got, want := rowText(g, 0), "xxxx    "; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
	if g.CursorC != 4 {
		t.Errorf("cursor col = %d, want 4", g.CursorC)
	}
}

func TestGrid_RepeatLast_NoopBeforeAnyOutput(t *testing.T) {
	g := newGrid(2, 8)
	g.RepeatLast(5)
	if g.CursorC != 0 || g.CursorR != 0 {
		t.Errorf("cursor moved to %d,%d on empty screen", g.CursorR, g.CursorC)
	}
	if got := rowText(g, 0); got != "        " {
		t.Errorf("row = %q, want blanks", got)
	}
}

func TestGrid_RepeatLast_NonPositiveIgnored(t *testing.T) {
	g := newGrid(1, 4)
	g.Put('a')
	g.RepeatLast(0)
	g.RepeatLast(-3)
	if got, want := rowText(g, 0), "a   "; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

// A repeat count far beyond the screen must terminate quickly rather than
// spinning the reader goroutine through billions of putCell calls.
func TestGrid_RepeatLast_CountBoundedToScreen(t *testing.T) {
	g := newGrid(3, 4)
	g.Put('z')
	g.RepeatLast(1 << 20)
	// The count is capped at one screenful (Rows*Cols = 12), so exactly 13
	// cells are written: 12 fill the screen and the 13th wraps, scrolling one
	// row off. Anything unbounded would never reach this assertion.
	for r, want := range []string{"zzzz", "zzzz", "z   "} {
		if got := rowText(g, r); got != want {
			t.Errorf("row %d = %q, want %q", r, got, want)
		}
	}
}

func TestGrid_RepeatLast_WideChar(t *testing.T) {
	g := newGrid(1, 6)
	g.Put('中')
	g.RepeatLast(2)
	if g.CursorC != 6 {
		t.Errorf("cursor col = %d, want 6 (three 2-cell glyphs)", g.CursorC)
	}
	for _, c := range []int{0, 2, 4} {
		if ch := g.At(0, c).Ch; ch != '中' {
			t.Errorf("col %d = %q, want 中", c, ch)
		}
	}
}

func TestGrid_SoftReset(t *testing.T) {
	g := newGrid(4, 8)
	g.Put('k')
	g.CurAttrs = attrBold | attrBlink
	g.CurFG = paletteColor(3)
	g.CurULStyle = ulCurly
	g.CursorVisible = false
	g.OriginMode = true
	g.InsertMode = true
	g.AutoWrap = false
	g.AppCursorKeys = true
	g.AppKeypad = true
	g.CharsetG0 = charsetDECSpecial
	g.ActiveG = 1
	g.SetScrollRegion(1, 2)
	g.MoveCursor(2, 3)
	g.SaveCursor()

	g.SoftReset()

	if g.CurAttrs != 0 || g.CurFG != defaultColor || g.CurULStyle != ulNone {
		t.Errorf("SGR not reset: attrs=%d fg=%d ul=%d", g.CurAttrs, g.CurFG, g.CurULStyle)
	}
	if !g.CursorVisible || g.OriginMode || g.InsertMode || !g.AutoWrap {
		t.Errorf("modes not reset: vis=%v origin=%v insert=%v wrap=%v",
			g.CursorVisible, g.OriginMode, g.InsertMode, g.AutoWrap)
	}
	if g.AppCursorKeys || g.AppKeypad {
		t.Error("keypad/cursor-key modes not reset")
	}
	if g.CharsetG0 != charsetASCII || g.CharsetG1 != charsetASCII || g.ActiveG != 0 {
		t.Errorf("charsets not reset: G0=%q G1=%q active=%d",
			g.CharsetG0, g.CharsetG1, g.ActiveG)
	}
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("scroll region = %d..%d, want full screen", g.Top, g.Bottom)
	}
	if g.saved.valid {
		t.Error("saved cursor should be dropped")
	}
	// DECSTR must not disturb screen contents or the active cursor.
	if got := rowText(g, 0)[:1]; got != "k" {
		t.Errorf("screen cleared by DECSTR: row0 = %q", rowText(g, 0))
	}
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("DECSTR moved the cursor to %d,%d", g.CursorR, g.CursorC)
	}
}

func TestGrid_SoftReset_EndsSyncBlock(t *testing.T) {
	g := newGrid(2, 4)
	g.BeginSync()
	g.SoftReset()
	if g.SyncActive || g.SyncOutput {
		t.Errorf("sync still active after DECSTR: active=%v output=%v",
			g.SyncActive, g.SyncOutput)
	}
}

func TestGrid_HardReset(t *testing.T) {
	g := newGrid(3, 6)
	g.ScrollbackCap = 10
	g.Put('a')
	g.Newline()
	g.Newline()
	g.Newline() // push a row into scrollback
	g.MouseTrack = true
	g.MouseSGR = true
	g.BracketedPaste = true
	g.FocusReporting = true
	g.PushKittyKeyFlags(3)
	g.CursorColor = rgbColor(1, 2, 3)
	g.CursorBlink = true
	g.ApplyDECSCUSR(3)
	g.ClearTabStop(true)
	g.EnterAlt()
	g.Put('A')

	g.HardReset()

	if g.AltActive {
		t.Error("still on alt screen after RIS")
	}
	if g.Scrollback.Len() != 0 {
		t.Errorf("scrollback = %d rows, want 0", g.Scrollback.Len())
	}
	if g.MouseTrack || g.MouseSGR || g.BracketedPaste || g.FocusReporting {
		t.Error("reporting modes survived RIS")
	}
	if g.KittyKeyFlags != 0 || len(g.kittyFlagStack) != 0 {
		t.Errorf("kitty flags survived RIS: %d stack=%d",
			g.KittyKeyFlags, len(g.kittyFlagStack))
	}
	if g.CursorColor != defaultColor || g.CursorBlink || g.cursorShape != cursorBlock {
		t.Error("cursor appearance survived RIS")
	}
	if !g.TabStops[8] || !g.TabStops[16] {
		t.Error("default tab stops not restored")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor at %d,%d, want home", g.CursorR, g.CursorC)
	}
	for r := range g.Rows {
		for c := range g.Cols {
			if ch := g.At(r, c).Ch; ch != ' ' {
				t.Fatalf("cell %d,%d = %q, want blank", r, c, ch)
			}
		}
	}
}

// RIS drops OSC 4 palette overrides (child-set); DECSTR keeps them.
func TestGrid_Reset_PaletteOverrides(t *testing.T) {
	g := newGrid(2, 4)
	g.SetPaletteColor(1, rgbColor(1, 2, 3))

	g.SoftReset()
	if g.palOverride == nil {
		t.Fatal("DECSTR dropped palette overrides")
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: 1}), gui.RGB(1, 2, 3); got != want {
		t.Errorf("after DECSTR: got %+v want %+v", got, want)
	}

	g.HardReset()
	if g.palOverride != nil {
		t.Error("RIS kept palette overrides")
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: 1}), DefaultTheme.ANSI[1]; got != want {
		t.Errorf("after RIS: got %+v want %+v", got, want)
	}
}

// RIS must leave nothing behind that would let REP resurrect pre-reset output.
func TestGrid_HardReset_ClearsRepeatState(t *testing.T) {
	g := newGrid(2, 4)
	g.Put('q')
	g.HardReset()
	g.RepeatLast(2)
	if got := rowText(g, 0); got != "    " {
		t.Errorf("row = %q, want blanks", got)
	}
}

// TestGrid_SetColumnMode_PinsWidthAndClears covers DECCOLM's full contract:
// the width changes, the screen is erased, the cursor homes, and the scroll
// region is reset. vttest draws a box immediately after each switch and
// depends on every part of that.
func TestGrid_SetColumnMode_PinsWidthAndClears(t *testing.T) {
	g := newGrid(10, 40)
	g.Put('X')
	g.Top, g.Bottom = 2, 5
	g.MoveCursor(4, 4)

	g.SetColumnMode(132)
	if g.Cols != 132 || g.ColumnMode != 132 {
		t.Fatalf("cols = %d, mode = %d, want 132/132", g.Cols, g.ColumnMode)
	}
	if c := g.At(0, 0); c == nil || c.Ch != ' ' {
		t.Error("screen not erased by DECCOLM")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor = (%d,%d), want home", g.CursorR, g.CursorC)
	}
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("region = %d..%d, want full screen", g.Top, g.Bottom)
	}

	g.SetColumnMode(80)
	if g.Cols != 80 {
		t.Fatalf("cols = %d, want 80", g.Cols)
	}

	// Releasing the pin leaves the width alone — the widget restores the
	// window-derived width on its next frame.
	g.SetColumnMode(0)
	if g.ColumnMode != 0 {
		t.Fatalf("mode = %d, want released", g.ColumnMode)
	}
}

// TestGrid_HardResetReleasesColumnMode: RIS is what `reset` runs to recover a
// terminal, so it has to undo a column pin an application left behind.
func TestGrid_HardResetReleasesColumnMode(t *testing.T) {
	g := newGrid(10, 40)
	g.AllowColumnMode = true
	g.SetColumnMode(132)

	g.HardReset()
	if g.ColumnMode != 0 || g.AllowColumnMode {
		t.Fatalf("mode = %d, allow = %v, want 0/false",
			g.ColumnMode, g.AllowColumnMode)
	}
}

// TestGrid_SetColumnMode_SkipsRedundantResize: Resize runs the full reflow
// whatever the content, and a child drives DECCOLM with six bytes. Asking for
// the width the grid already has must not pay for it.
func TestGrid_SetColumnMode_SkipsRedundantResize(t *testing.T) {
	g := newGrid(10, 80)
	before := &g.Cells[0]

	g.SetColumnMode(80)
	if g.ColumnMode != 80 || g.Cols != 80 {
		t.Fatalf("mode = %d, cols = %d, want 80/80", g.ColumnMode, g.Cols)
	}
	// A reflow replaces the cell backing store; skipping it keeps the same
	// array, which is the cheapest observable proxy for "no Resize ran".
	if &g.Cells[0] != before {
		t.Error("same-width DECCOLM reallocated the cell buffer")
	}
}

// DECALN and DECCOLM both flat-fill the screen, bypassing eraseSpan — which is
// what normally removes sixel/iTerm2 images. Those protocols have no delete
// sequence, so an image the fill did not drop stays painted over a screen the
// application believes it cleared.
func TestGrid_FlatFillClears_DropsOccludableGraphics(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(g *grid)
	}{
		{"DECALN", func(g *grid) { g.ScreenAlignment() }},
		{"DECCOLM", func(g *grid) { g.SetColumnMode(132) }},
		{"ClearAll", func(g *grid) { g.ClearAll() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := gridWithGraphic(t, 3, 5)
			tc.run(g)
			if len(g.Graphics) != 0 {
				t.Fatalf("image survived a full-screen clear: %d left",
					len(g.Graphics))
			}
		})
	}
}

// Kitty placements are their own layer: they survive text drawn over their
// cells and go away only via a=d. A flat-fill clear must not take them.
func TestGrid_FlatFillClears_KeepsKittyPlacements(t *testing.T) {
	g := newGrid(10, 40)
	g.CellPxW, g.CellPxH = 8, 16
	g.AddGraphicKitty("img.png", 16, 32, 0, 0, 7)
	g.ScreenAlignment()
	if len(g.Graphics) != 1 {
		t.Fatalf("DECALN removed a Kitty placement: %d left", len(g.Graphics))
	}
}
