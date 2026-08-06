package term

import (
	"math"
	"testing"
)

func TestGrid_Resize_Shrink(t *testing.T) {
	g := newGrid(3, 3)
	g.Put('a')
	g.Put('b')
	g.Put('c')
	g.Resize(2, 2)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' {
		t.Errorf("shrink should preserve top-left: %v %v",
			g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
}

func TestGrid_Resize_Grow(t *testing.T) {
	g := newGrid(2, 2)
	g.Put('x')
	g.Resize(4, 5)
	if g.At(0, 0).Ch != 'x' {
		t.Errorf("grow should preserve content: %v", g.At(0, 0).Ch)
	}
	if g.At(3, 4).Ch != ' ' || g.At(3, 4).FG != DefaultColor {
		t.Errorf("new cell not default: %+v", *g.At(3, 4))
	}
}

func TestGrid_Resize_Clamp(t *testing.T) {
	g := newGrid(2, 2)
	g.Resize(MaxGridDim+5, MaxGridDim+5)
	if g.Rows != MaxGridDim || g.Cols != MaxGridDim {
		t.Errorf("resize not clamped: %dx%d", g.Rows, g.Cols)
	}
}

func TestGrid_Resize_ClampsCursor(t *testing.T) {
	g := newGrid(10, 10)
	g.MoveCursor(9, 9)
	g.Resize(5, 5)
	if g.CursorR != 4 || g.CursorC != 4 {
		t.Errorf("cursor not clamped: %d %d", g.CursorR, g.CursorC)
	}
}

func TestGrid_Resize_ReflowsScrollback(t *testing.T) {

	g := newGrid(2, 4)
	g.ScrollbackCap = 10

	for _, r := range "abcd" {
		g.Put(r)
	}

	g.scrollUpRegion(1)
	if g.Scrollback.Len() != 1 {
		t.Fatalf("setup: scrollback len %d, want 1", g.Scrollback.Len())
	}

	g.Resize(2, 2)
	if g.Scrollback.Len() == 0 {
		t.Fatalf("shrink: scrollback empty, want at least 1 row")
	}
	if len(g.Scrollback.Row(0)) != 2 {
		t.Errorf("shrink: scrollback[0] width %d, want 2", len(g.Scrollback.Row(0)))
	}
	if g.Scrollback.Row(0)[0].Ch != 'a' || g.Scrollback.Row(0)[1].Ch != 'b' {
		t.Errorf("shrink: scrollback[0] = %v %v, want a b",
			g.Scrollback.Row(0)[0].Ch, g.Scrollback.Row(0)[1].Ch)
	}
	if g.At(0, 0).Ch != 'c' || g.At(0, 1).Ch != 'd' {
		t.Errorf("shrink: live[0] = %v %v, want c d",
			g.At(0, 0).Ch, g.At(0, 1).Ch)
	}

	g.Resize(2, 5)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' ||
		g.At(0, 2).Ch != 'c' || g.At(0, 3).Ch != 'd' {
		t.Errorf("grow: live[0] = %v%v%v%v, want abcd",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch, g.At(0, 3).Ch)
	}
	if c := g.At(0, 4); c.Ch != ' ' || c.FG != DefaultColor || c.BG != DefaultColor {
		t.Errorf("grow: live[0][4] not default blank: %+v", *c)
	}
}

func TestGrid_Resize_AdjustsSelectionByScrollbackDelta(t *testing.T) {

	g := newGrid(4, 4)
	g.ScrollbackCap = 10
	g.SelAnchor = contentPos{Row: 0, Col: 0}
	g.SelHead = contentPos{Row: 3, Col: 3}
	g.SelActive = true
	g.Resize(2, 2)
	if !g.SelActive {
		t.Error("Resize should preserve active selection (Phase 17)")
	}

	total := g.Scrollback.Len() + g.Rows
	if g.SelAnchor.Row < 0 || g.SelAnchor.Row >= total {
		t.Errorf("SelAnchor.Row %d out of [0,%d)", g.SelAnchor.Row, total)
	}
	if g.SelHead.Row < 0 || g.SelHead.Row >= total {
		t.Errorf("SelHead.Row %d out of [0,%d)", g.SelHead.Row, total)
	}
}

func TestGrid_ResizeResetsRegion(t *testing.T) {
	g := newGrid(10, 4)
	g.SetScrollRegion(2, 5)
	g.Resize(8, 4)
	if g.Top != 0 || g.Bottom != 7 {
		t.Errorf("Resize did not reset region: %d..%d", g.Top, g.Bottom)
	}
}

func TestGrid_Resize_Reflow_GrowWidth(t *testing.T) {

	g := newGrid(3, 5)
	for _, r := range "helloworld" {
		g.Put(r)
	}

	if g.At(0, 0).Ch != 'h' || g.At(1, 0).Ch != 'w' {
		t.Fatalf("setup: row0[0]=%c row1[0]=%c", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	g.Resize(3, 10)

	want := "helloworld"
	for i, r := range want {
		if g.At(0, i).Ch != r {
			t.Errorf("col %d: got %c, want %c", i, g.At(0, i).Ch, r)
		}
	}
	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after join")
	}
}

func TestGrid_Resize_Reflow_ShrinkWidth(t *testing.T) {

	g := newGrid(3, 10)
	for _, r := range "helloworld" {
		g.Put(r)
	}

	g.Resize(3, 5)

	if g.At(0, 0).Ch != 'h' || g.At(0, 4).Ch != 'o' {
		t.Errorf("row0 = %c..%c, want h..o", g.At(0, 0).Ch, g.At(0, 4).Ch)
	}
	if g.At(1, 0).Ch != 'w' || g.At(1, 4).Ch != 'd' {
		t.Errorf("row1 = %c..%c, want w..d", g.At(1, 0).Ch, g.At(1, 4).Ch)
	}
	if !g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be true (soft-wrap after shrink)")
	}
}

func TestGrid_Resize_Reflow_ExplicitNewline(t *testing.T) {

	g := newGrid(3, 5)
	for _, r := range "hello" {
		g.Put(r)
	}
	g.Newline()
	g.CursorC = 0
	for _, r := range "world" {
		g.Put(r)
	}

	if g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] should be false")
	}

	g.Resize(3, 10)

	for i, r := range "hello" {
		if g.At(0, i).Ch != r {
			t.Errorf("row0 col%d: got %c, want %c", i, g.At(0, i).Ch, r)
		}
	}
	for i, r := range "world" {
		if g.At(1, i).Ch != r {
			t.Errorf("row1 col%d: got %c, want %c", i, g.At(1, i).Ch, r)
		}
	}
}

