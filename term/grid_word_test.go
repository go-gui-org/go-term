package term

import "testing"

// wordGrid builds a grid whose rows are the given strings, left-aligned and
// padded with blanks. A fresh grid holds Ch == 0 in untouched cells, which
// blankCellAt treats as blank, so nothing needs to fill the tail.
func wordGrid(cols int, rows ...string) *grid {
	g := newGrid(len(rows), cols)
	for r, s := range rows {
		for c, ch := range s {
			if c >= cols {
				break
			}
			g.At(r, c).Ch = ch
		}
	}
	return g
}

func TestWordFwd(t *testing.T) {
	g := wordGrid(20, "alpha beta  gamma")
	cases := []struct {
		name string
		from contentPos
		want contentPos
	}{
		{"word start to next word", contentPos{0, 0}, contentPos{0, 6}},
		{"mid word to next word", contentPos{0, 2}, contentPos{0, 6}},
		{"last cell of word", contentPos{0, 4}, contentPos{0, 6}},
		{"from blank", contentPos{0, 5}, contentPos{0, 6}},
		{"across a two-blank gap", contentPos{0, 6}, contentPos{0, 12}},
		// Nothing but blanks after "gamma": clamp to the last cell rather
		// than running off the buffer.
		{"no further word", contentPos{0, 12}, contentPos{0, 19}},
	}
	for _, tc := range cases {
		if got := g.wordFwd(tc.from); got != tc.want {
			t.Errorf("%s: wordFwd(%v) = %v, want %v", tc.name, tc.from, got, tc.want)
		}
	}
}

func TestWordBack(t *testing.T) {
	g := wordGrid(20, "alpha beta  gamma")
	cases := []struct {
		name string
		from contentPos
		want contentPos
	}{
		{"mid word to its start", contentPos{0, 8}, contentPos{0, 6}},
		{"word start to previous word", contentPos{0, 6}, contentPos{0, 0}},
		{"from blank to previous word", contentPos{0, 11}, contentPos{0, 6}},
		{"from trailing blanks", contentPos{0, 19}, contentPos{0, 12}},
		// Already at the buffer start: nowhere further back to go.
		{"buffer start", contentPos{0, 0}, contentPos{0, 0}},
	}
	for _, tc := range cases {
		if got := g.wordBack(tc.from); got != tc.want {
			t.Errorf("%s: wordBack(%v) = %v, want %v", tc.name, tc.from, got, tc.want)
		}
	}
}

// A hard line break (no autowrap flag) ends a word even when the next row
// begins with non-blanks.
func TestWordMotion_HardRowBreak(t *testing.T) {
	g := wordGrid(5, "abcde", "fghij")
	if got := g.wordFwd(contentPos{0, 0}); got != (contentPos{1, 0}) {
		t.Errorf("wordFwd across hard break = %v, want {1 0}", got)
	}
	if got := g.wordBack(contentPos{1, 3}); got != (contentPos{1, 0}) {
		t.Errorf("wordBack should stop at hard break, got %v, want {1 0}", got)
	}
}

// A soft-wrapped row continues the same logical word, so the motion must treat
// the two rows as one run.
func TestWordMotion_SoftWrapJoinsWord(t *testing.T) {
	g := wordGrid(5, "abcde", "fghij")
	g.RowWrapped[0] = true
	// One word spanning both rows, then nothing: wordFwd clamps to the end.
	if got := g.wordFwd(contentPos{0, 0}); got != (contentPos{1, 4}) {
		t.Errorf("wordFwd over soft wrap = %v, want {1 4}", got)
	}
	// From inside the second row, the word's start is on the first row.
	if got := g.wordBack(contentPos{1, 3}); got != (contentPos{0, 0}) {
		t.Errorf("wordBack over soft wrap = %v, want {0 0}", got)
	}
}

// Motion across a row boundary where the next row starts with blanks.
func TestWordFwd_SkipsBlankRow(t *testing.T) {
	g := wordGrid(6, "ab", "", "cd")
	if got := g.wordFwd(contentPos{0, 0}); got != (contentPos{2, 0}) {
		t.Errorf("wordFwd over blank row = %v, want {2 0}", got)
	}
}

// Degenerate geometry must not panic or loop. newGrid clamps dimensions to at
// least 1, so zero the field directly to reach the guard.
func TestWordMotion_ZeroCols(t *testing.T) {
	g := newGrid(2, 1)
	g.Cols = 0
	from := contentPos{0, 0}
	if got := g.wordFwd(from); got != from {
		t.Errorf("wordFwd on zero-col grid = %v, want %v", got, from)
	}
	if got := g.wordBack(from); got != from {
		t.Errorf("wordBack on zero-col grid = %v, want %v", got, from)
	}
}

