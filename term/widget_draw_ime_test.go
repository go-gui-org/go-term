package term

import (
	"math"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// imeRule is one underline rect drawn under the composition strip.
type imeRule struct{ x0, x1, h float32 }

// imeRules splits the batch of color c into its FilledRect rects. FilledRect
// emits two triangles (12 floats) per rect: vertex 0 is the top-left and
// vertex 2 the bottom-right. Rects of one color share a batch, so the split is
// what tells the clause rule apart from the rules beside it.
func imeRules(dc *gui.DrawContext, c gui.Color) []imeRule {
	var out []imeRule
	for _, b := range dc.Batches() {
		if b.Color != c {
			continue
		}
		for i := 0; i+12 <= len(b.Triangles); i += 12 {
			tr := b.Triangles[i : i+12]
			out = append(out, imeRule{x0: tr[0], x1: tr[4], h: tr[5] - tr[1]})
		}
	}
	return out
}

// During multi-clause conversion the clause being converted, [compCursor,
// compCursor+selLen), gets a thick rule and the rest of the preedit a thin one,
// as platform IMEs draw it (#326). One uniform rule hid which clause the
// candidate window belongs to.
func TestDrawIME_SelectedClauseThickUnderline(t *testing.T) {
	t.Parallel()

	const cellW, cellH = 10, 28 // thick = cellH/14 = 2
	tm, dc := newDrawTerm(4, 10, cellW, cellH)
	tm.ime.composing = true
	tm.ime.compText = "ab中cd"
	tm.ime.compCursor = 2 // clause "中c": cols 2..5
	tm.ime.compSelLen = 2
	tm.grid.CursorR, tm.grid.CursorC = 0, 1

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4}
	tm.drawIME(&ds)

	const thin = float32(cellH / 14)
	rules := imeRules(dc, tm.grid.defaultFG())
	want := []imeRule{
		{x0: tm.colX(1), x1: tm.colX(3), h: thin},     // "ab"
		{x0: tm.colX(3), x1: tm.colX(6), h: 2 * thin}, // "中c", selected
		{x0: tm.colX(6), x1: tm.colX(7), h: thin},     // "d"
	}
	if len(rules) != len(want) {
		t.Fatalf("drew rules %v, want %v", rules, want)
	}
	for i := range want {
		if rules[i] != want[i] {
			t.Errorf("rule %d = %v, want %v", i, rules[i], want[i])
		}
	}
}

// No selected clause (selLen 0, or before conversion starts) keeps the single
// thin rule across the whole strip.
func TestDrawIME_NoSelectionSingleThinUnderline(t *testing.T) {
	t.Parallel()

	const cellW, cellH = 10, 28
	tm, dc := newDrawTerm(4, 10, cellW, cellH)
	tm.ime.composing = true
	tm.ime.compText = "abcd"
	tm.ime.compCursor = 2

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4}
	tm.drawIME(&ds)

	rules := imeRules(dc, tm.grid.defaultFG())
	want := imeRule{x0: tm.colX(0), x1: tm.colX(4), h: cellH / 14}
	if len(rules) != 1 || rules[0] != want {
		t.Fatalf("drew rules %v, want one %v", rules, want)
	}
}

// A selection the platform reports past the end of the preedit (or the rune
// cap) is clipped to it instead of drawing a rule beyond the strip.
func TestDrawIME_SelectionClampedToPreedit(t *testing.T) {
	t.Parallel()

	const cellW, cellH = 10, 28
	tm, dc := newDrawTerm(4, 10, cellW, cellH)
	tm.ime.composing = true
	tm.ime.compText = "abcd"
	tm.ime.compCursor = 3
	tm.ime.compSelLen = 50

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4}
	tm.drawIME(&ds)

	rules := imeRules(dc, tm.grid.defaultFG())
	want := []imeRule{
		{x0: tm.colX(0), x1: tm.colX(3), h: cellH / 14},
		{x0: tm.colX(3), x1: tm.colX(4), h: 2 * cellH / 14},
	}
	if len(rules) != len(want) {
		t.Fatalf("drew rules %v, want %v", rules, want)
	}
	for i := range want {
		if rules[i] != want[i] {
			t.Errorf("rule %d = %v, want %v", i, rules[i], want[i])
		}
	}
}

// The rune and width slices drawIME hands to drawCursor are reused across
// frames, so composing does not allocate on every redraw.
func TestDrawIME_ReusesScratchBuffers(t *testing.T) {
	tm, dc := newDrawTerm(4, 10, 10, 20)
	tm.ime.composing = true
	tm.ime.compText = "日本語abc"
	// renderRows 0 stops drawIME after the buffers are filled, before any
	// dc call, so only drawIME's own allocations are counted.
	ds := drawState{dc: dc, g: tm.grid, cols: 10, rows: 4}
	tm.drawIME(&ds) // warm-up grows the buffers once

	allocs := testing.AllocsPerRun(100, func() { tm.drawIME(&ds) })
	if allocs != 0 {
		t.Errorf("drawIME allocated %v times per frame, want 0", allocs)
	}
	if string(ds.imeRunes) != "日本語abc" || len(ds.imeWidths) != 6 {
		t.Errorf("runes %q widths %v", string(ds.imeRunes), ds.imeWidths)
	}
}

