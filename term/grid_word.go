package term

// Word motion over content coordinates, for copy mode's w / b keys.
//
// Pure grid code — no go-gui, no widget state — so it is unit-testable without
// a window. Semantics follow vim's plain `w` / `b` (not `W` / `B`): a word is a
// maximal run of non-blank cells, and blanks separate words. Unlike vim there
// is no punctuation/word-character split; a terminal buffer is full of paths,
// flags, and URLs, and breaking those apart at every '/' or '-' makes the
// motion useless for the thing copy mode exists to do.
//
// Rows are walked as one logical line while the autowrap flag says they
// continue (rowWrapped, shared with URL detection), so a path broken at the
// right margin is one word.

// maxWordScan caps how many cells a single word motion will walk. Without it,
// pressing 'w' on a buffer of blanks scans the entire scrollback — tens of
// millions of cells on the GUI thread, holding Mu, for one keystroke. The cap
// is far larger than any real gap between words (~300 rows at 200 columns), so
// it never truncates a legitimate motion; on exhaustion the motion simply stops
// where it got to, which is still a valid, clamped position.
const maxWordScan = 1 << 16

// maxLineScanRows bounds how far lineBoundsAt follows autowrap flags in each
// direction. Deliberately far larger than maxURLScanRows (which only has to
// cover a URL): a triple-click should select a whole pasted paragraph, and at
// 80 columns 1024 rows is ~80 KB of one logical line. The cap exists only so a
// buffer of rows all flagged wrapped cannot walk the entire scrollback under
// Mu on the GUI thread.
const maxLineScanRows = 1024

// blankCellAt reports whether the cell at (row, col) counts as whitespace for
// word motion. Cells never written hold Ch == 0; erased ones hold ' '. Both are
// blanks. Caller holds Mu.
func (g *grid) blankCellAt(row, col int) bool {
	ch := g.ContentCellAt(row, col).Ch
	return ch == 0 || ch == ' ' || ch == '\t'
}

// stepFwd advances one cell, wrapping to the next content row at the right
// margin. Returns ok=false at the end of the buffer. Caller holds Mu.
func (g *grid) stepFwd(p contentPos) (contentPos, bool) {
	if g.Cols <= 0 {
		return p, false
	}
	if p.Col+1 < g.Cols {
		p.Col++
		return p, true
	}
	if p.Row+1 >= g.ContentRows() {
		return p, false
	}
	return contentPos{Row: p.Row + 1}, true
}

// stepBack retreats one cell, wrapping to the end of the previous content row
// at the left margin. Returns ok=false at the start of the buffer. Caller
// holds Mu.
func (g *grid) stepBack(p contentPos) (contentPos, bool) {
	if g.Cols <= 0 {
		return p, false
	}
	if p.Col > 0 {
		p.Col--
		return p, true
	}
	if p.Row == 0 {
		return p, false
	}
	return contentPos{Row: p.Row - 1, Col: g.Cols - 1}, true
}

// rowBreakFwd reports whether moving from p to the next row crosses a hard
// line break — i.e. p is on the last column of a row that did *not* soft-wrap.
// Word motion stops at hard breaks the way vim's w stops at end of line only
// when the next line starts a new word; here it simply means the run of
// non-blanks cannot continue across the boundary. Caller holds Mu.
func (g *grid) rowBreakFwd(p contentPos) bool {
	return p.Col == g.Cols-1 && !g.rowWrapped(p.Row)
}

// wordFwd returns the position of the start of the next word after p, vim `w`.
// From inside a word it skips the rest of that word, then any blanks. Stops at
// the last cell of the buffer when no further word exists, so the motion is
// always well defined and never leaves the buffer. Caller holds Mu.
func (g *grid) wordFwd(p contentPos) contentPos {
	if g.Cols <= 0 || g.ContentRows() <= 0 {
		return p
	}
	cur := p
	budget := maxWordScan
	// Skip the remainder of the word under the cursor. A hard row break ends
	// the word even when the next row starts with non-blanks.
	for budget > 0 && !g.blankCellAt(cur.Row, cur.Col) {
		if g.rowBreakFwd(cur) {
			break
		}
		next, ok := g.stepFwd(cur)
		if !ok {
			return cur // end of buffer, still inside a word
		}
		cur = next
		budget--
	}
	// Skip blanks to land on the next word's first cell.
	for budget > 0 {
		next, ok := g.stepFwd(cur)
		if !ok {
			return cur // end of buffer, no further word
		}
		cur = next
		budget--
		if !g.blankCellAt(cur.Row, cur.Col) {
			return cur
		}
	}
	return cur // scan budget exhausted; see maxWordScan
}

