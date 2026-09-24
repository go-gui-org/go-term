package term

import "testing"

// Regression tests for the grid core review: each one reproduces a bug that was
// confirmed on the code before its fix.

// Lowering the scrollback cap drops the oldest rows, which shifts the whole
// content-row space. Marks and image anchors used to keep their old rows, so
// jump-to-prompt landed N rows too low and images drew in the wrong place.
func TestSetScrollbackRows_ShrinkShiftsMarksAndGraphics(t *testing.T) {
	tm := newSettingsTerm(Cfg{})
	g := tm.grid
	g.ScrollbackCap = 10
	g.Scrollback.EnsureGeom(10, g.Cols)
	for range 10 {
		g.Scrollback.Push(make([]cell, g.Cols), false)
	}
	// Row 8 survives the shrink to 3 (it becomes row 1); row 2 falls off.
	g.Marks = []mark{{Row: 2}, {Row: 8}}
	g.Graphics = []graphic{{OriginR: 8, Rows: 1, Cols: 1}}

	tm.SetScrollbackRows(3)

	if len(g.Marks) != 1 || g.Marks[0].Row != 1 {
		t.Errorf("marks = %+v, want one mark at row 1", g.Marks)
	}
	if len(g.Graphics) != 1 || g.Graphics[0].OriginR != 1 {
		t.Errorf("graphics = %+v, want one image at row 1", g.Graphics)
	}
}

// Turning scrollback off drops every history row, so anchors shift by all of it.
func TestSetScrollbackRows_DisableShiftsMarks(t *testing.T) {
	tm := newSettingsTerm(Cfg{})
	g := tm.grid
	g.ScrollbackCap = 10
	g.Scrollback.EnsureGeom(10, g.Cols)
	for range 4 {
		g.Scrollback.Push(make([]cell, g.Cols), false)
	}
	g.Marks = []mark{{Row: 1}, {Row: 5}} // scrollback row, live row 1

	tm.SetScrollbackRows(-1)

	if len(g.Marks) != 1 || g.Marks[0].Row != 1 {
		t.Errorf("marks = %+v, want one mark at live row 1", g.Marks)
	}
}

// ED 0 erases whole rows below the cursor, and those rows used to keep their
// wrap flags. The next resize then joined the blank rows into one logical line
// with the text written after the erase, and that text moved down a row.
// xterm clears the flag on every row it erases whole (ClearBufRows).
func TestGrid_ED0_ClearsWrapFlags(t *testing.T) {
	g, p := newParserGrid(4, 10)
	feed(t, g, p, []byte("aaaaaaaaaaaaaaa\x1b[1;1H\x1b[J\x1b[2;1Hx"))
	g.Resize(4, 5)
	if got := rowText(g, 1); got != "x    " {
		t.Errorf("row 1 after resize = %q, want %q", got, "x    ")
	}
}

// ED 1 is the mirror image: erased rows above the cursor must not join onto
// the cursor row, or its text is pushed right by a blank row's width.
func TestGrid_ED1_ClearsWrapFlags(t *testing.T) {
	g, p := newParserGrid(4, 10)
	feed(t, g, p, []byte("aaaaaaaaaaaaaaa\x1b[2;10H\x1b[1J\x1b[2;1Hx"))
	g.Resize(4, 5)
	if got := rowText(g, 1); got != "x    " {
		t.Errorf("row 1 after resize = %q, want %q", got, "x    ")
	}
}

// EL 0 removes the tail of the row, so the row no longer runs on into the next
// one (xterm ClearRight: "with the right part cleared, we can't be wrapping").
func TestGrid_EL0_ClearsWrapFlag(t *testing.T) {
	g, p := newParserGrid(4, 10)
	feed(t, g, p, []byte("aaaaaaaaaabb\x1b[1;4H\x1b[K"))
	if g.RowWrapped[0] {
		t.Error("row 0 still marked wrapped after EL 0 erased its tail")
	}
}

// With a wrap pending the cursor is still on the last column (DEC and xterm
// keep wrap-pending as a flag). EL 0, ECH, ICH and DCH act on that column and
// cancel the pending wrap, so the next glyph lands on the last column instead
// of wrapping. go-term skipped the column and kept the wrap.
func TestGrid_PendingWrap_EditsActOnLastColumn(t *testing.T) {
	cases := []struct {
		name, seq, want string
	}{
		{"EL 0", "\x1b[K", "abcdefghiY"},
		{"ECH", "\x1b[X", "abcdefghiY"},
		{"ICH", "\x1b[@", "abcdefghiY"},
		{"DCH", "\x1b[P", "abcdefghiY"},
	}
	for _, c := range cases {
		g, p := newParserGrid(3, 10)
		feed(t, g, p, []byte("abcdefghij"+c.seq+"Y"))
		if got := rowText(g, 0); got != c.want {
			t.Errorf("%s: row 0 = %q, want %q", c.name, got, c.want)
		}
		if got := rowText(g, 1); got != "          " {
			t.Errorf("%s: row 1 = %q, want blank (no wrap)", c.name, got)
		}
	}
}

// TBC 0 clears the stop under the cursor. With a wrap pending that is the last
// column, the same column HTS would set; it used to look at column Cols instead.
func TestGrid_TBC_PendingWrapClearsLastColumn(t *testing.T) {
	g, p := newParserGrid(3, 10)
	feed(t, g, p, []byte("\x1b[1;10H\x1bH\x1b[1;1Habcdefghij\x1b[g"))
	if g.TabStops[9] {
		t.Error("tab stop at the last column survived TBC with a wrap pending")
	}
}

// DECCRA writes cells like DECERA and DECFRA do, so it removes a sixel/iTerm2
// image under its destination the same way.
func TestGrid_CopyRectOccludesImage(t *testing.T) {
	g := newGrid(10, 10)
	g.AddGraphic("review.png", 8, 8) // one cell at (0,0): no cell size measured yet
	g.CopyRect(9, 9, 9, 9, 1, 1)
	if len(g.Graphics) != 0 {
		t.Errorf("CopyRect left %d graphics, want 0", len(g.Graphics))
	}
}
