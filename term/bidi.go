package term

import (
	"strings"

	"golang.org/x/text/unicode/bidi"
)

// rowHasRTL returns true if any non-null, non-continuation cell in
// cells[0:cols] carries a strong RTL codepoint (bidi class R or AL).
// Zero allocations.
func rowHasRTL(cells []cell, cols int) bool {
	if cols > len(cells) {
		cols = len(cells)
	}
	for i := range cols {
		c := cells[i]
		if c.Ch == 0 {
			continue // continuation or empty/uninitialized cell
		}
		if isRTLRune(c.Ch) {
			return true
		}
	}
	return false
}

// isRTLRune reports whether r has a strong RTL bidi class (R or AL).
func isRTLRune(r rune) bool {
	p, _ := bidi.LookupRune(r)
	cls := p.Class()
	return cls == bidi.R || cls == bidi.AL
}

// entry maps a bidi-paragraph rune index to its source cell index.
type entry struct{ cellIdx int }

// scanBidiCells makes a single pass over cells[0:cols], collecting non-null
// cells into an entry list and building the bidi-paragraph string while
// simultaneously detecting strong RTL content (bidi class R or AL).
// hasRTL=false when no RTL content is found — the common LTR-only case —
// enabling early exit without further bidi processing.
func scanBidiCells(cells []cell, cols int) (entries []entry, bidiStr string, hasRTL bool) {
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
		if !hasRTL {
			hasRTL = isRTLRune(c.Ch)
		}
		entries = append(entries, entry{cellIdx: i})
		sb.WriteRune(c.Ch)
	}
	return entries, sb.String(), hasRTL
}

// visualReorder applies the Unicode Bidirectional Algorithm (UAX#9) to
// cells[0:cols] and returns a visual (screen-order, left-to-right) copy.
//
// v2l[visualCol] = logicalCol; padding entries carry -1.
//
// Returns nil, nil when no RTL content is detected — the common case for
// LTR-only terminals, and the result costs zero allocations.
//
// Null cells (Ch==0: continuation or empty) are excluded from the bidi
// string to prevent trailing blank cells from being reordered in RTL
// paragraphs. Space characters (Ch==' ') are included as content.
func visualReorder(cells []cell, cols int) (visual []cell, v2l []int) {
	if cols > len(cells) {
		cols = len(cells)
	}
	entries, bidiStr, hasRTL := scanBidiCells(cells, cols)
	if !hasRTL {
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
		last := lastIncl + 1 // convert to exclusive for loop bounds

		if last > len(entries) {
			last = len(entries)
		}
		if first < 0 || first >= len(entries) {
			continue
		}

		if run.Direction() == bidi.RightToLeft {
			// RTL run: cells appear in reverse logical order on screen.
			for j := last - 1; j >= first; j-- {
				visual, v2l = appendVisualCell(visual, v2l, cells, entries[j].cellIdx, blank, cols)
			}
		} else {
			for j := first; j < last; j++ {
				visual, v2l = appendVisualCell(visual, v2l, cells, entries[j].cellIdx, blank, cols)
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
func appendVisualCell(visual []cell, v2l []int, cells []cell, cellIdx int, blank cell, cols int) ([]cell, []int) {
	if len(visual) >= cols {
		return visual, v2l
	}
	cell := cells[cellIdx]
	visual = append(visual, cell)
	v2l = append(v2l, cellIdx)
	if cell.Width == 2 && len(visual) < cols {
		visual = append(visual, cell.continuation())
		v2l = append(v2l, cellIdx+1) // logical continuation at cellIdx+1
	}
	return visual, v2l
}
