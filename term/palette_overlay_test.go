package term

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

type shippedTheme struct {
	name  string
	theme Theme
}

// shippedThemes is every theme go-term ships: DefaultTheme plus the whole
// generated bundle. Shared by TestOverlayContrast (which checks that the
// overlays derived from a theme stay visible against it) and
// TestThemeDimSlotIsVisible.
//
// Deriving this from BundledThemes rather than listing themes by hand is the
// point: regenerating the table re-runs every readability assertion over the
// new corpus, so a theme that would render go-term's own chrome unreadable
// cannot arrive unnoticed. It also means these two tests cover ~600 palettes
// instead of the 17 that were maintained by hand.
//
// A function, not a var: DefaultTheme is assigned in palette.go's init, which
// runs after package-level variable initialization, so a var here would
// capture a zero-value theme and quietly pass everything.
func shippedThemes() []shippedTheme {
	bundled := BundledThemes()
	out := make([]shippedTheme, 0, len(bundled)+1)
	out = append(out, shippedTheme{"Default", DefaultTheme})
	for _, nt := range bundled {
		out = append(out, shippedTheme{nt.Name, nt.Theme})
	}
	return out
}

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
				{"bellFlash", withAlpha(ov.bellFlash, ov.bellPeakAlpha), bg, minBellDist},

				{"fail", withAlpha(ov.fail, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"progress", withAlpha(ov.progress, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"paused", withAlpha(ov.paused, scrollbarTickIdleAlpha), bg, minChromeDist},
				{"indet", withAlpha(ov.indet, scrollbarTickIdleAlpha), bg, minChromeDist},

				{"copyCursor", withAlpha(ov.copyCurFill, 255), bg, minCursorDist},
			}
			for _, c := range checks {
				if d := chanDist(compositeOver(c.c, c.on), c.on); d < c.min {
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
				plate := compositeOver(l.fill, bg)
				if d := chanDist(l.text, plate); d < minTextDist {
					t.Errorf("%s: distance %d from its plate %v, want >= %d",
						l.name, d, plate, minTextDist)
				}
			}

			// Hover must read as a response to the pointer, not as a
			// coincidence: strictly more separated than the idle thumb.
			idle := chanDist(compositeOver(ov.thumb, bg), bg)
			hover := chanDist(compositeOver(ov.thumbHover, bg), bg)
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

	// Solarized's two halves are the cleanest available pair: one palette,
	// two backgrounds, so the only thing the derivation can be responding to
	// is the light/dark character itself.
	dark := deriveOverlay(mustBundled(t, "iTerm2 Solarized Dark"))
	light := deriveOverlay(mustBundled(t, "iTerm2 Solarized Light"))

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
	if d := chanDist(compositeOver(withAlpha(g.ov.bellFlash, bellFlashPeakAlpha),
		g.Theme.DefaultBG), g.Theme.DefaultBG); d < minBellDist {
		t.Errorf("bell wash distance %d after OSC 11, want >= %d", d, minBellDist)
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

	// No exemptions: the whole bundled corpus clears this floor. If a
	// regenerated table ever brings in a theme that does not, add it here with
	// the upstream reason rather than lowering minDimDist — Cfg.MinimumContrast
	// is the escape hatch for a user who picks such a theme anyway.
	const minDimDist = 40
	for _, th := range shippedThemes() {
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
	g.setTheme(mustBundled(t, "Catppuccin Mocha"))
	dark := g.ov

	latte := mustBundled(t, "Catppuccin Latte")
	g.setTheme(latte)
	if g.ov == dark {
		t.Fatal("overlay palette unchanged after a dark -> light theme swap")
	}
	if want := deriveOverlay(latte); g.ov != want {
		t.Errorf("grid overlay does not match deriveOverlay for the new theme")
	}
}

// TestDeriveBellAlphaOnlyRisesWhenForced pins the escalation order in
// deriveBell: the wash's color is what moves first, and the alpha only leaves
// its baseline for a background that leaves no headroom. Without this, a change
// that raised the alpha eagerly would make the bell louder on every theme and
// no test would notice — TestOverlayContrast only checks the floor is met, not
// what it cost to meet it.
func TestDeriveBellAlphaOnlyRisesWhenForced(t *testing.T) {
	t.Parallel()

	// Hot Dog Stand's background is a saturated red at middling luma: at the
	// baseline alpha even a pure white wash cannot clear minBellDist, so this
	// is a theme where the alpha genuinely has to move.
	forced := deriveOverlay(mustBundled(t, "Hot Dog Stand"))
	if forced.bellPeakAlpha <= bellFlashPeakAlpha {
		t.Errorf("bellPeakAlpha = %d, want above the %d baseline — this theme "+
			"cannot reach minBellDist without it", forced.bellPeakAlpha, bellFlashPeakAlpha)
	}
	if forced.bellPeakAlpha > bellMaxPeakAlpha {
		t.Errorf("bellPeakAlpha = %d exceeds the %d cap", forced.bellPeakAlpha, bellMaxPeakAlpha)
	}

	// The overwhelming majority must be untouched, or the escalation is not a
	// last resort. Counted rather than asserted per theme: the exact set is a
	// property of the corpus, the proportion is the design intent.
	var raised int
	for _, th := range shippedThemes() {
		if deriveOverlay(th.theme).bellPeakAlpha != bellFlashPeakAlpha {
			raised++
		}
	}
	if limit := len(shippedThemes()) / 20; raised > limit {
		t.Errorf("%d of %d themes needed a raised bell alpha, want at most %d — "+
			"the wash's color should be doing this work",
			raised, len(shippedThemes()), limit)
	}
	t.Logf("%d of %d themes needed a raised bell alpha", raised, len(shippedThemes()))
}

// pushOff's last line is documented as unreachable for every bundled theme,
// which is exactly why it needs a test: a background midway between the poles
// combined with a near-transparent alpha genuinely puts the floor out of
// reach, and the loop has to give up with the best available answer rather
// than run off the end or spin.
func TestPushOffUnreachableFloorReturnsPole(t *testing.T) {
	t.Parallel()

	// Mid gray: neither pole is far from it. Alpha 1 means essentially nothing
	// of the color lands on screen, so no blend can move the composite.
	bg := gui.RGB(128, 128, 128)
	got := pushOff(gui.RGB(200, 40, 40), bg, 1, 700)

	// Black is the marginally farther pole from mid gray (Rec.601 is not in
	// play here — pushOff picks by chanDist).
	if want := overlayBlack; got != want {
		t.Errorf("pushOff on an unreachable floor = %v, want the far pole %v", got, want)
	}

	// And the ordinary case must still short-circuit: a color already clearing
	// the floor comes back untouched, so the tuned look is preserved.
	c := gui.RGB(255, 255, 255)
	if got := pushOff(c, gui.RGB(0, 0, 0), 255, minChromeDist); got != c {
		t.Errorf("pushOff moved a color that already cleared the floor: %v -> %v", c, got)
	}
}
