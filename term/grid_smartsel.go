package term

import (
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Smart selection: the unit a double-click selects when the clicked cell is part
// of something with a known shape — a URL, a quoted string, an IP address, a
// file path, or a hex hash. Without one the plain word from wordBoundsAt is used,
// so the gesture never selects less than it did before this existed unless a
// rule says the extra characters are not part of the thing.
//
// The word rule has no punctuation class (see grid_word.go), so a word already
// holds a whole path or URL. What it cannot do is tell the thing from what is
// around it: `(https://go.dev)`, `--out=/tmp/x`, `sha256:9f86d0…`, a path with a
// `:42:` compiler suffix, and a quoted string with spaces in it. The rules below
// fix exactly those cases.
//
// Matching runs over the joined logical line (the same joinRows scan as URL
// detection), so a unit broken at the right margin is found whole. This is a
// click path, not the render loop: the regexps allocate a little per call.

// schemeRe matches a URL with any scheme, for the schemes urlRe leaves to the
// word rule (ftp://, ssh://, file://). The stop set is urlRe's.
var schemeRe = regexp.MustCompile("\\b[A-Za-z][A-Za-z0-9+.-]*://[^\\s<>\"'`]+")

// scpRe matches an scp-style remote path, user@host:path, as git and rsync
// print it.
var scpRe = regexp.MustCompile(`[\pL\pN_.-]+@[\pL\pN_.-]+:[\pL\pN_.@%+~#/-]+`)

// quoteRe matches a quoted string in one of three quote styles. Each style is a
// capture group so the match can carry one char of leading context: a single
// quote opens a string only after a char that is not a letter or digit, or
// else the apostrophe in "it's" would pair with the next quote on the line.
// Double quotes honor backslash escapes, as shells and most languages do.
var quoteRe = regexp.MustCompile("(?:^|[^\\pL\\pN])('[^']*')|(\"(?:[^\"\\\\]|\\\\.)*\")|(`[^`]*`)")

// ipv4Re matches a dotted IPv4 address, with an optional port or CIDR prefix
// length. The address part is checked with netip afterwards, so 999.1.1.1 does
// not count.
var ipv4Re = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}(?::\d{1,5}|/\d{1,2})?\b`)

// ipv6Re finds candidates for an IPv6 address: two or more colon-separated hex
// groups, with an optional prefix length. The pattern is wide on purpose; netip
// decides, which is what rejects a MAC address or a clock time.
var ipv6Re = regexp.MustCompile(`(?:[0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f]{0,4}(?:/\d{1,3})?`)

// pathRe matches a POSIX-style path with at least one slash: absolute, home
// (~/…, ~user/…), dot-relative, or a bare relative path such as term/grid.go.
// The char class leaves out ':' (so a compiler's file:line suffix stays
// outside), '=' (so --out=/tmp/x gives /tmp/x), brackets, quotes and commas.
var pathRe = regexp.MustCompile(`(?:~[\pL\pN_.-]*|[\pL\pN_.@%+~#-]+)?(?:/[\pL\pN_.@%+~#-]+)+/?`)

// winPathRe matches an absolute Windows path with a drive letter.
var winPathRe = regexp.MustCompile(`[A-Za-z]:(?:\\[\pL\pN_.@%+~#$-]+)+\\?`)

// hashRe matches a run of hex digits as long as an abbreviated git hash or
// longer. The rule below also wants at least one digit and one letter in it;
// all digits is a number and all letters is a word, and the word rule already
// selects those.
var hashRe = regexp.MustCompile(`\b[0-9A-Fa-f]{7,128}\b`)

// unitBoundsAt returns the inclusive cell bounds a double-click on p selects: the
// smart unit covering p when there is one, else the word from wordBoundsAt.
// Caller holds Mu.
func (g *grid) unitBoundsAt(p contentPos) (start, end contentPos) {
	if s, e, ok := g.smartBoundsAt(p); ok {
		return s, e
	}
	return g.wordBoundsAt(p)
}

// smartBoundsAt returns the inclusive cell bounds of the first smart unit (in
// rule order) that covers p. ok is false when no rule matches p. Caller holds
// Mu.
func (g *grid) smartBoundsAt(p contentPos) (start, end contentPos, ok bool) {
	if g.Cols <= 0 || p.Row < 0 || p.Row >= g.ContentRows() || p.Col < 0 || p.Col >= g.Cols {
		return p, p, false
	}
	// A click on the right half of a wide glyph means the glyph. The joined
	// text holds only the head cell, so look for that one.
	if p.Col > 0 {
		if c := g.ContentCellAt(p.Row, p.Col); c.Width == 0 && c.Ch == 0 &&
			g.ContentCellAt(p.Row, p.Col-1).Width == 2 {
			p.Col--
		}
	}

	runes, rows, cols, bytes, byteLen := g.joinRows(
		g.logicalLineStart(p.Row), g.logicalLineEnd(p.Row, maxURLScanRows))
	cp := -1
	for i := range runes {
		if rows[i] == p.Row && cols[i] == p.Col {
			cp = i
			break
		}
	}
	if cp < 0 {
		return p, p, false
	}

	is, ie, ok := g.smartRangeAt(runes, bytes, byteLen, cp)
	if !ok {
		return p, p, false
	}
	start = contentPos{Row: rows[is], Col: cols[is]}
	end = contentPos{Row: rows[ie-1], Col: cols[ie-1]}
	// Cover the right half of a wide glyph at the end, so the selection holds
	// the whole glyph and not half a cell of it.
	if g.ContentCellAt(end.Row, end.Col).Width == 2 && end.Col+1 < g.Cols {
		end.Col++
	}
	return start, end, true
}

// smartRangeAt runs the rules in order over a joined line and returns the rune
// range [is, ie) of the first match that covers rune cp. The order settles
// overlaps: a URL holds slashes the path rule would also take, and an IPv4 CIDR
// (10.0.0.0/8) looks like a relative path, so URL and IP go before path.
// Caller holds Mu; runes and bytes come from joinRows.
func (g *grid) smartRangeAt(runes []rune, bytes []int, byteLen, cp int) (is, ie int, ok bool) {
	// URL first, through the same detector Cmd+click uses, so a double-click
	// and a Cmd+click agree on where a link ends.
	g.urlMatches = g.urlRangesIn(runes, bytes, byteLen, g.urlMatches[:0])
	for _, m := range g.urlMatches {
		if cp >= m[0] && cp < m[1] {
			return m[0], m[1], true
		}
	}

	// trim drops trailing sentence punctuation, with the same bracket balance
	// rule as URL detection.
	trim := func(s, e int) (int, int, bool) {
		return s, s + trimTrailingURL(runes[s:e]), true
	}
	rules := [...]struct {
		re *regexp.Regexp
		// keep adjusts or rejects a match's rune range [s, e).
		keep func(s, e int) (int, int, bool)
	}{
		// Any other scheme (ftp://, ssh://, file://) is still one unit. Without
		// this rule the path rule would cut ftp://host/x down to /host/x.
		{schemeRe, trim},
		// A quoted string counts only when the click is on one of its quote
		// marks. A click on a word inside the quotes still means that word —
		// the string-wide unit is there for the case the word rule cannot
		// reach, a string with spaces in it.
		{quoteRe, func(s, e int) (int, int, bool) {
			return s, e, cp == s || cp == e-1
		}},
		{ipv4Re, func(s, e int) (int, int, bool) {
			return s, e, validIP(runes[s:e], ":/")
		}},
		{ipv6Re, func(s, e int) (int, int, bool) {
			// A trailing single colon is punctuation ("fe80::1:"), a trailing
			// double colon is part of the address ("fe80::").
			if e-s >= 2 && runes[e-1] == ':' && runes[e-2] != ':' {
				e--
			}
			// The pattern has no word boundaries, so check them here: without
			// this, the d:: in std::string is a valid address.
			if s > 0 && isWordRune(runes[s-1]) || e < len(runes) && isWordRune(runes[e]) {
				return s, e, false
			}
			return s, e, validIP(runes[s:e], "/")
		}},
		// scp-style remote (git@github.com:org/repo.git) before path, or the
		// path rule would take only the part after the colon.
		{scpRe, trim},
		{pathRe, trim},
		{winPathRe, trim},
		{hashRe, func(s, e int) (int, int, bool) {
			return s, e, isHash(runes[s:e])
		}},
	}

	text := g.appendSearchBytes(runes)
	for _, r := range rules {
		for _, m := range r.re.FindAllSubmatchIndex(text, -1) {
			// Use the first group that took part in the match, or the whole
			// match for a pattern with no groups.
			b0, b1 := m[0], m[1]
			for k := 2; k+1 < len(m); k += 2 {
				if m[k] >= 0 {
					b0, b1 = m[k], m[k+1]
					break
				}
			}
			s := sort.SearchInts(bytes, b0)
			e := runeIndexForByte(bytes, byteLen, b1)
			if s, e, ok := r.keep(s, e); ok && s < e && cp >= s && cp < e {
				return s, e, true
			}
		}
	}
	return 0, 0, false
}

// validIP reports whether r, cut at the first of the cut chars (a port or a
// prefix length), is an IP address netip accepts.
func validIP(r []rune, cut string) bool {
	s := string(r)
	if i := strings.IndexAny(s, cut); i >= 0 {
		s = s[:i]
	}
	_, err := netip.ParseAddr(s)
	return err == nil
}

// isWordRune reports whether r continues a word: a letter, a digit, or '_'.
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// isHash reports whether a hex run holds both a digit and a letter, the shape
// that tells a hash from a plain number or a word made of a–f.
func isHash(r []rune) bool {
	var digit, letter bool
	for _, c := range r {
		if c >= '0' && c <= '9' {
			digit = true
		} else {
			letter = true
		}
	}
	return digit && letter
}
