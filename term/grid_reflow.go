package term

// reflowBuffer copies src (oldRows×oldCols) into a freshly allocated
// newRows×newCols buffer, preserving the top-left intersection and
// padding the rest with default cells. Used by Resize for both the
// active cell buffer and (when alt-active) the saved main buffer.
func reflowBuffer(src []cell, oldRows, oldCols, newRows, newCols int) []cell {
	next := make([]cell, newRows*newCols)
	if len(src) == 0 || oldRows <= 0 || oldCols <= 0 {
		for i := range next {
			next[i] = defaultCell()
		}
		return next
	}
	rcopy := min(newRows, oldRows)
	ccopy := min(newCols, oldCols)
	// Copy the overlap first so those cells never need defaultCell init.
	for r := range rcopy {
		copy(next[r*newCols:r*newCols+ccopy], src[r*oldCols:r*oldCols+ccopy])
	}
	// Initialize only cells not covered by the copy above.
	// Right strip within copy rows (columns ccopy..newCols-1):
	for r := range rcopy {
		for c := ccopy; c < newCols; c++ {
			next[r*newCols+c] = defaultCell()
		}
	}
	// Rows beyond the copy region (rows rcopy..newRows-1):
	for i := rcopy * newCols; i < len(next); i++ {
		next[i] = defaultCell()
	}
	return next
}

// physRow is used internally by the logical reflow pipeline.
// wrapped == true means this row ended with an autowrap and the next
// row is its soft-wrapped continuation.
type physRow struct {
	cells   []cell
	wrapped bool
}

// isDefaultBlank reports whether c is an untouched default blank cell —
// i.e., no content was ever written to it. Used by logicalReflow to trim
// trailing padding from the last physical row of a logical line.
func isDefaultBlank(c cell) bool {
	return c.Ch == ' ' && c.FG == DefaultColor && c.BG == DefaultColor &&
		c.Attrs == 0 && c.Width == 1 && c.LinkID == 0 && c.ULStyle == 0
}

// rowArena carves fixed-width row slices from lazy-allocated flat blocks
// so reflow does not pay one heap allocation per physical row. Blocks are
// 256 rows each; growth never copies, so rows carved from earlier blocks
// remain valid after the arena grows.
type rowArena struct {
	buf  []cell
	off  int
	rowW int
}

// next returns a zero-length row slice whose capacity is exactly rowW
// (three-index slice), so append can fill it but never bleed into the
// next row. Returns a nil slice when rowW <= 0 (defense-in-depth; all
// upstream callers pass positive widths).
func (a *rowArena) next() []cell {
	if a.rowW <= 0 {
		return nil
	}
	if a.off+a.rowW > len(a.buf) {
		a.buf = make([]cell, a.rowW*256)
		a.off = 0
	}
	row := a.buf[a.off : a.off : a.off+a.rowW]
	a.off += a.rowW
	return row
}

// rewrapLine re-wraps a flat slice of cells (the content of one logical
// line, with continuation cells already stripped) into physical rows of
// newCols columns. All rows except the last are marked wrapped=true.
// An empty input produces a single blank row.
// Rows are carved from arena to avoid per-row heap allocations.
func rewrapLine(cells []cell, newCols int, arena *rowArena) []physRow {
	if len(cells) == 0 {
		blank := arena.next()
		for len(blank) < newCols {
			blank = append(blank, defaultCell())
		}
		return []physRow{{cells: blank, wrapped: false}}
	}

	var rows []physRow
	cur := arena.next()

	for i := 0; i < len(cells); {
		c := cells[i]

		if c.Width == 0 && c.Ch == 0 {
			i++
			continue
		}
		w := 1
		if c.Width == 2 {
			w = 2
		}

		if len(cur)+w > newCols {

			for len(cur) < newCols {
				cur = append(cur, defaultCell())
			}
			rows = append(rows, physRow{cells: cur, wrapped: true})
			cur = arena.next()
		}
		cur = append(cur, c)
		if w == 2 {
			cur = append(cur, c.continuation())
		}
		i++
	}

	for len(cur) < newCols {
		cur = append(cur, defaultCell())
	}
	rows = append(rows, physRow{cells: cur, wrapped: false})
	return rows
}

// maxReflowRows caps the total (scrollback + live) row count processed
// by logicalReflow so a runaway input cannot trigger a massive allocation.
// The upstream MaxScrollbackCap + MaxGridDim already bounds this to ~101K;
// this constant is defense-in-depth for the internal struct-param path.
const maxReflowRows = 200_000

// reflowConfig holds the parameters for logicalReflow so call sites
// are readable without positional-parameter noise.
type reflowConfig struct {
	cells         []cell
	rowWrapped    []bool
	scrollback    [][]cell
	sbWrapped     []bool
	oldRows       int
	oldCols       int
	newRows       int
	newCols       int
	cursorR       int
	cursorC       int
	scrollbackCap int
}

