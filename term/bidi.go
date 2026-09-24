package term

import (
	"slices"
	"strings"

	"golang.org/x/text/unicode/bidi"
)

// rowNeedsBidi returns true if any non-null cell in cells[0:cols]
// carries a strong RTL codepoint (bidi class R or AL, RLM included).
// The gate fires exactly when reordering changes the row: explicit
// embedding/override/isolate controls without strong content are
// identity through the run-direction API below (verified against
// x/text: RLO abc PDF yields one LTR run), so they correctly skip.
// Zero allocations.
func rowNeedsBidi(cells []cell, cols int) bool {
	if cols > len(cells) {
		cols = len(cells)
	}
	for i := range cols {
		c := cells[i]
		if c.Ch == 0 {
			continue // continuation or empty/uninitialized cell
		}
		if needsReorder(c.Ch) {
			return true
		}
	}
	return false
}

// needsReorder reports whether r has a strong RTL bidi class (R or AL).
//
// ASCII fast path: every rune below 0x80 is L, EN, WS, or ON —
// never a trigger — so Latin rows skip the property-table lookup.
func needsReorder(r rune) bool {
	if r < 0x80 {
		return false
	}
	p, _ := bidi.LookupRune(r)
	cls := p.Class()
	return cls == bidi.R || cls == bidi.AL
}

// entry maps a bidi-paragraph rune index to its source cell index.
type entry struct{ cellIdx int }

// scanBidiCells makes a single pass over cells[0:cols], collecting non-null
// cells into an entry list and building the bidi-paragraph string.
//
// Exactly one rune is written per entry, so rune indices in the paragraph
// always align 1:1 with entry indices — run.Pos() output indexes entries
// directly. Multi-codepoint cluster tails (combining marks, ZWJ, VS16)
// are deliberately dropped from the ordering input: they ride on their
// base cell's position (NSM/BN tails inherit the base level anyway), and
// expanding them here would break the index alignment. The full cluster
// text still renders and copies from the cell's clusterID.
func scanBidiCells(cells []cell, cols int) (entries []entry, bidiStr string) {
	if cols <= 0 {
		return
	}
	var sb strings.Builder
	sb.Grow(cols * 4) // UTF-8 max per rune; avoids intermediate reallocations
	entries = make([]entry, 0, cols)
	for i := range cols {
		c := cells[i]
		if c.Ch == 0 {
			continue
		}
		entries = append(entries, entry{cellIdx: i})
		sb.WriteRune(c.Ch)
	}
	return entries, sb.String()
}

// visualReorder applies the Unicode Bidirectional Algorithm (UAX#9) to
// cells[0:cols] and returns a visual (screen-order, left-to-right) copy.
//
// v2l[visualCol] = logicalCol; padding entries carry -1.
//
// Returns nil, nil when no reorder trigger is detected — the common case for
// LTR-only terminals, and the result costs zero allocations.
//
// Null cells (Ch==0: continuation or empty) are excluded from the bidi
// string; every other cell — including spaces — participates, exactly as
// UAX#9 treats them. That means a short RTL line on a space-filled row
// renders paragraph-aligned (trailing spaces swing to the front), not
// left-aligned: "שלום    " displays as "    םולש". The v2l map keeps
// cursor, selection, and hit-testing on the right glyphs regardless.
//
// Mirroring (UAX#9 rule L4): ASCII brackets in right-to-left runs render
// with their mirrored glyph, so "(שלום)" displays as "(םולש)" rather
// than ")םולש(". Only single-rune cells mirror — cluster cells render
// from their interned string, and non-ASCII mirrored pairs are left
// alone. Terminal-common cases covered; full Bidi_Mirrored lookup omitted.
//
// Security note: directional controls (RLO/LRO/RLE/LRE,
// LRI/RLI/FSI/PDI) are zero-width cells preserved verbatim in storage,
// selection copy, and clipboard — standard terminal behavior, and the
// classic Trojan-Source shape (displayed order differs from byte order
// once strong RTL content is present). Level-only effects without
// strong content (e.g. RLO abc PDF) are display-inert here: x/text's
// Order surfaces direction per run, not levels, so such rows render in
// logical order. Embedders showing untrusted code should be aware;
// stripping controls on copy would corrupt legitimate RTL text, so the
// policy is preserve-and-document, not sanitize.
func visualReorder(cells []cell, cols int) (visual []cell, v2l []int) {
	if cols > len(cells) {
		cols = len(cells)
	}
	// Fast path: zero-allocation check before any allocations in scanBidiCells.
	if !rowNeedsBidi(cells, cols) {
		return nil, nil
	}
	entries, bidiStr := scanBidiCells(cells, cols)
	if len(entries) == 0 {
		return nil, nil
	}

	var p bidi.Paragraph
	if _, err := p.SetString(bidiStr); err != nil {
		return nil, nil
	}
	order, err := p.Order()
	if err != nil {
		return nil, nil
	}

	blank := defaultCell()
	visual = make([]cell, 0, cols)
	v2l = make([]int, 0, cols)

	for i := range order.NumRuns() {
		run := order.Run(i)
		// Pos returns rune indices; end is INCLUSIVE.
		first, lastIncl := run.Pos()
		last := min(
			// convert to exclusive for loop bounds
			lastIncl+1, len(entries))
		if first < 0 || first >= len(entries) {
			continue
		}

		if run.Direction() == bidi.RightToLeft {
			// RTL run: cells appear in reverse logical order on screen.
			for j := last - 1; j >= first; j-- {
				visual, v2l = appendVisualCell(visual, v2l, cells, entries[j].cellIdx, blank, cols, true)
			}
		} else {
			for j := first; j < last; j++ {
				visual, v2l = appendVisualCell(visual, v2l, cells, entries[j].cellIdx, blank, cols, false)
			}
		}
	}

	// Pad to cols with blank cells.
	for len(visual) < cols {
		visual = append(visual, blank)
		v2l = append(v2l, -1)
	}

	return visual[:cols], v2l[:cols]
}

