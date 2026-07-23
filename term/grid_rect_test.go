package term

import "testing"

// fillGrid writes ch into every cell of g, leaving the cursor at the origin.
// Rect operations are easiest to read against a uniform background.
func fillGrid(g *grid, ch rune) {
	for r := range g.Rows {
		for c := range g.Cols {
			g.Cells[r*g.Cols+c] = cell{
				Ch: ch, FG: DefaultColor, BG: DefaultColor,
				ULColor: DefaultColor, Width: 1,
			}
		}
	}
	g.CursorR, g.CursorC = 0, 0
}

// wantRows asserts the full screen contents row by row.
func wantRows(t *testing.T, g *grid, want []string) {
	t.Helper()
	for r, w := range want {
		if got := rowText(g, r); got != w {
			t.Errorf("row %d = %q, want %q", r, got, w)
		}
	}
}

func TestGrid_EraseRect_ClearsInclusiveCorners(t *testing.T) {
	g := newGrid(4, 6)
	fillGrid(g, 'X')
	// 1-based, inclusive: rows 2..3, columns 2..4.
	g.EraseRect(2, 2, 3, 4)
	wantRows(t, g, []string{"XXXXXX", "X   XX", "X   XX", "XXXXXX"})
}

func TestGrid_EraseRect_DefaultsCoverWholePage(t *testing.T) {
	g := newGrid(3, 4)
	fillGrid(g, 'X')
	g.EraseRect(0, 0, 0, 0) // all four params defaulted
	wantRows(t, g, []string{"    ", "    ", "    "})
}

func TestGrid_EraseRect_ClampsAndIgnoresDegenerate(t *testing.T) {
	g := newGrid(3, 4)
	fillGrid(g, 'X')
	g.EraseRect(2, 3, 99, 99) // bottom-right past the page: clamp, don't panic
	wantRows(t, g, []string{"XXXX", "XX  ", "XX  "})

	fillGrid(g, 'X')
	g.EraseRect(3, 1, 2, 4) // top below bottom: ignored per VT510
	wantRows(t, g, []string{"XXXX", "XXXX", "XXXX"})

	fillGrid(g, 'X')
	g.EraseRect(1, 4, 3, 2) // left right of right: ignored
	wantRows(t, g, []string{"XXXX", "XXXX", "XXXX"})
}

func TestGrid_EraseRect_UsesCurrentBackground(t *testing.T) {
	g := newGrid(2, 3)
	fillGrid(g, 'X')
	g.CurBG = paletteColor(4)
	g.EraseRect(1, 1, 1, 2)
	for c := range 2 {
		if got := g.At(0, c).BG; got != paletteColor(4) {
			t.Errorf("col %d bg = %#x, want BCE background", c, got)
		}
	}
	if got := g.At(0, 2).BG; got != DefaultColor {
		t.Errorf("cell outside the rect was repainted: bg %#x", got)
	}
}

func TestGrid_EraseRect_OriginModeIsRegionRelative(t *testing.T) {
	g := newGrid(5, 4)
	fillGrid(g, 'X')
	g.SetScrollRegion(1, 3)
	g.OriginMode = true
	// Row 1 of the region is screen row 1; the bottom clamps to the region.
	g.EraseRect(1, 1, 9, 2)
	wantRows(t, g, []string{"XXXX", "  XX", "  XX", "  XX", "XXXX"})
}

func TestGrid_EraseRect_SplitsWideCharAtEdge(t *testing.T) {
	g := newGrid(1, 6)
	g.Put('世') // cols 0-1
	g.Put('界') // cols 2-3
	// Erase columns 2..3 (1-based 3..4) — the left pair must survive intact.
	g.EraseRect(1, 3, 1, 4)
	if g.At(0, 0).Ch != '世' || g.At(0, 1).Width != 0 {
		t.Errorf("wide pair outside the rect was damaged: %+v %+v", *g.At(0, 0), *g.At(0, 1))
	}
	if g.At(0, 2).Ch != ' ' || g.At(0, 3).Ch != ' ' {
		t.Errorf("rect not cleared: %q %q", g.At(0, 2).Ch, g.At(0, 3).Ch)
	}

	// Now erase only the left half of a wide pair: the orphaned right half
	// must be blanked too rather than left as a dangling continuation.
	g = newGrid(1, 6)
	g.Put('世')
	g.EraseRect(1, 1, 1, 1)
	if got := rowText(g, 0); got != "      " {
		t.Errorf("orphaned continuation left behind: %q", got)
	}
}

