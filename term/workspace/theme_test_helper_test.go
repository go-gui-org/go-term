package workspace

import (
	"strings"
	"testing"

	"github.com/go-gui-org/go-term/term"
)

// testTheme picks a bundled theme by name for tests that need a concrete
// palette distinct from the default. Most of them only care that the value is
// distinguishable — theme selection is tested by identity, not by color — but
// naming one keeps the failure message legible.
//
// Centralised so that regenerating term/themes_bundled.txt from an upstream
// that renamed a theme fails in one place with a clear message, rather than in
// a dozen tests as an undefined identifier.
func testTheme(t testing.TB, name string) term.Theme {
	t.Helper()
	for _, nt := range term.BundledThemes() {
		if strings.EqualFold(nt.Name, name) {
			return nt.Theme
		}
	}
	t.Fatalf("bundled theme %q not found — pick another name from docs/themes.md", name)
	return term.Theme{}
}

// themeOpts builds a termOpts with only the theme set, going through the same
// setter production code uses. Tests that construct Workspace state directly
// use it so a fixture cannot pair a theme value with the wrong name — the
// exact drift termOpts.setTheme exists to prevent.
func themeOpts(nt term.NamedTheme) termOpts {
	var o termOpts
	o.setTheme(nt)
	return o
}
