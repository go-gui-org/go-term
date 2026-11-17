package term

import (
	"math"
	"testing"
)

func TestGrid_ScrollUp(t *testing.T) {
	g := newGrid(3, 2)
	for r := range g.Rows {
		for c := range g.Cols {
			g.At(r, c).Ch = rune('a' + r)
		}
	}
	g.scrollUpRegion(1)
	if g.At(0, 0).Ch != 'b' || g.At(1, 0).Ch != 'c' {
		t.Errorf("scrollUp shift wrong: %v %v", g.At(0, 0).Ch, g.At(1, 0).Ch)
	}
	if g.At(2, 0).Ch != ' ' || g.At(2, 1).Ch != ' ' {
		t.Errorf("scrollUp last row not cleared: %v %v",
			g.At(2, 0).Ch, g.At(2, 1).Ch)
	}
}

func TestGrid_ScrollViewClamp(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10

	for range 4 {
		g.scrollUpRegion(1)
	}
	if g.Scrollback.Len() != 4 {
		t.Fatalf("setup: len=%d", g.Scrollback.Len())
	}
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("ScrollView(2): %d", g.ViewOffset)
	}
	g.ScrollView(100)
	if g.ViewOffset != 4 {
		t.Errorf("upper clamp: %d", g.ViewOffset)
	}
	g.ScrollView(-100)
	if g.ViewOffset != 0 {
		t.Errorf("lower clamp: %d", g.ViewOffset)
	}
	g.ScrollViewTop()
	if g.ViewOffset != 4 {
		t.Errorf("ScrollViewTop: %d", g.ViewOffset)
	}
	g.ResetView()
	if g.ViewOffset != 0 {
		t.Errorf("ResetView: %d", g.ViewOffset)
	}
}

// labeledGrid returns a grid whose every line, on screen and in scrollback,
// carries a unique rune in column 0. The pin tests compare what is on screen
// before and after output arrives. Blank rows would all look the same and
// let a drifted view pass, so every row gets a label.
// pushLabeled feeds n more lines and keeps the labels unique.
func labeledGrid(rows, cols, sbCap int) (*grid, func(n int)) {
	g := newGrid(rows, cols)
	g.ScrollbackCap = sbCap
	next := 'A'
	for r := range g.Rows {
		g.At(r, 0).Ch = next
		next++
	}
	push := func(n int) {
		for range n {
			// Scroll first, then label the new blank bottom row. The label
			// therefore belongs to the row that the next push sends up.
			g.scrollUpRegion(1)
			g.At(g.Rows-1, 0).Ch = next
			next++
		}
	}
	return g, push
}

// viewLabels returns the column-0 rune of every visible viewport row. This is
// what the reader sees, so it is the real test of a pin. ViewOffset alone can
// be right while the rows shown are wrong.
func viewLabels(g *grid) string {
	out := make([]rune, g.Rows)
	for r := range g.Rows {
		out[r] = g.ViewCellAt(r, 0).Ch
	}
	return string(out)
}

// A view scrolled back into history stays on the same rows while output
// pushes more into scrollback; one at the live bottom keeps following.
func TestGrid_ScrolledViewStaysPut(t *testing.T) {
	g, push := labeledGrid(3, 2, 10)
	push(4)
	g.ScrollView(2)
	want := viewLabels(g)
	push(3)
	if g.ViewOffset != 5 {
		t.Errorf("scrolled view drifted: offset %d, want 5", g.ViewOffset)
	}
	if got := viewLabels(g); got != want {
		t.Errorf("scrolled view shows %q, want %q", got, want)
	}
	g.ResetView()
	push(1)
	if g.ViewOffset != 0 {
		t.Errorf("live view stopped following: offset %d", g.ViewOffset)
	}
}

