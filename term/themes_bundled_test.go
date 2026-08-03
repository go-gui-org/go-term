package term

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// mustBundled looks up a bundled theme by name and fails the test if it is
// missing. Tests that need a specific palette (a known-light theme, a
// known-low-contrast one) go through this rather than hardcoding colors, so
// regenerating the table surfaces a renamed theme as a clear failure instead
// of a silently different assertion.
// Takes testing.TB so benchmarks can select a palette the same way tests do.
func mustBundled(t testing.TB, name string) Theme {
	t.Helper()
	th, ok := bundledByName(name)
	if !ok {
		t.Fatalf("bundled theme %q not found — did term/themes_bundled.txt get "+
			"regenerated from an upstream that renamed it?", name)
	}
	return th
}

// TestBundledThemesDecode is the table's structural guard: every line must
// yield a complete theme. The decoder skips malformed lines rather than
// panicking, so without this a truncated or hand-edited table would silently
// ship fewer themes.
func TestBundledThemesDecode(t *testing.T) {
	t.Parallel()

	got := BundledThemes()
	if len(got) == 0 {
		t.Fatal("no bundled themes decoded")
	}

	// Count non-comment, non-blank lines in the raw table: the decoder must
	// have accepted all of them.
	var want int
	for _, line := range strings.Split(themesBundledRaw, "\n") {
		if line != "" && line[0] != '#' {
			want++
		}
	}
	if len(got) != want {
		t.Errorf("decoded %d themes from %d table lines — %d lines were rejected",
			len(got), want, want-len(got))
	}
}

// TestBundledThemesWellFormed checks the invariants the theme browser and the
// config file's name lookup rely on: every color set, every name usable, and
// no two themes sharing a name.
func TestBundledThemesWellFormed(t *testing.T) {
	t.Parallel()

	seen := make(map[string]string, len(BundledThemes()))
	for _, nt := range BundledThemes() {
		if nt.Name == "" {
			t.Fatal("bundled theme with empty name")
		}
		if strings.TrimSpace(nt.Name) != nt.Name {
			t.Errorf("%q: name has leading or trailing space", nt.Name)
		}

		// Names are matched case-insensitively by the config file's `theme`
		// key and by the workspace's persisted theme name, so a collision
		// under folding would make one of the two unreachable.
		key := strings.ToLower(nt.Name)
		if prev, dup := seen[key]; dup {
			t.Errorf("%q and %q collide when matched case-insensitively", prev, nt.Name)
		}
		seen[key] = nt.Name

		// An unset color is gui.Color's zero value, which renders as
		// transparent rather than as a color — a hole in the palette.
		for i, c := range nt.Theme.ANSI {
			if !c.IsSet() {
				t.Errorf("%q: ANSI %d is unset", nt.Name, i)
			}
		}
		if !nt.Theme.DefaultFG.IsSet() {
			t.Errorf("%q: DefaultFG is unset", nt.Name)
		}
		if !nt.Theme.DefaultBG.IsSet() {
			t.Errorf("%q: DefaultBG is unset", nt.Name)
		}
	}
}

// TestBundledThemesSorted pins the table's order. BundledThemes is handed
// straight to the theme browser's list, so the file order is the display
// order; nothing sorts it at runtime.
func TestBundledThemesSorted(t *testing.T) {
	t.Parallel()

	got := BundledThemes()
	for i := 1; i < len(got); i++ {
		prev, cur := strings.ToLower(got[i-1].Name), strings.ToLower(got[i].Name)
		if prev > cur {
			t.Fatalf("table not sorted: %q precedes %q", got[i-1].Name, got[i].Name)
		}
	}
}

// TestBundledThemesReadable holds the corpus to the same floor go-term's own
// themes were held to: default foreground has to be legible on default
// background. A theme failing this is unusable for its primary purpose, not
// merely ugly.
func TestBundledThemesReadable(t *testing.T) {
	t.Parallel()

	// Same floor as TestThemeDimSlotIsVisible: not a perceptual standard, but
	// one a washed-out pairing cannot clear.
	const minFGDist = 40
	for _, nt := range BundledThemes() {
		fg, bg := nt.Theme.DefaultFG, nt.Theme.DefaultBG
		if d := chanDist(fg, bg); d < minFGDist {
			t.Errorf("%q: DefaultFG %v is %d from DefaultBG %v, want >= %d",
				nt.Name, fg, d, bg, minFGDist)
		}
	}
}

// TestBundledThemesHaveBothCharacters guards against a regeneration that
// silently drops one half of the corpus — the light themes are the ones a
// broken parse would most plausibly lose, and their absence would not fail any
// other test here.
func TestBundledThemesHaveBothCharacters(t *testing.T) {
	t.Parallel()

	var dark, light int
	for _, nt := range BundledThemes() {
		if nt.Theme.IsDark() {
			dark++
		} else {
			light++
		}
	}
	if dark == 0 || light == 0 {
		t.Fatalf("corpus is one-sided: %d dark, %d light", dark, light)
	}
}

// TestBundledThemesCachedNotRebuilt covers the sync.Once: the browser rebuilds
// its view on every keystroke and calls BundledThemes each time, so decoding
// per call would turn filtering into ~600 theme decodes per character typed.
func TestBundledThemesCachedNotRebuilt(t *testing.T) {
	t.Parallel()

	a, b := BundledThemes(), BundledThemes()
	if len(a) == 0 {
		t.Fatal("no bundled themes")
	}
	if &a[0] != &b[0] {
		t.Error("BundledThemes re-decoded the table instead of returning the cache")
	}
}

// TestDecodeBundledThemesSkipsGarbage pins the decoder's totality. A corrupt
// table must cost the user the bad lines, not the terminal.
func TestDecodeBundledThemesSkipsGarbage(t *testing.T) {
	t.Parallel()

	good := "Good\t" + strings.Repeat("ab", 54)
	raw := strings.Join([]string{
		"# a comment",
		"",
		"NoTab",
		"ShortPayload\tabcdef",
		"BadHex\t" + strings.Repeat("zz", 54),
		"\t" + strings.Repeat("ab", 54), // empty name
		good,
	}, "\n")

	got := decodeBundledThemes(raw)
	if len(got) != 1 {
		t.Fatalf("decoded %d themes, want 1 (only the well-formed line)", len(got))
	}
	if got[0].Name != "Good" {
		t.Errorf("name = %q, want %q", got[0].Name, "Good")
	}
	if want := gui.RGB(0xab, 0xab, 0xab); got[0].Theme.ANSI[0] != want {
		t.Errorf("ANSI[0] = %v, want %v", got[0].Theme.ANSI[0], want)
	}
}