func TestGrid_SelectiveEraseRect_SkipsProtected(t *testing.T) {
	g := newGrid(2, 5)
	fillGrid(g, 'X')
	// Protect (0,1) and (1,3).
	g.At(0, 1).Attrs |= attrProtected
	g.At(1, 3).Attrs |= attrProtected

	g.SelectiveEraseRect(1, 1, 2, 5)
	wantRows(t, g, []string{" X   ", "   X "})

	// DECERA ignores protection entirely.
	g.EraseRect(1, 1, 2, 5)
	wantRows(t, g, []string{"     ", "     "})
}

func TestGrid_FillRect_FillsWithCurrentSGR(t *testing.T) {
	g := newGrid(3, 4)
	g.CurAttrs = attrBold
	g.CurFG = paletteColor(2)
	g.FillRect('*', 2, 2, 3, 3)
	wantRows(t, g, []string{"    ", " ** ", " ** "})
	c := g.At(1, 1)
	if c.Attrs&attrBold == 0 || c.FG != paletteColor(2) {
		t.Errorf("fill did not take current SGR: %+v", *c)
	}
}

func TestGrid_FillRect_RejectsOutOfRangeChar(t *testing.T) {
	g := newGrid(2, 3)
	fillGrid(g, 'X')
	for _, pch := range []int{0, 31, 127, 159, 256, 0x4E16} {
		g.FillRect(pch, 1, 1, 2, 3)
		wantRows(t, g, []string{"XXX", "XXX"})
	}
	// The GR range is legal.
	g.FillRect(0xA7, 1, 1, 1, 1)
	if g.At(0, 0).Ch != 0xA7 {
		t.Errorf("GR fill char rejected: %q", g.At(0, 0).Ch)
	}
}

func TestGrid_FillRect_FeedsREP(t *testing.T) {
	g := newGrid(1, 6)
	g.FillRect('=', 1, 1, 1, 2)
	g.MoveCursor(0, 3)
	g.RepeatLast(2)
	if got := rowText(g, 0); got != "== == " {
		t.Errorf("REP after DECFRA = %q, want \"== == \"", got)
	}
}

func TestGrid_ChangeAttrsRect_RectangleExtent(t *testing.T) {
	g := newGrid(3, 5)
	fillGrid(g, 'X')
	g.RectExtent = 2
	// Bold + underline over rows 1..2, cols 2..3 (1-based).
	g.ChangeAttrsRect([]int{1, 2, 2, 3, 1, 4})
	for r := range 2 {
		for c := range 5 {
			got := g.At(r, c).Attrs
			want := uint16(0)
			if c >= 1 && c <= 2 {
				want = attrBold | attrUnderline
			}
			if got != want {
				t.Errorf("(%d,%d) attrs %#x, want %#x", r, c, got, want)
			}
		}
	}
	if g.At(0, 1).ULStyle != ulSingle {
		t.Errorf("underline turned on without a style: %d", g.At(0, 1).ULStyle)
	}
	if g.At(2, 1).Attrs != 0 {
		t.Errorf("row outside the rect was touched: %#x", g.At(2, 1).Attrs)
	}

	// Turning underline back off drops the style with it.
	g.ChangeAttrsRect([]int{1, 2, 2, 3, 24})
	if c := g.At(0, 1); c.Attrs != attrBold || c.ULStyle != ulNone {
		t.Errorf("SGR 24 in rect: attrs %#x style %d", c.Attrs, c.ULStyle)
	}
}