// wordBoundsAt returns the inclusive cell bounds of the run containing p — the
// unit a double-click selects. Uses the same blank/non-blank split and the same
// wrap-following as wordFwd/wordBack, so a path broken at the right margin is
// one word and double-clicking a URL or a --flag=value grabs all of it.
//
// When p sits on a blank the blank run is returned instead, which keeps the
// gesture symmetric: a double-click in the gutter selects the gutter rather
// than silently jumping to a neighbouring word. Caller holds Mu.
func (g *grid) wordBoundsAt(p contentPos) (start, end contentPos) {
	if g.Cols <= 0 || g.ContentRows() <= 0 {
		return p, p
	}
	// Pick the predicate from the cell under the pointer, then expand while
	// neighbours agree with it.
	blank := g.blankCellAt(p.Row, p.Col)
	start, end = p, p

	budget := maxWordScan
	for budget > 0 {
		prev, ok := g.stepBack(start)
		if !ok || g.blankCellAt(prev.Row, prev.Col) != blank {
			break
		}
		// Crossing back into a row that did not soft-wrap ends the unit: the
		// rows are separate logical lines that happen to be adjacent.
		if prev.Row != start.Row && !g.rowWrapped(prev.Row) {
			break
		}
		start = prev
		budget--
	}
	for budget > 0 {
		if g.rowBreakFwd(end) {
			break
		}
		next, ok := g.stepFwd(end)
		if !ok || g.blankCellAt(next.Row, next.Col) != blank {
			break
		}
		end = next
		budget--
	}
	return start, end
}

// lineBoundsAt returns the first and last content rows of the logical line
// containing row — the unit a triple-click selects. Soft-wrapped rows are
// followed in both directions, so one logical line is selected whole however
// many screen rows it occupies.
//
// On the alt screen the walk will not cross into scrollback: the alt buffer
// keeps its own wrap flags and the main-screen history sitting below it is
// unrelated content. Same rule detectURLAt follows. Caller holds Mu.
func (g *grid) lineBoundsAt(row int) (startRow, endRow int) {
	total := g.ContentRows()
	if total <= 0 {
		return 0, 0
	}
	row = clamp(row, 0, total-1)
	lo := 0
	if g.AltActive {
		lo = g.Scrollback.Len()
	}
	startRow, endRow = row, row
	for startRow > lo && startRow > row-maxLineScanRows && g.rowWrapped(startRow-1) {
		startRow--
	}
	for endRow < total-1 && endRow < row+maxLineScanRows && g.rowWrapped(endRow) {
		endRow++
	}
	return startRow, endRow
}

// wordBack returns the position of the start of the word at or before p, vim
// `b`. From inside a word it goes to that word's first cell; from the first
// cell (or from a blank) it goes to the previous word's first cell. Stops at
// the buffer start. Caller holds Mu.
func (g *grid) wordBack(p contentPos) contentPos {
	if g.Cols <= 0 || g.ContentRows() <= 0 {
		return p
	}
	cur := p
	// Step back one first: standing on a word's first cell must move to the
	// previous word, not stay put.
	prev, ok := g.stepBack(cur)
	if !ok {
		return cur
	}
	cur = prev
	budget := maxWordScan
	// Skip blanks backwards onto the previous word's last cell.
	for budget > 0 && g.blankCellAt(cur.Row, cur.Col) {
		prev, ok := g.stepBack(cur)
		if !ok {
			return cur // buffer start, nothing but blanks behind
		}
		cur = prev
		budget--
	}
	// Walk to that word's first cell, stopping at a hard row break.
	for budget > 0 {
		prev, ok := g.stepBack(cur)
		if !ok {
			return cur
		}
		if g.blankCellAt(prev.Row, prev.Col) {
			return cur
		}
		// Crossing back into a row that didn't soft-wrap ends the word.
		if prev.Row != cur.Row && !g.rowWrapped(prev.Row) {
			return cur
		}
		cur = prev
		budget--
	}
	return cur // scan budget exhausted; see maxWordScan
}
