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