func TestGrid_Resize_Reflow_CursorTracking(t *testing.T) {

	g := newGrid(3, 5)
	for _, r := range "abcde" {
		g.Put(r)
	}

	g.Resize(3, 3)
	if g.CursorR != 1 || g.CursorC != 1 {
		t.Errorf("cursor = (%d,%d), want (1,1)", g.CursorR, g.CursorC)
	}
}

func TestGrid_Resize_Reflow_WideChar(t *testing.T) {

	g := newGrid(2, 4)

	for _, r := range "abc" {
		g.Put(r)
	}
	g.Put('你')
	if g.At(0, 0).Ch != 'a' {
		t.Fatalf("setup: At(0,0)=%c, want a", g.At(0, 0).Ch)
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] not set")
	}

	g.Resize(2, 6)
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != 'b' || g.At(0, 2).Ch != 'c' {
		t.Errorf("chars: a=%c b=%c c=%c", g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	if g.At(0, 3).Ch != '你' || g.At(0, 3).Width != 2 {
		t.Errorf("wide char: ch=%c width=%d, want 你 width 2", g.At(0, 3).Ch, g.At(0, 3).Width)
	}
	if g.At(0, 4).Width != 0 {
		t.Errorf("continuation cell width=%d, want 0", g.At(0, 4).Width)
	}
}

func TestGrid_Resize_Reflow_DeepScrollbackNarrow_CursorSurvives(t *testing.T) {
	// Fill a wide grid with scrollback, then shrink to 1 column.
	// Each wide row explodes into oldCols new rows; the allNew trim must
	// keep the cursor row valid and scrollback within its cap.
	const rows, cols = 5, 20
	g := newGrid(rows, cols)
	g.ScrollbackCap = 50
	for range 20 {
		for c := range cols {
			if cell := g.At(0, c); cell != nil {
				cell.Ch = 'x'
			}
		}
		g.scrollUpRegion(1)
	}
	g.CursorR, g.CursorC = rows-1, cols/2
	g.Resize(rows, 1)

	if g.CursorR < 0 || g.CursorR >= g.Rows {
		t.Errorf("cursor row %d out of bounds [0,%d)", g.CursorR, g.Rows)
	}
	if g.CursorC < 0 || g.CursorC >= g.Cols {
		t.Errorf("cursor col %d out of bounds [0,%d)", g.CursorC, g.Cols)
	}
	if g.At(g.CursorR, g.CursorC) == nil {
		t.Error("cursor cell nil after narrow reflow")
	}
	if g.Scrollback.Len() > g.ScrollbackCap {
		t.Errorf("scrollback len %d exceeds cap %d", g.Scrollback.Len(), g.ScrollbackCap)
	}
}

func TestGrid_Resize_ZerosViewSubPx(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ViewSubPx = 12.5
	g.ViewOffset = 2
	g.Resize(4, 3)
	if g.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v after Resize, want 0", g.ViewSubPx)
	}
}

