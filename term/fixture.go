package term

import (
	"encoding/base64"
)

// Fixture is a replay-test scenario used by the emulator conformance
// test harness and the script2fixture CLI tool. It is public so that
// external test packages (term_test) can use it, but it is not part of
// the widget's public API — embedders should not depend on it.
//
// Input bytes are base64-encoded so control characters survive any text
// editor round-trip.
type Fixture struct {
	Name      string   `json:"name"`
	InputB64  string   `json:"input_b64"`
	WantTitle string   `json:"want_title,omitempty"`
	WantCwd   string   `json:"want_cwd,omitempty"`
	WantLines []string `json:"want_lines"`
	Rows      int      `json:"rows"`
	Cols      int      `json:"cols"`
	WantRow   int      `json:"want_row"`
	WantCol   int      `json:"want_col"`
}

// gridLines returns the grid's visible lines as equal-length strings, one
// per row. NUL cells are rendered as spaces.
func gridLines(g *grid) []string {
	lines := make([]string, g.Rows)
	for r := range g.Rows {
		row := make([]rune, g.Cols)
		for c := range g.Cols {
			ch := g.At(r, c).Ch
			if ch == 0 {
				ch = ' '
			}
			row[c] = ch
		}
		lines[r] = string(row)
	}
	return lines
}

// CaptureFixture feeds raw terminal bytes through a fresh Grid+Parser and
// returns a Fixture representing the final state. This is test
// infrastructure — used by the script2fixture CLI tool and the
// fixture_capture test helper — and is not part of the widget's public API.
func CaptureFixture(name string, rows, cols int, input []byte) Fixture {
	g := newGrid(rows, cols)
	p := newParser(g)
	var gotTitle string
	p.SetTitleHandler(func(s string) { gotTitle = s })

	g.Mu.Lock()
	p.Feed(input)
	g.Mu.Unlock()

	return Fixture{
		Name:      name,
		Rows:      rows,
		Cols:      cols,
		InputB64:  base64.StdEncoding.EncodeToString(input),
		WantLines: gridLines(g),
		WantRow:   g.CursorR,
		WantCol:   g.CursorC,
		WantTitle: gotTitle,
		WantCwd:   g.Cwd,
	}
}