func TestGrid_ChangeAttrsRect_StreamExtentIsDefault(t *testing.T) {
	g := newGrid(3, 4)
	fillGrid(g, 'X')
	if g.RectExtent != 0 {
		t.Fatalf("DECSACE default is rectangle, want stream: %d", g.RectExtent)
	}
	// Stream: (1,3) through (2,2) covers cols 2-3 of row 0 and cols 0-1 of row 1.
	g.ChangeAttrsRect([]int{1, 3, 2, 2, 7})
	want := [3][4]bool{
		{false, false, true, true},
		{true, true, false, false},
		{},
	}
	for r := range 3 {
		for c := range 4 {
			got := g.At(r, c).Attrs&attrInverse != 0
			if got != want[r][c] {
				t.Errorf("(%d,%d) inverse=%v, want %v", r, c, got, want[r][c])
			}
		}
	}
}

func TestGrid_ChangeAttrsRect_ZeroClearsAll(t *testing.T) {
	g := newGrid(1, 3)
	fillGrid(g, 'X')
	g.RectExtent = 2
	g.At(0, 0).Attrs = attrBold | attrInverse | attrItalic | attrProtected
	g.ChangeAttrsRect([]int{1, 1, 1, 1, 0})
	// 0 clears only the four attributes DECCARA owns; italic and protection
	// are outside its set and survive.
	if got := g.At(0, 0).Attrs; got != attrItalic|attrProtected {
		t.Errorf("attrs after DECCARA 0 = %#x, want %#x", got, attrItalic|attrProtected)
	}
}

func TestGrid_ReverseAttrsRect_Toggles(t *testing.T) {
	g := newGrid(1, 3)
	fillGrid(g, 'X')
	g.RectExtent = 2
	g.At(0, 0).Attrs = attrBold
	g.ReverseAttrsRect([]int{1, 1, 1, 2, 1}) // toggle bold over cols 0-1
	if g.At(0, 0).Attrs&attrBold != 0 {
		t.Error("bold not cleared by DECRARA on a bold cell")
	}
	if g.At(0, 1).Attrs&attrBold == 0 {
		t.Error("bold not set by DECRARA on a plain cell")
	}
	if g.At(0, 2).Attrs != 0 {
		t.Error("cell outside the rect was toggled")
	}
}

func TestGrid_CopyRect_NonOverlapping(t *testing.T) {
	g := newGrid(4, 4)
	fillGrid(g, '.')
	g.MoveCursor(0, 0)
	g.Put('a')
	g.Put('b')
	g.MoveCursor(1, 0)
	g.Put('c')
	g.Put('d')
	// Copy the 2x2 block at (1,1) to (3,3) — 1-based.
	g.CopyRect(1, 1, 2, 2, 3, 3)
	wantRows(t, g, []string{"ab..", "cd..", "..ab", "..cd"})
}

func TestGrid_CopyRect_OverlapReadsPreCopyContent(t *testing.T) {
	g := newGrid(1, 6)
	for i, ch := range "abcdef" {
		g.Cells[i] = cell{Ch: ch, FG: DefaultColor, BG: DefaultColor,
			ULColor: DefaultColor, Width: 1}
	}
	// Shift cols 1..4 one column right; the destination overlaps the source.
	g.CopyRect(1, 1, 1, 4, 1, 2)
	if got := rowText(g, 0); got != "aabcd"+"f" {
		t.Errorf("overlapping copy = %q, want %q", got, "aabcdf")
	}

	// Same in the other direction.
	g = newGrid(1, 6)
	for i, ch := range "abcdef" {
		g.Cells[i] = cell{Ch: ch, FG: DefaultColor, BG: DefaultColor,
			ULColor: DefaultColor, Width: 1}
	}
	g.CopyRect(1, 3, 1, 6, 1, 2)
	if got := rowText(g, 0); got != "acdeff" {
		t.Errorf("overlapping copy = %q, want %q", got, "acdeff")
	}
}

func TestGrid_CopyRect_ClipsAtPageEdge(t *testing.T) {
	g := newGrid(3, 4)
	fillGrid(g, '.')
	g.MoveCursor(0, 0)
	g.Put('a')
	g.Put('b')
	g.Put('c')
	// A 1x3 source landing at column 3 keeps only what fits.
	g.CopyRect(1, 1, 1, 3, 3, 3)
	wantRows(t, g, []string{"abc.", "....", "..ab"})
}

