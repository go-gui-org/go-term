package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

func TestPalette_FGBG_Default(t *testing.T) {
	c := defaultCell()
	if got := DefaultTheme.fg(c); got != DefaultTheme.DefaultFG {
		t.Errorf("default fg: %+v want %+v", got, DefaultTheme.DefaultFG)
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.DefaultBG {
		t.Errorf("default bg: %+v want %+v", got, DefaultTheme.DefaultBG)
	}
}

func TestPalette_FGBG_Indexed(t *testing.T) {
	c := cell{Ch: ' ', FG: 1, BG: 2}
	if got := DefaultTheme.fg(c); got != DefaultTheme.ANSI[1] {
		t.Errorf("fg index 1: %+v want %+v", got, DefaultTheme.ANSI[1])
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.ANSI[2] {
		t.Errorf("bg index 2: %+v want %+v", got, DefaultTheme.ANSI[2])
	}
}

func TestPalette_Inverse_SwapsFGBG(t *testing.T) {
	c := cell{Ch: ' ', FG: 1, BG: 2, Attrs: attrInverse}
	if got := DefaultTheme.fg(c); got != DefaultTheme.ANSI[2] {
		t.Errorf("inverse fg should be bg color: %+v want %+v",
			got, DefaultTheme.ANSI[2])
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.ANSI[1] {
		t.Errorf("inverse bg should be fg color: %+v want %+v",
			got, DefaultTheme.ANSI[1])
	}
}

func TestPalette_256_Cube(t *testing.T) {
	// xterm cube: index 16 + 36*r + 6*g + b with levels {0,95,135,175,215,255}.
	// Index 196 = 16 + 36*5 + 0 + 0 → pure red (255, 0, 0).
	if got, want := palette[196], gui.RGB(255, 0, 0); got != want {
		t.Errorf("palette[196]: got %+v want %+v", got, want)
	}
	// Index 21 = 16 + 0 + 0 + 5 → pure blue (0, 0, 255).
	if got, want := palette[21], gui.RGB(0, 0, 255); got != want {
		t.Errorf("palette[21]: got %+v want %+v", got, want)
	}
}

func TestPalette_256_Grayscale(t *testing.T) {
	// 232 = first gray, value 8.
	if got, want := palette[232], gui.RGB(8, 8, 8); got != want {
		t.Errorf("palette[232]: got %+v want %+v", got, want)
	}
	// 255 = last gray, value 8 + 23*10 = 238.
	if got, want := palette[255], gui.RGB(238, 238, 238); got != want {
		t.Errorf("palette[255]: got %+v want %+v", got, want)
	}
}

func TestPalette_TruecolorRoundtrip(t *testing.T) {
	c := cell{Ch: ' ', FG: rgbColor(255, 100, 0), BG: rgbColor(10, 20, 30)}
	if got, want := DefaultTheme.fg(c), gui.RGB(255, 100, 0); got != want {
		t.Errorf("truecolor fg: got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.bg(c), gui.RGB(10, 20, 30); got != want {
		t.Errorf("truecolor bg: got %+v want %+v", got, want)
	}
}

func TestPalette_ResolveUnknownTagUsesPaletteByte(t *testing.T) {
	// Tag 0x42 is neither colorPalette (0x00) nor colorRGB (0x01) nor
	// DefaultColor (0xFF). Decoder must not panic; falls back to
	// palette[low byte] which is in-bounds (palette has 256 entries).
	bad := uint32(0x42)<<24 | uint32(5)
	if got, want := DefaultTheme.resolve(bad, DefaultTheme.DefaultFG), DefaultTheme.ANSI[5]; got != want {
		t.Errorf("resolve unknown tag (FG default): got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.resolve(bad, DefaultTheme.DefaultBG), DefaultTheme.ANSI[5]; got != want {
		t.Errorf("resolve unknown tag (BG default): got %+v want %+v", got, want)
	}
}

func TestPalette_TruecolorInverse(t *testing.T) {
	c := cell{
		Ch:    ' ',
		FG:    rgbColor(1, 2, 3),
		BG:    rgbColor(10, 20, 30),
		Attrs: attrInverse,
	}
	if got, want := DefaultTheme.fg(c), gui.RGB(10, 20, 30); got != want {
		t.Errorf("inverse truecolor fg: got %+v want %+v", got, want)
	}
	if got, want := DefaultTheme.bg(c), gui.RGB(1, 2, 3); got != want {
		t.Errorf("inverse truecolor bg: got %+v want %+v", got, want)
	}
}

func TestPalette_Inverse_DefaultsSwap(t *testing.T) {
	c := cell{Ch: ' ', FG: DefaultColor, BG: DefaultColor, Attrs: attrInverse}
	if got := DefaultTheme.fg(c); got != DefaultTheme.DefaultBG {
		t.Errorf("inverse default fg: %+v", got)
	}
	if got := DefaultTheme.bg(c); got != DefaultTheme.DefaultFG {
		t.Errorf("inverse default bg: %+v", got)
	}
}

func TestPalette_ThemeOverridesANSI(t *testing.T) {
	custom := Theme{
		ANSI:      DefaultTheme.ANSI,
		DefaultFG: DefaultTheme.DefaultFG,
		DefaultBG: DefaultTheme.DefaultBG,
	}
	custom.ANSI[1] = gui.RGB(255, 0, 128) // override red
	c := cell{Ch: ' ', FG: 1}
	if got, want := custom.fg(c), gui.RGB(255, 0, 128); got != want {
		t.Errorf("theme ANSI override: got %+v want %+v", got, want)
	}
	// Extended colors (≥16) should be unaffected by ANSI override.
	c2 := cell{Ch: ' ', FG: paletteColor(196)}
	if got, want := custom.fg(c2), palette[196]; got != want {
		t.Errorf("extended color unchanged: got %+v want %+v", got, want)
	}
}

func TestPalette_OverrideBeatsThemeAndCube(t *testing.T) {
	g := newGrid(2, 4)
	// Index 1 normally comes from Theme.ANSI, 196 from the static cube.
	g.SetPaletteColor(1, rgbColor(1, 2, 3))
	g.SetPaletteColor(196, rgbColor(4, 5, 6))

	if got, want := g.fgOf(cell{Ch: ' ', FG: 1}), gui.RGB(1, 2, 3); got != want {
		t.Errorf("ANSI index override: got %+v want %+v", got, want)
	}
	if got, want := g.bgOf(cell{Ch: ' ', BG: paletteColor(196)}), gui.RGB(4, 5, 6); got != want {
		t.Errorf("cube index override: got %+v want %+v", got, want)
	}
	// Neighboring indices are untouched.
	if got, want := g.fgOf(cell{Ch: ' ', FG: 2}), DefaultTheme.ANSI[2]; got != want {
		t.Errorf("index 2: got %+v want %+v", got, want)
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: paletteColor(197)}), palette[197]; got != want {
		t.Errorf("index 197: got %+v want %+v", got, want)
	}
}

func TestPalette_OverrideIgnoresTruecolorAndDefault(t *testing.T) {
	g := newGrid(2, 4)
	// Overriding index 1 must not touch cells whose color is a packed RGB
	// triple or DefaultColor, even when their low byte happens to be 1.
	g.SetPaletteColor(1, rgbColor(9, 9, 9))
	c := cell{Ch: ' ', FG: rgbColor(0, 0, 1), BG: DefaultColor}
	if got, want := g.fgOf(c), gui.RGB(0, 0, 1); got != want {
		t.Errorf("truecolor fg: got %+v want %+v", got, want)
	}
	if got, want := g.bgOf(c), g.Theme.DefaultBG; got != want {
		t.Errorf("default bg: got %+v want %+v", got, want)
	}
}

func TestPalette_OverrideHonorsInverse(t *testing.T) {
	g := newGrid(2, 4)
	g.SetPaletteColor(1, rgbColor(1, 2, 3))
	g.SetPaletteColor(2, rgbColor(4, 5, 6))
	c := cell{Ch: ' ', FG: 1, BG: 2, Attrs: attrInverse}
	if got, want := g.fgOf(c), gui.RGB(4, 5, 6); got != want {
		t.Errorf("inverse fg: got %+v want %+v", got, want)
	}
	if got, want := g.bgOf(c), gui.RGB(1, 2, 3); got != want {
		t.Errorf("inverse bg: got %+v want %+v", got, want)
	}
}

func TestPalette_NoOverrideMatchesTheme(t *testing.T) {
	g := newGrid(2, 4)
	if g.palOverride != nil {
		t.Fatal("fresh grid allocated an override table")
	}
	for _, c := range []cell{
		{Ch: ' ', FG: 1, BG: 2},
		{Ch: ' ', FG: paletteColor(196), BG: paletteColor(232)},
		{Ch: ' ', FG: DefaultColor, BG: DefaultColor, Attrs: attrInverse},
	} {
		if got, want := g.fgOf(c), g.Theme.fg(c); got != want {
			t.Errorf("fgOf(%+v) = %+v, want %+v", c, got, want)
		}
		if got, want := g.bgOf(c), g.Theme.bg(c); got != want {
			t.Errorf("bgOf(%+v) = %+v, want %+v", c, got, want)
		}
	}
}

func TestPalette_ResetColorAndAll(t *testing.T) {
	g := newGrid(2, 4)
	g.SetPaletteColor(1, rgbColor(1, 2, 3))
	g.SetPaletteColor(2, rgbColor(4, 5, 6))

	g.ResetPaletteColor(1)
	if got, want := g.fgOf(cell{Ch: ' ', FG: 1}), DefaultTheme.ANSI[1]; got != want {
		t.Errorf("after ResetPaletteColor: got %+v want %+v", got, want)
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: 2}), gui.RGB(4, 5, 6); got != want {
		t.Errorf("unrelated index dropped: got %+v want %+v", got, want)
	}

	g.ResetPalette()
	if g.palOverride != nil {
		t.Error("ResetPalette left the override table allocated")
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: 2}), DefaultTheme.ANSI[2]; got != want {
		t.Errorf("after ResetPalette: got %+v want %+v", got, want)
	}
}

func TestPalette_OverrideSurvivesThemeSwitch(t *testing.T) {
	g := newGrid(2, 4)
	g.SetPaletteColor(1, rgbColor(1, 2, 3))
	// An embedder theme switch (Term.SetTheme assigns g.Theme) leaves
	// child-set OSC 4 entries in place — they live in their own layer.
	gruvbox := mustBundled(t, "Gruvbox Dark")
	g.setTheme(gruvbox)
	if got, want := g.fgOf(cell{Ch: ' ', FG: 1}), gui.RGB(1, 2, 3); got != want {
		t.Errorf("override after theme switch: got %+v want %+v", got, want)
	}
	if got, want := g.fgOf(cell{Ch: ' ', FG: 2}), gruvbox.ANSI[2]; got != want {
		t.Errorf("non-overridden index: got %+v want %+v", got, want)
	}
}

func TestRGBToGUIColor_Roundtrip(t *testing.T) {
	// Verify each byte position survives the uint32→RGB trip intact.
	cases := []uint32{
		0x00000000,
		0x00FF0000, // red
		0x0000FF00, // green
		0x000000FF, // blue
		0x00FF00FF, // magenta
		0x00FFFFFF, // white
		0x00123456, // arbitrary
	}
	for _, c := range cases {
		got := rgbToGUIColor(c)
		want := gui.RGB(uint8(c>>16), uint8(c>>8), uint8(c))
		if got != want {
			t.Errorf("rgbToGUIColor(%#08x) = %+v, want %+v", c, got, want)
		}
	}
}

func TestRGBToGUIColor_PreservesAlphaZero(t *testing.T) {
	// Alpha byte (bits 24-31) is discarded by uint8 casts.
	got := rgbToGUIColor(0xFF000000)
	want := gui.RGB(0, 0, 0)
	if got != want {
		t.Errorf("alpha bits should be ignored: got %+v, want %+v", got, want)
	}
}

// TestGrid_DECSCNM_ReversesScreen covers vttest's light/dark background
// screens: DECSCNM swaps foreground and background for the whole screen, so a
// default cell renders inverted while a cell that is *already* inverse comes
// back out normal.
func TestGrid_DECSCNM_ReversesScreen(t *testing.T) {
	g := newGrid(1, 4)
	plain := defaultCell()
	inverse := defaultCell()
	inverse.Attrs |= attrInverse

	fg, bg := g.Theme.DefaultFG, g.Theme.DefaultBG

	if got := g.fgOf(plain); got != fg {
		t.Errorf("normal fg = %v, want %v", got, fg)
	}

	g.ReverseScreen = true
	if got := g.fgOf(plain); got != bg {
		t.Errorf("reversed fg = %v, want %v", got, bg)
	}
	if got := g.bgOf(plain); got != fg {
		t.Errorf("reversed bg = %v, want %v", got, fg)
	}
	// Double reversal cancels.
	if got := g.fgOf(inverse); got != fg {
		t.Errorf("reversed inverse fg = %v, want %v", got, fg)
	}
	// The theme-color paint sites must agree with what bgOf resolves for a
	// default cell, or the canvas fill and the cell runs disagree.
	if g.defaultBG() != g.bgOf(plain) || g.defaultFG() != g.fgOf(plain) {
		t.Error("defaultFG/defaultBG disagree with fgOf/bgOf under DECSCNM")
	}
}

// TestParser_DECSCNM_SetResetAndRIS wires the mode through the parser and
// checks that a hard reset clears it — a pane left reversed by a crashed app
// is what `reset` has to recover from.
func TestParser_DECSCNM_SetResetAndRIS(t *testing.T) {
	g := newGrid(2, 4)
	p := newParser(g)

	feed(t, g, p, []byte("\x1b[?5h"))
	if !g.ReverseScreen {
		t.Fatal("?5h did not enable reverse video")
	}
	feed(t, g, p, []byte("\x1b[?5l"))
	if g.ReverseScreen {
		t.Fatal("?5l did not disable reverse video")
	}

	feed(t, g, p, []byte("\x1b[?5h\x1bc"))
	if g.ReverseScreen {
		t.Fatal("RIS did not clear reverse video")
	}
}

// TestThemeSelectionBG covers the exported selection tint the theme browser's
// preview draws with. It has to agree with what the grid paints per cell —
// they share selectionTint precisely so an embedder's chrome cannot end up a
// different shade than the pane beside it.
func TestThemeSelectionBG(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"Dracula", "Catppuccin Latte", "iTerm2 Solarized Light"} {
		th := mustBundled(t, name)
		g := newGrid(2, 4)
		g.setTheme(th)

		if got, want := th.SelectionBG(), g.selectionBG(th.DefaultBG); got != want {
			t.Errorf("%s: Theme.SelectionBG() = %v, grid paints %v", name, got, want)
		}
		// A tint equal to the background is no selection at all, which is the
		// failure the blend's "farther endpoint" rule exists to prevent.
		if th.SelectionBG() == th.DefaultBG {
			t.Errorf("%s: selection tint is identical to DefaultBG", name)
		}
	}
}

// Every bundled theme must produce a visible selection, not just the few named
// above. A selection that vanishes is indistinguishable from a broken one.
func TestBundledThemesSelectionVisible(t *testing.T) {
	t.Parallel()

	// Lower than the overlay floors: the selection is a tint behind text that
	// stays readable, so it is meant to be subtle. What this rules out is no
	// tint at all.
	const minSelDist = 12
	for _, nt := range BundledThemes() {
		if d := chanDist(nt.Theme.SelectionBG(), nt.Theme.DefaultBG); d < minSelDist {
			t.Errorf("%q: selection tint is %d from DefaultBG, want >= %d",
				nt.Name, d, minSelDist)
		}
	}
}
