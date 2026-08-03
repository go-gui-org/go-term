package term

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/go-gui-org/go-gui/gui"
)

// themesBundledRaw is the generated color table. Regenerate with
//
//	go run ./term/genthemes -src <clone>/ghostty -sha <commit>
//
// (no go:generate directive: it needs a clone path and a commit SHA that only
// a human picks, so an unattended `go generate ./...` could not run it).
// Text rather than Go literals
// on purpose: gui.Color's `set` field is unexported, so a Go table would have
// to be ~11k gui.RGB calls executed in init() — worse source size, worse
// compile time, and it would build every theme whether or not the process ever
// opens the theme browser.
//
//go:embed themes_bundled.txt
var themesBundledRaw string

// hexPayloadLen is the fixed width of a table line's color payload: 18 RGB
// triples (ANSI 0-15, foreground, background) as 6 hex digits each.
const hexPayloadLen = 18 * 6

var (
	bundledOnce   sync.Once
	bundledThemes []NamedTheme
)

// BundledThemes returns every color theme shipped with go-term, sorted
// case-insensitively by name. The table is decoded once on first call and
// cached; callers must not mutate the returned slice.
//
// These are generated from the Ghostty-format schemes in
// mbadolato/iTerm2-Color-Schemes (MIT). See docs/themes.md for the full name
// list and attribution.
func BundledThemes() []NamedTheme {
	bundledOnce.Do(func() { bundledThemes = decodeBundledThemes(themesBundledRaw) })
	return bundledThemes
}

// decodeBundledThemes parses the embedded table. It is deliberately total: a
// line it cannot make sense of is skipped rather than panicking, so a
// corrupted table costs the user some themes instead of the whole terminal.
// The generator's tests are what guarantee the table is well-formed.
func decodeBundledThemes(raw string) []NamedTheme {
	// One allocation for the slice; each theme is a value, so decoding 600
	// themes costs one backing array plus the name strings, which are
	// substrings of the embedded blob and so share its storage.
	out := make([]NamedTheme, 0, strings.Count(raw, "\n"))
	for len(raw) > 0 {
		var line string
		if i := strings.IndexByte(raw, '\n'); i >= 0 {
			line, raw = raw[:i], raw[i+1:]
		} else {
			line, raw = raw, ""
		}
		if line == "" || line[0] == '#' {
			continue
		}
		name, payload, ok := strings.Cut(line, "\t")
		if !ok || name == "" || len(payload) != hexPayloadLen {
			continue
		}
		th, ok := decodeThemeHex(payload)
		if !ok {
			continue
		}
		out = append(out, NamedTheme{Name: name, Theme: th})
	}
	return out
}

// decodeThemeHex turns a 108-digit payload into a Theme.
func decodeThemeHex(p string) (Theme, bool) {
	var th Theme
	for i := range 16 {
		c, ok := hexColor(p[i*6 : i*6+6])
		if !ok {
			return Theme{}, false
		}
		th.ANSI[i] = c
	}
	fg, ok := hexColor(p[16*6 : 17*6])
	if !ok {
		return Theme{}, false
	}
	bg, ok := hexColor(p[17*6 : 18*6])
	if !ok {
		return Theme{}, false
	}
	th.DefaultFG, th.DefaultBG = fg, bg
	return th, true
}

// hexColor decodes exactly six lowercase-or-uppercase hex digits.
func hexColor(s string) (gui.Color, bool) {
	var v [3]uint8
	for i := range 3 {
		hi, ok1 := hexNibble(s[i*2])
		lo, ok2 := hexNibble(s[i*2+1])
		if !ok1 || !ok2 {
			return gui.Color{}, false
		}
		v[i] = hi<<4 | lo
	}
	return gui.RGB(v[0], v[1], v[2]), true
}

func hexNibble(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	}
	return 0, false
}

// bundledByName is a test and internal helper. Embedders resolve names against
// their own Cfg.Themes (see workspace.findTheme), not against the bundle, so
// this deliberately stays unexported — BundledThemes is the whole public
// surface this file adds.
func bundledByName(name string) (Theme, bool) {
	for _, nt := range BundledThemes() {
		if strings.EqualFold(nt.Name, name) {
			return nt.Theme, true
		}
	}
	return Theme{}, false
}