// A view scrolled back by less than one row (ViewOffset 0, ViewSubPx > 0) is
// not live: the scrollbar shows and the cursor hides. Output must not slide
// the text under the reader. A small trackpad scroll gives this state.
func TestGrid_SubRowScrolledViewStaysPut(t *testing.T) {
	g, push := labeledGrid(3, 2, 10)
	push(4)
	const cellH = 10
	g.ScrollViewPx(5, cellH)
	if g.ViewOffset != 0 || g.ViewSubPx != 5 {
		t.Fatalf("setup: offset %d subpx %v, want 0 / 5", g.ViewOffset, g.ViewSubPx)
	}
	want := viewLabels(g)
	wantTop := g.partialTopRow()[0].Ch
	push(2)
	if g.ViewOffset != 2 {
		t.Errorf("sub-row view drifted: offset %d, want 2", g.ViewOffset)
	}
	if g.ViewSubPx != 5 {
		t.Errorf("sub-row remainder changed: %v, want 5", g.ViewSubPx)
	}
	if got := viewLabels(g); got != want {
		t.Errorf("sub-row view shows %q, want %q", got, want)
	}
	// The partly visible row above the viewport is on screen too.
	if got := g.partialTopRow()[0].Ch; got != wantTop {
		t.Errorf("partial top row %q, want %q", got, wantTop)
	}
}

// Once the ring is full, every push evicts the oldest row and shifts every
// content index down by one. The ViewOffset bump makes up for that exactly:
// the view stays on the same rows until ViewOffset reaches Scrollback.Len().
// After that the clamp holds the view at the oldest row, and the view drifts
// as that row is evicted. Long build logs fill the ring, so this is the
// common case.
func TestGrid_ScrolledViewPinnedThroughEviction(t *testing.T) {
	const sbCap = 3
	g, push := labeledGrid(3, 2, sbCap)
	push(sbCap + 2) // ring full and already evicting
	if g.Scrollback.Len() != sbCap {
		t.Fatalf("setup: scrollback len %d, want %d", g.Scrollback.Len(), sbCap)
	}
	g.ScrollView(1)
	want := viewLabels(g)

	// Two pushes take ViewOffset from 1 to the clamp (3). Each one evicts.
	for i := range 2 {
		push(1)
		if g.ViewOffset != 2+i {
			t.Fatalf("push %d: offset %d, want %d", i+1, g.ViewOffset, 2+i)
		}
		if got := viewLabels(g); got != want {
			t.Fatalf("push %d: view shows %q, want %q (slipped during eviction)",
				i+1, got, want)
		}
	}

	// At the clamp the view shows the oldest row, so the next eviction
	// removes the top row. The view must move by exactly that one row and
	// must not jump further.
	push(1)
	if g.ViewOffset != sbCap {
		t.Errorf("past clamp: offset %d, want %d", g.ViewOffset, sbCap)
	}
	if got := viewLabels(g); got != want[1:]+got[len(got)-1:] {
		t.Errorf("past clamp: view shows %q, want it to slide one row from %q",
			got, want)
	}
}

func TestGrid_ScrollView_SaturatingAdd(t *testing.T) {
	g := newGrid(2, 2)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUpRegion(1)
	}
	if got := g.Scrollback.Len(); got != 5 {
		t.Fatalf("setup: scrollback len=%d", got)
	}

	g.ViewOffset = 3
	g.ScrollView(math.MaxInt)
	if g.ViewOffset != 5 {
		t.Errorf("MaxInt delta: got %d, want 5", g.ViewOffset)
	}

	g.ViewOffset = 3
	g.ScrollView(math.MinInt)
	if g.ViewOffset != 0 {
		t.Errorf("MinInt delta: got %d, want 0", g.ViewOffset)
	}

	g.ViewOffset = 0
	g.ScrollView(2)
	if g.ViewOffset != 2 {
		t.Errorf("normal delta: got %d, want 2", g.ViewOffset)
	}
}

func TestGrid_SetScrollRegion(t *testing.T) {
	g := newGrid(10, 4)
	g.SetScrollRegion(2, 5)
	if g.Top != 2 || g.Bottom != 5 {
		t.Errorf("region = %d..%d, want 2..5", g.Top, g.Bottom)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}

	g.SetScrollRegion(5, 5)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("degenerate not reset: %d..%d", g.Top, g.Bottom)
	}

	g.SetScrollRegion(-1, 3)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("negative top not reset: %d..%d", g.Top, g.Bottom)
	}
	g.SetScrollRegion(0, g.Rows)
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("oversize bottom not reset: %d..%d", g.Top, g.Bottom)
	}
}

func TestGrid_ScrollUpRegion_Partial(t *testing.T) {
	g := newGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.ScrollbackCap = 100
	g.scrollUpRegion(1)
	want := []rune{'A', 'C', 'D', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}

	if g.Scrollback.Len() != 0 {
		t.Errorf("partial-region scroll polluted scrollback: %d", g.Scrollback.Len())
	}
}

