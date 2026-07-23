package term

// VT420 rectangular area operations and DECSCA character protection.
//
// The family: DECSCA (CSI Ps " q) marks characters protected; DECSEL/DECSED
// (grid_edit.go) and DECSERA below are the only functions that honor that
// mark. DECERA/DECFRA/DECCARA/DECRARA/DECCRA operate on a rectangle of the
// page regardless of protection, matching xterm — only its
// ScrnWipeRectangle (DECSERA) tests the PROTECTED bit.
//
// Coordinates arrive 1-based and are subject to origin mode (DECOM), like
// CUP. Left/right margins (DECLRMM) are not implemented, so the horizontal
// extent is always the full page width.

// SetProtection applies DECSCA (CSI Ps " q). Ps=1 protects the characters
// written from here on; 0 and 2 (and anything unrecognized, per xterm's
// lenient reading) make them erasable again. Protection rides in CurAttrs so
// DECSC/DECRC and the alt-screen swap carry it, but it is not an SGR
// attribute: applySGR preserves the bit across SGR 0.
func (g *grid) SetProtection(ps int) {
	if ps == 1 {
		g.CurAttrs |= attrProtected
		return
	}
	g.CurAttrs &^= attrProtected
}

// SetRectExtent applies DECSACE (CSI Ps * x), selecting whether DECCARA and
// DECRARA treat their four parameters as a rectangle (Ps=2) or as a stream of
// character positions running from the first corner to the second (Ps=0/1,
// the power-on default).
func (g *grid) SetRectExtent(ps int) {
	if ps == 2 {
		g.RectExtent = 2
		return
	}
	g.RectExtent = 0
}

// rect is a resolved, page-clamped rectangle in 0-based grid coordinates,
// inclusive on all four edges.
type rect struct{ top, left, bottom, right int }

// rectBounds resolves the Pt;Pl;Pb;Pr parameter quartet. Values are 1-based;
// 0 (or a missing parameter) means the default — first row/column for the
// top/left, last row/column for the bottom/right. Under origin mode rows are
// relative to the scroll region and clipped to it, matching CUP.
//
// ok is false when the area is degenerate — the second corner precedes the
// first — which VT510 says the terminal ignores. Under the DECSACE stream
// extent the columns may run backwards (the run wraps through the rows
// between), so the column ordering is only enforced when stream is false or
// the area is a single row.
func (g *grid) rectBounds(pt, pl, pb, pr int, stream bool) (rect, bool) {
	rowOrigin, rowLast := 0, g.Rows-1
	if g.OriginMode && g.regionValid() {
		rowOrigin, rowLast = g.Top, g.Bottom
	}
	// A zero/absent parameter selects the extreme in that direction.
	if pt == 0 {
		pt = 1
	}
	if pl == 0 {
		pl = 1
	}
	if pb == 0 {
		pb = rowLast - rowOrigin + 1
	}
	if pr == 0 {
		pr = g.Cols
	}
	r := rect{
		top:    clamp(rowOrigin+pt-1, rowOrigin, rowLast),
		left:   clamp(pl-1, 0, g.Cols-1),
		bottom: clamp(rowOrigin+pb-1, rowOrigin, rowLast),
		right:  clamp(pr-1, 0, g.Cols-1),
	}
	if r.top > r.bottom {
		return rect{}, false
	}
	if r.left > r.right && (!stream || r.top == r.bottom) {
		return rect{}, false
	}
	return r, true
}

// rowSpan returns the [from,to) column span an operation covers on row r.
// For a rectangle that is the rectangle's own columns on every row. For the
// DECSACE stream extent it is the run of positions from (top,left) through
// (bottom,right) in reading order: the first row runs to the page edge, rows
// between are complete, and the last row starts at column 0.
func (g *grid) rowSpan(rc rect, r int, stream bool) (from, to int) {
	if !stream || rc.top == rc.bottom {
		return rc.left, rc.right + 1
	}
	switch r {
	case rc.top:
		return rc.left, g.Cols
	case rc.bottom:
		return 0, rc.right + 1
	default:
		return 0, g.Cols
	}
}

// wideEdge says what an operation does to a double-width pair straddling the
// edge of its span.
type wideEdge uint8

const (
	// splitWide blanks the half left outside the span, so no orphaned glyph
	// half survives. Used by the destructive operations.
	splitWide wideEdge = iota
	// splitWideErasable does the same, except on a protected pair — DECSERA
	// must not damage what it is forbidden to erase. Head and continuation
	// always share Attrs (see cell.continuation), so testing the cell inside
	// the span settles it for both halves.
	splitWideErasable
	// keepWide leaves the pair intact: DECCARA/DECRARA change attributes, and
	// blanking a character they were only meant to restyle would lose text.
	keepWide
)

