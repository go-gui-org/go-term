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

// rowsAreDistinct reports whether every screen row has its own storage.
func rowsAreDistinct(g *grid) bool {
	if len(g.rowMap) != g.Rows {
		return false
	}
	seen := map[*cell]bool{}
	for r := range g.Rows {
		row := g.row(r)
		if seen[&row[0]] {
			return false
		}
		seen[&row[0]] = true
	}
	return true
}

// Scrolls now rotate row headers instead of moving cells. Every row-moving edit must
// give the same screen as the old memmove version, keep every row on its own storage,
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
	if !rowsAreDistinct(g) {
		t.Fatal("screen rows share storage")
	}
}

// Rotated rows must not leak into consumers that read Cells as row-major:
// the alt-screen stash (restored by ExitAlt) and Resize's reflow.
func TestGrid_RowMap_SurvivesAltAndResize(t *testing.T) {
	g := newGrid(4, 3)
	g.ScrollbackCap = 10
	labelRows(g)
	g.scrollUpRegion(1)
	labelRows(g) // screen now "ABCD" but rows are rotated
	if &g.row(0)[0] == &g.Cells[0] {
		t.Fatal("setup: expected rotated rows")
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
	if !rowsAreDistinct(g) {
		t.Fatal("screen rows share storage after resize")
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
// "y\n" into a 50x200 screen. Before row rotation every line feed memmoved the whole
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

// After a scroll, rowMap is no longer the identity, so an edit that still indexed
// Cells flat would land on the wrong row. A fresh grid cannot catch that, so every
// per-row reader and writer is checked on a rotated layout.
func TestGrid_RowMap_RowEditsFollowRotatedLayout(t *testing.T) {
	g := newGrid(4, 6)
	g.scrollUpRegion(1)
	if g.rowMap[0] == 0 {
		t.Fatal("setup: scroll did not rotate the row map")
	}
	for r, s := range []string{"abcdef", "ghijkl", "mnopqr", "stuvwx"} {
		labelLine(g, r, s)
	}
	bl := string(blankCell(g.CurFG, g.CurBG, g.CurAttrs).Ch)

	g.CopyRect(1, 1, 1, 2, 4, 5) // DECCRA: row 1 cols 1-2 to row 4 col 5 (1-based)
	g.MoveCursor(2, 0)
	g.InsertChars(1)
	g.MoveCursor(1, 1)
	g.DeleteChars(2)
	wantRows(t, g, []string{"abcdef", "gjkl" + bl + bl, bl + "mnopq", "stuvab"})

	rr, _ := g.searchRow(g.Scrollback.Len()+3, nil, nil)
	if got := string(rr); got != "stuvab" {
		t.Errorf("searchRow(live 3) = %q, want %q", got, "stuvab")
	}
	if got := g.ContentCellAt(g.Scrollback.Len()+3, 5).Ch; got != 'b' {
		t.Errorf("ContentCellAt(live 3, 5) = %q, want 'b'", got)
	}

	g.MoveCursor(3, 4)
	g.EraseInLine(0) // EL 0
	g.MoveCursor(0, 1)
	g.EraseChars(2) // ECH 2
	g.MoveCursor(2, 5)
	g.Put('Z') // putCell
	g.FlushGrapheme()
	wantRows(t, g, []string{"a" + bl + bl + "def", "gjkl" + bl + bl, bl + "mnopZ", "stuv" + bl + bl})
}