func TestGrid_ScrollUpRegion_FullScreenScrollback(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for i, ch := range []rune{'A', 'B', 'C'} {
		fillRow(g, i, ch)
	}

	g.scrollUpRegion(1)
	if g.Scrollback.Len() != 1 || g.Scrollback.Row(0)[0].Ch != 'A' {
		t.Errorf("full-screen scroll didn't push: %+v", g.Scrollback)
	}
}

// TestGrid_ScrollUpRegion_BottomMarginScrollback pins the inline-TUI case:
// a region that starts at row 0 but reserves rows at the bottom for a fixed
// input area still feeds scrollback, because rows leaving row 0 really did
// scroll off the screen. Matches Ghostty; without it an app like muse or
// any prompt-pinned TUI has no reachable history at all.
func TestGrid_ScrollUpRegion_BottomMarginScrollback(t *testing.T) {
	g := newGrid(5, 2)
	g.ScrollbackCap = 10
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 0, 2 // rows 3..4 are the pinned input area

	g.scrollUpRegion(1)

	if g.Scrollback.Len() != 1 {
		t.Fatalf("scrollback len = %d, want 1", g.Scrollback.Len())
	}
	if got := g.Scrollback.Row(0)[0].Ch; got != 'A' {
		t.Errorf("scrollback row 0 = %q, want 'A'", got)
	}
	// Region scrolled; the rows below the margin are untouched.
	want := []rune{'B', 'C', ' ', 'D', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

// TestGrid_ScrollUpRegion_TopMarginNoScrollback is the other half of the
// rule: a region below row 0 keeps a header pane on screen, so its displaced
// rows are redraw churn and must not reach history.
func TestGrid_ScrollUpRegion_TopMarginNoScrollback(t *testing.T) {
	g := newGrid(5, 2)
	g.ScrollbackCap = 10
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 4

	g.scrollUpRegion(1)

	if g.Scrollback.Len() != 0 {
		t.Errorf("top-margin scroll polluted scrollback: %d", g.Scrollback.Len())
	}
}

func TestGrid_ScrollUpRegion_OverHeight(t *testing.T) {
	g := newGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.scrollUpRegion(99)
	want := []rune{'A', ' ', ' ', ' ', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
}

func TestGrid_ScrollDownRegion_Partial(t *testing.T) {
	g := newGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		fillRow(g, i, ch)
	}
	g.Top, g.Bottom = 1, 3
	g.ScrollbackCap = 100
	g.scrollDownRegion(1)
	want := []rune{'A', ' ', 'B', 'C', 'E'}
	for i, w := range want {
		if got := rowChar(g, i); got != w {
			t.Errorf("row %d = %q, want %q", i, got, w)
		}
	}
	if g.Scrollback.Len() != 0 {
		t.Errorf("scrollDown polluted scrollback")
	}
}

func TestGrid_ScrollUp_ShiftsWrappedFlags(t *testing.T) {
	g := newGrid(3, 4)
	g.ScrollbackCap = 10

	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollUpRegion(1)

	if g.Scrollback.Len() != 1 || !g.Scrollback.Wrapped(0) {
		t.Errorf("Scrollback wrap = len %d wrapped(0)=%v, want 1/true",
			g.Scrollback.Len(), g.Scrollback.Len() > 0 && g.Scrollback.Wrapped(0))
	}

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after scroll up")
	}
}

func TestGrid_ScrollUp_TrimsScrollbackWrapped(t *testing.T) {
	g := newGrid(2, 2)
	g.ScrollbackCap = 2
	for range 4 {
		g.RowWrapped[0] = true
		g.scrollUpRegion(1)
	}

	if g.Scrollback.Len() != 2 {
		t.Errorf("scrollback len %d, want 2 (cap)", g.Scrollback.Len())
	}
}

func TestGrid_ScrollDown_ShiftsWrappedFlags(t *testing.T) {
	g := newGrid(3, 4)
	g.RowWrapped[0] = true
	g.RowWrapped[1] = false
	g.RowWrapped[2] = false
	g.scrollDownRegion(1)

	if g.RowWrapped[0] {
		t.Error("RowWrapped[0] should be false after scroll down (inserted row)")
	}

	if !g.RowWrapped[1] {
		t.Error("RowWrapped[1] should be true (shifted from row 0)")
	}
}

func TestGrid_ScrollViewTop_PinsToOldestRow(t *testing.T) {
	g := newGrid(3, 5)
	g.ScrollbackCap = 10
	for range 5 {
		g.scrollUpRegion(1)
	}
	sb := g.Scrollback.Len()
	if sb == 0 {
		t.Skip("scrollback not populated")
	}
	g.ScrollViewTop()
	if g.ViewOffset != sb {
		t.Errorf("ViewOffset = %d, want %d (len(Scrollback))", g.ViewOffset, sb)
	}

	g2 := newGrid(3, 5)
	g2.ScrollViewTop()
	if g2.ViewOffset != 0 {
		t.Errorf("empty scrollback: ViewOffset = %d, want 0", g2.ViewOffset)
	}
}

func TestGrid_ScrollViewPx_SubRow(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	const cellH float32 = 20
	// Scroll 5px — less than one row, stays at ViewOffset=0.
	g.ScrollViewPx(5, cellH)
	if g.ViewOffset != 0 {
		t.Errorf("ViewOffset = %d, want 0", g.ViewOffset)
	}
	if math.Abs(float64(g.ViewSubPx-5)) > 0.001 {
		t.Errorf("ViewSubPx = %v, want 5", g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_MultiRow(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	const cellH float32 = 20
	// Scroll 1.5 rows = 30px.
	g.ScrollViewPx(30, cellH)
	if g.ViewOffset != 1 {
		t.Errorf("ViewOffset = %d, want 1", g.ViewOffset)
	}
	if math.Abs(float64(g.ViewSubPx-10)) > 0.001 {
		t.Errorf("ViewSubPx = %v, want 10", g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_ClampAtZero(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	const cellH float32 = 20
	g.ScrollViewPx(10, cellH)  // scroll forward 10px
	g.ScrollViewPx(-50, cellH) // try to scroll past the bottom
	if g.ViewOffset != 0 {
		t.Errorf("ViewOffset = %d, want 0", g.ViewOffset)
	}
	if g.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v, want 0", g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_ClampAtTop(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	const cellH float32 = 20
	// Scroll way past the top.
	g.ScrollViewPx(9999, cellH)
	if g.ViewOffset != 4 {
		t.Errorf("ViewOffset = %d, want 4", g.ViewOffset)
	}
	if g.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v, want 0 at top", g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_ResetView(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ScrollViewPx(35, 20)
	g.ResetView()
	if g.ViewOffset != 0 || g.ViewSubPx != 0 {
		t.Errorf("ResetView: got (%d, %v), want (0, 0)", g.ViewOffset, g.ViewSubPx)
	}
}

func TestGrid_ScrollViewTop_ZerosViewSubPx(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ViewSubPx = 7.5
	g.ScrollViewTop()
	if g.ViewSubPx != 0 {
		t.Errorf("ViewSubPx = %v after ScrollViewTop, want 0", g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_NaNDeltaIsNoOp(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ScrollViewPx(10, 20)
	offset, subPx := g.ViewOffset, g.ViewSubPx
	g.ScrollViewPx(float32(math.NaN()), 20)
	if g.ViewOffset != offset || g.ViewSubPx != subPx {
		t.Errorf("NaN delta changed state: (%d,%v) → (%d,%v)", offset, subPx, g.ViewOffset, g.ViewSubPx)
	}
	g.ScrollViewPx(float32(math.Inf(1)), 20)
	if g.ViewOffset != offset || g.ViewSubPx != subPx {
		t.Errorf("Inf delta changed state: (%d,%v) → (%d,%v)", offset, subPx, g.ViewOffset, g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_ZeroCellHIsNoOp(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ScrollViewPx(10, 20)
	offset, subPx := g.ViewOffset, g.ViewSubPx
	g.ScrollViewPx(99, 0)
	if g.ViewOffset != offset || g.ViewSubPx != subPx {
		t.Errorf("zero cellH changed state: (%d,%v) → (%d,%v)", offset, subPx, g.ViewOffset, g.ViewSubPx)
	}
}

func TestGrid_ScrollViewPx_ScrollView_ZerosSubPx(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.ScrollViewPx(15, 20) // ViewOffset=0, ViewSubPx=15
	g.ScrollView(1)        // integer jump; must clear ViewSubPx
	if g.ViewSubPx != 0 {
		t.Errorf("ScrollView did not zero ViewSubPx: got %v", g.ViewSubPx)
	}
	if g.ViewOffset != 1 {
		t.Errorf("ViewOffset = %d, want 1", g.ViewOffset)
	}
}

// BenchmarkGrid_ScrollUpRegion_FullScreen measures scrolling the full screen
// up by 1 line, which is the common case on every linefeed.
func BenchmarkGrid_ScrollUpRegion_FullScreen(b *testing.B) {
	g := newGrid(48, 120)
	// Fill with content so cells are not all default.
	for i := range g.Cells {
		g.Cells[i].Ch = rune(0x41 + i%26)
	}
	b.ResetTimer()
	for range b.N {
		g.scrollUpRegion(1)
	}
}

// BenchmarkGrid_ScrollDownRegion_FullScreen measures reverse scroll.
func BenchmarkGrid_ScrollDownRegion_FullScreen(b *testing.B) {
	g := newGrid(48, 120)
	for i := range g.Cells {
		g.Cells[i].Ch = rune(0x41 + i%26)
	}
	b.ResetTimer()
	for range b.N {
		g.scrollDownRegion(1)
	}
}

func TestGrid_SetViewFractional(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 8 {
		g.scrollUpRegion(1)
	}
	if g.Scrollback.Len() != 8 {
		t.Fatalf("setup: len=%d", g.Scrollback.Len())
	}
	const cellH = 20

	// Whole-row offset: no sub-pixel remainder.
	g.SetViewFractional(3, cellH)
	if g.ViewOffset != 3 || g.ViewSubPx != 0 {
		t.Errorf("off=3: got (%d,%v), want (3,0)", g.ViewOffset, g.ViewSubPx)
	}

	// Fractional offset: remainder scaled by cellH.
	g.SetViewFractional(2.5, cellH)
	if g.ViewOffset != 2 || g.ViewSubPx != 10 {
		t.Errorf("off=2.5: got (%d,%v), want (2,10)", g.ViewOffset, g.ViewSubPx)
	}

	// Clamp above scrollback length.
	g.SetViewFractional(99, cellH)
	if g.ViewOffset != 8 || g.ViewSubPx != 0 {
		t.Errorf("upper clamp: got (%d,%v), want (8,0)", g.ViewOffset, g.ViewSubPx)
	}

	// Clamp below zero.
	g.SetViewFractional(-5, cellH)
	if g.ViewOffset != 0 || g.ViewSubPx != 0 {
		t.Errorf("lower clamp: got (%d,%v), want (0,0)", g.ViewOffset, g.ViewSubPx)
	}
}

func TestGrid_SetViewFractional_NoOpGuards(t *testing.T) {
	g := newGrid(3, 2)
	g.ScrollbackCap = 10
	for range 4 {
		g.scrollUpRegion(1)
	}
	g.SetViewFractional(2, 20)
	off, sub := g.ViewOffset, g.ViewSubPx

	for _, bad := range []struct {
		off, cellH float32
		name       string
	}{
		{2, 0, "zero cellH"},
		{2, -1, "negative cellH"},
		{2, float32(math.NaN()), "NaN cellH"},
		{2, float32(math.Inf(1)), "Inf cellH"},
		{float32(math.NaN()), 20, "NaN off"},
		{float32(math.Inf(1)), 20, "Inf off"},
	} {
		g.SetViewFractional(bad.off, bad.cellH)
		if g.ViewOffset != off || g.ViewSubPx != sub {
			t.Errorf("%s changed state: (%d,%v)", bad.name, g.ViewOffset, g.ViewSubPx)
		}
	}
}

// viewLive is the single test for "the view is at the live bottom". The pin
// in scrollUpRegion, the cursor, IME, blink, scrollbar and mouse paths all
// use it, so they cannot disagree about a sub-row scroll again.
func TestGrid_ViewLive(t *testing.T) {
	cases := []struct {
		name   string
		off    int
		subPx  float32
		frozen bool
		want   bool
	}{
		{"live", 0, 0, false, true},
		{"whole rows back", 2, 0, false, false},
		{"sub-row back", 0, 5, false, false},
		{"rows and sub-row", 1, 3, false, false},
		// ViewFrozen pins a live view, but the view is still live: the
		// cursor shows and the scrollbar can hide.
		{"frozen at bottom", 0, 0, true, true},
	}
	for _, tc := range cases {
		g := newGrid(3, 2)
		g.ViewOffset, g.ViewSubPx, g.ViewFrozen = tc.off, tc.subPx, tc.frozen
		if got := g.viewLive(); got != tc.want {
			t.Errorf("%s: viewLive() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
