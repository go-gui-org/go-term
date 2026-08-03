package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

type shippedTheme struct {
	name  string
	theme Theme
	dark  bool
}

// shippedThemes is every built-in theme with the light/dark character it must
// report. Shared by TestThemeIsDark (which checks the character itself) and
// TestOverlayContrast (which checks that the overlays derived from it are
// visible). A new built-in has to be added here, which is the point: it forces
// the question "does this theme's chrome still work" to be answered once.
//
// A function, not a var: the theme values are assigned in palette.go's init,
// which runs after package-level variable initialization, so a var here would
// capture zero-value themes and quietly pass everything.
func shippedThemes() []shippedTheme {
	return []shippedTheme{
		{"Default", DefaultTheme, true},
		{"Gruvbox", GruvboxTheme, true},
		{"Nord", NordTheme, true},
		{"SolarizedDark", SolarizedDarkTheme, true},
		{"Dracula", DraculaTheme, true},
		{"CatppuccinMocha", CatppuccinMochaTheme, true},
		{"TokyoNight", TokyoNightTheme, true},
		{"Monokai", MonokaiTheme, true},
		{"OneDark", OneDarkTheme, true},
		{"RosePine", RosePineTheme, true},
		{"Kanagawa", KanagawaTheme, true},
		{"AyuDark", AyuDarkTheme, true},
		{"Everforest", EverforestTheme, true},
		{"GitHubDark", GitHubDarkTheme, true},
		{"SolarizedLight", SolarizedLightTheme, false},
		{"GitHubLight", GitHubLightTheme, false},
		{"CatppuccinLatte", CatppuccinLatteTheme, false},
	}
}

// composite returns c drawn at its own alpha over bg — what the eye actually
// sees for the overlays that are painted semi-transparently. Comparing the raw
// color against the background would pass colors that vanish once composited,
// which is exactly the failure mode a light theme introduces.
func composite(c, bg gui.Color) gui.Color {
	a := int(c.A)
	mix := func(f, b uint8) uint8 {
		return uint8((int(f)*a + int(b)*(255-a)) / 255)
	}
	return gui.RGB(mix(c.R, bg.R), mix(c.G, bg.G), mix(c.B, bg.B))
}

// Minimum channel-distance (chanDist, so 0..765) an overlay must keep from
// what it sits on. Not a perceptual standard — chanDist isn't one — but a
// floor that a color washing out into its background cannot clear.
const (
	// Chrome drawn over live cells: the scrollbar thumb, the bell wash. Low,
	// because these are meant to be quiet; the bar to clear is "visible", not
	// "prominent".
	minChromeDist = 80
	// The copy-mode cursor against the canvas. Higher than chrome: it is a
	// position marker the user is aiming with, and it is the only overlay
	// whose whole signal is its fill.
	//
	// Pill plates get no such floor. A plate is backing for a label, not a
	// signal — the size badge's near-black plate on the near-black default
	// theme has always been invisible as a shape and perfectly legible as a
	// badge, because what the eye finds is the text. The requirement that
	// actually matters for a pill is minTextDist, below.
	minCursorDist = 100
	// A pill's label against its own plate. Higher: this is text being read,
	// not chrome being noticed.
	minTextDist = 250
	// The bell wash gets its own, much lower floor: bellFlashPeakAlpha is 22,
	// so even a pure white wash over pure black composites to 66. The wash is
	// meant to be caught peripherally, not read — what this floor rules out is
	// the case that motivated the whole pass, a wash on the same side of the
	// spectrum as the background.
	minBellDist = 45
)

