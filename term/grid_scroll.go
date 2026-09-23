package term

import "math"

// row returns the cells of screen row r (0 <= r < Rows). Caller holds Mu.
func (g *grid) row(r int) []cell { return g.slots[g.rowMap[r]] }

// fillScreen sets every screen cell to c without per-row bookkeeping (no
// eraseSpan, no occlusion, no dirty marks). It goes row by row, never flat over
// Cells: the screen slab also holds rows that were swapped into scrollback.
func (g *grid) fillScreen(c cell) {
	for r := range g.Rows {
		row := g.row(r)
		for i := range row {
			row[i] = c
		}
	}
}

// rowsOver carves cells (row-major, nRows*cols) into capped per-row slices,
// reusing dst's backing array when it is large enough.
func rowsOver(dst [][]cell, cells []cell, nRows, cols int) [][]cell {
	if cap(dst) >= nRows {
		dst = dst[:nRows]
	} else {
		dst = make([][]cell, nRows)
	}
	for r := range dst {
		o := r * cols
		dst[r] = cells[o : o+cols : o+cols]
	}
	return dst
}

// identityMap fills dst (reallocating if short) with 0..n-1.
func identityMap(dst []int32, n int) []int32 {
	if cap(dst) >= n {
		dst = dst[:n]
	} else {
		dst = make([]int32, n)
	}
	for i := range dst {
		dst[i] = int32(i)
	}
	return dst
}

// resetRows lays the screen rows over Cells in order. Call it after Cells is
// replaced by a row-major buffer (newGrid, EnterAlt, Resize). The slots and
// rowMap arrays are reused, so a caller that stashed them elsewhere must nil
// them first.
func (g *grid) resetRows() {
	g.slots = rowsOver(g.slots, g.Cells, g.Rows, g.Cols)
	g.rowMap = identityMap(g.rowMap, g.Rows)
	if cap(g.rowTmp) < g.Rows {
		g.rowTmp = make([]int32, g.Rows)
	}
	g.rowTmp = g.rowTmp[:g.Rows]
	g.rowsBorrowed = false
}

// flatScreen returns screen rows (slots indexed through rowMap) as a row-major
// buffer of len(rowMap)*cols cells. When the rows still sit in order over
// cells — no scroll since the slab was laid out — it returns cells itself and
// copies nothing. Pass cells == nil to force a fresh copy. Short source rows
// (a slot narrower than cols, possible after the ring re-carved its slab)
// are padded with default cells so no zero-value cell leaks through.
func flatScreen(slots [][]cell, rowMap []int32, cells []cell, cols int) []cell {
	inOrder := len(cells) == len(rowMap)*cols
	for r := 0; inOrder && r < len(rowMap); r++ {
		row := slots[rowMap[r]]
		inOrder = len(row) > 0 && &row[0] == &cells[r*cols]
	}
	if inOrder {
		return cells
	}
	out := make([]cell, len(rowMap)*cols)
	for r, s := range rowMap {
		n := copy(out[r*cols:(r+1)*cols], slots[s])
		for i := n; i < cols; i++ {
			out[r*cols+i] = defaultCell()
		}
	}
	return out
}

// reclaimRows copies every screen row into a fresh slab of our own. Used when
// the ring re-carved its slab while the screen still held rows borrowed from
// it: those rows may now alias live ring slots. A fresh slab is required —
// copying in place over the old one would overwrite rows still being read.
func (g *grid) reclaimRows() {
	g.Cells = flatScreen(g.slots, g.rowMap, nil, g.Cols)
	g.resetRows()
}

// syncScrollbackGen reclaims borrowed rows, on the live screen and on a main
// screen parked behind the alt screen, once the ring has re-carved or dropped its
// slab. Call it right after every grid-side ring change (ED 3, RIS, a cap change):
// until then the borrowed rows keep the old slab alive — up to ScrollbackCap×Cols
// cells after the ring meant to free it — and a reused slab can alias live ring
// slots. scrollUpRegion calls it too, as the backstop for ring changes made
// anywhere else. One screen-sized allocation, only on those rare paths.
func (g *grid) syncScrollbackGen() {
	if g.Scrollback.gen == g.sbGen {
		return
	}
	g.sbGen = g.Scrollback.gen
	if g.rowsBorrowed {
		g.reclaimRows()
	}
	// The alt screen never swaps rows with the ring, but the main screen it
	// hides may still hold rows it borrowed before EnterAlt.
	if m := &g.mainSaved; g.AltActive && m.rowsBorrowed && len(m.rowMap) == g.Rows {
		m.cells = flatScreen(m.slots, m.rowMap, nil, g.Cols)
		m.slots = rowsOver(m.slots, m.cells, g.Rows, g.Cols)
		m.rowMap = identityMap(m.rowMap, g.Rows)
		m.rowsBorrowed = false
	}
}

