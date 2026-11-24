package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// Wide cells must reach go-glyph with TextStyle.CellWidth set (#312). go-glyph
// uses it to size a CJK fallback face so one ideograph fills its two cells;
// without it the face is sized by cap height and the ideograph fills only
// 80-85% of its box, which shows as a gap after every character.
//
// Narrow cells must NOT carry it. CellWidth also switches on go-glyph's
// box-drawing snap, which rounds the cell to a whole pixel. go-term's own cell
// widths alternate (8, 9, 8, ... px for an 8.4 px cell), so a rounded box glyph
// would leave 1 px gaps in horizontal lines.

const wideTestCellW = 10

// putWide writes a width-2 cell at (r, c) and its zero continuation cell.
func putWide(g *grid, r, c int, ch rune) {
	row := g.row(r)
	row[c] = defaultCell()
	row[c].Ch = ch
	row[c].Width = 2
	row[c+1] = cell{}
}

// putNarrow writes a width-1 cell at (r, c).
func putNarrow(g *grid, r, c int, ch rune) {
	row := g.row(r)
	row[c] = defaultCell()
	row[c].Ch = ch
	row[c].Width = 1
}

// TestOnDraw_WideCellCarriesCellWidth covers the foreground pass and the block
// cursor, which redraws the glyph under it and must draw it at the same size.
func TestOnDraw_WideCellCarriesCellWidth(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.grid.Mu.Lock()
	g := tm.grid
	putWide(g, 0, 0, '中')
	putNarrow(g, 0, 2, 'a')
	putNarrow(g, 0, 3, 'é') // non-ASCII narrow: takes the per-cell emit path
	g.CursorR, g.CursorC = 0, 0
	g.CursorVisible = true
	tm.grid.Mu.Unlock()

	tm.onDraw(dc)

	var wide int
	for _, te := range dc.Texts() {
		switch te.Text {
		case "中":
			wide++
			if te.Style.CellWidth != wideTestCellW {
				t.Errorf("wide glyph at x=%v: CellWidth = %v, want %v",
					te.X, te.Style.CellWidth, wideTestCellW)
			}
		default:
			if te.Style.CellWidth != 0 {
				t.Errorf("narrow text %q: CellWidth = %v, want 0",
					te.Text, te.Style.CellWidth)
			}
		}
	}
	// One from the foreground pass, one from the block cursor.
	if wide != 2 {
		t.Fatalf("drew the wide glyph %d times, want 2 (text pass + cursor)", wide)
	}
}

// TestDrawCopyCursor_WideCellCarriesCellWidth: the copy-mode cursor redraws the
// glyph under it, like the text cursor.
func TestDrawCopyCursor_WideCellCarriesCellWidth(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	putWide(tm.grid, 0, 0, '中')
	tm.copy.cursor.Row, tm.copy.cursor.Col = 0, 0

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 8, rows: 4, renderRows: 4}
	tm.drawCopyCursor(&ds)

	texts := dc.Texts()
	if len(texts) != 1 || texts[0].Text != "中" {
		t.Fatalf("copy cursor drew %v, want the wide glyph once", texts)
	}
	if got := texts[0].Style.CellWidth; got != wideTestCellW {
		t.Errorf("CellWidth = %v, want %v", got, wideTestCellW)
	}
}

// TestDrawIME_WideRuneCarriesCellWidth: CJK input methods are where wide
// glyphs are typed, so the pre-edit strip must draw them at the same size the
// committed text will have. Its narrow runes stay without CellWidth.
func TestDrawIME_WideRuneCarriesCellWidth(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.ime.composing = true
	tm.ime.compText = "中a"
	tm.grid.CursorR, tm.grid.CursorC = 0, 0

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 8, rows: 4, renderRows: 4}
	tm.drawIME(&ds)

	texts := dc.Texts()
	if len(texts) != 2 {
		t.Fatalf("drew %d composition glyphs, want 2", len(texts))
	}
	if got := texts[0].Style.CellWidth; got != wideTestCellW {
		t.Errorf("wide rune: CellWidth = %v, want %v", got, wideTestCellW)
	}
	if got := texts[0].Style.EmojiBoxWidth; got != tm.spanW(0, 2) {
		t.Errorf("wide rune: EmojiBoxWidth = %v, want %v", got, tm.spanW(0, 2))
	}
	if got := texts[1].Style.CellWidth; got != 0 {
		t.Errorf("narrow rune: CellWidth = %v, want 0", got)
	}
}