func TestGrid_CopyRect_CarriesAttributesAndProtection(t *testing.T) {
	g := newGrid(2, 4)
	fillGrid(g, '.')
	src := g.At(0, 0)
	src.Ch, src.Attrs, src.FG = 'Z', attrBold|attrProtected, paletteColor(3)
	g.CopyRect(1, 1, 1, 1, 2, 2)
	dst := g.At(1, 1)
	if dst.Ch != 'Z' || dst.Attrs != attrBold|attrProtected || dst.FG != paletteColor(3) {
		t.Errorf("copy lost cell state: %+v", *dst)
	}
}

func TestGrid_CopyRect_BlanksClippedWideHalves(t *testing.T) {
	g := newGrid(2, 6)
	fillGrid(g, '.')
	g.MoveCursor(0, 0)
	g.Put('世') // cols 0-1
	// Copy only the wide char's left half to row 2: the copied head has no
	// continuation, so it must degrade to a blank.
	g.CopyRect(1, 1, 1, 1, 2, 1)
	if got := g.At(1, 0).Ch; got != ' ' {
		t.Errorf("clipped wide head kept its glyph: %q", got)
	}
	// And copying only the right half must not leave a bare continuation.
	g.CopyRect(1, 2, 1, 2, 2, 3)
	if c := g.At(1, 2); c.Ch != ' ' || c.Width != 1 {
		t.Errorf("clipped continuation left behind: %+v", *c)
	}
}

func TestGrid_CopyRect_SelfCopyIsNoop(t *testing.T) {
	g := newGrid(2, 3)
	fillGrid(g, 'X')
	g.CopyRect(1, 1, 2, 3, 1, 1)
	wantRows(t, g, []string{"XXX", "XXX"})
}

func TestGrid_CopyRect_OriginMode(t *testing.T) {
	g := newGrid(5, 4)
	fillGrid(g, '.')
	// Put source data in region row 2 (screen row 2).
	g.MoveCursor(2, 0)
	g.Put('A')
	g.Put('B')
	g.SetScrollRegion(1, 3)
	g.OriginMode = true
	// Copy region (2,1)-(2,2) to (1,1) — move row 2 cols 0-1 up to row 1.
	// 1-based region row 1 = screen row 1, region row 2 = screen row 2.
	g.CopyRect(2, 1, 2, 2, 1, 1)
	wantRows(t, g, []string{"....", "AB..", "AB..", "....", "...."})
}

func TestGrid_SelectiveErase_LineAndDisplay(t *testing.T) {
	g := newGrid(3, 4)
	fillGrid(g, 'X')
	g.At(0, 1).Attrs |= attrProtected
	g.At(2, 2).Attrs |= attrProtected

	g.MoveCursor(0, 0)
	g.SelectiveEraseInLine(2)
	if got := rowText(g, 0); got != " X  " {
		t.Errorf("DECSEL row = %q, want \" X  \"", got)
	}

	g.SelectiveEraseInDisplay(2)
	wantRows(t, g, []string{" X  ", "    ", "  X "})

	// Plain ED ignores protection.
	g.EraseInDisplay(2)
	wantRows(t, g, []string{"    ", "    ", "    "})
}

func TestGrid_EraseInLine_CursorPastRightMargin(t *testing.T) {
	// With a wrap pending the cursor sits at Cols; EL 1 must clamp rather
	// than run one cell into the following row.
	g := newGrid(2, 3)
	fillGrid(g, 'X')
	g.CursorR, g.CursorC = 0, g.Cols
	g.EraseInLine(1)
	wantRows(t, g, []string{"   ", "XXX"})
}

func TestGrid_SetProtection_Params(t *testing.T) {
	g := newGrid(1, 1)
	g.SetProtection(1)
	if g.CurAttrs&attrProtected == 0 {
		t.Error("DECSCA 1 did not protect")
	}
	for _, ps := range []int{0, 2, 9} {
		g.SetProtection(1)
		g.SetProtection(ps)
		if g.CurAttrs&attrProtected != 0 {
			t.Errorf("DECSCA %d did not clear protection", ps)
		}
	}
}