// reflowResult holds the output of logicalReflow.
type reflowResult struct {
	cells      []cell
	rowWrapped []bool
	scrollback [][]cell
	sbWrapped  []bool
	cursorR    int
	cursorC    int
}

// logicalReflow joins soft-wrapped physical rows into logical lines,
// re-wraps them at newCols, and returns the new cell buffer, wrap flags,
// scrollback, and cursor position. Hard newlines (wrapped==false) are
// never joined across.
//
// Parameters:
//   - cells/rowWrapped: live cell buffer and per-row wrap flags (oldRows×oldCols)
//   - scrollback/sbWrapped: scrollback ring and its wrap flags
//   - oldRows, oldCols: current grid dims
//   - newRows, newCols: target dims
//   - cursorR, cursorC: cursor in the live buffer
//   - scrollbackCap: maximum scrollback rows (0 = unlimited trim handled by caller)
func logicalReflow(cfg reflowConfig) reflowResult {
	cells := cfg.cells
	rowWrapped := cfg.rowWrapped
	scrollback := cfg.scrollback
	sbWrapped := cfg.sbWrapped
	oldRows, oldCols := cfg.oldRows, cfg.oldCols
	newRows, newCols := cfg.newRows, cfg.newCols
	cursorR, cursorC := cfg.cursorR, cfg.cursorC
	scrollbackCap := cfg.scrollbackCap

	// Defensive: clamp dims to >=1 so division and allocation
	// arithmetic never hits zero or negative. Callers already clamp
	// via clampDim; this is defense-in-depth for the struct-param path.
	if newCols < 1 {
		newCols = 1
	}
	if newRows < 1 {
		newRows = 1
	}
	if oldCols < 1 {
		oldCols = 1
	}
	if oldRows < 0 {
		oldRows = 0
	}

	var (
		newCells      []cell
		newRowWrapped []bool
		newScrollback [][]cell
		newSbWrapped  []bool
		newCursorR    int
		newCursorC    int
	)

	nSB := len(scrollback)
	total := nSB + oldRows
	// Cap total so a runaway scrollback count doesn't trigger a
	// massive physRow allocation. MaxScrollbackCap + MaxGridDim
	// already bounds this to ~101K; the cap here is belt-and-suspenders.
	if total > maxReflowRows {
		total = maxReflowRows
		if nSB > maxReflowRows {
			nSB = maxReflowRows
			scrollback = scrollback[len(scrollback)-nSB:]
		}
		oldRows = total - nSB
		cells = cells[len(cells)-oldRows*oldCols:]
	}
	phys := make([]physRow, total)
	for i, row := range scrollback {
		w := false
		if i < len(sbWrapped) {
			w = sbWrapped[i]
		}
		phys[i] = physRow{cells: row, wrapped: w}
	}
	for r := 0; r < oldRows; r++ {
		row := cells[r*oldCols : (r+1)*oldCols] // slice into live buffer; safe under Mu
		w := false
		if r < len(rowWrapped) {
			w = rowWrapped[r]
		}
		phys[nSB+r] = physRow{cells: row, wrapped: w}
	}

	cursorPhys := nSB + clamp(cursorR, 0, oldRows-1)

	// --- Identify logical lines and the one containing the cursor ---
	type logLine struct {
		start, end int // inclusive indices into phys[]
	}
	var lines []logLine
	lineStart := 0
	cursorLineIdx := 0
	cursorLineFound := false
	for i, pr := range phys {
		if !pr.wrapped {
			ll := logLine{lineStart, i}
			if !cursorLineFound && cursorPhys >= lineStart && cursorPhys <= i {
				cursorLineIdx = len(lines)
				cursorLineFound = true
			}
			lines = append(lines, ll)
			lineStart = i + 1
		}
	}

	if lineStart < len(phys) {
		if !cursorLineFound && cursorPhys >= lineStart {
			cursorLineIdx = len(lines)
			cursorLineFound = true
		}
		lines = append(lines, logLine{lineStart, len(phys) - 1})
	}
	if !cursorLineFound && len(lines) > 0 {
		cursorLineIdx = len(lines) - 1
	}

	// Cursor's display-column offset within its logical line.
	// Each preceding wrapped physical row contributes oldCols columns.
	// A pending-wrap cursor sits one column past the right margin after a
	// glyph was written in the last cell; keep it anchored to that last
	// cell instead of treating it as content beyond the row.
	var cursorLogCol int
	if len(lines) > 0 && cursorLineIdx < len(lines) {
		ll := lines[cursorLineIdx]
		effectiveCursorC := clamp(cursorC, 0, oldCols-1)
		cursorLogCol = (cursorPhys-ll.start)*oldCols + effectiveCursorC
	}

	// --- Re-wrap all logical lines ---
	// Capacity estimate: each logical line produces at least one physical
	// row at newCols; long lines produce oldCols/newCols+1 rows. The
	// ceiling avoids overallocation from wrapping every row individually.
	estRows := max(len(lines)+(nSB*oldCols+oldRows*oldCols)/newCols, total)
	// Cap the capacity hint so a degenerate newCols=1 + wide oldCols
	// combination can't trigger a multi-GB pre-allocation. The slice
	// will grow as needed; the cap only skips the upfront reservation.
	if estRows > maxReflowRows {
		estRows = maxReflowRows
	}
	allNew := make([]physRow, 0, estRows)
	cursorNewPhysStart := 0
	var cursorLineRewrapped []physRow

	// lineCells is reused across logical lines to avoid per-line allocation.
	var lineCells []cell

	// Transient arena for rewrapLine: rows are carved from flat blocks
	// instead of allocated individually.
	arena := rowArena{rowW: newCols}

	for li, ll := range lines {
		// Collect cells for this logical line. Trim trailing default
		// blanks from the last physical row to avoid padding from creating
		// spurious extra physical rows after re-wrap. Only preserve cells
		// up to and including the cursor column when the cursor is within
		// the row bounds (cursorC < len(row)). When cursorC >= len(row)
		// (pending-wrap state past the right margin), don't preserve blanks
		// — the cursor position will be clamped to the rewrapped line's end.
		lineCells = lineCells[:0]
		for pi := ll.start; pi <= ll.end; pi++ {
			row := phys[pi].cells
			trimTo := len(row)
			if pi < ll.end && phys[pi].wrapped {

				next := phys[pi+1].cells
				if len(next) > 0 && next[0].Width == 2 {
					for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
						trimTo--
					}
				}
			}
			if pi == ll.end {
				for trimTo > 0 && isDefaultBlank(row[trimTo-1]) {
					trimTo--
				}

				if pi == cursorPhys && cursorC < len(row) && cursorC+1 > trimTo {
					trimTo = cursorC + 1
				}
			}
			lineCells = append(lineCells, row[:trimTo]...)
		}

		rewrapped := rewrapLine(lineCells, newCols, &arena)
		if li == cursorLineIdx {
			cursorNewPhysStart = len(allNew)
			cursorLineRewrapped = rewrapped
		}
		allNew = append(allNew, rewrapped...)

		if li < cursorLineIdx {
			capRows := max(newRows+scrollbackCap, newRows*2)
			if len(allNew) > capRows {
				allNew = allNew[len(allNew)-capRows:]
			}
		}
	}

	rowOffset := 0
	colOffset := 0
	if newCols > 0 && len(cursorLineRewrapped) > 0 {
		maxLogCol := max(len(cursorLineRewrapped)*newCols-1, 0)
		effective := min(cursorLogCol, maxLogCol)
		rowOffset = effective / newCols
		colOffset = effective % newCols
		if rowOffset >= len(cursorLineRewrapped) {
			rowOffset = len(cursorLineRewrapped) - 1
		}
	}
	newCursorPhys := cursorNewPhysStart + rowOffset

	maxStart := max(len(allNew)-newRows, 0)
	liveStart := min(newCursorPhys-(newRows-1), maxStart)
	if liveStart < 0 {
		liveStart = 0
	}

	newScrollback = make([][]cell, 0, liveStart)
	newSbWrapped = make([]bool, 0, liveStart)
	for _, pr := range allNew[:liveStart] {
		newScrollback = append(newScrollback, pr.cells)
		newSbWrapped = append(newSbWrapped, pr.wrapped)
	}
	if scrollbackCap > 0 && len(newScrollback) > scrollbackCap {
		trim := len(newScrollback) - scrollbackCap
		newScrollback = newScrollback[trim:]
		newSbWrapped = newSbWrapped[trim:]
	}

	newCells = make([]cell, newRows*newCols)
	for i := range newCells {
		newCells[i] = defaultCell()
	}
	newRowWrapped = make([]bool, newRows)
	liveRows := allNew[liveStart:]
	for r, pr := range liveRows {
		if r >= newRows {
			break
		}
		copy(newCells[r*newCols:(r+1)*newCols], pr.cells)
		newRowWrapped[r] = pr.wrapped
	}

	newCursorR = newCursorPhys - liveStart
	newCursorC = colOffset
	if newCursorR < 0 {
		newCursorR = 0
	}
	if newCursorR >= newRows {
		newCursorR = newRows - 1
	}
	if newCursorC < 0 {
		newCursorC = 0
	}
	if newCursorC >= newCols {
		newCursorC = newCols - 1
	}
	return reflowResult{
		cells:      newCells,
		rowWrapped: newRowWrapped,
		scrollback: newScrollback,
		sbWrapped:  newSbWrapped,
		cursorR:    newCursorR,
		cursorC:    newCursorC,
	}
}