// appendVisualCell appends cells[cellIdx] — and its continuation cell when
// Width==2 — to the visual/v2l slices, stopping at the cols capacity limit.
// With mirror, single-rune cells render their UAX#9 mirrored glyph.
//
// No dangling-wide hazard: appended output is entries plus one continuation
// per wide cell, which cannot exceed the non-null source cells, so the
// cols cap never splits a wide pair — the guard is belt-and-braces.
func appendVisualCell(visual []cell, v2l []int, cells []cell, cellIdx int, blank cell, cols int, mirror bool) ([]cell, []int) {
	if len(visual) >= cols {
		return visual, v2l
	}
	cell := cells[cellIdx]
	if mirror && cell.clusterID == 0 {
		cell.Ch = mirrorRTL(cell.Ch)
	}
	visual = append(visual, cell)
	v2l = append(v2l, cellIdx)
	if cell.Width == 2 && len(visual) < cols {
		visual = append(visual, cell.continuation())
		v2l = append(v2l, cellIdx+1) // logical continuation at cellIdx+1
	}
	return visual, v2l
}

// mirrorRTL maps ASCII bracket pairs to their UAX#9 mirrored glyphs.
// Anything else passes through: full Bidi_Mirrored coverage is omitted,
// and cluster cells never reach here (they render from clusterID text).
func mirrorRTL(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	}
	return r
}

// logicalMouseCol maps a visual viewport cell column to its logical column
// for SGR mouse reports to the child, which address the logical grid.
// Pixel-mode (?1016) reports carry raw pixels and stay visual — physical
// pixels have no logical mapping. Takes grid.Mu.
func (t *Term) logicalMouseCol(r, c int) int {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return logicalCell(t.v2lForViewportRow(r), c, t.grid.Cols)
}

// v2lForViewportRow returns the visual→logical column map for viewport
// row r, or nil when the row needs no reorder (identity). Input events call
// it: hover and motion reports on every pointer move, often twice per move.
// The maps of the last two rows seen are cached (see v2lCacheEntry), so a
// pointer moving over one RTL row reruns the bidi algorithm only when the row
// changes. The per-frame draw path uses prepareBiDi instead: its maps belong
// to the last frame and can be stale after output or a scroll. Caller holds
// grid.Mu; main thread only.
func (t *Term) v2lForViewportRow(r int) []int {
	g := t.grid
	cols := g.Cols
	if cols <= 0 || r < 0 || r >= g.Rows {
		return nil
	}
	// Gate before allocating: almost every row is LTR-only. Same check as
	// prepareBiDi's scrolled-view path.
	needBidi := false
	for c := range cols {
		if ch := g.ViewCellAt(r, c).Ch; ch != 0 && needsReorder(ch) {
			needBidi = true
			break
		}
	}
	if !needBidi {
		return nil
	}
	for i := range t.mouse.v2lCache {
		if e := &t.mouse.v2lCache[i]; e.matches(g, r, cols) {
			return e.v2l
		}
	}
	e := &t.mouse.v2lCache[t.mouse.v2lNext]
	t.mouse.v2lNext = (t.mouse.v2lNext + 1) % len(t.mouse.v2lCache)
	// Reuse the entry's buffers: after the first miss per slot, only
	// visualReorder itself allocates.
	e.scratch = slices.Grow(e.scratch[:0], cols)[:cols]
	e.ch = slices.Grow(e.ch[:0], cols)[:cols]
	e.w = slices.Grow(e.w[:0], cols)[:cols]
	for c := range cols {
		vc := g.ViewCellAt(r, c)
		e.scratch[c] = vc
		e.ch[c], e.w[c] = vc.Ch, vc.Width
	}
	_, e.v2l = visualReorder(e.scratch, cols)
	e.valid = true
	return e.v2l
}

