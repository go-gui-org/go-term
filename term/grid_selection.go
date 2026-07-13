package term

import "strings"

// selOrder returns the selection bounds in forward order (start <= end).
func (g *grid) selOrder() (start, end contentPos) {
	a, b := g.SelAnchor, g.SelHead
	if b.Row < a.Row || (b.Row == a.Row && b.Col < a.Col) {
		a, b = b, a
	}
	return a, b
}

// SelectedText extracts the selection as a UTF-8 string. Trailing
// blanks per row are trimmed; row breaks emit '\n' (kitty convention).
// Returns "" when nothing is selected. Column coordinates are cell
// *boundaries* (0..Cols) and the span is half-open [s.Col, e.Col), so a
// one-cell drag yields one cell. Coordinates are content-relative and are
// clamped so stale coords from a Resize never produce a negative span.
func (g *grid) SelectedText() string {
	if !g.SelActive || g.Rows <= 0 || g.Cols <= 0 {
		return ""
	}
	total := g.Scrollback.Len() + g.Rows
	s, e := g.selOrder()
	s.Row, s.Col = clamp(s.Row, 0, total-1), clamp(s.Col, 0, g.Cols)
	e.Row, e.Col = clamp(e.Row, 0, total-1), clamp(e.Col, 0, g.Cols)
	if s == e {
		return ""
	}
	var b strings.Builder
	b.Grow((e.Row-s.Row+1)*g.Cols + (e.Row - s.Row))
	for r := s.Row; r <= e.Row; r++ {
		// Half-open: c1 is the last cell index (boundary e.Col minus 1) on the
		// end row; full row width otherwise.
		c0, c1 := 0, g.Cols-1
		if r == s.Row {
			c0 = s.Col
		}
		if r == e.Row {
			c1 = e.Col - 1
		}

		end := c0 - 1
		for c := c0; c <= c1; c++ {
			if g.ContentCellAt(r, c).Ch != ' ' {
				end = c
			}
		}
		for c := c0; c <= end; c++ {
			cell := g.ContentCellAt(r, c)
			if cell.clusterID != 0 && int(cell.clusterID) < len(g.clusters) {
				b.WriteString(g.clusters[cell.clusterID])
			} else if cell.Ch != 0 {
				b.WriteRune(cell.Ch)
			}
		}
		if r < e.Row {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ClearSelection drops any active selection.
func (g *grid) ClearSelection() {
	g.SelActive = false
	g.SelAnchor = contentPos{}
	g.SelHead = contentPos{}
}
