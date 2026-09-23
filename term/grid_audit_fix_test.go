package term

import (
	"regexp"
	"testing"
)

// Regression tests for the grid audit fixes. Each test fails on the
// pre-fix code (usually by panicking) and passes after.

// Empty regex matches (a*, x?) are zero-width. matchGridSpan indexes
// colMap[ci+cl-1], so l == 0 reads colMap[-1] or past the end.
func TestGridAudit_FindRegexEmptyMatchNoPanic(t *testing.T) {
	g := newGrid(5, 20)
	for _, ch := range "hello" {
		g.Put(ch)
	}
	for _, pat := range []string{"a*", "x?", "b*"} {
		re := regexp.MustCompile(pat)
		if _, _, ok := g.FindRegex(re, contentPos{}, true); ok {
			t.Errorf("FindRegex(%q) matched empty, want no match", pat)
		}
		if _, _, ok := g.FindRegex(re, contentPos{Row: 4, Col: 19}, false); ok {
			t.Errorf("FindRegex backward(%q) matched empty, want no match", pat)
		}
		if ms := g.ViewportMatchesRegex(re); len(ms) != 0 {
			t.Errorf("ViewportMatchesRegex(%q) = %d matches, want 0", pat, len(ms))
		}
	}
}

// A non-empty match from a nullable pattern (a* on "aaa") still finds text.
func TestGridAudit_FindRegexNullableFindsText(t *testing.T) {
	g := newGrid(5, 20)
	for _, ch := range "baaab" {
		g.Put(ch)
	}
	re := regexp.MustCompile("a*")
	pos, _, ok := g.FindRegex(re, contentPos{}, true)
	if !ok {
		t.Fatal("FindRegex(a*) found nothing on baaab")
	}
	if pos.Col != 1 {
		t.Errorf("FindRegex(a*) col = %d, want 1", pos.Col)
	}
}

// Shrinking the alt screen below the cursor must clamp it; the next Put
// indexes RowWrapped[CursorR] with no guard and panics otherwise.
func TestGridAudit_AltResizeCursorClamp(t *testing.T) {
	g := newGrid(10, 20)
	g.EnterAlt()
	g.MoveCursor(9, 19)
	g.Resize(5, 10)
	if g.CursorR > 4 || g.CursorC > 9 {
		t.Fatalf("alt cursor = %d,%d after shrink, want within 5x10",
			g.CursorR, g.CursorC)
	}
	g.Put('x') // must not panic
}

// ClearAll and ED 2 fill the screen flat; stale RowWrapped flags would make
// the next Resize join blank rows into one logical line.
func TestGridAudit_ClearAllResetsWrap(t *testing.T) {
	g := newGrid(3, 10)
	for range 11 {
		g.Put('a')
	}
	if !g.RowWrapped[0] {
		t.Fatal("setup: RowWrapped[0] should be true after wrap")
	}
	g.ClearAll()
	for r, w := range g.RowWrapped {
		if w {
			t.Errorf("ClearAll left RowWrapped[%d] set", r)
		}
	}
}

func TestGridAudit_EraseDisplayResetsWrap(t *testing.T) {
	g := newGrid(3, 10)
	for range 11 {
		g.Put('a')
	}
	g.EraseInDisplay(2)
	for r, w := range g.RowWrapped {
		if w {
			t.Errorf("ED 2 left RowWrapped[%d] set", r)
		}
	}
}

// Sixel/iTerm2 images die by painting over their cells. Rect erase and fill
// bypass eraseSpan, so they must occlude explicitly.
func TestGridAudit_RectEraseOccludesImage(t *testing.T) {
	g := newGrid(10, 10)
	g.AddGraphic("audit.png", 8, 8)
	if len(g.Graphics) != 1 {
		t.Fatalf("setup: len(Graphics) = %d, want 1", len(g.Graphics))
	}
	g.EraseRect(1, 1, 1, 1)
	if len(g.Graphics) != 0 {
		t.Errorf("EraseRect left %d graphics, want 0", len(g.Graphics))
	}
}

func TestGridAudit_RectFillOccludesImage(t *testing.T) {
	g := newGrid(10, 10)
	g.AddGraphic("audit.png", 8, 8)
	g.FillRect('x', 1, 1, 1, 1)
	if len(g.Graphics) != 0 {
		t.Errorf("FillRect left %d graphics, want 0", len(g.Graphics))
	}
}

// The alt screen must not inherit the main screen link, and exiting must
// restore the main screen link rather than the alt screen one.
func TestGridAudit_AltPreservesLink(t *testing.T) {
	g := newGrid(3, 10)
	mainID := g.internLink("https://main.example")
	g.CurLinkID = mainID
	g.EnterAlt()
	if g.CurLinkID != 0 {
		t.Errorf("alt CurLinkID = %d, want 0", g.CurLinkID)
	}
	altID := g.internLink("https://alt.example")
	g.CurLinkID = altID
	g.ExitAlt()
	if g.CurLinkID != mainID {
		t.Errorf("after ExitAlt CurLinkID = %d, want %d", g.CurLinkID, mainID)
	}
}

// TabBackward from a pending-wrap cursor must settle first, like Tab does.
func TestGridAudit_TabBackwardPendingWrap(t *testing.T) {
	g := newGrid(2, 20)
	g.MoveCursor(0, 19)
	g.SetTabStop() // stop at 19
	g.MoveCursor(0, 0)
	for range 20 {
		g.Put('a')
	}
	if g.CursorC != g.Cols {
		t.Fatalf("setup: CursorC = %d, want pending %d", g.CursorC, g.Cols)
	}
	g.TabBackward(1)
	if g.CursorC != 16 {
		t.Errorf("TabBackward from pending = %d, want 16", g.CursorC)
	}
}

