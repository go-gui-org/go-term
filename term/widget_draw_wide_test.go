package term

import "testing"

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
