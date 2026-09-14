package term

import "math"

// row returns the cells of screen row r (0 <= r < Rows). The slice is capped
// at Cols so an append can never spill into the next slot. Caller holds Mu.
func (g *grid) row(r int) []cell {
	o := int(g.rowMap[r]) * g.Cols
	return g.Cells[o : o+g.Cols : o+g.Cols]
}

// resetRowMap makes rowMap the identity for the current Rows. Call it after
// Cells is replaced by a row-major buffer (newGrid, ExitAlt, Resize).
func (g *grid) resetRowMap() {
	if cap(g.rowMap) < g.Rows {
		g.rowMap = make([]int32, g.Rows)
		g.rowTmp = make([]int32, g.Rows)
	}
	g.rowMap = g.rowMap[:g.Rows]
	g.rowTmp = g.rowTmp[:g.Rows]
	for i := range g.rowMap {
		g.rowMap[i] = int32(i)
	}
}

// linearize rewrites Cells into row-major order and resets rowMap to the
// identity. It is a no-op when the map is already the identity, which is the
// common case outside heavy scrolling. The spare buffer is swapped, not
// copied back, so this costs one screen copy and no allocation after the
// first call. Caller holds Mu.
func (g *grid) linearize() {
	identity := true
	for i, s := range g.rowMap {
		if int(s) != i {
			identity = false
			break
		}
	}
	if identity {
		return
	}
	n := g.Rows * g.Cols
	if cap(g.linearBuf) < n {
		g.linearBuf = make([]cell, n)
	}
	buf := g.linearBuf[:n]
	for r := range g.Rows {
		copy(buf[r*g.Cols:(r+1)*g.Cols], g.row(r))
	}
	g.Cells, g.linearBuf = buf, g.Cells
	g.resetRowMap()
}

// rotateRowsUp moves screen rows [top+n..bottom] to [top..bottom-n] by
// rotating rowMap. The n slots that fell off the top land at
// [bottom-n+1..bottom] still holding their old cells; the caller blanks
// them. Requires 0 < n <= bottom-top+1. RowWrapped is not touched.
func (g *grid) rotateRowsUp(top, bottom, n int) {
	tmp := g.rowTmp[:n]
	copy(tmp, g.rowMap[top:top+n])
	copy(g.rowMap[top:], g.rowMap[top+n:bottom+1])
	copy(g.rowMap[bottom+1-n:bottom+1], tmp)
}

// rotateRowsDown is the mirror of rotateRowsUp: rows [top..bottom-n] move to
// [top+n..bottom], and the n slots pushed off the bottom land at
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
		evicted := 0
		for r := 0; r < n; r++ {
			src := g.row(g.Top + r)
			if g.Scrollback.Push(src, g.RowWrapped[g.Top+r]) {
				evicted++
			}
		}
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
	if cellH <= 0 || math.IsNaN(float64(cellH)) || math.IsInf(float64(cellH), 0) || math.IsNaN(float64(deltaPx)) {
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
	if cellH <= 0 || math.IsNaN(float64(off)) || math.IsInf(float64(off), 0) {
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
