package term

// Put writes ch at the cursor with current attrs and advances. Wraps
// to the next line at right margin; scrolls up at bottom. Honors east-
// asian wide / emoji widths via runeWidth: a width-2 rune occupies the
// current cell and the cell to its right (the "continuation"), and
// wraps early if only one column remains. Width-0 runes (combining
// marks, ZWJ, etc.) are dropped — Phase 11 doesn't model combining.
func (g *grid) Put(ch rune) {
	ch = g.translateRune(ch)
	w := runeWidth(ch)
	if w == 0 {
		return
	}
	justWrapped := false
	if !g.AutoWrap {
		if g.CursorC >= g.Cols {
			g.CursorC = g.Cols - 1
		}
		if w == 2 && g.CursorC+1 >= g.Cols {
			w = 1
		}
	} else if g.CursorC >= g.Cols {
		g.RowWrapped[g.CursorR] = true
		g.Newline()
		g.CursorC = 0
		justWrapped = true
	}

	// Guard with justWrapped: if we already wrapped once (cursor is now at
	// col 0), the wide-char check would fire again when Cols==1, producing a
	// second spurious Newline for a single Put call.
	if !justWrapped && g.AutoWrap && w == 2 && g.CursorC+1 >= g.Cols {
		if c := g.At(g.CursorR, g.CursorC); c != nil {
			*c = blankCell(g.CurFG, g.CurBG, g.CurAttrs)
		}
		g.RowWrapped[g.CursorR] = true
		g.Newline()
		g.CursorC = 0
	}

	if g.InsertMode {
		g.InsertChars(w)
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	if w == 2 {
		g.eraseWideAt(g.CursorR, g.CursorC+1)
	}
	head := cell{
		Ch: ch, FG: g.CurFG, BG: g.CurBG,
		Attrs: g.CurAttrs, Width: uint8(w), LinkID: g.CurLinkID,
		ULStyle: g.CurULStyle, ULColor: g.CurULColor,
	}
	if c := g.At(g.CursorR, g.CursorC); c != nil {
		*c = head
	}
	if w == 2 {
		if c := g.At(g.CursorR, g.CursorC+1); c != nil {
			*c = head.continuation()
		}
	}
	g.markDirty(g.CursorR)
	g.CursorC += w
	if !g.AutoWrap && g.CursorC >= g.Cols {
		g.CursorC = g.Cols - 1
	}
}

// eraseWideAt sanitizes the wide-char pair (if any) covering (r,c) so
// a subsequent overwrite doesn't leave half a glyph behind. If (r,c)
// is a wide head, blanks the continuation to its right. If it's a
// continuation, blanks the head to its left. No-op for normal cells.
func (g *grid) eraseWideAt(r, c int) {
	cell := g.At(r, c)
	if cell == nil {
		return
	}
	switch {
	case cell.Width == 2:
		if right := g.At(r, c+1); right != nil &&
			right.Width == 0 && right.Ch == 0 {
			*right = defaultCell()
		}
	case cell.Width == 0 && cell.Ch == 0:
		if left := g.At(r, c-1); left != nil && left.Width == 2 {
			*left = defaultCell()
		}
	}
}

// Newline moves to next row, scrolling the region if needed. Column
// unchanged (LF only); shells emit CRLF. When the cursor sits on the
// scroll region's Bottom row, scrollUpRegion is invoked so apps that
// shrink the active area (less, vim status line) don't blow away
// untouched rows below. When the cursor is below Bottom (outside the
// region), it advances toward Rows-1 without scrolling.
func (g *grid) Newline() {
	switch {
	case g.CursorR == g.Bottom:
		g.scrollUpRegion(1)
	case g.CursorR+1 < g.Rows:
		g.markDirty(g.CursorR)
		g.CursorR++
		g.markDirty(g.CursorR)
	}
}

// NextLine implements ESC E (NEL): CR + LF.
func (g *grid) NextLine() {
	g.CarriageReturn()
	g.Newline()
}

// CarriageReturn moves to column 0.
func (g *grid) CarriageReturn() { g.CursorC = 0 }

// Backspace moves cursor left one column. No erase.
func (g *grid) Backspace() {
	if g.CursorC > 0 {
		g.CursorC--
	}
}

// Tab advances the cursor to the next tab stop. Scans TabStops from
// CursorC+1; if no stop exists within the row, clamps to Cols-1.
func (g *grid) Tab() {
	if g.CursorC < 0 {
		g.CursorC = 0
	}
	for c := g.CursorC + 1; c < g.Cols; c++ {
		if g.TabStops[c] {
			g.CursorC = c
			return
		}
	}
	g.CursorC = g.Cols - 1
}

// SetTabStop sets a tab stop at the current cursor column. Implements ESC H (HTS).
func (g *grid) SetTabStop() {
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = true
	}
}