func TestGrid_ProtectedBlanksInheritCurrentState(t *testing.T) {
	// Cells blanked while DECSCA is on come out protected, since the blank
	// carries CurAttrs — the same rule xterm's ClearCells follows.
	g := newGrid(1, 4)
	fillGrid(g, 'X')
	g.SetProtection(1)
	g.MoveCursor(0, 0)
	g.EraseInLine(2)
	for c := range 4 {
		if g.At(0, c).Attrs&attrProtected == 0 {
			t.Fatalf("col %d not protected after erase under DECSCA", c)
		}
	}
	g.SetProtection(0)
	g.SelectiveEraseInLine(2)
	if got := rowText(g, 0); got != "    " {
		t.Errorf("row changed: %q", got)
	}
	if g.At(0, 0).Attrs&attrProtected == 0 {
		t.Error("DECSEL cleared a protected blank")
	}
}

func TestGrid_HardResetClearsRectExtent(t *testing.T) {
	g := newGrid(2, 2)
	g.SetRectExtent(2)
	g.SetProtection(1)
	g.SoftReset()
	if g.CurAttrs&attrProtected != 0 {
		t.Error("DECSTR left DECSCA set")
	}
	if g.RectExtent != 2 {
		t.Error("DECSTR reset DECSACE; VT510's table does not list it")
	}
	g.HardReset()
	if g.RectExtent != 0 {
		t.Error("RIS left DECSACE at the rectangle extent")
	}
}

func TestGrid_ChangeAttrsRect_KeepsWideCharAtEdge(t *testing.T) {
	// DECCARA restyles; it must never blank a wide character whose partner
	// half falls outside the rectangle.
	g := newGrid(1, 6)
	g.Put('世') // cols 0-1
	g.Put('界') // cols 2-3
	g.RectExtent = 2
	g.ChangeAttrsRect([]int{1, 2, 1, 3, 1}) // cols 1-2: half of each pair
	if g.At(0, 0).Ch != '世' || g.At(0, 2).Ch != '界' {
		t.Errorf("DECCARA damaged wide characters: %q %q", g.At(0, 0).Ch, g.At(0, 2).Ch)
	}
	if g.At(0, 1).Attrs&attrBold == 0 || g.At(0, 2).Attrs&attrBold == 0 {
		t.Error("DECCARA did not apply inside the rect")
	}
}

func TestGrid_SelectiveErase_KeepsProtectedWidePair(t *testing.T) {
	// A protected wide character straddling the erase edge must survive whole:
	// splitting it would damage exactly what protection forbids touching.
	g := newGrid(1, 6)
	g.SetProtection(1)
	g.Put('世') // cols 0-1, both halves protected
	g.SetProtection(0)
	g.Put('x')

	g.SelectiveEraseRect(1, 2, 1, 3) // cuts the pair at col 1
	if g.At(0, 0).Ch != '世' || g.At(0, 1).Width != 0 {
		t.Errorf("DECSERA damaged a protected wide pair: %+v %+v",
			*g.At(0, 0), *g.At(0, 1))
	}
	if g.At(0, 2).Ch != ' ' {
		t.Error("DECSERA did not clear the unprotected cell")
	}

	// DECSEL from inside the pair leaves it alone as well.
	g.MoveCursor(0, 1)
	g.SelectiveEraseInLine(0)
	if g.At(0, 0).Ch != '世' {
		t.Errorf("DECSEL damaged a protected wide pair: %q", g.At(0, 0).Ch)
	}

	// The unprotected form still splits, so no orphan half is left.
	g = newGrid(1, 6)
	g.Put('世')
	g.MoveCursor(0, 1)
	g.SelectiveEraseInLine(0)
	if got := rowText(g, 0); got != "      " {
		t.Errorf("unprotected pair not split by DECSEL: %q", got)
	}
}
