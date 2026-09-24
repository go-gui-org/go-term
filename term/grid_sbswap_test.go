package term

import (
	"fmt"
	"testing"
	"unsafe"
)

// rowPtr identifies a row's storage by its first cell's address.
func rowPtr(row []cell) unsafe.Pointer {
	if len(row) == 0 {
		return nil
	}
	return unsafe.Pointer(&row[0])
}

// assertRowsDisjoint fails when two live rows (screen, alt stash, or scrollback)
// share storage. After the scrollback swap, rows move between the screen slab and
// the ring slab, so any path that reuses a slab while the other side still holds
// rows from it would show up here as a shared pointer.
func assertRowsDisjoint(t *testing.T, g *grid, step string) {
	t.Helper()
	seen := map[unsafe.Pointer]string{}
	add := func(p unsafe.Pointer, who string) {
		if p == nil {
			return
		}
		if prev, dup := seen[p]; dup {
			t.Fatalf("%s: %s shares storage with %s", step, who, prev)
		}
		seen[p] = who
	}
	for r := range g.Rows {
		add(rowPtr(g.row(r)), fmt.Sprintf("screen row %d", r))
	}
	for i := range g.Scrollback.Len() {
		add(rowPtr(g.Scrollback.Row(i)), fmt.Sprintf("scrollback row %d", i))
	}
}

// labelLine writes s at the start of screen row r.
func labelLine(g *grid, r int, s string) {
	for c, ch := range s {
		g.At(r, c).Ch = ch
	}
}

// lineText returns the first n runes of a row.
func lineText(row []cell, n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = row[i].Ch
	}
	return string(b)
}

// A row leaving the top of the screen must move into scrollback by reference,
// not by copy: copying it cost ~33% of vtebench's scrolling time.
func TestScrollUp_SwapsRowIntoScrollback(t *testing.T) {
	g := newGrid(3, 4)
	g.ScrollbackCap = 2
	labelLine(g, 0, "top!")
	top := rowPtr(g.row(0))

	g.scrollUpRegion(1)

	sb := g.Scrollback.Row(g.Scrollback.Len() - 1)
	if got := lineText(sb, 4); got != "top!" {
		t.Fatalf("scrollback row = %q, want %q", got, "top!")
	}
	if rowPtr(sb) != top {
		t.Fatalf("scrolled-off row was copied, want its storage moved into scrollback")
	}
	if got := lineText(g.row(2), 4); got != "    " {
		t.Fatalf("new bottom row = %q, want blank", got)
	}
	assertRowsDisjoint(t, g, "after one scroll")
}

// Every path that reallocates, reuses or drops a slab must leave screen and
// scrollback rows on separate storage, with content intact.
func TestScrollbackSwap_NoSharedRowsAcrossSlabChanges(t *testing.T) {
	g := newGrid(4, 5)
	g.ScrollbackCap = 3
	p := newParser(g)
	feed := func(s string) { p.Feed([]byte(s)) }

	lines := func(from, to int) {
		for i := from; i < to; i++ {
			feed(fmt.Sprintf("\r\nL%03d", i))
		}
	}

	lines(0, 10) // fills and evicts the ring
	assertRowsDisjoint(t, g, "after evictions")
	if got := lineText(g.Scrollback.Row(g.Scrollback.Len()-1), 4); got != "L005" {
		t.Fatalf("newest scrollback = %q, want L005", got)
	}

	// Slab reuse: same capacity and cols, so SetGeom keeps the backing array
	// while the screen still holds rows that came from it.
	g.Scrollback.SetGeom(3, 5)
	lines(10, 16)
	assertRowsDisjoint(t, g, "after SetGeom reuse")
	if got := lineText(g.row(g.Rows-1), 4); got != "L015" {
		t.Fatalf("bottom row = %q, want L015", got)
	}

	// Capacity change: EnsureGeom copies into a new slab.
	g.ScrollbackCap = 5
	g.Scrollback.EnsureGeom(5, 5)
	lines(16, 24)
	assertRowsDisjoint(t, g, "after EnsureGeom grow")

	// ED 3 drops the ring's backing while the screen holds its rows.
	feed("\x1b[3J")
	lines(24, 30)
	assertRowsDisjoint(t, g, "after ED 3")

	// Alt screen round trip with a scrollback drop inside it.
	feed("\x1b[?1049h")
	feed("\x1b[3J")
	lines(100, 104)
	feed("\x1b[?1049l")
	lines(30, 36)
	assertRowsDisjoint(t, g, "after alt round trip")
	if got := lineText(g.row(g.Rows-1), 4); got != "L035" {
		t.Fatalf("bottom row after alt = %q, want L035", got)
	}

	// Resize rebuilds both sides.
	g.Resize(6, 7)
	lines(36, 44)
	assertRowsDisjoint(t, g, "after resize")
	if got := lineText(g.row(g.CursorR), 4); got != "L043" {
		t.Fatalf("cursor row after resize = %q, want L043", got)
	}
	for i := range g.Scrollback.Len() {
		want := fmt.Sprintf("L%03d", 43-g.CursorR-g.Scrollback.Len()+i)
		if got := lineText(g.Scrollback.Row(i), 4); got != want {
			t.Fatalf("scrollback[%d] = %q, want %q", i, got, want)
		}
	}
}