// eachRectRow walks the rows of rc, handing fn the row's cell slice and the
// [from,to) span to touch, then marks the row dirty. RowWrapped is
// deliberately untouched: these are partial-row edits, like EL.
func (g *grid) eachRectRow(rc rect, stream bool, edge wideEdge, fn func(row []cell, from, to int)) {
	for r := rc.top; r <= rc.bottom; r++ {
		from, to := g.rowSpan(rc, r, stream)
		if from >= to {
			continue
		}
		g.splitWideAt(r, from, edge)
		g.splitWideAt(r, to-1, edge)
		fn(g.Cells[r*g.Cols:(r+1)*g.Cols], from, to)
		g.markDirty(r)
	}
}

// splitWideAt blanks the far half of a wide pair covering (r,c) per edge.
func (g *grid) splitWideAt(r, c int, edge wideEdge) {
	switch edge {
	case keepWide:
		return
	case splitWideErasable:
		if cell := g.At(r, c); cell == nil || cell.Attrs&attrProtected != 0 {
			return
		}
	}
	g.eraseWideAt(r, c)
}

// EraseRect implements DECERA (CSI Pt;Pl;Pb;Pr $ z): blank every cell in the
// rectangle. Protection does not apply — DECSERA is the selective form.
// Cells take the current bg/attrs like every other erase here (BCE), rather
// than VT510's "no visual attributes".
func (g *grid) EraseRect(pt, pl, pb, pr int) { g.eraseRect(pt, pl, pb, pr, false) }

// SelectiveEraseRect implements DECSERA (CSI Pt;Pl;Pb;Pr $ {): EraseRect
// that steps over DECSCA-protected cells.
func (g *grid) SelectiveEraseRect(pt, pl, pb, pr int) { g.eraseRect(pt, pl, pb, pr, true) }

func (g *grid) eraseRect(pt, pl, pb, pr int, selective bool) {
	rc, ok := g.rectBounds(pt, pl, pb, pr, false)
	if !ok {
		return
	}
	edge := splitWide
	if selective {
		edge = splitWideErasable
	}
	blank := blankCell(g.CurFG, g.CurBG, g.CurAttrs)
	g.eachRectRow(rc, false, edge, func(row []cell, from, to int) {
		for c := from; c < to; c++ {
			if selective && row[c].Attrs&attrProtected != 0 {
				continue
			}
			row[c] = blank
		}
	})
}

// FillRect implements DECFRA (CSI Pch;Pt;Pl;Pb;Pr $ x): fill the rectangle
// with character Pch, which takes the current SGR attributes. Pch must be a
// printable single-width code in 32..126 or 160..255 (VT510's ranges); any
// other value makes the whole command a no-op rather than painting garbage.
func (g *grid) FillRect(pch, pt, pl, pb, pr int) {
	if !validFillChar(pch) {
		return
	}
	rc, ok := g.rectBounds(pt, pl, pb, pr, false)
	if !ok {
		return
	}
	fill := cell{
		Ch: rune(pch), FG: g.CurFG, BG: g.CurBG, ULColor: g.CurULColor,
		Attrs: g.CurAttrs, Width: 1, ULStyle: g.CurULStyle, LinkID: g.CurLinkID,
	}
	g.eachRectRow(rc, false, splitWide, func(row []cell, from, to int) {
		for c := from; c < to; c++ {
			row[c] = fill
		}
	})
	// A fill is a graphic write, so REP repeats it.
	g.lastGraphic, g.lastGraphicID, g.lastGraphicW = fill.Ch, 0, 1
}

// validFillChar reports whether pch is a legal DECFRA fill character. The two
// ranges are GL and GR printable space; C0/C1 and anything past 255 would be
// meaningless or (for wide characters) break the single-cell fill model.
func validFillChar(pch int) bool {
	return (pch >= 32 && pch <= 126) || (pch >= 160 && pch <= 255)
}

// ChangeAttrsRect implements DECCARA (CSI Pt;Pl;Pb;Pr;Ps… $ r): turn the
// listed attributes on or off across the area, leaving the characters and
// their colors alone. ReverseAttrsRect (DECRARA, $ t) toggles the same set
// instead. Both honor DECSACE: rectangle or stream extent.
//
// The parameter set is VT510's: 0 (all attributes off / all toggled), 1 bold,
// 4 underline, 5 blink, 7 negative image, and the 22/24/25/27 off-forms.
// Anything else is ignored — notably SGR colors, which xterm accepts as an
// extension but which would need per-cell color rewriting here.
func (g *grid) ChangeAttrsRect(params []int) { g.markAttrsRect(params, false) }

// ReverseAttrsRect implements DECRARA (CSI Pt;Pl;Pb;Pr;Ps… $ t).
func (g *grid) ReverseAttrsRect(params []int) { g.markAttrsRect(params, true) }

// deccaraAttrs is the attribute set DECCARA/DECRARA may touch.
const deccaraAttrs = attrBold | attrUnderline | attrBlink | attrInverse