// TestOverlayContrast is what holds up the claim that the overlays work on a
// light theme. Every derived color is measured against what it is drawn on —
// composited first where it is drawn with alpha — for every shipped theme.
//
// Without this the light themes would ship with a bell flash that is white on
// white and a scrollbar thumb nobody can find, which is how they were tuned
// before: by eye, against a dark background, once.
func TestOverlayContrast(t *testing.T) {
	t.Parallel()

	for _, th := range shippedThemes() {
		t.Run(th.name, func(t *testing.T) {
			bg := th.theme.DefaultBG
			ov := deriveOverlay(th.theme)

			// name, color, what it sits on, floor. Alpha is baked into the
			// value for the pills and the thumb; the scrollbar accents and
			// the bell take theirs at the draw site, so withAlpha applies it
			// here.
			checks := []struct {
				name string
				c    gui.Color
				on   gui.Color
				min  int
			}{
				{"thumb", ov.thumb, bg, minChromeDist},
				{"thumbHover", ov.thumbHover, bg, minChromeDist},
				{"bellFlash", withAlpha(ov.bellFlash, bellFlashPeakAlpha), bg, minBellDist},

				{"fail", withAlpha(ov.fail, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"progress", withAlpha(ov.progress, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"paused", withAlpha(ov.paused, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"indet", withAlpha(ov.indet, scrollbarTickIdleAlpha), bg, minChromeDist},

				{"copyCursor", withAlpha(ov.copyCurFill, 255), bg, minCursorDist},
			}
			for _, c := range checks {
				if d := chanDist(composite(c.c, c.on), c.on); d < c.min {
					t.Errorf("%s: distance %d from what it sits on %v, want >= %d",
						c.name, d, c.on, c.min)
				}
			}

			// Labels against their own plates. The plate is composited over
			// the canvas first: a semi-transparent pill on a light theme is
			// lighter than its nominal fill, and the label has to clear the
			// color that is actually there.
			labels := []struct {
				name       string
				text, fill gui.Color
			}{
				{"recordText", ov.recordText, ov.recordFill},
				{"badgeText", ov.badgeText, ov.badgeFill},
				{"copyBarText", ov.copyBarText, ov.copyBarFill},
				{"searchText", ov.searchText, ov.searchFill},
				{"searchText/noMatch", ov.searchText, ov.searchNoMatchFill},
				// drawCopyCursor paints the cell's glyph in DefaultBG over the
				// copy cursor's fill, so that pairing is a label too.
				{"copyCursorGlyph", bg, withAlpha(ov.copyCurFill, 255)},
			}
			for _, l := range labels {
				plate := composite(l.fill, bg)
				if d := chanDist(l.text, plate); d < minTextDist {
					t.Errorf("%s: distance %d from its plate %v, want >= %d",
						l.name, d, plate, minTextDist)
				}
			}

			// Hover must read as a response to the pointer, not as a
			// coincidence: strictly more separated than the idle thumb.
			idle := chanDist(composite(ov.thumb, bg), bg)
			hover := chanDist(composite(ov.thumbHover, bg), bg)
			if hover <= idle {
				t.Errorf("thumb hover distance %d not above idle %d", hover, idle)
			}
		})
	}
}

// TestDeriveOverlayFollowsCharacter pins the direction of the derivation
// rather than the values: a light theme's overlays must be darker than a dark
// theme's, or the whole pass is decorative.
func TestDeriveOverlayFollowsCharacter(t *testing.T) {
	t.Parallel()

	dark := deriveOverlay(SolarizedDarkTheme)
	light := deriveOverlay(SolarizedLightTheme)

	luma := func(c gui.Color) int {
		return 299*int(c.R) + 587*int(c.G) + 114*int(c.B)
	}
	cases := []struct {
		name       string
		dark, ligh gui.Color
	}{
		{"bellFlash", dark.bellFlash, light.bellFlash},
		{"fail", dark.fail, light.fail},
		{"progress", dark.progress, light.progress},
		{"badgeText", dark.badgeText, light.badgeText},
		{"copyCursor", dark.copyCurFill, light.copyCurFill},
	}
	for _, c := range cases {
		if luma(c.ligh) >= luma(c.dark) {
			t.Errorf("%s: light-theme color %v is not darker than dark-theme %v",
				c.name, c.ligh, c.dark)
		}
	}

	// The thumb is deliberately absent from that list: it is pulled toward
	// DefaultBG, so on a light theme it is *lighter* than a dark theme's while
	// still being darker than what it sits on. TestOverlayContrast is what
	// covers it, by measuring against the background instead of in absolutes.

	// Pill plates go the other way — they lighten, so dark text can sit on
	// them — which is the flip that keeps a badge from being a hole in a
	// light page.
	if luma(light.badgeFill) <= luma(dark.badgeFill) {
		t.Errorf("badge plate %v did not lighten for a light theme (dark: %v)",
			light.badgeFill, dark.badgeFill)
	}
}

// TestOverlayTracksDynamicColors covers the OSC 11 path: a child that repaints
// the background light has changed what the overlays sit on just as much as a
// theme swap has, and rebuildPalette never runs for it.
func TestOverlayTracksDynamicColors(t *testing.T) {
	t.Parallel()

	g := newGrid(4, 8)
	before := g.ov.bellFlash
	g.SetDynColor(11, rgbColor(253, 246, 227)) // Solarized Light's base3
	if g.ov.bellFlash == before {
		t.Fatal("bell wash unchanged after OSC 11 repainted the background light")
	}
	if d := chanDist(composite(withAlpha(g.ov.bellFlash, bellFlashPeakAlpha),
		g.Theme.DefaultBG), g.Theme.DefaultBG); d < minBellDist {
		t.Errorf("bell wash distance %d after OSC 11, want >= %d", d, minBellDist)
	}
}

// TestSolarizedLightSharesDarkANSI pins Solarized's defining property: it is
// one palette with two backgrounds, so the light and dark schemes have
// identical ANSI tables and differ only in DefaultFG/DefaultBG. Upstream's
// Xresources ships a single color0–15 block, and the official iTerm2 presets
// for Light and Dark have byte-identical ANSI entries.
//
// This exists because the obvious-looking "fix" — reversing the base tones so
// the table matches the light background — is wrong, and its symptom is
// subtle: it puts base3 (the background) in slot 8, and every glyph an app
// dims with bright-black silently vanishes.
func TestSolarizedLightSharesDarkANSI(t *testing.T) {
	t.Parallel()

	if SolarizedLightTheme.ANSI != SolarizedDarkTheme.ANSI {
		t.Error("Solarized Light and Dark must share one ANSI table")
	}
	if SolarizedLightTheme.DefaultBG == SolarizedDarkTheme.DefaultBG {
		t.Error("Solarized Light and Dark must differ in DefaultBG")
	}
	if SolarizedLightTheme.IsDark() {
		t.Error("Solarized Light reports dark")
	}
}

// dimSlot is ANSI 8, "bright black". Tools use it for text that should read as
// muted but still legible — eza's permission-column dashes, ls separators,
// comment coloring — which makes it the one slot whose collision with the
// background is a bug rather than a convention.
const dimSlot = 8

// TestThemeDimSlotIsVisible checks that slot 8 is distinguishable from the
// background in every shipped theme.
//
// Slot 0 is deliberately not checked: ANSI black equalling the background is
// the norm for a dark theme (as slot 15 equalling it is for a light one), and
// nothing prints black-on-default expecting to read it.
func TestThemeDimSlotIsVisible(t *testing.T) {
	t.Parallel()

	// Solarized Dark is upstream's own wart, not ours: its color8 is base03,
	// which is also its background. Every Solarized implementation renders
	// bright-black invisible there, and deviating would make this theme not
	// Solarized. Cfg.MinimumContrast is the way out for a user who hits it.
	exempt := map[string]bool{"SolarizedDark": true}

	const minDimDist = 40
	for _, th := range shippedThemes() {
		if exempt[th.name] {
			continue
		}
		c := th.theme.ANSI[dimSlot]
		if d := chanDist(c, th.theme.DefaultBG); d < minDimDist {
			t.Errorf("%s: ANSI 8 %v is %d from DefaultBG %v, want >= %d — "+
				"dimmed punctuation will be invisible",
				th.name, c, d, th.theme.DefaultBG, minDimDist)
		}
	}
}

// TestSetThemeRebuildsOverlay covers the wiring rather than the derivation:
// deriveOverlay is pure and tested above, but it is only useful if every theme
// swap runs it. rebuildPalette is the single funnel, so a swap that reached the
// cells without reaching the overlays would leave a dark theme's chrome
// painted over a light theme's canvas.
func TestSetThemeRebuildsOverlay(t *testing.T) {
	t.Parallel()

	g := newGrid(4, 8)
	g.setTheme(CatppuccinMochaTheme)
	dark := g.ov

	g.setTheme(CatppuccinLatteTheme)
	if g.ov == dark {
		t.Fatal("overlay palette unchanged after a dark -> light theme swap")
	}
	if want := deriveOverlay(CatppuccinLatteTheme); g.ov != want {
		t.Errorf("grid overlay does not match deriveOverlay for the new theme")
	}
}