// TestRowArena_GrowthPreservesEarlierRows carves enough rows to cross several
// block boundaries (blocks are 8, 16, 32 … rows) and checks the invariant the
// geometric growth rests on: abandoning a full block must not invalidate the
// rows already carved from it, and a row must never bleed into its neighbour.
func TestRowArena_GrowthPreservesEarlierRows(t *testing.T) {
	const rowW, rows = 5, 40 // spans the 8, 16 and 32-row blocks

	a := rowArena{rowW: rowW}
	carved := make([][]cell, rows)
	for i := range carved {
		row := a.next()
		if cap(row) != rowW {
			t.Fatalf("row %d: cap = %d, want %d", i, cap(row), rowW)
		}
		if len(row) != 0 {
			t.Fatalf("row %d: len = %d, want 0", i, len(row))
		}
		// Fill the row to capacity with a value unique to this row, so a
		// row whose backing store was reused shows up as a wrong sentinel.
		for range rowW {
			row = append(row, cell{Ch: rune('A' + i)})
		}
		carved[i] = row
	}

	for i, row := range carved {
		want := rune('A' + i)
		for j, c := range row {
			if c.Ch != want {
				t.Fatalf("row %d cell %d: Ch = %q, want %q", i, j, c.Ch, want)
			}
		}
	}
}

// TestRowArena_BlockSizeCapsAt256 pins the other end of the growth curve.
// The doubling must saturate at 256 rows: without the cap a deep-scrollback
// reflow would keep doubling into ever-larger blocks, and with the cap set
// too low it would fall back toward per-row allocation.
func TestRowArena_BlockSizeCapsAt256(t *testing.T) {
	// Cumulative rows at each block boundary are 8, 24, 56, 120, 248, 504 —
	// so 600 rows lands well past the first capped block.
	const rowW, rows = 4, 600

	a := rowArena{rowW: rowW}
	for range rows {
		if row := a.next(); cap(row) != rowW {
			t.Fatalf("cap = %d, want %d", cap(row), rowW)
		}
	}
	if a.blk != 256 {
		t.Errorf("block size = %d after %d rows, want 256", a.blk, rows)
	}
	if last := a.blocks[len(a.blocks)-1]; len(last) != rowW*256 {
		t.Errorf("last block len = %d, want %d", len(last), rowW*256)
	}
}

// TestRowArena_NonPositiveWidth checks the defensive path: a non-positive
// rowW must yield nil rather than allocating or panicking on a zero-size or
// negative-size block.
func TestRowArena_NonPositiveWidth(t *testing.T) {
	for _, rowW := range []int{0, -1, math.MinInt} {
		a := rowArena{rowW: rowW}
		if row := a.next(); row != nil {
			t.Errorf("rowW %d: next() = %v, want nil", rowW, row)
		}
		if len(a.blocks) != 0 {
			t.Errorf("rowW %d: allocated blocks, want none", rowW)
		}
	}
}

// --- benchmarks ---