func (g *grid) markAttrsRect(params []int, reverse bool) {
	stream := g.RectExtent != 2
	rc, ok := g.rectBounds(paramAt(params, 0), paramAt(params, 1),
		paramAt(params, 2), paramAt(params, 3), stream)
	if !ok {
		return
	}
	var set, clear uint16
	for _, ps := range params[min(4, len(params)):] {
		switch ps {
		case 0:
			// "All attributes off" — under DECRARA this toggles all four.
			set |= deccaraAttrs
			clear |= deccaraAttrs
		case 1:
			set |= attrBold
		case 4:
			set |= attrUnderline
		case 5:
			set |= attrBlink
		case 7:
			set |= attrInverse
		case 22:
			clear |= attrBold
		case 24:
			clear |= attrUnderline
		case 25:
			clear |= attrBlink
		case 27:
			clear |= attrInverse
		}
	}
	if reverse {
		// DECRARA has no on/off distinction: every named attribute flips.
		set |= clear
		clear = 0
	} else {
		// A parameter list carrying both forms of one attribute (e.g. "1;22")
		// resolves as off, matching a left-to-right SGR reading of the tail.
		set &^= clear
	}
	if set == 0 && clear == 0 {
		return
	}
	g.eachRectRow(rc, stream, keepWide, func(row []cell, from, to int) {
		for c := from; c < to; c++ {
			if reverse {
				row[c].Attrs ^= set
			} else {
				row[c].Attrs = row[c].Attrs&^clear | set
			}
			// An underline turned on here needs a style to draw with; one
			// turned off leaves none behind.
			switch {
			case row[c].Attrs&attrUnderline == 0:
				row[c].ULStyle = ulNone
			case row[c].ULStyle == ulNone:
				row[c].ULStyle = ulSingle
			}
		}
	})
}

// CopyRect implements DECCRA (CSI Pts;Pls;Pbs;Prs;Pps;Ptd;Pld;Ppd $ v): copy
// the source rectangle to a destination whose top-left corner is (Ptd,Pld),
// clipping whatever falls off the page. The whole cell travels — glyph,
// colors, link, grapheme cluster and protection alike.
//
// Source and destination may overlap: the source is staged in a scratch
// buffer first, exactly as VT510 describes. The page parameters are parsed
// and ignored; this terminal has a single page, and per spec an out-of-range
// page clamps to the last available one.
func (g *grid) CopyRect(pts, pls, pbs, prs, ptd, pld int) {
	src, ok := g.rectBounds(pts, pls, pbs, prs, false)
	if !ok {
		return
	}
	// The destination corner resolves through the same origin-mode rules, but
	// it is a point, not an extent: a bare rectBounds call would clamp it into
	// a rectangle and hide an off-page corner.
	rowOrigin, rowLast := 0, g.Rows-1
	if g.OriginMode && g.regionValid() {
		rowOrigin, rowLast = g.Top, g.Bottom
	}
	if ptd == 0 {
		ptd = 1
	}
	if pld == 0 {
		pld = 1
	}
	dstTop := clamp(rowOrigin+ptd-1, rowOrigin, rowLast)
	dstLeft := clamp(pld-1, 0, g.Cols-1)

	// Clip the copied extent to whatever fits below/right of the destination.
	rows := min(src.bottom-src.top+1, rowLast-dstTop+1)
	cols := min(src.right-src.left+1, g.Cols-dstLeft)
	if rows <= 0 || cols <= 0 {
		return
	}
	if dstTop == src.top && dstLeft == src.left {
		return // copy onto itself
	}

	// Stage the source so an overlapping destination reads pre-copy content.
	if cap(g.rectBuf) < rows*cols {
		g.rectBuf = make([]cell, rows*cols)
	}
	buf := g.rectBuf[:rows*cols]
	for r := range rows {
		copy(buf[r*cols:(r+1)*cols], g.Cells[(src.top+r)*g.Cols+src.left:])
	}
	for r := range rows {
		dr := dstTop + r
		g.eraseWideAt(dr, dstLeft)
		g.eraseWideAt(dr, dstLeft+cols-1)
		copy(g.Cells[dr*g.Cols+dstLeft:dr*g.Cols+dstLeft+cols], buf[r*cols:(r+1)*cols])
		g.sanitizeWideEdges(dr, dstLeft, dstLeft+cols)
		g.markDirty(dr)
	}
}

// sanitizeWideEdges repairs half a wide character left at either edge of the
// span [from,to) on row r — a continuation whose head was clipped away by the
// copy, or a head whose continuation was. Both degrade to a blank carrying
// the cell's own colors, so the row keeps its background.
func (g *grid) sanitizeWideEdges(r, from, to int) {
	if from >= to {
		return
	}
	row := g.Cells[r*g.Cols : (r+1)*g.Cols]
	if head := row[from]; head.Width == 0 && head.Ch == 0 {
		row[from] = blankCell(head.FG, head.BG, head.Attrs)
	}
	if tail := row[to-1]; tail.Width == 2 {
		row[to-1] = blankCell(tail.FG, tail.BG, tail.Attrs)
	}
}

// paramAt returns params[i] or 0 (meaning "default") when the list is short.
// The rectangle operations read their corners positionally, so unlike
// parser.param they cannot substitute a per-position default here.
func paramAt(params []int, i int) int {
	if i < 0 || i >= len(params) {
		return 0
	}
	return params[i]
}