// Widening the selected clause keeps the preedit text and its start the same,
// so the selection length must be part of View's change key or the thick rule
// would stay on the old span until something else forced a redraw.
func TestTerm_IMESelLenChangeRedraws(t *testing.T) {
	win := gui.NewWindow(gui.WindowCfg{})
	term, err := New(win, Cfg{})
	if err != nil {
		t.Fatalf("New term: %v", err)
	}
	defer func() { _ = term.Close() }()

	compose := func(selLen int32) {
		win.EventFn(&gui.Event{
			Type:      gui.EventIMEComposition,
			IMEText:   "わたしは",
			IMEStart:  0,
			IMELength: selLen,
		})
		_ = term.View(win).GenerateLayout(win)
	}
	compose(1)
	if term.ime.compSelLen != 1 {
		t.Fatalf("compSelLen = %d, want 1", term.ime.compSelLen)
	}
	before := term.drawVersion.Load()
	compose(3)
	if term.ime.compSelLen != 3 {
		t.Errorf("compSelLen = %d, want 3", term.ime.compSelLen)
	}
	if term.drawVersion.Load() == before {
		t.Error("selection-length change did not bump the draw version")
	}
}

// triangleFloats counts the vertex floats in every batch, so a test can tell
// whether a draw call added anything to the context.
func triangleFloats(dc *gui.DrawContext) int {
	n := 0
	for _, b := range dc.Batches() {
		n += len(b.Triangles)
	}
	return n
}

// While a clause is selected the caret is hidden, as macOS and Windows hide the
// insertion point during conversion. Drawn at the clause start, a block caret
// covered the first cell of the thick clause rule and made the clause look one
// cell short (#329). The candidate-window rect must still be staged at the
// clause start, or the candidate list would lose its anchor.
func TestDrawCursor_HiddenWhileClauseSelected(t *testing.T) {
	t.Parallel()

	const cellW, cellH = 10, 28
	tm, dc := newDrawTerm(4, 10, cellW, cellH)
	tm.ime.composing = true
	tm.ime.compText = "ab中cd"
	tm.ime.compCursor = 2 // clause "中c" starts at col 1+2 = 3
	tm.ime.compSelLen = 2
	tm.grid.CursorR, tm.grid.CursorC = 0, 1

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4, bidiV2LRows: make([][]int, 4)}
	tm.drawIME(&ds)
	before := triangleFloats(dc)
	tm.drawCursor(&ds)

	if got := triangleFloats(dc); got != before {
		t.Errorf("drawCursor drew %d vertex floats over the selected clause, want 0",
			got-before)
	}
	if !ds.pendingIME {
		t.Fatal("candidate-window rect not staged while the caret is hidden")
	}
	if want := float32(3 * cellW); ds.imeX != want {
		t.Errorf("candidate rect x = %v, want %v (clause start)", ds.imeX, want)
	}
}

// Without a selected clause the caret still marks the insertion point inside
// the preedit.
func TestDrawCursor_ShownWithoutClauseSelection(t *testing.T) {
	t.Parallel()

	tm, dc := newDrawTerm(4, 10, 10, 28)
	tm.ime.composing = true
	tm.ime.compText = "abcd"
	tm.ime.compCursor = 2

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4, bidiV2LRows: make([][]int, 4)}
	tm.drawIME(&ds)
	before := triangleFloats(dc)
	tm.drawCursor(&ds)

	if triangleFloats(dc) == before {
		t.Error("caret not drawn in a preedit with no selected clause")
	}
	if !ds.pendingIME {
		t.Error("candidate-window rect not staged")
	}
}

// A platform length near the int limit must not wrap the clause end negative
// and drop the selection: the clause runs to the end of the preedit.
func TestDrawIME_HugeSelLenSelectsToEnd(t *testing.T) {
	t.Parallel()

	tm, dc := newDrawTerm(4, 10, 10, 28)
	tm.ime.composing = true
	tm.ime.compText = "abcd"
	tm.ime.compCursor = 1
	tm.ime.compSelLen = math.MaxInt

	ds := drawState{dc: dc, style: tm.style(), g: tm.grid, cols: 10, rows: 4, renderRows: 4}
	tm.drawIME(&ds)

	if !ds.imeClauseSel {
		t.Fatal("selection lost for a length near MaxInt")
	}
	rules := imeRules(dc, tm.grid.defaultFG())
	want := []imeRule{
		{x0: tm.colX(0), x1: tm.colX(1), h: 28 / 14},
		{x0: tm.colX(1), x1: tm.colX(4), h: 2 * 28 / 14},
	}
	if len(rules) != len(want) || rules[0] != want[0] || rules[1] != want[1] {
		t.Fatalf("drew rules %v, want %v", rules, want)
	}
}