func BenchmarkResize_Reflow_DeepScrollback(b *testing.B) {
	const rows, cols = 24, 80
	const scrollbackLines = 10000

	g := newGrid(rows, cols)
	g.ScrollbackCap = scrollbackLines

	// Fill scrollback with content.
	p := newParser(g)
	for range scrollbackLines {
		feedBench(b, g, p, []byte("filling scrollback with content to reflow later\n"))
	}

	b.ResetTimer()
	for b.Loop() {
		g.Resize(30, 100) // grow
		g.Resize(24, 80)  // shrink back
	}
}

// BenchmarkResize_Reflow_Empty measures the floor: a reflow that produces
// only a screenful of rows. The arena's block size must scale down with the
// row count, so this should cost far less than the deep-scrollback case.
// The widths alternate 132/80 because that is what DECCOLM drives, and the
// scrollback cap is the real default so the ring's steady-state reslicing
// (rather than a fresh allocation per resize) is part of the measurement.
func BenchmarkResize_Reflow_Empty(b *testing.B) {
	g := newGrid(24, 100)
	g.ScrollbackCap = defaultScrollbackRows

	b.ResetTimer()
	for b.Loop() {
		g.Resize(24, 132)
		g.Resize(24, 80)
	}
}

// BenchmarkResize_Reflow_LargeCap isolates the content-independent,
// cap-scaled cost: an empty grid at MaxScrollbackCap under the same
// alternating 132/80 DECCOLM widths as BenchmarkResize_Reflow_Empty.
// The ring's reuse band should hold steady-state resizing to near-zero
// allocation after the first width change; a residual cost here means
// SetGeom (or the reflow) is still allocating proportional to the cap
// rather than to stored content (issue #126's separate observation).
func BenchmarkResize_Reflow_LargeCap(b *testing.B) {
	g := newGrid(24, 100)
	g.ScrollbackCap = MaxScrollbackCap

	b.ResetTimer()
	for b.Loop() {
		g.Resize(24, 132)
		g.Resize(24, 80)
	}
}

// feedBench is like feed but for benchmarks (no test error helpers).
func feedBench(b *testing.B, g *grid, p *parser, data []byte) {
	b.Helper()
	g.Mu.Lock()
	defer g.Mu.Unlock()
	p.Feed(data)
}

func TestRewrapLine_PreserveAttributes(t *testing.T) {
	c := cell{Ch: '🍣', Width: 2, FG: 1, BG: 2, Attrs: attrBold, LinkID: 42}
	cells := []cell{c, {Width: 0}} // wide char + continuation

	var dest []physRow
	n := rewrapLine(cells, 10, &rowArena{rowW: 10}, &dest)
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
	rows := dest
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// Check continuation cell at index 1
	cont := rows[0].cells[1]
	if cont.Width != 0 {
		t.Errorf("expected width 0, got %d", cont.Width)
	}
	if cont.LinkID != 42 {
		t.Errorf("expected LinkID 42, got %d", cont.LinkID)
	}
	if cont.Attrs != attrBold {
		t.Errorf("expected Bold attr, got %d", cont.Attrs)
	}
}

// TestRewrapLine_AppendsIntoDest pins the append contract: consecutive
// calls stack into the same destination slice and report their own counts.
func TestRewrapLine_AppendsIntoDest(t *testing.T) {
	arena := &rowArena{rowW: 10}
	var dest []physRow
	if n := rewrapLine(nil, 10, arena, &dest); n != 1 || len(dest) != 1 {
		t.Fatalf("blank line: n=%d dest=%d, want 1/1", n, len(dest))
	}
	cells := []cell{{Ch: 'a', Width: 1}, {Ch: 'b', Width: 1}}
	if n := rewrapLine(cells, 10, arena, &dest); n != 1 || len(dest) != 2 {
		t.Fatalf("single-row line: n=%d dest=%d, want 1/2", n, len(dest))
	}
	if got := dest[1].cells[0].Ch; got != 'a' {
		t.Errorf("second append misaligned: dest[1][0] = %q, want 'a'", got)
	}
}