// A short screen row (possible after the ring re-carved its slab — see
// flatScreen) takes the copying Push fallback, which returns the row itself.
// Nothing was borrowed, so rowsBorrowed must stay false; a needless reclaim
// would cost a screen-sized allocation on the next slab change.
func TestScrollUp_ShortRowFallback_DoesNotBorrow(t *testing.T) {
	g := newGrid(3, 5)
	g.ScrollbackCap = 10
	short := []cell{{Ch: 'a', Width: 1}, {Ch: 'b', Width: 1}}
	g.slots[g.rowMap[0]] = short

	g.scrollUpRegion(1)

	if g.rowsBorrowed {
		t.Error("rowsBorrowed = true after a fallback copy, want false")
	}
	if g.Scrollback.Len() != 1 {
		t.Fatalf("scrollback len = %d, want 1", g.Scrollback.Len())
	}
	sb := g.Scrollback.Row(0)
	if len(sb) != 5 {
		t.Fatalf("scrollback row len = %d, want 5", len(sb))
	}
	if sb[0].Ch != 'a' || sb[1].Ch != 'b' || sb[2].Ch != 0 {
		t.Errorf("scrollback row = %q,%q,%d; want a,b,0-padded",
			sb[0].Ch, sb[1].Ch, sb[2].Ch)
	}
	assertRowsDisjoint(t, g, "after short-row fallback")

	// A full-width row on the next scroll swaps for real and marks borrowed.
	g.scrollUpRegion(1)
	if !g.rowsBorrowed {
		t.Error("rowsBorrowed = false after a real swap, want true")
	}
	assertRowsDisjoint(t, g, "after swap following fallback")
}

// Clearing the screen after rows have been swapped must clear only the screen:
// a flat fill over the screen slab would wipe scrollback rows stored in it.
func TestScrollbackSwap_ClearScreenKeepsScrollback(t *testing.T) {
	g := newGrid(3, 4)
	g.ScrollbackCap = 10
	p := newParser(g)
	for i := range 8 {
		p.Feed([]byte(fmt.Sprintf("\r\nR%02d", i)))
	}
	before := make([]string, g.Scrollback.Len())
	for i := range before {
		before[i] = lineText(g.Scrollback.Row(i), 3)
	}
	for _, seq := range []string{"\x1b[2J", "\x1b#8"} { // ED 2, DECALN
		p.Feed([]byte(seq))
		for i, want := range before {
			if got := lineText(g.Scrollback.Row(i), 3); got != want {
				t.Fatalf("%q: scrollback[%d] = %q, want %q", seq, i, got, want)
			}
		}
	}
	g.ClearAll()
	for i, want := range before {
		if got := lineText(g.Scrollback.Row(i), 3); got != want {
			t.Fatalf("ClearAll: scrollback[%d] = %q, want %q", i, got, want)
		}
	}
}