// DECSC keeps the pending-wrap column; DECRC must restore it, not clamp it.
func TestGridAudit_SaveRestorePendingWrap(t *testing.T) {
	g := newGrid(2, 10)
	for range 10 {
		g.Put('a')
	}
	if g.CursorC != g.Cols {
		t.Fatalf("setup: CursorC = %d, want pending %d", g.CursorC, g.Cols)
	}
	g.SaveCursor()
	g.MoveCursor(0, 0)
	g.RestoreCursor()
	if g.CursorC != g.Cols {
		t.Errorf("restored CursorC = %d, want pending %d", g.CursorC, g.Cols)
	}
}

// eraseSpan must tolerate spans outside the row instead of panicking.
func TestGridAudit_EraseSpanClamped(t *testing.T) {
	g := newGrid(3, 5)
	g.Put('a')
	g.eraseSpan(1, -10, 1000, false)
	g.eraseSpan(9, 0, 5, false)
	if ch := g.At(1, 0).Ch; ch != ' ' {
		t.Errorf("row not blanked, Ch = %q", ch)
	}
}

// putCell with an out-of-range cursor row must not panic (belt and braces
// behind the alt-resize clamp).
func TestGridAudit_PutOOBCursorNoPanic(t *testing.T) {
	g := newGrid(3, 5)
	g.CursorR, g.CursorC = 99, 5
	g.Put('x')
}

// A wide char re-wrapped to a 1-column grid must fit: no row wider than
// newCols, no stranded Width==2 head without its continuation.
func TestGridAudit_RewrapNarrowWide(t *testing.T) {
	wide := cell{Ch: '中', FG: defaultColor, BG: defaultColor,
		ULColor: defaultColor, Width: 2}
	arena := &rowArena{rowW: 1}
	var dest []physRow
	if n := rewrapLine([]cell{wide}, 1, arena, &dest); n < 1 {
		t.Fatalf("rewrapLine produced %d rows, want >= 1", n)
	}
	for i, pr := range dest {
		if len(pr.cells) != 1 {
			t.Errorf("row %d len = %d, want 1", i, len(pr.cells))
		}
		for _, c := range pr.cells {
			if c.Width == 2 {
				t.Errorf("row %d holds a stranded wide head", i)
			}
		}
	}
}

// EnsureGeom with an absurd capacity must clamp like SetGeom instead of
// handing make() a multi-petabyte length.
func TestGridAudit_EnsureGeomHugeNoAlloc(t *testing.T) {
	var r scrollbackRing
	r.SetGeom(5, 80)
	r.EnsureGeom(1<<40, 80)
	if r.cap*80 > 1<<28 {
		t.Errorf("EnsureGeom cap = %d, want clamped to <= %d cells", r.cap, 1<<28/80)
	}
}

// HardReset drops every image, so the occlusion bound must go with them.
func TestGridAudit_HardResetClearsOccludeBound(t *testing.T) {
	g := newGrid(5, 10)
	g.AddGraphic("audit.png", 8, 8)
	g.HardReset()
	if g.occludeMaxR != 0 {
		t.Errorf("occludeMaxR = %d after HardReset, want 0", g.occludeMaxR)
	}
}

// matchGridSpan rejects empty and out-of-range spans instead of panicking.
func TestGridAudit_MatchGridSpanGuards(t *testing.T) {
	colMap := []int{0, 1, 2}
	rr := []rune{'a', 'b', 'c'}
	if _, _, ok := matchGridSpan(colMap, rr, 0, 0); ok {
		t.Error("empty span ok = true, want false")
	}
	if _, _, ok := matchGridSpan(colMap, rr, 3, 1); ok {
		t.Error("past-end span ok = true, want false")
	}
	if _, _, ok := matchGridSpan(colMap, rr, 2, 2); ok {
		t.Error("overlong span ok = true, want false")
	}
	if _, _, ok := matchGridSpan(colMap, rr, -1, 1); ok {
		t.Error("negative span ok = true, want false")
	}
	col, w, ok := matchGridSpan(colMap, rr, 1, 2)
	if !ok || col != 1 || w != 2 {
		t.Errorf("valid span = %d,%d,%v, want 1,2,true", col, w, ok)
	}
}

// runeWidth on non-ASCII costs one small alloc for the uniseg call; cap it
// there so the Put hot path never regresses past it.
func TestGridAudit_RuneWidthBoundedAlloc(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { runeWidth('中') }); n > 1 {
		t.Errorf("runeWidth allocs = %v, want <= 1", n)
	}
	if w := runeWidth('中'); w != 2 {
		t.Errorf("runeWidth(中) = %d, want 2", w)
	}
}

// A fully truncated live buffer (oldRows == 0) must not produce a negative
// cursorPhys via clamp(x, 0, -1).
func TestGridAudit_LogicalReflowEmptyLive(t *testing.T) {
	row := make([]cell, 80)
	for i := range row {
		row[i] = defaultCell()
	}
	res := logicalReflow(reflowConfig{
		scrollback: [][]cell{row},
		sbWrapped:  []bool{false},
		oldRows:    0,
		oldCols:    80,
		newRows:    24,
		newCols:    80,
	})
	if res.cursorR < 0 || res.cursorR >= 24 {
		t.Errorf("cursorR = %d, want in [0,24)", res.cursorR)
	}
}