// Scrollback rows participate: content coordinates span history + live grid.
func TestWordMotion_AcrossScrollback(t *testing.T) {
	g := newGrid(1, 6)
	g.ScrollbackCap = 4
	g.Scrollback.SetGeom(4, 6)
	row := make([]cell, 6)
	for i, ch := range "old   " {
		row[i] = cell{Ch: ch, Width: 1}
	}
	g.Scrollback.Push(row, false)
	for c, ch := range "new" {
		g.At(0, c).Ch = ch
	}
	// Content row 0 is the scrollback row, row 1 the live row.
	if got := g.wordFwd(contentPos{0, 0}); got != (contentPos{1, 0}) {
		t.Errorf("wordFwd into live grid = %v, want {1 0}", got)
	}
	if got := g.wordBack(contentPos{1, 0}); got != (contentPos{0, 0}) {
		t.Errorf("wordBack into scrollback = %v, want {0 0}", got)
	}
}

// A buffer of nothing but blanks must not send word motion scanning every cell
// in the scrollback: the scan is capped (maxWordScan) so one keypress costs
// bounded work on the GUI thread while holding Mu. The motion still returns a
// position inside the buffer.
func TestWordMotion_ScanBudget(t *testing.T) {
	// Deliberately larger than maxWordScan cells so the cap, not the buffer
	// end, is what stops the walk.
	const cols = 80
	rows := maxWordScan/cols + 50
	blank := make([]string, rows)
	g := wordGrid(cols, blank...)

	got := g.wordFwd(contentPos{})
	if got.Row < 0 || got.Row >= g.ContentRows() || got.Col < 0 || got.Col >= cols {
		t.Errorf("wordFwd left the buffer: %v", got)
	}
	// The cap, not the buffer end: a full walk would have reached the last row.
	if got.Row >= rows-1 {
		t.Errorf("wordFwd scanned to row %d; the budget should have stopped it earlier", got.Row)
	}

	last := contentPos{Row: rows - 1, Col: cols - 1}
	back := g.wordBack(last)
	if back.Row < 0 || back.Row >= g.ContentRows() || back.Col < 0 || back.Col >= cols {
		t.Errorf("wordBack left the buffer: %v", back)
	}
	if back.Row == 0 {
		t.Error("wordBack scanned to row 0; the budget should have stopped it earlier")
	}
}

// wordBoundsAt is what a double-click selects: the whole run under the
// pointer, in either direction, without a punctuation class — so a path or a
// --flag=value comes out intact.
func TestWordBoundsAt(t *testing.T) {
	g := wordGrid(30, "ls /usr/local/bin --all")
	cases := []struct {
		name               string
		at                 contentPos
		wantStart, wantEnd contentPos
	}{
		{"first word", contentPos{0, 1}, contentPos{0, 0}, contentPos{0, 1}},
		// The whole path, slashes included: no punctuation split.
		{"path from its middle", contentPos{0, 8}, contentPos{0, 3}, contentPos{0, 16}},
		{"path from its first cell", contentPos{0, 3}, contentPos{0, 3}, contentPos{0, 16}},
		{"path from its last cell", contentPos{0, 16}, contentPos{0, 3}, contentPos{0, 16}},
		// A flag keeps its leading dashes and its = value.
		{"flag", contentPos{0, 20}, contentPos{0, 18}, contentPos{0, 22}},
	}
	for _, tc := range cases {
		start, end := g.wordBoundsAt(tc.at)
		if start != tc.wantStart || end != tc.wantEnd {
			t.Errorf("%s: wordBoundsAt(%v) = %v..%v, want %v..%v",
				tc.name, tc.at, start, end, tc.wantStart, tc.wantEnd)
		}
	}
}

// Clicking whitespace selects the whitespace run rather than jumping to a
// neighbouring word, so the gesture is symmetric in both directions.
func TestWordBoundsAt_BlankRun(t *testing.T) {
	g := wordGrid(20, "ab    cd")
	start, end := g.wordBoundsAt(contentPos{0, 3})
	if start != (contentPos{0, 2}) || end != (contentPos{0, 5}) {
		t.Errorf("wordBoundsAt on blanks = %v..%v, want {0 2}..{0 5}", start, end)
	}
}