// TestRowArena_PersistedReuse checks the arena survives a rowW change the
// way a grid-persisted arena sees it across Resizes: logicalReflow only
// resets off/bi/rowW, so a second reflow must re-carve the retained blocks
// without appending while they still fit, and rows carved before the reset
// must stay valid (they alias the shared blocks). Rows come back
// zero-length with cap == rowW (append fills them), so len stays 0.
func TestRowArena_PersistedReuse(t *testing.T) {
	var a rowArena
	a.rowW = 132
	first := make([][]cell, 0, 64)
	for range 64 {
		row := a.next()
		if cap(row) != 132 {
			t.Fatalf("carved row cap %d, want 132", cap(row))
		}
		first = append(first, row)
	}
	blocks := len(a.blocks)

	// Reset exactly as logicalReflow does for the next reflow.
	a.off = 0
	a.bi = 0
	a.rowW = 80
	for range 64 {
		if row := a.next(); cap(row) != 80 {
			t.Fatalf("carved row cap %d, want 80", cap(row))
		}
	}
	if len(a.blocks) != blocks {
		t.Errorf("appended %d block(s) when the retained blocks still fit",
			len(a.blocks)-blocks)
	}
	for i, row := range first {
		if cap(row) != 132 {
			t.Errorf("row %d cap %d, want 132", i, cap(row))
		}
	}
}

// TestRowArena_SkipsTooSmallBlocks pins the advance path of next(): a
// re-carve at a much larger rowW must walk past retained blocks that cannot
// hold even one row of the new width before appending, and must not lose
// rows along the way.
func TestRowArena_SkipsTooSmallBlocks(t *testing.T) {
	var a rowArena
	a.rowW = 4
	for range 8 { // one 8-row block of 4-wide cells = 32 cells
		a.next()
	}
	if len(a.blocks) != 1 {
		t.Fatalf("carved %d block(s), want 1", len(a.blocks))
	}

	// 100-wide rows need 100 cells; every retained block is too small, so
	// next() must skip them all and append a fresh block at the new width.
	a.off = 0
	a.bi = 0
	a.rowW = 100
	if row := a.next(); cap(row) != 100 {
		t.Fatalf("carved row cap %d, want 100", cap(row))
	}
	if len(a.blocks) != 2 {
		t.Errorf("blocks = %d, want 2 (skip the small block, append one)",
			len(a.blocks))
	}
	if last := a.blocks[len(a.blocks)-1]; len(last) < 100 {
		t.Errorf("new block len %d, want >= 100", len(last))
	}
}

// TestRowArena_PersistedReuse_DeepThenShallow re-carves a deep arena for a
// shallow reflow: the first block alone must cover the whole carve, leaving
// every later block untouched — the shallow reflow pays no allocation.
func TestRowArena_PersistedReuse_DeepThenShallow(t *testing.T) {
	var a rowArena
	a.rowW = 80
	for range 2000 {
		a.next()
	}
	blocks := len(a.blocks)
	first := a.blocks[0]

	a.off = 0
	a.bi = 0
	a.rowW = 80
	for range 24 {
		if row := a.next(); cap(row) != 80 {
			t.Fatalf("carved row cap %d, want 80", cap(row))
		}
	}
	if len(a.blocks) != blocks {
		t.Errorf("appended %d block(s) for a 24-row reflow",
			len(a.blocks)-blocks)
	}
	if !sameSlicePtr(a.blocks[0], first) {
		t.Error("shallow reflow replaced the first block")
	}
}

// sameSlicePtr reports whether two non-empty slices share an underlying
// array (and identical len/cap headers).
func sameSlicePtr(a, b []cell) bool {
	return len(a) == len(b) && cap(a) == cap(b) && &a[0] == &b[0]
}

func TestLogicalReflow_ZeroDimsClamped(t *testing.T) {
	// The defensive guards must prevent division by zero and negative
	// allocations without panicking.
	cfg := reflowConfig{
		cells:      nil,
		rowWrapped: nil,
		scrollback: nil,
		sbWrapped:  nil,
		oldRows:    0,
		oldCols:    0,
		newRows:    0,
		newCols:    0,
		cursorR:    0,
		cursorC:    0,
	}
	// Must not panic.
	res := logicalReflow(cfg)
	// oldRows=0, newRows clamped to 1, newCols clamped to 1.
	if len(res.cells) != 1 {
		t.Fatalf("zero dims: got %d cells, want 1", len(res.cells))
	}
	if len(res.rowWrapped) != 1 {
		t.Fatalf("zero dims: got %d rowWrapped, want 1", len(res.rowWrapped))
	}
	if res.cursorR != 0 || res.cursorC != 0 {
		t.Errorf("zero dims cursor: (%d,%d), want (0,0)", res.cursorR, res.cursorC)
	}
}