// cursorRects returns every FilledRect of color c as {x0, x1}. FilledRect emits
// two triangles (12 floats) per rect: vertex 0 is the top-left, vertex 2 the
// bottom-right.
func cursorRects(dc *gui.DrawContext, c gui.Color) [][2]float32 {
	var out [][2]float32
	for _, b := range dc.Batches() {
		if b.Color != c {
			continue
		}
		for i := 0; i+12 <= len(b.Triangles); i += 12 {
			out = append(out, [2]float32{b.Triangles[i], b.Triangles[i+4]})
		}
	}
	return out
}

// A block or underline cursor on a wide cell must cover both of its columns.
// A one-column box cut the glyph in half: the right half kept the normal
// colors while the left half was inverted. The bar marks the insertion point at
// the left edge, so its width does not change.
func TestDrawCursorShape_WideCellSpansBothColumns(t *testing.T) {
	for _, shape := range []CursorStyle{CursorStyleBlock, CursorStyleUnderline} {
		tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
		tm.focused.Store(true)
		c := defaultCell()
		c.Ch, c.Width = '中', 2
		tm.drawCursorShape(dc, 1, 0, c, shape, tm.style())

		rects := cursorRects(dc, tm.grid.fgOf(c))
		if len(rects) != 1 {
			t.Fatalf("shape %v: drew rects %v, want 1", shape, rects)
		}
		if want := [2]float32{tm.colX(1), tm.colX(3)}; rects[0] != want {
			t.Errorf("shape %v: rect x %v, want %v", shape, rects[0], want)
		}
	}
}

// The copy-mode cursor box must cover a wide cell too.
func TestDrawCopyCursor_WideCellSpansBothColumns(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.grid.Mu.Lock()
	defer tm.grid.Mu.Unlock()
	putWide(tm.grid, 0, 0, '中')
	tm.copy.cursor.Row, tm.copy.cursor.Col = 0, 0

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 8, rows: 4, renderRows: 4}
	tm.drawCopyCursor(&ds)

	rects := cursorRects(dc, tm.grid.ov.copyCurFill)
	if want := [2]float32{tm.colX(0), tm.colX(2)}; len(rects) != 1 || rects[0] != want {
		t.Errorf("copy cursor rects %v, want [%v]", rects, want)
	}
}

// While composing, the caret takes the width of the preedit rune it sits on,
// not of the grid cell under it.
func TestDrawCursor_IMEWideRuneSpansBothColumns(t *testing.T) {
	tm, dc := newDrawTerm(4, 8, wideTestCellW, 20)
	tm.focused.Store(true)
	tm.ime.composing = true
	tm.ime.compText = "a中"
	tm.ime.compCursor = 1 // caret on 中
	g := tm.grid
	g.CursorR, g.CursorC, g.CursorVisible = 0, 0, true
	putNarrow(g, 0, 1, 'x') // narrow grid cell under the wide preedit rune

	ds := drawState{dc: dc, style: tm.style(), g: g, cols: 8, rows: 4, renderRows: 4,
		bidiV2LRows: make([][]int, 4)}
	tm.drawIME(&ds)
	tm.drawCursor(&ds)

	c := defaultCell()
	rects := cursorRects(dc, g.fgOf(c))
	want := [2]float32{tm.colX(1), tm.colX(3)}
	found := false
	for _, r := range rects {
		if r == want {
			found = true
		}
	}
	if !found {
		t.Errorf("caret rects %v, want one at %v", rects, want)
	}
}