// A word broken at the right margin is one word, so a double-click on either
// half selects the whole thing — the property that makes double-clicking a
// wrapped path useful.
func TestWordBoundsAt_SoftWrap(t *testing.T) {
	g := wordGrid(5, "abcde", "fghij")
	g.RowWrapped[0] = true
	start, end := g.wordBoundsAt(contentPos{1, 2})
	if start != (contentPos{0, 0}) || end != (contentPos{1, 4}) {
		t.Errorf("wordBoundsAt over soft wrap = %v..%v, want {0 0}..{1 4}", start, end)
	}
}

// A hard break is a different logical line: the run must not continue across
// it even though both rows are full of non-blanks.
func TestWordBoundsAt_HardBreakStops(t *testing.T) {
	g := wordGrid(5, "abcde", "fghij") // RowWrapped[0] left false
	start, end := g.wordBoundsAt(contentPos{0, 2})
	if start != (contentPos{0, 0}) || end != (contentPos{0, 4}) {
		t.Errorf("wordBoundsAt across hard break = %v..%v, want {0 0}..{0 4}", start, end)
	}
}

// lineBoundsAt is what a triple-click selects: the logical line, however many
// screen rows the wrap flags say it occupies.
func TestLineBoundsAt(t *testing.T) {
	g := wordGrid(5, "aaaaa", "bbbbb", "ccccc", "ddddd")
	g.RowWrapped[1] = true // rows 1 and 2 are one logical line

	cases := []struct {
		name               string
		row                int
		wantStart, wantEnd int
	}{
		{"unwrapped row alone", 0, 0, 0},
		{"first row of a wrapped pair", 1, 1, 2},
		{"second row of a wrapped pair", 2, 1, 2},
		{"last row", 3, 3, 3},
	}
	for _, tc := range cases {
		start, end := g.lineBoundsAt(tc.row)
		if start != tc.wantStart || end != tc.wantEnd {
			t.Errorf("%s: lineBoundsAt(%d) = %d..%d, want %d..%d",
				tc.name, tc.row, start, end, tc.wantStart, tc.wantEnd)
		}
	}
}

// An out-of-range row must clamp rather than index past the buffer: the row
// comes from a pointer position that a concurrent resize can invalidate.
func TestLineBoundsAt_ClampsOutOfRange(t *testing.T) {
	g := wordGrid(5, "aaaaa", "bbbbb")
	if start, end := g.lineBoundsAt(999); start != 1 || end != 1 {
		t.Errorf("lineBoundsAt(999) = %d..%d, want 1..1", start, end)
	}
	if start, end := g.lineBoundsAt(-5); start != 0 || end != 0 {
		t.Errorf("lineBoundsAt(-5) = %d..%d, want 0..0", start, end)
	}
}

// A run of rows all flagged wrapped must not walk the whole buffer under Mu.
func TestLineBoundsAt_ScanBudget(t *testing.T) {
	rows := maxLineScanRows + 50
	blank := make([]string, rows)
	g := wordGrid(5, blank...)
	for i := range g.RowWrapped {
		g.RowWrapped[i] = true
	}
	mid := rows / 2
	start, end := g.lineBoundsAt(mid)
	if start < 0 || end >= rows {
		t.Fatalf("lineBoundsAt left the buffer: %d..%d", start, end)
	}
	if mid-start > maxLineScanRows || end-mid > maxLineScanRows {
		t.Errorf("lineBoundsAt(%d) = %d..%d, exceeds the %d-row budget",
			mid, start, end, maxLineScanRows)
	}
}

// On the alt screen, lineBoundsAt must not follow a wrap flag across the
// scrollback-live boundary into rows belonging to the main screen.
func TestLineBoundsAt_AltScreenBoundary(t *testing.T) {
	g := newGrid(2, 10)
	g.ScrollbackCap = 5
	sbRow := make([]cell, 10)
	for i := range sbRow {
		sbRow[i] = cell{Ch: 'x', Width: 1}
	}
	g.Scrollback.SetGeom(5, 10)
	for range 3 {
		g.Scrollback.Push(sbRow, true) // pushed with wrapped=true
	}
	// Content layout: rows 0..2 are scrollback, rows 3..4 are alt screen.
	g.AltActive = true
	// Both alt-screen rows tagged so the forward walk reaches the second row.
	g.RowWrapped[0] = true

	// Query the *second* alt-screen row. The backward walk must stop at lo=3
	// (the boundary) rather than continue into scrollback wrapped flags.
	start, end := g.lineBoundsAt(4)
	if start != 3 || end != 4 {
		t.Errorf("AltActive: lineBoundsAt(4) = %d..%d, want 3..4 (bounded at lo)",
			start, end)
	}
}