// rotateRowsUp moves screen rows [top+n..bottom] to [top..bottom-n] by
// rotating rowMap. The n rows that fell off the top land at
// [bottom-n+1..bottom] still holding their old cells; the caller blanks
// them. Requires 0 < n <= bottom-top+1. RowWrapped is not touched.
func (g *grid) rotateRowsUp(top, bottom, n int) {
	tmp := g.rowTmp[:n]
	copy(tmp, g.rowMap[top:top+n])
	copy(g.rowMap[top:], g.rowMap[top+n:bottom+1])
	copy(g.rowMap[bottom+1-n:bottom+1], tmp)
}

// rotateRowsDown is the mirror of rotateRowsUp: rows [top..bottom-n] move to
// [top+n..bottom], and the n rows pushed off the bottom land at
// [top..top+n-1] for the caller to blank.
func (g *grid) rotateRowsDown(top, bottom, n int) {
	tmp := g.rowTmp[:n]
	copy(tmp, g.rowMap[bottom+1-n:bottom+1])
	copy(g.rowMap[top+n:bottom+1], g.rowMap[top:bottom+1-n])
	copy(g.rowMap[top:top+n], tmp)
}

// scrollUpRegion shifts rows [Top..Bottom] up by n, clearing the bottom
// n rows of the region with default cells. When the region starts at row 0
// and ScrollbackCap > 0, the displaced top rows are pushed to the scrollback
// ring (oldest first) and trimmed to cap — see regionFeedsScrollback. n is
// clamped: n <= 0 is a no-op, n >= region height clears the region.
func (g *grid) scrollUpRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	feeds := g.regionFeedsScrollback()
	if feeds && g.ScrollbackCap > 0 && !g.AltActive {
		g.Scrollback.EnsureGeom(g.ScrollbackCap, g.Cols)
		// The ring re-carved its slab since our borrowed rows were taken, so
		// they may alias ring slots now. Move them to our own slab before any
		// more rows cross over. Rare: a cap change, ED 3, RIS or a reflow.
		if g.Scrollback.gen != g.sbGen { // inline test: this runs every line feed
			g.syncScrollbackGen()
		}
		evicted := 0
		for r := 0; r < n; r++ {
			// Hand the row to the ring by reference; copying it was a third of
			// vtebench's scrolling time. The spare row the ring gives back takes
			// its place and, once the rotation below moves it to the bottom of
			// the region, is blanked with the other exposed rows.
			s := g.rowMap[g.Top+r]
			spare, ev := g.Scrollback.PushSwap(g.slots[s], g.RowWrapped[g.Top+r])
			g.slots[s] = spare
			if ev {
				evicted++
			}
		}
		g.rowsBorrowed = true
		if evicted > 0 {
			g.trimMarks(evicted)
			g.trimGraphics(evicted)
		}
		// Frozen viewport (copy mode): ViewOffset counts rows back from the
		// live bottom, so the bottom moving away from the viewer is exactly
		// what makes the view drift. Bump by the same n to cancel it out.
		// Clamped to the ring length, which is also what caps the pin once
		// eviction starts — see grid.ViewFrozen.
		if g.ViewFrozen {
			g.ViewOffset = clamp(g.ViewOffset+n, 0, g.Scrollback.Len())
		}
		// Graphics are anchored in content coordinates (scrollback length +
		// screen row), so rows that moved up by n keep the same coordinate
		// for free — the ring grew by the same n. Rows *below* a bottom
		// margin did not move, so the ring's growth silently shifted them;
		// push them down by n to cancel it. No-op without a bottom margin.
		if g.Bottom < g.Rows-1 {
			g.scrollGraphicsRegion(g.Bottom+1, g.Rows-1, n, true)
		}
	} else {
		// Nothing entered scrollback, so the content-row space did not
		// absorb the scroll — images have to travel with their text.
		g.scrollGraphicsRegion(g.Top, g.Bottom, n, false)
	}

	if n < height {
		g.rotateRowsUp(g.Top, g.Bottom, n)
		copy(g.RowWrapped[g.Top:g.Bottom+1-n], g.RowWrapped[g.Top+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom + 1 - n; r <= g.Bottom; r++ {
		row := g.row(r)
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
}

// scrollDownRegion shifts rows [Top..Bottom] down by n, clearing the
// top n rows with default cells. Never writes to scrollback (down-scroll
// reveals erased space, not displaced history).
func (g *grid) scrollDownRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	// Down-scroll never touches scrollback, so images always travel with
	// the rows they sit on.
	g.scrollGraphicsRegion(g.Top, g.Bottom, n, true)
	if n < height {

		g.rotateRowsDown(g.Top, g.Bottom, n)
		copy(g.RowWrapped[g.Top+n:g.Bottom+1], g.RowWrapped[g.Top:g.Bottom+1-n])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Top; r < g.Top+n && r <= g.Bottom; r++ {
		row := g.row(r)
		for i := range row {
			row[i] = blank
		}
		g.RowWrapped[r] = false
	}
	g.markAllDirty()
}

// SetScrollRegion implements DECSTBM (CSI Pt;Pb r). top/bottom are
// 0-based inclusive. Invalid or degenerate ranges (top >= bottom,
// out of bounds) reset to full screen. Cursor is homed to (0, 0)
// per DEC convention.
func (g *grid) SetScrollRegion(top, bottom int) {
	if top < 0 || bottom >= g.Rows || top >= bottom {
		g.Top = 0
		g.Bottom = g.Rows - 1
	} else {
		g.Top = top
		g.Bottom = bottom
	}
	if g.OriginMode && g.regionValid() {
		g.CursorR, g.CursorC = g.Top, 0
		return
	}
	g.CursorR, g.CursorC = 0, 0
}

// ScrollUp implements CSI Ps S — scroll the region up by n rows,
// cursor unchanged. Wrapper around scrollUpRegion.
func (g *grid) ScrollUp(n int) { g.scrollUpRegion(n) }

// ScrollDown implements CSI Ps T — scroll the region down by n rows.
func (g *grid) ScrollDown(n int) { g.scrollDownRegion(n) }

// ScrollView shifts the viewport by `delta` rows: positive = back into
// scrollback (toward older content), negative = forward (toward live).
// Result clamped to [0, len(Scrollback)]. Saturating add: a delta near
// math.MinInt/MaxInt (e.g. derived from NaN/Inf wheel deltas) would
// overflow ViewOffset+delta before clamp, so detect the wrap.
// ViewSubPx is zeroed so integer jumps (PgUp/PgDn, jump-to-mark) land
// cleanly on row boundaries.
func (g *grid) ScrollView(delta int) {
	max := g.Scrollback.Len()
	switch {
	case delta > 0 && g.ViewOffset > max-delta:
		g.ViewOffset = max
	case delta < 0 && g.ViewOffset < -delta:
		g.ViewOffset = 0
	default:
		g.ViewOffset = clamp(g.ViewOffset+delta, 0, max)
	}
	g.ViewSubPx = 0
}

// ScrollViewPx scrolls the viewport by deltaPx pixels. The total pixel
// position (ViewOffset*cellH + ViewSubPx + deltaPx) is converted back
// into a whole-row ViewOffset and a fractional ViewSubPx remainder.
// Clamped to [0, Scrollback.Len()*cellH]. cellH <= 0 is a no-op.
func (g *grid) ScrollViewPx(deltaPx, cellH float32) {
	if cellH <= 0 || math.IsNaN(float64(cellH)) || math.IsInf(float64(cellH), 0) ||
		math.IsNaN(float64(deltaPx)) || math.IsInf(float64(deltaPx), 0) {
		return
	}
	total := float64(g.ViewOffset)*float64(cellH) + float64(g.ViewSubPx) + float64(deltaPx)
	maxPx := float64(g.Scrollback.Len()) * float64(cellH)
	if total < 0 {
		total = 0
	} else if total > maxPx {
		total = maxPx
	}
	rows := int(total / float64(cellH))
	g.ViewOffset = rows
	g.ViewSubPx = float32(total - float64(rows)*float64(cellH))
}

// scrollTopTo puts contentRow at the top of the viewport. A row in the live
// region snaps to the live view, since the live rows are always the bottom
// of the content and cannot be scrolled above. Caller holds Mu.
func (g *grid) scrollTopTo(contentRow int) {
	sb := g.Scrollback.Len()
	if contentRow >= sb {
		g.ViewOffset = 0
	} else {
		g.ViewOffset = clamp(sb-contentRow, 0, sb)
	}
	g.ViewSubPx = 0
}

// ResetView snaps the viewport back to the live grid.
func (g *grid) ResetView() {
	g.ViewOffset = 0
	g.ViewSubPx = 0
}

// ScrollViewTop moves the viewport to the oldest scrollback row.
func (g *grid) ScrollViewTop() {
	g.ViewOffset = g.Scrollback.Len()
	g.ViewSubPx = 0
}

// SetViewFractional positions the viewport at a fractional row offset from
// the live bottom (0 = live, Scrollback.Len() = oldest). Whole rows go to
// ViewOffset; the fractional remainder, scaled by cellH, goes to ViewSubPx so
// the scrollbar thumb tracks the pointer sub-cell smoothly. Clamped to
// [0, Scrollback.Len()]. Non-finite off or cellH <= 0 is a no-op.
func (g *grid) SetViewFractional(off, cellH float32) {
	if cellH <= 0 || math.IsNaN(float64(cellH)) || math.IsInf(float64(cellH), 0) ||
		math.IsNaN(float64(off)) || math.IsInf(float64(off), 0) {
		return
	}
	maxOff := float32(g.Scrollback.Len())
	if off < 0 {
		off = 0
	} else if off > maxOff {
		off = maxOff
	}
	rows := int(off)
	g.ViewOffset = rows
	g.ViewSubPx = (off - float32(rows)) * cellH
}