// rowsInSlab lists the live rows (screen, plus the main screen parked behind the
// alt screen) whose storage lies inside slab.
func rowsInSlab(g *grid, slab []cell) []string {
	if len(slab) == 0 {
		return nil
	}
	lo := uintptr(unsafe.Pointer(&slab[0]))
	hi := lo + uintptr(len(slab))*unsafe.Sizeof(slab[0])
	var hits []string
	check := func(row []cell, who string) {
		if p := uintptr(rowPtr(row)); p >= lo && p < hi {
			hits = append(hits, who)
		}
	}
	for r := range g.Rows {
		check(g.row(r), fmt.Sprintf("screen row %d", r))
	}
	if g.AltActive {
		for r, s := range g.mainSaved.rowMap {
			check(g.mainSaved.slots[s], fmt.Sprintf("main row %d", r))
		}
	}
	return hits
}

// Dropping or replacing the ring's slab must free it at once. Screen rows swapped in
// from it used to keep all ScrollbackCap×Cols cells alive until the next scroll, and
// forever once scrollback was switched off.
func TestScrollbackSwap_SlabChangeReleasesBorrowedRows(t *testing.T) {
	// borrow fills a grid until its screen holds rows carved from the ring's slab,
	// and returns that slab.
	borrow := func(t *testing.T, g *grid) []cell {
		t.Helper()
		g.ScrollbackCap = 20
		p := newParser(g)
		for i := range 10 {
			p.Feed([]byte(fmt.Sprintf("\r\nL%03d", i)))
		}
		slab := g.Scrollback.cells
		if len(rowsInSlab(g, slab)) == 0 {
			t.Fatal("setup: no screen row borrowed from the ring")
		}
		return slab
	}

	for _, tc := range []struct {
		name, seq string
		// exit is fed after the slab check; bottom is the bottom row expected
		// after it ("" to skip). ED 3 and RIS blank the screen, so only the alt
		// case has content left to check: the main screen it restores.
		exit, bottom string
	}{
		{"ED 3", "\x1b[3J", "", ""},
		{"RIS", "\x1bc", "", ""},
		{"ED 3 inside alt", "\x1b[?1049h\x1b[3J", "\x1b[?1049l", "L009"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(4, 5)
			slab := borrow(t, g)
			newParser(g).Feed([]byte(tc.seq))
			if g.Scrollback.Len() != 0 {
				t.Fatalf("setup: scrollback not dropped, len %d", g.Scrollback.Len())
			}
			if hits := rowsInSlab(g, slab); len(hits) != 0 {
				t.Fatalf("dropped slab still referenced by %v", hits)
			}
			newParser(g).Feed([]byte(tc.exit))
			if tc.bottom != "" {
				if got := lineText(g.row(g.Rows-1), 4); got != tc.bottom {
					t.Fatalf("bottom row = %q, want %q", got, tc.bottom)
				}
			}
		})
	}

	t.Run("cap change", func(t *testing.T) {
		tm := newSettingsTerm(Cfg{})
		slab := borrow(t, tm.grid)
		tm.SetScrollbackRows(40)
		if hits := rowsInSlab(tm.grid, slab); len(hits) != 0 {
			t.Fatalf("replaced slab still referenced by %v", hits)
		}
		if got := lineText(tm.grid.row(tm.grid.CursorR), 4); got != "L009" {
			t.Fatalf("cursor row = %q, want L009", got)
		}
	})
}

// scrollUpRegion marks screen rows borrowed only when PushSwap really took the
// row. Every fallback PushSwap has (short row, disabled ring) returns the row
// itself; the old check looked only at the row length, so a fallback on a ring
// with cap 0 but a full-width row counted as a swap.
func TestPushSwapBorrowed(t *testing.T) {
	row := make([]cell, 4)
	cases := []struct {
		name string
		cap  int
		row  []cell
		want bool
	}{
		{"swap", 8, row, true},
		{"disabled ring, full-width row", 0, row, false},
		{"short row", 8, row[:2], false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var r scrollbackRing
			r.SetGeom(tc.cap, 4)
			spare, _ := r.PushSwap(tc.row, false)
			if got := pushSwapBorrowed(tc.row, spare); got != tc.want {
				t.Errorf("pushSwapBorrowed = %v, want %v", got, tc.want)
			}
		})
	}
	if pushSwapBorrowed(nil, nil) {
		t.Error("empty row reported as borrowed")
	}
}
