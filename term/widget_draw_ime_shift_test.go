package term

import (
	"strings"
	"testing"
)

// glyphXs maps each single-rune text the frame drew to its x origin.
func glyphXs(t *testing.T, tm *Term, rows, cols int, row string, cursorC int,
	comp string) map[string]float32 {
	t.Helper()
	tm.grid.Mu.Lock()
	g := tm.grid
	for c, r := range []rune(row) {
		putNarrow(g, 0, c, r)
	}
	g.CursorR, g.CursorC, g.CursorVisible = 0, cursorC, false
	tm.grid.Mu.Unlock()
	tm.ime.composing = true
	tm.ime.compText = comp
	tm.ime.compCursor = len([]rune(comp))

	_, dc := newDrawTerm(rows, cols, wideTestCellW, 20)
	tm.onDraw(dc)
	xs := map[string]float32{}
	for _, te := range dc.Texts() {
		xs[te.Text] = te.X
	}
	return xs
}

// The preedit is text the user is inserting at the cursor, and the shell inserts
// it there on commit. While composing, the text right of the cursor must move
// right to make room for the strip, as it will after the commit. Painting the
// strip over those cells hid them, which looked like the IME typed over them.
func TestOnDraw_IMEPreeditPushesRowRight(t *testing.T) {
	tm, _ := newDrawTerm(4, 10, wideTestCellW, 20)
	// Greek is non-ASCII, so every cell emits on its own and carries its own x.
	xs := glyphXs(t, tm, 4, 10, "αβγδ", 2, "中")

	want := map[string]int{"α": 0, "β": 1, "中": 2, "γ": 4, "δ": 5}
	for s, col := range want {
		x, ok := xs[s]
		if !ok {
			t.Errorf("%q not drawn; drew %v", s, xs)
			continue
		}
		if x != tm.colX(col) {
			t.Errorf("%q at x=%v, want col %d (x=%v)", s, x, col, tm.colX(col))
		}
	}
}

// Cells pushed past the right edge are not drawn while composing, and a wide
// cell that no longer fits whole is dropped instead of drawn cut in half.
func TestOnDraw_IMEPreeditDropsCellsPastEdge(t *testing.T) {
	tm, _ := newDrawTerm(4, 6, wideTestCellW, 20)
	tm.grid.Mu.Lock()
	putWide(tm.grid, 0, 3, '日') // cols 3-4, pushed to 5-6: does not fit
	tm.grid.Mu.Unlock()
	xs := glyphXs(t, tm, 4, 6, "αβγ", 1, "中")

	if x, ok := xs["β"]; !ok || x != tm.colX(3) {
		t.Errorf("β at %v (drawn %v), want col 3", x, ok)
	}
	if x, ok := xs["γ"]; !ok || x != tm.colX(4) {
		t.Errorf("γ at %v (drawn %v), want col 4", x, ok)
	}
	if _, ok := xs["日"]; ok {
		t.Errorf("wide cell drawn at %v, want dropped past the edge", xs["日"])
	}
}

// With the cursor on a wide cell's continuation, the continuation moves right
// alone. It must become a blank cell, not stay a continuation with no head.
func TestPrepareIMEShift_OrphanContinuationBlanked(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	putWide(tm.grid, 0, 0, '日')
	tm.grid.CursorR, tm.grid.CursorC = 0, 1
	tm.ime.composing, tm.ime.compText = true, "a"

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 8, rows: 4, renderRows: 4}
	tm.prepareFastPath(&ds)
	tm.prepareBiDi(&ds)
	tm.prepareIMEShift(&ds)

	if got := ds.resolveVisual(0, 2); got.Width != 1 || got.Ch != ' ' {
		t.Errorf("cell after the strip = %+v, want a blank cell", got)
	}
	if got := ds.resolveVisual(0, 0); got.Ch != '日' {
		t.Errorf("head moved: col 0 = %q", got.Ch)
	}
}

// imeStripCols must count columns the way drawIME draws them: wide runes take
// two, zero-width runes still take one, and runes past the cap are not drawn.
func TestIMEStripCols(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"ab", 2},
		{"中a", 3},
		{"\u0301", 1}, // combining mark: drawIME gives it a column of its own
		{strings.Repeat("a", imeCompRuneLimit+10), imeCompRuneLimit},
	}
	for _, c := range cases {
		if got := imeStripCols(c.in); got != c.want {
			t.Errorf("imeStripCols(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// No shift where drawIME draws no strip: while the view is scrolled back, and
// when the cursor is in the deferred-wrap column past the last cell.
func TestPrepareIMEShift_NoShiftWithoutStrip(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cursorC  int
		scrolled bool
	}{
		{"scrolled back", 1, true},
		{"deferred wrap", 8, false},
	} {
		tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
		tm.grid.Mu.Lock()
		tm.grid.CursorR, tm.grid.CursorC = 0, tc.cursorC
		if tc.scrolled {
			tm.grid.ViewOffset = 1
		}
		tm.ime.composing, tm.ime.compText = true, "a"
		ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 8, rows: 4, renderRows: 4}
		tm.prepareFastPath(&ds)
		tm.prepareResolved(&ds)
		tm.prepareBiDi(&ds)
		tm.prepareIMEShift(&ds)
		tm.grid.Mu.Unlock()
		if ds.bidiVisRows[0] != nil {
			t.Errorf("%s: cursor row shifted, want it left alone", tc.name)
		}
	}
}