func TestLogicalReflow_HugeDimsClamped(t *testing.T) {
	// Mirror of the zero-dim guard at the other end. Unclamped, newCols
	// this large overflows the rowArena block product (rowW*blk) and the
	// newRows*newCols buffer, handing make() a negative length and
	// panicking. Callers clamp via clampDim; this covers the struct-param
	// path, which nothing outside the package constrains.
	cfg := reflowConfig{
		oldRows: 1,
		oldCols: math.MaxInt,
		newRows: math.MaxInt,
		newCols: math.MaxInt,
	}
	// Must not panic.
	res := logicalReflow(cfg)
	if len(res.cells) != MaxGridDim*MaxGridDim {
		t.Fatalf("huge dims: got %d cells, want %d",
			len(res.cells), MaxGridDim*MaxGridDim)
	}
	if len(res.rowWrapped) != MaxGridDim {
		t.Fatalf("huge dims: got %d rowWrapped, want %d",
			len(res.rowWrapped), MaxGridDim)
	}
}

func TestLogicalReflow_ShortCellBufferTruncates(t *testing.T) {
	// oldRows claims 4 rows but the buffer holds only 2. Slicing row 2 out
	// of it would panic, so the reflow must believe the buffer: keep the
	// rows that are really there and drop the phantom ones.
	cells := make([]cell, 10) // 2 rows of 5
	for i, r := range "helloworld" {
		cells[i] = cell{Ch: r, Width: 1, FG: DefaultColor, BG: DefaultColor}
	}
	cfg := reflowConfig{
		cells:      cells,
		rowWrapped: []bool{true, false},
		oldRows:    4, // lies: only 2 rows of cells exist
		oldCols:    5,
		newRows:    4,
		newCols:    10,
	}
	// Must not panic.
	res := logicalReflow(cfg)
	if len(res.cells) != 4*10 {
		t.Fatalf("got %d cells, want %d", len(res.cells), 4*10)
	}
	// The two real rows were soft-wrapped, so they rejoin into one row of 10.
	for i, want := range "helloworld" {
		if got := res.cells[i].Ch; got != want {
			t.Errorf("col %d: got %q, want %q", i, got, want)
		}
	}
}

func TestLogicalReflow_TinyDimsNoPanic(t *testing.T) {
	// newCols=1 with cells present should not panic on rewrap or
	// division. Regression test for the oldCols/newCols division in
	// the estRows formula.
	g := newGrid(3, 5)
	for _, r := range "hello" {
		g.Put(r)
	}
	cfg := reflowConfig{
		cells:      g.Cells,
		rowWrapped: g.RowWrapped,
		scrollback: nil,
		sbWrapped:  nil,
		oldRows:    g.Rows,
		oldCols:    g.Cols,
		newRows:    3,
		newCols:    1,
		cursorR:    g.CursorR,
		cursorC:    g.CursorC,
	}
	res := logicalReflow(cfg)
	if len(res.cells) != 3 {
		t.Errorf("tiny dims: got %d cells, want 3", len(res.cells))
	}
	if res.cursorR < 0 || res.cursorR >= 3 {
		t.Errorf("cursorR %d out of [0,3)", res.cursorR)
	}
}

func TestLogicalReflow_NegativeOldRowsClamped(t *testing.T) {
	// oldRows < 0 must be clamped to 0 without panicking.
	cfg := reflowConfig{
		cells:      nil,
		rowWrapped: nil,
		scrollback: nil,
		sbWrapped:  nil,
		oldRows:    -5,
		oldCols:    10,
		newRows:    2,
		newCols:    4,
		cursorR:    0,
		cursorC:    0,
	}
	// Must not panic.
	res := logicalReflow(cfg)
	// oldRows clamped to 0, newRows=2, newCols=4 → 8 cells total.
	if len(res.cells) != 8 {
		t.Errorf("negative oldRows: got %d cells, want 8", len(res.cells))
	}
}
