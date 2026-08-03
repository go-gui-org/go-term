package workspace

import (
	"math"
	"testing"

	"github.com/go-gui-org/go-term/term"
)

// ---------------------------------------------------------------------------
// listRowH
// ---------------------------------------------------------------------------

// Row height feeds both reveal helpers, which write the result into a scroll
// offset. A NaN there would not be caught by their `<= 0` guards, so it has to
// be clamped at the source.
func TestListRowH_ClampsDegenerateSizes(t *testing.T) {
	const factor = 2
	want := float32(listFallbackSize) * factor

	for _, tc := range []struct {
		name string
		size float32
	}{
		{"zero", 0},
		{"negative", -12},
		{"NaN", float32(math.NaN())},
		{"+Inf", float32(math.Inf(1))},
		{"-Inf", float32(math.Inf(-1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listRowH(tc.size, factor); got != want {
				t.Errorf("listRowH(%v) = %v, want the clamped %v", tc.size, got, want)
			}
		})
	}

	if got := listRowH(10, factor); got != 20 {
		t.Errorf("listRowH(10, 2) = %v, want 20", got)
	}
}

// ---------------------------------------------------------------------------
// themeCountText
// ---------------------------------------------------------------------------

func TestThemeCountText(t *testing.T) {
	themes := func(n int) []term.NamedTheme {
		out := make([]term.NamedTheme, n)
		for i := range out {
			out[i] = term.NamedTheme{Name: "T", Theme: term.DefaultTheme}
		}
		return out
	}
	matches := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i
		}
		return out
	}

	tests := []struct {
		name         string
		total, shown int
		want         string
	}{
		{"unfiltered", 3, 3, "3 themes"},
		{"filtered", 3, 1, "1 of 3 themes"},
		{"filter matched nothing", 3, 0, "0 of 3 themes"},
		{"singular total", 1, 1, "1 theme"},
		{"no themes at all", 0, 0, "0 themes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ws := &Workspace{cfg: Cfg{Themes: themes(tc.total)}}
			ws.browser.matches = matches(tc.shown)
			if got := ws.themeCountText(); got != tc.want {
				t.Errorf("themeCountText() = %q, want %q", got, tc.want)
			}
		})
	}
}
