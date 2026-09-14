package term

import (
	"bytes"
	"testing"
)

// labelRows writes 'A'+r into column 0 of every screen row so row identity
// can be followed through scrolls.
func labelRows(g *grid) {
	for r := range g.Rows {
		g.At(r, 0).Ch = rune('A' + r)
	}
}

// col0 returns column 0 of every screen row as a string.
func col0(g *grid) string {
	b := make([]rune, g.Rows)
	for r := range g.Rows {
		b[r] = g.At(r, 0).Ch
	}
	return string(b)
}

// rowMapIsPermutation reports whether rowMap holds each slot exactly once.
func rowMapIsPermutation(g *grid) bool {
	if len(g.rowMap) != g.Rows {
		return false
	}
	seen := make([]bool, g.Rows)
	for _, s := range g.rowMap {
		if s < 0 || int(s) >= g.Rows || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}

// Scrolls now rotate rowMap instead of moving cells. Every row-moving edit must
// give the same screen as the old memmove version, keep rowMap a permutation,
// and leave the rows it exposes blank.
func TestGrid_RowMap_RotatingEdits(t *testing.T) {
	g := newGrid(6, 4)
	labelRows(g)

	g.scrollUpRegion(2) // full screen
	if got, want := col0(g), "CDEF  "; got != want {
		t.Fatalf("scrollUp: got %q want %q", got, want)
	}

	labelRows(g)
	g.SetScrollRegion(1, 4) // rows B..E
	g.scrollDownRegion(1)
	if got, want := col0(g), "A BCDF"; got != want {
		t.Fatalf("scrollDown in region: got %q want %q", got, want)
	}

	labelRows(g)
	g.CursorR = 2
	g.DeleteLines(1) // region still 1..4
	if got, want := col0(g), "ABDE F"; got != want {
		t.Fatalf("DeleteLines: got %q want %q", got, want)
	}

	labelRows(g)
	g.CursorR = 2
	g.InsertLines(2)
	if got, want := col0(g), "AB  CF"; got != want {
		t.Fatalf("InsertLines: got %q want %q", got, want)
	}
	if !rowMapIsPermutation(g) {
		t.Fatalf("rowMap not a permutation: %v", g.rowMap)
	}
}

// A rotated rowMap must not leak into consumers that read Cells as row-major:
// the alt-screen stash (restored by ExitAlt) and Resize's reflow.
func TestGrid_RowMap_SurvivesAltAndResize(t *testing.T) {
	g := newGrid(4, 3)
	g.ScrollbackCap = 10
	labelRows(g)
	g.scrollUpRegion(1)
	labelRows(g) // screen now "ABCD" but rowMap is rotated
	if g.rowMap[0] == 0 {
		t.Fatalf("setup: expected rotated rowMap, got %v", g.rowMap)
	}

	g.EnterAlt()
	g.At(0, 0).Ch = 'z'
	g.scrollUpRegion(1) // rotate the alt buffer too
	g.ExitAlt()
	if got, want := col0(g), "ABCD"; got != want {
		t.Fatalf("after alt round trip: got %q want %q", got, want)
	}

	g.scrollUpRegion(1)
	g.At(3, 0).Ch = 'E' // screen "BCDE", scrollback "A","A"
	// Reflow keeps rows only down to the cursor; park it on the last written row.
	g.CursorR = 3
	g.Resize(4, 5)
	if !rowMapIsPermutation(g) {
		t.Fatalf("rowMap after resize: %v", g.rowMap)
	}
	sb := g.Scrollback.Len()
	var got []rune
	for r := sb - 1; r < g.ContentRows(); r++ {
		got = append(got, g.ContentCellAt(r, 0).Ch)
	}
	if string(got) != "ABCDE" {
		t.Fatalf("content after resize: got %q want %q", string(got), "ABCDE")
	}
}

// BenchmarkGrid_LineFeedScroll mirrors vtebench's "scrolling" test: 1 MiB of
// "y\n" into a 50x200 screen. Before rowMap every line feed memmoved the whole
// screen (~1.7 s per MiB).
func BenchmarkGrid_LineFeedScroll(b *testing.B) {
	g := newGrid(50, 200)
	g.ScrollbackCap = 10000
	p := newParser(g)
	input := bytes.Repeat([]byte("y\n"), 1<<19)
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	for b.Loop() {
		g.Mu.Lock()
		p.Feed(input)
		g.Mu.Unlock()
	}
}
