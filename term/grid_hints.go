package term

import "sort"

// maxHintTargets caps how many links one hint pass will label. It is exactly
// the two-character label space, so the scan can never outrun the alphabet
// that has to name its results — see hintLabels. Well past the point where
// picking a link by eye beats searching for it, which is why the cap costs
// nothing in practice.
const maxHintTargets = len(hintAlphabet) * len(hintAlphabet)

// hintTarget is one keyboard-reachable link in the viewport: the URL to act on,
// plus the per-row grid spans it occupies so the overlay can underline it and
// anchor a label at its first span.
//
// spans hold only the *visible* rows of the link — a URL running off the top or
// bottom of the viewport keeps its full url text but is labelled where it shows.
type hintTarget struct {
	url   string
	spans []urlSpan
}

// hintTargets collects every link visible in the viewport in reading order
// (top-to-bottom, then left-to-right), appending into dst[:0] so a repeat pass
// reuses the caller's backing array. Caller must hold Mu.
//
// Two sources are merged, mirroring what Cmd+click already does: explicit OSC 8
// hyperlinks (grid.links, addressed by cell.LinkID) take precedence, and
// implicit regexp-detected URLs fill in the rest. Without that precedence a
// terminal running `ls --hyperlink` would show a link that is clickable but not
// hintable, or hint the same link twice.
func (g *grid) hintTargets(dst []hintTarget) []hintTarget {
	dst = dst[:0]
	if g.Cols <= 0 || g.Rows <= 0 {
		return dst
	}
	first, last := g.viewportRowRange()
	if first > last {
		return dst
	}

	// OSC 8 first: the explicit set is what the implicit pass is filtered
	// against, so it has to exist before that pass runs. osc is a snapshot of
	// just those entries — the implicit pass may grow dst and move its backing
	// array, but the snapshot keeps addressing the OSC targets either way.
	g.collectOSCLinks(&dst, first, last)
	osc := dst
	g.collectImplicitURLs(&dst, first, last, osc)

	// Both passes emit in row order individually, but interleaving them means a
	// final sort is what actually produces reading order.
	sort.SliceStable(dst, func(i, j int) bool {
		a, b := dst[i].spans[0], dst[j].spans[0]
		if a.Row != b.Row {
			return a.Row < b.Row
		}
		return a.C0 < b.C0
	})
	return dst
}

// viewportRowRange returns the inclusive content-row range currently on screen.
// The range is contiguous even when ViewOffset exceeds Rows, because the
// viewport is always Rows rows starting at the scroll position. Caller holds Mu.
func (g *grid) viewportRowRange() (first, last int) {
	sb := g.Scrollback.Len()
	first = sb - clamp(g.ViewOffset, 0, sb)
	last = min(first+g.Rows-1, g.ContentRows()-1)
	return first, last
}

// collectOSCLinks appends one target per visible OSC 8 hyperlink run.
// Caller holds Mu.
func (g *grid) collectOSCLinks(dst *[]hintTarget, first, last int) {
	start := len(*dst)
	for row := first; row <= last; row++ {
		for col := 0; col < g.Cols; {
			id := g.ContentCellAt(row, col).LinkID
			if id == 0 {
				col++
				continue
			}
			// Extend the run while the link ID holds.
			c0 := col
			for col < g.Cols && g.ContentCellAt(row, col).LinkID == id {
				col++
			}
			c1 := col - 1

			// Merge into the previous target when this is the continuation of a
			// wrapped link rather than a second link that happens to share the
			// URL (identical URLs intern to one ID, so the ID alone can't tell
			// them apart). A real wrap runs to the right margin and resumes at
			// column 0 on the very next row.
			if n := len(*dst); n > start && c0 == 0 {
				prev := &(*dst)[n-1]
				tail := prev.spans[len(prev.spans)-1]
				if tail.Row == row-1 && tail.C1 == g.Cols-1 && prev.url == g.LinkURL(id) {
					prev.spans = append(prev.spans, urlSpan{Row: row, C0: c0, C1: c1})
					continue
				}
			}
			url := g.LinkURL(id)
			if url == "" {
				continue // registry evicted or never held this ID
			}
			if len(*dst) >= maxHintTargets {
				return
			}
			*dst = append(*dst, hintTarget{
				url:   url,
				spans: []urlSpan{{Row: row, C0: c0, C1: c1}},
			})
		}
	}
}

// collectImplicitURLs appends one target per regexp-detected URL visible in the
// viewport, skipping any that overlaps a cell already claimed by one of the
// explicit OSC 8 targets in osc. Caller holds Mu.
func (g *grid) collectImplicitURLs(dst *[]hintTarget, first, last int, osc []hintTarget) {
	for row := first; row <= last; {
		// Join from the true start of the logical line, not from the viewport
		// edge: a URL that begins above the fold must still be recovered whole,
		// or the opened link would be a truncated prefix of the real one.
		start := g.logicalLineStart(row)
		end := g.logicalLineEnd(row, maxURLScanRows)
		runes, rows, cols, bytes, byteLen := g.joinRows(start, end)
		if len(runes) > 0 {
			g.urlMatches = urlRangesIn(runes, bytes, byteLen, g.urlMatches[:0])
			for _, m := range g.urlMatches {
				if len(*dst) >= maxHintTargets {
					return
				}
				spans := visibleSpans(spansFor(rows, cols, m[0], m[1]), first, last)
				if len(spans) == 0 || spansOverlapAny(spans, osc) {
					continue
				}
				*dst = append(*dst, hintTarget{url: string(runes[m[0]:m[1]]), spans: spans})
			}
		}
		row = end + 1
	}
}

// visibleSpans filters spans down to the rows within [first, last], in place.
// A link may extend past either viewport edge; only the on-screen part can
// carry a label or an underline.
func visibleSpans(spans []urlSpan, first, last int) []urlSpan {
	out := spans[:0]
	for _, sp := range spans {
		if sp.Row >= first && sp.Row <= last {
			out = append(out, sp)
		}
	}
	return out
}

// spansOverlapAny reports whether any span shares a cell with any target in
// targets. Target counts are bounded by what fits on one screen, so the
// quadratic scan is cheaper than building an index.
func spansOverlapAny(spans []urlSpan, targets []hintTarget) bool {
	for _, sp := range spans {
		for i := range targets {
			for _, other := range targets[i].spans {
				if other.Row == sp.Row && sp.C0 <= other.C1 && other.C0 <= sp.C1 {
					return true
				}
			}
		}
	}
	return false
}