// Resize reflows to new dims using logical line wrapping. Rows that ended
// with an autowrap (RowWrapped[r]==true) are joined with their successor
// into a single logical line and re-wrapped at the new column width, so
// terminal output reflowed like a modern terminal instead of cropping.
// Cursor position is tracked through the reflow. Rows separated by an
// explicit newline (RowWrapped[r]==false) are never joined.
//
// When alt-screen is active the alt buffer is reflowed with simple
// crop/pad (full-screen apps control every cell), while the saved main
// buffer receives logical reflow.
//
// The scroll region is reset after resize; apps re-issue DECSTBM after
// SIGWINCH. Selection is dropped. ViewOffset is reset to the live view.
func (g *grid) Resize(rows, cols int) {
	rows = clampDim(rows)
	cols = clampDim(cols)
	if rows == g.Rows && cols == g.Cols {
		return
	}

	oldSbLen := g.Scrollback.Len()

	sbRows := make([][]cell, oldSbLen)
	sbWrap := make([]bool, oldSbLen)
	for i := range oldSbLen {
		sbRows[i] = g.Scrollback.Row(i) // aliases backing buffer; safe under Mu
		sbWrap[i] = g.Scrollback.Wrapped(i)
	}

	if g.AltActive {

		g.Cells = reflowBuffer(g.Cells, g.Rows, g.Cols, rows, cols)
		newRW := make([]bool, rows)
		copy(newRW, g.RowWrapped)
		g.RowWrapped = newRW

		if len(g.mainSaved.cells) == g.Rows*g.Cols {
			savedRW := g.mainSaved.rowWrapped
			if len(savedRW) != g.Rows {
				savedRW = make([]bool, g.Rows)
			}
			res := logicalReflow(reflowConfig{
				cells:         g.mainSaved.cells,
				rowWrapped:    savedRW,
				scrollback:    sbRows,
				sbWrapped:     sbWrap,
				oldRows:       g.Rows,
				oldCols:       g.Cols,
				newRows:       rows,
				newCols:       cols,
				cursorR:       g.mainSaved.cursorR,
				cursorC:       g.mainSaved.cursorC,
				scrollbackCap: g.ScrollbackCap,
			})
			g.mainSaved.cells = res.cells
			g.mainSaved.rowWrapped = res.rowWrapped
			g.repopulateScrollback(res.scrollback, res.sbWrapped, cols)
			g.mainSaved.cursorR = res.cursorR
			g.mainSaved.cursorC = res.cursorC
			g.mainSaved.top = 0
			g.mainSaved.bottom = rows - 1
		}
	} else {
		res := logicalReflow(reflowConfig{
			cells:         g.Cells,
			rowWrapped:    g.RowWrapped,
			scrollback:    sbRows,
			sbWrapped:     sbWrap,
			oldRows:       g.Rows,
			oldCols:       g.Cols,
			newRows:       rows,
			newCols:       cols,
			cursorR:       g.CursorR,
			cursorC:       g.CursorC,
			scrollbackCap: g.ScrollbackCap,
		})
		g.Cells = res.cells
		g.RowWrapped = res.rowWrapped
		g.repopulateScrollback(res.scrollback, res.sbWrapped, cols)
		g.CursorR = res.cursorR
		g.CursorC = res.cursorC
	}

	g.Rows = rows
	g.Cols = cols
	g.Dirty = make([]bool, rows)
	g.markAllDirty()

	g.Top = 0
	g.Bottom = rows - 1
	g.ViewOffset = 0
	g.ViewSubPx = 0

	if g.SelActive {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.SelAnchor.Row = clamp(g.SelAnchor.Row+delta, 0, total-1)
		g.SelHead.Row = clamp(g.SelHead.Row+delta, 0, total-1)
	}

	if len(g.Marks) > 0 {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.shiftMarks(delta, total)
	}

	if len(g.Graphics) > 0 {
		delta := g.Scrollback.Len() - oldSbLen
		total := g.Scrollback.Len() + rows
		g.shiftGraphics(delta, total)
	}
}

// repopulateScrollback resets the ring to (ScrollbackCap, cols) and
// pushes the freshly reflowed rows back in oldest-first. Used by Resize
// at the reflow boundary so a single backing allocation replaces the
// per-row slices reflow produced.
func (g *grid) repopulateScrollback(rows [][]cell, wrapped []bool, cols int) {
	g.Scrollback.SetGeom(g.ScrollbackCap, cols)
	for i, row := range rows {
		w := false
		if i < len(wrapped) {
			w = wrapped[i]
		}
		g.Scrollback.Push(row, w)
	}
}