// v2lCacheEntry is one cached visual→logical map. The key is the row content
// that visualReorder reads, rune and width per cell (see scanBidiCells and
// appendVisualCell), compared exactly. A row that moved, scrolled, or was
// rewritten therefore misses; a hash could collide and hand back a wrong map.
type v2lCacheEntry struct {
	ch      []rune
	w       []uint8
	v2l     []int
	scratch []cell
	valid   bool
}

// matches reports whether e was built from viewport row r's current content.
func (e *v2lCacheEntry) matches(g *grid, r, cols int) bool {
	if !e.valid || len(e.ch) != cols {
		return false
	}
	for c := range cols {
		vc := g.ViewCellAt(r, c)
		if vc.Ch != e.ch[c] || vc.Width != e.w[c] {
			return false
		}
	}
	return true
}

// logicalCell maps a visual cell column to its logical column.
// Padding (v2l -1, synthesized blanks with no source cell) holds position.
func logicalCell(v2l []int, v, cols int) int {
	if v2l == nil {
		return clamp(v, 0, max(cols-1, 0))
	}
	if v < 0 {
		return 0
	}
	if v >= len(v2l) {
		return max(cols-1, 0)
	}
	if l := v2l[v]; l >= 0 {
		return l
	}
	return clamp(v, 0, max(cols-1, 0))
}

// logicalBoundary maps a visual selection boundary in [0..cols] to its
// logical boundary. Point approximation for clicks and shift-extends;
// same-row drags use logicalSpan for exact glyph-set coverage.
//
// Visual boundary b is the left edge of visual cell b. In a left-to-right run
// that edge is the logical start of the cell (v2l[b]). In a right-to-left run
// the cell's logical start is on its visual right, so its left edge is the
// logical end, v2l[b]+1. The row edges follow the same rule: on a reversed row
// visual 0 is the logical end of the row and visual cols is logical 0. When no
// real cell sits right of b (the right edge, or padding), the right edge of
// cell b-1 is used, mirrored the same way.
func logicalBoundary(v2l []int, b, cols int) int {
	if v2l == nil {
		return clamp(b, 0, max(cols, 0))
	}
	b = clamp(b, 0, max(cols, 0))
	if b < len(v2l) && v2l[b] >= 0 {
		if cellRTL(v2l, b) {
			return v2l[b] + 1
		}
		return v2l[b]
	}
	if l := b - 1; l >= 0 && l < len(v2l) && v2l[l] >= 0 {
		if cellRTL(v2l, l) {
			return v2l[l]
		}
		return v2l[l] + 1
	}
	return b
}

// cellRTL reports whether visual cell v sits in a right-to-left run: a visual
// neighbor holds the next logical cell on the wrong side (right neighbor one
// lower, or left neighbor one higher). A one-cell run has no neighbor to show
// its direction and counts as left-to-right; both answers select that glyph
// alone, so the choice only moves the boundary by that one cell.
func cellRTL(v2l []int, v int) bool {
	l := v2l[v]
	if v+1 < len(v2l) && v2l[v+1] >= 0 && v2l[v+1] == l-1 {
		return true
	}
	return v > 0 && v2l[v-1] >= 0 && v2l[v-1] == l+1
}

// logicalSpan maps a visual glyph range [v0,v1) to the covering logical
// span [l0,l1). A contiguous visual range can straddle runs whose logical
// cells are disjoint; the single-span selection model takes min..max+1,
// so gap glyphs between runs select along — highlight shows exactly what
// copy will yield. ok=false when the range holds only padding.
func logicalSpan(v2l []int, v0, v1, cols int) (l0, l1 int, ok bool) {
	lo, hi := min(v0, v1), max(v0, v1)
	lo, hi = clamp(lo, 0, max(cols, 0)), clamp(hi, 0, max(cols, 0))
	if v2l == nil {
		return lo, hi, hi > lo
	}
	m0, m1 := cols, -1
	for v := lo; v < hi; v++ {
		if v < len(v2l) && v2l[v] >= 0 {
			m0, m1 = min(m0, v2l[v]), max(m1, v2l[v])
		}
	}
	if m1 < 0 {
		return 0, 0, false
	}
	return m0, m1 + 1, true
}

// visualSpan maps a logical inclusive span [l0,l1] to the covering visual
// span [v0,v1] for overlay geometry (hover underlines, hint pills).
// ok=false when no visual cell maps into the span.
func visualSpan(v2l []int, l0, l1 int) (v0, v1 int, ok bool) {
	if v2l == nil {
		return l0, l1, l1 >= l0
	}
	m0, m1 := len(v2l), -1
	for v, l := range v2l {
		if l >= l0 && l <= l1 {
			m0, m1 = min(m0, v), max(m1, v)
		}
	}
	if m1 < 0 {
		return 0, 0, false
	}
	return m0, m1, true
}

// visualPoint maps one logical cell to its visual column, or -1 when the
// cell has no visual position (excluded null). Linear scan, like the
// drawCursor lookup — cols-bounded and off the hot path.
func visualPoint(v2l []int, l int) int {
	if v2l == nil {
		return l
	}
	for v, ll := range v2l {
		if ll == l {
			return v
		}
	}
	return -1
}
