package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The generated table is embedded and decoded at runtime by a decoder that is
// deliberately total — it skips any line it cannot parse rather than failing.
// That makes these tests the only thing standing between a malformed upstream
// file and a theme silently missing from the corpus, so they check the two
// properties the decoder relies on: every emitted line is well-formed, and
// anything that could not produce a well-formed line was rejected here.

// writeTheme writes a minimal Ghostty theme file with the given extra lines
// appended, and returns its directory.
func writeTheme(t *testing.T, dir, name string, extra ...string) {
	t.Helper()
	var sb strings.Builder
	for i := range 16 {
		// Distinct per slot so a mis-indexed palette entry shows up.
		sb.WriteString("palette = " + strconv.Itoa(i) + "=#0" + hexDigit(i) + "0102\n")
	}
	sb.WriteString("background = #101112\n")
	sb.WriteString("foreground = #f0f1f2\n")
	for _, l := range extra {
		sb.WriteString(l + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hexDigit(i int) string { return string("0123456789abcdef"[i]) }

func TestNormHex(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in, want string
	}{
		{"#AABBCC", "aabbcc"},
		{"aabbcc", "aabbcc"},
		{"  #AaBbCc  ", "aabbcc"},
		{"#abc", "aabbcc"},   // CSS-style shorthand expands
		{"#ABC", "aabbcc"},   // …case-insensitively
		{"", ""},             // empty
		{"#ab", ""},          // too short
		{"#aabbccdd", ""},    // too long
		{"#gggggg", ""},      // not hex
		{"#aabbc", ""},       // five digits
		{"rgb:aa/bb/cc", ""}, // X11 form is not accepted
	} {
		if got := normHex(tc.in); got != tc.want {
			t.Errorf("normHex(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUsableName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"Gruvbox Dark", true},
		{"Rosé Pine", true},
		{"", false},
		{" leading", false},
		{"trailing ", false},
		{"has\ttab", false},
		{"has\nnewline", false},
		{"has\rcarriage", false},
	} {
		if got := usableName(tc.in); got != tc.want {
			t.Errorf("usableName(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// A theme missing any of its 18 colors must be rejected rather than emitted
// with holes: an unset color decodes to gui.Color's zero value, which renders
// transparent.
func TestThemeComplete(t *testing.T) {
	t.Parallel()

	full := func() *theme {
		th := &theme{name: "x", fg: "ffffff", bg: "000000"}
		for i := range 16 {
			th.ansi[i], th.seen[i] = "010203", true
		}
		return th
	}
	if !full().complete() {
		t.Fatal("a fully-populated theme reported incomplete")
	}
	for _, tc := range []struct {
		name   string
		mangle func(*theme)
	}{
		{"missing ANSI 0", func(th *theme) { th.seen[0] = false }},
		{"missing ANSI 15", func(th *theme) { th.seen[15] = false }},
		{"missing fg", func(th *theme) { th.fg = "" }},
		{"missing bg", func(th *theme) { th.bg = "" }},
	} {
		th := full()
		tc.mangle(th)
		if th.complete() {
			t.Errorf("%s: reported complete", tc.name)
		}
	}
}

// hex is what lands in the table; its width is the decoder's only structural
// check, so it must be exactly 18 triples in ANSI-then-fg-then-bg order.
func TestThemeHexLayout(t *testing.T) {
	t.Parallel()

	th := &theme{fg: "aabbcc", bg: "ddeeff"}
	for i := range 16 {
		th.ansi[i] = hexDigit(i) + "00000"[:5]
	}
	got := th.hex()
	if len(got) != 18*6 {
		t.Fatalf("payload length = %d, want %d", len(got), 18*6)
	}
	if got[15*6:16*6] != "f00000" {
		t.Errorf("ANSI 15 slot = %q, want %q", got[15*6:16*6], "f00000")
	}
	if got[16*6:17*6] != "aabbcc" {
		t.Errorf("fg slot = %q, want %q", got[16*6:17*6], "aabbcc")
	}
	if got[17*6:] != "ddeeff" {
		t.Errorf("bg slot = %q, want %q", got[17*6:], "ddeeff")
	}
}

// parseFile has to survive the shapes upstream files actually take: comments,
// blank lines, keys go-term does not model, and 256-color palette overrides
// that fall outside term.Theme's 16 slots.
func TestParseFile_IgnoresUnmodelledKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "Sample",
		"# a comment",
		"",
		"cursor-color = #ff0000",
		"selection-background = #00ff00",
		"palette = 200=#123456", // beyond ANSI 15
		"palette = -1=#123456",  // negative index
		"palette = zz=#123456",  // non-numeric index
		"palette = 3",           // no '=' in the value
	)

	th, err := parseFile(filepath.Join(dir, "Sample"))
	if err != nil {
		t.Fatal(err)
	}
	if !th.complete() {
		t.Fatal("theme reported incomplete despite all 18 colors present")
	}
	if th.name != "Sample" {
		t.Errorf("name = %q, want %q", th.name, "Sample")
	}
	if th.bg != "101112" || th.fg != "f0f1f2" {
		t.Errorf("fg/bg = %q/%q, want f0f1f2/101112", th.fg, th.bg)
	}
	if th.ansi[3] != "030102" {
		t.Errorf("ANSI 3 = %q, want %q — an out-of-range override overwrote it",
			th.ansi[3], "030102")
	}
}

func TestParseFile_MissingFileReturnsError(t *testing.T) {
	t.Parallel()

	if _, err := parseFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing file returned no error")
	}
}

// parseDir is the gate: incomplete themes and unusable names are counted as
// skipped, never emitted, and subdirectories are stepped over.
func TestParseDir_SkipsUnusable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "Good")
	writeTheme(t, dir, "Also Good")
	writeTheme(t, dir, "Bad\tName")
	if err := os.WriteFile(filepath.Join(dir, "Incomplete"),
		[]byte("background = #000000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o750); err != nil {
		t.Fatal(err)
	}

	themes, skipped, err := parseDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(themes) != 2 {
		names := make([]string, len(themes))
		for i, th := range themes {
			names[i] = th.name
		}
		t.Fatalf("kept %v, want the two good themes only", names)
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2 (incomplete + unusable name)", skipped)
	}
}

func TestParseDir_MissingDirReturnsError(t *testing.T) {
	t.Parallel()

	if _, _, err := parseDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("missing directory returned no error")
	}
}

// The table's shape is the decoder's contract: a comment header, then one
// "<name>\t<108 hex digits>" line per theme and nothing else.
func TestWriteTable_EveryLineDecodable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTheme(t, dir, "Zed")
	writeTheme(t, dir, "alpha")
	themes, _, err := parseDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "themes.txt")
	if err := writeTable(out, "deadbeef", themes); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "deadbeef") {
		t.Error("header does not record the upstream SHA")
	}

	var data int
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		data++
		name, payload, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("line %q has no TAB separator", line)
		}
		if !usableName(name) {
			t.Errorf("emitted unusable name %q", name)
		}
		if len(payload) != 18*6 {
			t.Errorf("%q: payload is %d chars, want %d", name, len(payload), 18*6)
		}
		for i := range len(payload) {
			if !isHexDigit(payload[i]) {
				t.Errorf("%q: payload has non-hex byte %q", name, payload[i])
				break
			}
		}
	}
	if data != 2 {
		t.Errorf("emitted %d data lines, want 2", data)
	}
}

// writeDocs is user-facing, but the count and attribution in it are the two
// things a stale regeneration gets wrong silently.
func TestWriteDocs_RecordsCountAndSHA(t *testing.T) {
	t.Parallel()

	themes := []*theme{{name: "One"}, {name: "Two"}}
	out := filepath.Join(t.TempDir(), "themes.md")
	if err := writeDocs(out, "cafebabe", themes); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cafebabe", "## Names (2)", "- One", "- Two",
		"Mark Badolato", "GENERATED FILE"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("docs missing %q", want)
		}
	}
}

func TestWriteTable_UnwritablePathReturnsError(t *testing.T) {
	t.Parallel()

	// A path whose parent is a regular file cannot be created.
	base := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeTable(filepath.Join(base, "table.txt"), "sha", nil); err == nil {
		t.Error("writeTable to an unwritable path returned no error")
	}
	if err := writeDocs(filepath.Join(base, "docs.md"), "sha", nil); err == nil {
		t.Error("writeDocs to an unwritable path returned no error")
	}
}