// ClearTabStop clears the tab stop at the current cursor column (all==false)
// or clears all tab stops (all==true). Implements CSI g (TBC).
func (g *grid) ClearTabStop(all bool) {
	if all {
		g.TabStops = [MaxGridDim]bool{}
		return
	}
	if g.CursorC >= 0 && g.CursorC < MaxGridDim {
		g.TabStops[g.CursorC] = false
	}
}

// EraseInLine implements CSI K. mode: 0 = cursor to EOL, 1 = SOL to
// cursor, 2 = entire line. Cleared cells use current bg/attrs so
// painted backgrounds persist.
func (g *grid) EraseInLine(mode int) {
	row := g.CursorR
	if row < 0 || row >= g.Rows {
		return
	}
	cFrom, cTo := 0, g.Cols
	switch mode {
	case 0:
		g.eraseWideAt(row, g.CursorC)
		cFrom = g.CursorC
	case 1:
		g.eraseWideAt(row, g.CursorC)
		cTo = g.CursorC + 1
	case 2:

	default:
		return
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := cFrom; c < cTo; c++ {
		g.Cells[row*g.Cols+c] = blank
	}
	g.markDirty(row)
}

// EraseInDisplay implements CSI J. mode: 0 = cursor to end of screen,
// 1 = start of screen to cursor, 2/3 = entire screen.
func (g *grid) EraseInDisplay(mode int) {
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	switch mode {
	case 0:
		g.EraseInLine(0)
		for r := g.CursorR + 1; r < g.Rows; r++ {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
		g.markAllDirty()
	case 1:
		g.EraseInLine(1)
		for r := range g.CursorR {
			for c := range g.Cols {
				g.Cells[r*g.Cols+c] = blank
			}
		}
		g.markAllDirty()
	case 2, 3:
		for i := range g.Cells {
			g.Cells[i] = blank
		}
		g.markAllDirty()
	}
}

// InsertLines implements CSI Ps L (IL): insert n blank lines at the
// cursor row, pushing existing rows toward Bottom; rows pushed past
// Bottom are discarded. No-op when the cursor is outside the active
// scroll region (DEC behavior).
func (g *grid) InsertLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		for r := g.Bottom; r >= g.CursorR+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.CursorR; r < g.CursorR+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
}

// DeleteLines implements CSI Ps M (DL): delete n lines starting at the
// cursor row, shifting rows below up; blank rows fill the bottom of
// the region. No-op when cursor is outside the region.
func (g *grid) DeleteLines(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	if g.CursorR < g.Top || g.CursorR > g.Bottom {
		return
	}
	height := g.Bottom - g.CursorR + 1
	if n > height {
		n = height
	}
	if n < height {
		copy(
			g.Cells[g.CursorR*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.CursorR+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
		copy(g.RowWrapped[g.CursorR:g.Bottom+1-n], g.RowWrapped[g.CursorR+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom - n + 1; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.CursorC = 0
	g.markAllDirty()
}

// InsertChars implements CSI Ps @ (ICH): insert n blanks at the cursor,
// shifting existing cells right within the row; cells past the right
// margin are discarded. Blanks use current SGR bg/attrs.
func (g *grid) InsertChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	g.eraseWideAt(g.CursorR, g.Cols-1)
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC+n:], row[g.CursorC:g.Cols-n])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.CursorC; c < g.CursorC+n; c++ {
		row[c] = blank
	}
	g.markDirty(g.CursorR)
}

// DeleteChars implements CSI Ps P (DCH): delete n cells at the cursor,
// shifting cells from the right inward; blanks fill at the right edge.
func (g *grid) DeleteChars(n int) {
	if n <= 0 || g.CursorR < 0 || g.CursorR >= g.Rows {
		return
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		return
	}
	width := g.Cols - g.CursorC
	if n > width {
		n = width
	}
	g.eraseWideAt(g.CursorR, g.CursorC)
	g.eraseWideAt(g.CursorR, g.CursorC+n)
	row := g.Cells[g.CursorR*g.Cols : (g.CursorR+1)*g.Cols]
	if n < width {
		copy(row[g.CursorC:], row[g.CursorC+n:g.Cols])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for c := g.Cols - n; c < g.Cols; c++ {
		row[c] = blank
	}
	g.markDirty(g.CursorR)
}
