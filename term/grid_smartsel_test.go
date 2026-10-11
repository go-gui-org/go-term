package term

import (
	"strings"
	"testing"
)

// smartText returns the text a double-click at col on row 0 of a one-row grid
// holding s selects, via unitBoundsAt — the same call the mouse path makes.
func smartText(t *testing.T, s string, col int) string {
	t.Helper()
	g := wordGrid(len(s)+4, s)
	start, end := g.unitBoundsAt(contentPos{0, col})
	if start.Row != 0 || end.Row != 0 {
		t.Fatalf("unitBoundsAt(%d) in %q left row 0: %v..%v", col, s, start, end)
	}
	return s[start.Col : end.Col+1]
}

// Each semantic unit wins over the plain word when it covers the clicked cell.
// The word around each one carries punctuation that the unit trims away.
func TestUnitBoundsAt_SemanticUnits(t *testing.T) {
	cases := []struct {
		name, line, at, want string
	}{
		// URL: brackets and sentence punctuation stay outside.
		{"url in parens", "see (https://go.dev/doc).", "go.dev", "https://go.dev/doc"},
		// Path: the flag name and the = sign are not part of the path.
		{"path after flag", "--out=/tmp/build/x.o", "build", "/tmp/build/x.o"},
		{"relative path with line", "term/grid.go:42: bad", "grid", "term/grid.go"},
		{"home path", "cd ~/src/go-term;", "src", "~/src/go-term"},
		{"dot path", "run ./bin/tool,", "bin", "./bin/tool"},
		{"windows path", `at C:\Users\me\x.txt:`, "Users", `C:\Users\me\x.txt`},
		// IPv4 with port, and an IPv4 CIDR that the path rule would also match.
		{"ipv4 port", "addr=10.0.0.12:8080,", "0.0", "10.0.0.12:8080"},
		{"ipv4 cidr", "route 192.168.1.0/24.", "168", "192.168.1.0/24"},
		{"ipv6", "via [fe80::1ff:fe23:4567:890a]:22", "1ff", "fe80::1ff:fe23:4567:890a"},
		// Hash: one side of a git range, or a digest after its algorithm name.
		{"hash in range", "abc1234..def5678", "def", "def5678"},
		{"digest", "sha256:9f86d081884c7d65", "9f86", "9f86d081884c7d65"},
		// Quoted string: only a click on a quote mark selects the whole string,
		// quotes included, and it may hold spaces.
		{"double quote open", `echo "hello world" x`, `"hello`, `"hello world"`},
		{"single quote close", `x 'a b' y`, `' y`, `'a b'`},
		{"backtick", "run `go test ./...` now", "`go", "`go test ./...`"},
		{"escaped quotes", `say "a \"b\" c" x`, `"a`, `"a \"b\" c"`},
		// Other URL schemes and scp-style remotes stay whole: the path rule
		// alone would keep only the part after the colon.
		{"ftp url", "get (ftp://host/pub/x).", "host", "ftp://host/pub/x"},
		{"scp remote", "clone git@github.com:org/repo.git,", "org", "git@github.com:org/repo.git"},
	}
	for _, tc := range cases {
		col := strings.Index(tc.line, tc.at)
		if col < 0 {
			t.Fatalf("%s: %q not in %q", tc.name, tc.at, tc.line)
		}
		if got := smartText(t, tc.line, col); got != tc.want {
			t.Errorf("%s: selected %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Without a semantic unit under the click, the plain word is selected as
// before. A click on a word inside quotes picks the word, not the string, and
// an apostrophe inside a word does not open a quoted string.
func TestUnitBoundsAt_FallsBackToWord(t *testing.T) {
	cases := []struct {
		name, line, at, want string
	}{
		{"plain word", "alpha beta", "beta", "beta"},
		{"word inside quotes", `echo "hello world"`, "world", `world"`},
		{"apostrophe", "it's 'x'", "'s", "it's"},
		// A MAC address and a clock time are not valid IP addresses.
		{"mac", "ether aa:bb:cc:dd:ee:ff", "cc", "aa:bb:cc:dd:ee:ff"},
		{"time", "at 12:34:56", "34", "12:34:56"},
		// An address netip rejects is not an IP, and the d:: inside a C++
		// scope operator is not an IPv6 address.
		{"bad ipv4", "x 999.1.1.1", "999", "999.1.1.1"},
		{"scope operator", "std::string", "d::", "std::string"},
		// All digits or all letters is a number or a word, not a hash.
		{"digits", "pid 1234567", "234", "1234567"},
		{"letters", "the deadbeef", "dead", "deadbeef"},
	}
	for _, tc := range cases {
		col := strings.Index(tc.line, tc.at)
		if col < 0 {
			t.Fatalf("%s: %q not in %q", tc.name, tc.at, tc.line)
		}
		if got := smartText(t, tc.line, col); got != tc.want {
			t.Errorf("%s: selected %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A unit broken at the right margin is found whole from either row, because the
// match runs over the joined logical line.
func TestUnitBoundsAt_SoftWrap(t *testing.T) {
	g := wordGrid(10, "x=/usr/loc", "al/bin; y")
	g.RowWrapped[0] = true
	start, end := g.unitBoundsAt(contentPos{1, 1})
	if start != (contentPos{0, 2}) || end != (contentPos{1, 5}) {
		t.Errorf("wrapped path = %v..%v, want {0 2}..{1 5}", start, end)
	}
}

// A wide glyph at the end of a unit is selected whole: the end bound covers its
// continuation cell, and a click on the continuation cell finds the unit too.
func TestUnitBoundsAt_WideGlyph(t *testing.T) {
	g := newGrid(1, 12)
	for c, ch := range "(/tmp/" {
		g.At(0, c).Ch = ch
	}
	g.At(0, 6).Ch, g.At(0, 6).Width = '中', 2
	g.At(0, 7).Ch, g.At(0, 7).Width = 0, 0
	g.At(0, 8).Ch = ')'
	for _, col := range []int{2, 7} {
		start, end := g.unitBoundsAt(contentPos{0, col})
		if start != (contentPos{0, 1}) || end != (contentPos{0, 7}) {
			t.Errorf("click %d: wide path = %v..%v, want {0 1}..{0 7}", col, start, end)
		}
	}
}

// Blank cells keep the word rule: a never-written cell and a space each select
// their blank run, never a neighbouring unit.
func TestUnitBoundsAt_Blank(t *testing.T) {
	g := wordGrid(20, "/a/b   /c/d")
	start, end := g.unitBoundsAt(contentPos{0, 5})
	if start != (contentPos{0, 4}) || end != (contentPos{0, 6}) {
		t.Errorf("blank run = %v..%v, want {0 4}..{0 6}", start, end)
	}
	start, end = g.unitBoundsAt(contentPos{0, 15})
	if start != (contentPos{0, 11}) || end != (contentPos{0, 19}) {
		t.Errorf("unwritten tail = %v..%v, want {0 11}..{0 19}", start, end)
	}
}
