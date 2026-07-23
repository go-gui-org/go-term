package term

import (
	"regexp"
	"sort"
	"unicode/utf8"
)

// urlRe matches an implicit (un-marked-up) URL: an http/https/mailto scheme
// followed by one or more non-terminating bytes. The stop set excludes
// whitespace and the delimiter characters that commonly bracket a URL in
// prose (<>"'`). A \b anchor before the scheme prevents matching a scheme
// embedded mid-word (e.g. "xhttp://"). Trailing sentence punctuation that the
// class still admits (".,;:!?)]}") is trimmed afterwards by trimTrailingURL,
// which the RE2 grammar cannot express with balanced-paren awareness.
var urlRe = regexp.MustCompile("(?i)\\b(?:https?://|mailto:)[^\\s<>\"'`]+")

// maxURLScanRows bounds how far detectURLAt walks a soft-wrapped logical line
// in each direction from the hovered row. A URL wrapping across 8 rows at 80
// columns already spans ~640 characters, well beyond any realistic link, so
// this caps the work without ever splitting a plausible URL.
const maxURLScanRows = 8

// urlSpan is the inclusive grid-column range [C0, C1] on one content Row that a
// detected URL occupies. A URL that spans soft-wrapped rows yields one urlSpan
// per row it touches, in top-to-bottom order.
type urlSpan struct {
	Row, C0, C1 int
}

// rowWrapped reports whether content row contentRow ended with an autowrap,
// i.e. it continues into contentRow+1 as one logical line. Scrollback rows
// carry the flag in the ring; live rows in g.RowWrapped. Caller holds Mu.
func (g *grid) rowWrapped(contentRow int) bool {
	sb := g.Scrollback.Len()
	if contentRow < sb {
		return g.Scrollback.Wrapped(contentRow)
	}
	lr := contentRow - sb
	if lr < 0 || lr >= len(g.RowWrapped) {
		return false
	}
	return g.RowWrapped[lr]
}

// detectURLAt scans the logical line containing content-position cp for an
// implicit URL covering that cell, joining soft-wrapped rows so a URL broken at
// the right margin is found as one link. On a hit it returns the URL text and
// the per-row grid-column spans it covers. Caller must hold Mu.
//
// This is an on-demand path driven by Cmd-hover / Cmd-click, not the render hot
// loop, so a modest amount of work per call is fine; the persisted scratch
// buffers keep it allocation-light after warmup.
func (g *grid) detectURLAt(cp contentPos) (url string, spans []urlSpan, ok bool) {
	sb := g.Scrollback.Len()
	total := g.ContentRows()
	if g.Cols <= 0 || cp.Row < 0 || cp.Row >= total {
		return "", nil, false
	}

	// Find the logical-line bounds around cp by following wrap flags, capped at
	// maxURLScanRows per direction. On the alt screen, do not cross the
	// scrollback/live boundary — the alt buffer has its own wrap state and the
	// old main-screen scrollback below it is unrelated.
	lo := 0
	if g.AltActive {
		lo = sb
	}
	start := cp.Row
	for start > lo && start > cp.Row-maxURLScanRows && g.rowWrapped(start-1) {
		start--
	}
	end := cp.Row
	for end < total-1 && end < cp.Row+maxURLScanRows && g.rowWrapped(end) {
		end++
	}

	// Build the joined clean text of [start, end] plus rune→(row, col, byte)
	// maps, and record cp's rune index within it. Continuation cells (Ch == 0,
	// the trailing half of a wide glyph or an empty tail) are stripped by
	// searchRow, so a hover over blank space yields cpRune == -1 → no match.
	runes := g.urlRunes[:0]
	rows := g.urlRows[:0]
	cols := g.urlCols[:0]
	bytes := g.urlBytes[:0]
	cpRune := -1
	byteLen := 0
	for row := start; row <= end; row++ {
		rr, colMap := g.searchRow(row, g.searchRunes, g.searchCols)
		g.searchRunes, g.searchCols = rr, colMap
		for i, r := range rr {
			if row == cp.Row && colMap[i] == cp.Col {
				cpRune = len(runes)
			}
			runes = append(runes, r)
			rows = append(rows, row)
			cols = append(cols, colMap[i])
			bytes = append(bytes, byteLen)
			byteLen += utf8.RuneLen(r)
		}
	}
	g.urlRunes, g.urlRows, g.urlCols, g.urlBytes = runes, rows, cols, bytes
	if cpRune < 0 || len(runes) == 0 {
		return "", nil, false
	}

	// Regexp works on the byte string; map each match's byte span back to rune
	// indices via the byte-offset table (bytes[i] is the start of rune i;
	// byteLen is the one-past-end sentinel). Pick the match covering cpRune.
	line := string(runes)
	for _, m := range urlRe.FindAllStringIndex(line, -1) {
		is := sort.SearchInts(bytes, m[0])
		ie := runeIndexForByte(bytes, byteLen, m[1])
		ie = is + trimTrailingURL(runes[is:ie])
		if is >= ie || cpRune < is || cpRune >= ie {
			continue
		}
		return string(runes[is:ie]), spansFor(rows, cols, is, ie), true
	}
	return "", nil, false
}

// runeIndexForByte returns the rune index whose byte offset is b, treating b ==
// byteLen (one past the last rune) as len(bytes).
func runeIndexForByte(bytes []int, byteLen, b int) int {
	if b >= byteLen {
		return len(bytes)
	}
	return sort.SearchInts(bytes, b)
}

// spansFor groups the rune range [is, ie) of a detected URL into one urlSpan
// per content row it touches. Runes are contiguous and ordered, so each row
// appears once with its min/max grid column.
func spansFor(rows, cols []int, is, ie int) []urlSpan {
	var spans []urlSpan
	for k := is; k < ie; {
		row := rows[k]
		c0, c1 := cols[k], cols[k]
		for k < ie && rows[k] == row {
			c1 = cols[k]
			k++
		}
		spans = append(spans, urlSpan{Row: row, C0: c0, C1: c1})
	}
	return spans
}

// trimTrailingURL returns the length of runes with trailing sentence
// punctuation removed. A trailing closing bracket is kept when the span holds at
// least as many opening as closing brackets (so a link with a parenthesized
// path segment such as a Wikipedia "(disambiguation)" URL survives), otherwise
// it is trimmed. Mirrors the heuristic used by iTerm2/kitty/Windows Terminal.
//
// Pre-counts brackets once, then walks backward from the end decrementing close
// counters as punctuation is trimmed — O(n) instead of re-scanning the prefix
// for every bracket char.
func trimTrailingURL(runes []rune) int {
	var openP, clsP int
	var openB, clsB int
	var openC, clsC int
	for _, r := range runes {
		switch r {
		case '(':
			openP++
		case ')':
			clsP++
		case '[':
			openB++
		case ']':
			clsB++
		case '{':
			openC++
		case '}':
			clsC++
		}
	}
	n := len(runes)
	for n > 0 {
		switch runes[n-1] {
		case '.', ',', ';', ':', '!', '?':
			n--
		case ')':
			if openP >= clsP {
				return n
			}
			clsP--
			n--
		case ']':
			if openB >= clsB {
				return n
			}
			clsB--
			n--
		case '}':
			if openC >= clsC {
				return n
			}
			clsC--
			n--
		default:
			return n
		}
	}
	return n
}
