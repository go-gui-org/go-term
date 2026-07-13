package term

import "math"

// scrollUpRegion shifts rows [Top..Bottom] up by n, clearing the bottom
// n rows of the region with default cells. When the region spans the
// full screen and ScrollbackCap > 0, the displaced top rows are pushed
// to the scrollback ring (oldest first) and trimmed to cap. n is
// clamped: n <= 0 is a no-op, n >= region height clears the region.
func (g *grid) scrollUpRegion(n int) {
	if n <= 0 || !g.regionValid() {
		return
	}
	height := g.Bottom - g.Top + 1
	if n > height {
		n = height
	}
	full := g.regionFullScreen()
	if full && g.ScrollbackCap > 0 && !g.AltActive {
		g.Scrollback.EnsureGeom(g.ScrollbackCap, g.Cols)
		evicted := 0
		for r := 0; r < n; r++ {
			src := g.Cells[(g.Top+r)*g.Cols : (g.Top+r+1)*g.Cols]
			if g.Scrollback.Push(src, g.RowWrapped[g.Top+r]) {
				evicted++
			}
		}
		if evicted > 0 {
			g.trimMarks(evicted)
			g.trimGraphics(evicted)
		}
	}

	if n < height {
		copy(
			g.Cells[g.Top*g.Cols:(g.Bottom+1)*g.Cols],
			g.Cells[(g.Top+n)*g.Cols:(g.Bottom+1)*g.Cols],
		)
		copy(g.RowWrapped[g.Top:g.Bottom+1-n], g.RowWrapped[g.Top+n:g.Bottom+1])
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Bottom + 1 - n; r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
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
	if n < height {

		for r := g.Bottom; r >= g.Top+n; r-- {
			copy(
				g.Cells[r*g.Cols:(r+1)*g.Cols],
				g.Cells[(r-n)*g.Cols:(r-n+1)*g.Cols],
			)
			g.RowWrapped[r] = g.RowWrapped[r-n]
		}
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	for r := g.Top; r < g.Top+n && r <= g.Bottom; r++ {
		row := g.Cells[r*g.Cols : (r+1)*g.Cols]
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
