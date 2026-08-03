package term

import "github.com/go-gui-org/go-gui/gui"

// Overlay colors, derived from the active theme.
//
// Everything the widget paints *over* the cells — the scrollbar lane, the
// visual bell, the copy/search/HUD pills — used to be a literal picked by eye
// against a dark background. That is fine while every shipped theme is dark
// and silently broken the moment one isn't: a white bell flash over a white
// canvas is a no-op, and a gray thumb over cream is barely there.
//
// So they follow the same rule selectionBG (palette.go) already follows:
// resolve against the theme, once, and let the draw path read a plain color.
// Three classes, by what the color sits on:
//
//   - Washes over live content (bell, scrollbar thumb) are positioned relative
//     to DefaultFG, which is by construction visible against DefaultBG — the
//     exact guarantee these need, and one no fixed gray can offer.
//   - Alpha-composited accents (failure ticks, progress fill) keep their hue —
//     red still means failed — but darken on light themes, because a bright
//     accent at alpha 120 over a pale track washes out to nothing.
//   - Pills that carry their own fill *and* text flip as a pair: light text on
//     a saturated fill for dark themes, dark text on a pale fill for light
//     ones, so the badge reads as a plate rather than a hole punched in the
//     page.
//
// Rebuilt from grid.rebuildPalette and grid.SetDynColor — the two places that
// can change what the overlays sit on. Never assign the struct directly.
type overlayColors struct {
	// Washes. Alpha is applied at the draw site for the bell (it fades) and
	// baked in for the thumb (it doesn't).
	bellFlash  gui.Color
	thumb      gui.Color
	thumbHover gui.Color

	// bellPeakAlpha is the wash's alpha at the instant the bell fires, before
	// the fade. Derived rather than constant because a saturated mid-luma
	// background (Hot Dog Stand's red, C64's blue) leaves no headroom: at the
	// baseline alpha even a pure white wash cannot clear minBellDist, so the
	// alpha is what has to give. Equals bellFlashPeakAlpha for the great
	// majority of themes.
	bellPeakAlpha uint8

	// Scrollbar-lane accents. Alpha is applied at the draw site, which varies
	// it with the scrollbar's idle/active state.
	fail     gui.Color
	progress gui.Color
	paused   gui.Color
	indet    gui.Color

	// Pills: fill/text pairs. Fill alphas are baked in.
	recordFill, recordText   gui.Color
	badgeFill, badgeText     gui.Color
	copyBarFill, copyBarText gui.Color
	copyCurFill              gui.Color
	searchFill               gui.Color
	searchNoMatchFill        gui.Color
	searchText               gui.Color
}

// Accent hues. These are the identity of each overlay — what the user learns
// to read at a glance — and are held fixed across themes; only their lightness
// moves. The values are the literals the overlays shipped with, so on a dark
// theme every hue-based overlay (accents, pills, copy cursor) renders exactly
// as it did before this became theme-derived. The two that do move are the
// thumb and the bell wash, which are derived from the theme itself rather than
// from a hue.
var (
	overlayFailHue     = gui.RGB(214, 74, 66)   // failure ticks, error progress
	overlayProgressHue = gui.RGB(64, 132, 214)  // normal progress
	overlayPausedHue   = gui.RGB(214, 158, 66)  // paused / warning progress
	overlayIndetHue    = gui.RGB(128, 128, 128) // indeterminate progress
	overlayRecordHue   = gui.RGB(150, 30, 30)   // ● REC pill
	overlayBadgeHue    = gui.RGB(20, 20, 20)    // size-readout HUD
	overlayCopyBarHue  = gui.RGB(30, 70, 45)    // copy-mode status bar
	overlayCopyCurHue  = gui.RGB(255, 176, 0)   // copy-mode cursor
	overlaySearchHue   = gui.RGB(40, 40, 90)    // search bar
	overlayNoMatchHue  = gui.RGB(90, 20, 20)    // search bar, no matches
)

// Pill fill alphas, unchanged from the literals they replace: semi-transparent
// so a badge dims the cells under it rather than hiding them.
const (
	overlayRecordAlpha uint8 = 210
	overlayBadgeAlpha  uint8 = 225
)

// Blend fractions used by deriveOverlay, as integer percentages — float-free
// like the rest of the color math, though this one runs once per theme change
// rather than per cell.
const (
	// How far a pill's text is pushed past its fill: toward white on dark
	// themes, toward black on light ones. High enough that the label reads as
	// text rather than as a tint of the plate.
	pillTextMix = 85

	// How far a pill's fill is lightened on a light theme. Deliberately short
	// of a pastel: at 55% the plate still separates from a white canvas, which
	// an 80% tint does not.
	pillFillLighten = 55

	// How far an alpha-composited accent is darkened on a light theme. The
	// accent is drawn at alpha 120 over the track, so it loses roughly half
	// its distance from the background before the eye ever sees it.
	accentDarken = 35

	// How much of DefaultFG is mixed into the bell wash. The wash starts from
	// the extreme *away* from the background (white on a dark theme, black on
	// a light one) rather than from DefaultFG, because it is painted at
	// bellFlashPeakAlpha — around 9% — and at that alpha anything short of the
	// extreme is not a flash, it is nothing. The theme tint is what keeps it
	// from being a bare white/black strobe.
	bellTint = 25

	// The thumb is DefaultFG pulled back toward DefaultBG: the pure pole is a
	// text color and reads as too loud for chrome, but anything neutral (the
	// fixed gray this replaces) only works against one background. Hovering
	// moves it toward the pole *and* raises its alpha, so the response is
	// visible on a light theme, where the alpha alone is nearly a wash.
	//
	// The two mixes are chosen so the composited distance from DefaultBG comes
	// out about the same on a dark theme as on a light one — see
	// TestOverlayContrast, which measures exactly that.
	thumbMix              = 45
	thumbHoverMix         = 20
	thumbAlpha      uint8 = 120
	thumbHoverAlpha uint8 = 175
)

var (
	overlayWhite = gui.RGB(255, 255, 255)
	overlayBlack = gui.RGB(0, 0, 0)
)

// Minimum channel-distance (chanDist, so 0..765) each overlay must keep from
// what it is drawn on, measured after alpha compositing. Not a perceptual
// standard — chanDist isn't one — but a floor a color washing out into its
// background cannot clear.
//
// These live here rather than in the test because deriveOverlay now *enforces*
// them (see pushOff): they are the derivation's contract, and the test verifies
// the contract rather than defining it. They exist because the blend fractions
// above were tuned by eye against a handful of dark themes; across the ~600
// bundled ones, 36 produced chrome that washed out — a mid-gray or saturated
// background defeats any fixed blend percentage.
const (
	// Chrome drawn over live cells: the scrollbar thumb, the bell wash. Low,
	// because these are meant to be quiet; the bar to clear is "visible", not
	// "prominent".
	minChromeDist = 80
	// The copy-mode cursor against the canvas. Higher than chrome: it is a
	// position marker the user is aiming with, and it is the only overlay
	// whose whole signal is its fill. It also has the glyph drawn over it in
	// DefaultBG, so it carries minTextDist as well.
	minCursorDist = 100
	// A pill's label against its own plate, and the copy cursor's glyph
	// against its fill. Higher: this is text being read, not chrome being
	// noticed.
	minTextDist = 250
	// The bell wash gets its own, much lower floor: bellFlashPeakAlpha is 22,
	// so even a pure white wash over pure black composites to 66. The wash is
	// meant to be caught peripherally, not read — what this floor rules out is
	// the case that motivated the whole pass, a wash on the same side of the
	// spectrum as the background.
	minBellDist = 45
)

// compositeOver returns c drawn at its own alpha over bg — what the eye
// actually sees for the overlays painted semi-transparently. Comparing a raw
// color against the background would accept colors that vanish once
// composited, which is exactly the failure mode a light theme introduces.
func compositeOver(c, bg gui.Color) gui.Color {
	a := int(c.A)
	mix := func(f, b uint8) uint8 {
		return uint8((int(f)*a + int(b)*(255-a)) / 255)
	}
	return gui.RGB(mix(c.R, bg.R), mix(c.G, bg.G), mix(c.B, bg.B))
}

// overlayDist is the distance the eye sees between c, drawn at alpha over bg,
// and bg itself. The single measure both pushOff and TestOverlayContrast use,
// so the derivation cannot satisfy a different metric than the test checks.
func overlayDist(c, bg gui.Color, alpha uint8) int {
	return chanDist(compositeOver(withAlpha(c, alpha), bg), bg)
}

// pushOff nudges c toward whichever pole (white or black) is farther from bg,
// stopping at the first step that clears min once composited at alpha. It
// returns c unchanged when the derived color already clears the floor, which
// is the common case — this only engages for the minority of themes whose
// background defeats the fixed blends.
//
// Stepping rather than solving: alpha compositing plus the uint8 rounding in
// mixPct make the closed form fiddly, and this runs once per theme change, not
// per cell. Moving toward a pole desaturates, so the smallest step that works
// is taken — a "fail" tick that has been pushed 20% toward white is still
// recognisably red, which one pushed to the pole would not be.
func pushOff(c, bg gui.Color, alpha uint8, min int) gui.Color {
	if overlayDist(c, bg, alpha) >= min {
		return c
	}
	// Whichever pole is farther from the background is the one with headroom.
	pole := overlayWhite
	if chanDist(overlayBlack, bg) > chanDist(overlayWhite, bg) {
		pole = overlayBlack
	}
	for pct := 5; pct <= 100; pct += 5 {
		if cand := mixPct(c, pole, pct); overlayDist(cand, bg, alpha) >= min {
			return cand
		}
	}
	// Unreachable for every bundled theme, but a background mid-way between
	// the poles can genuinely put a low-alpha wash out of reach. The pole is
	// the best available answer; returning c would be strictly worse.
	return pole
}

// mixPct blends a toward b by pct percent (0 = a, 100 = b), per channel.
func mixPct(a, b gui.Color, pct int) gui.Color {
	return gui.RGB(
		uint8((int(a.R)*(100-pct)+int(b.R)*pct)/100),
		uint8((int(a.G)*(100-pct)+int(b.G)*pct)/100),
		uint8((int(a.B)*(100-pct)+int(b.B)*pct)/100),
	)
}

// withAlpha returns c at alpha a.
func withAlpha(c gui.Color, a uint8) gui.Color {
	return gui.RGBA(c.R, c.G, c.B, a)
}

// pillPair resolves one pill's fill and text from its hue. On a dark theme the
// hue *is* the fill and the text is a near-white tint of it; on a light theme
// the two swap roles — the fill is a pale tint and the hue's shade becomes the
// text. Either way fill and text come from the same hue, so a pill can't end
// up with a label that clashes with its own plate.
func pillPair(hue gui.Color, alpha uint8, dark bool) (fill, text gui.Color) {
	if dark {
		return withAlpha(hue, alpha), mixPct(hue, overlayWhite, pillTextMix)
	}
	return withAlpha(mixPct(hue, overlayWhite, pillFillLighten), alpha),
		mixPct(hue, overlayBlack, pillTextMix)
}

// accent resolves a scrollbar-lane accent for the theme's character: the hue
// as-is on a dark theme, darkened on a light one.
func accent(hue gui.Color, dark bool) gui.Color {
	if dark {
		return hue
	}
	return mixPct(hue, overlayBlack, accentDarken)
}

// bellMaxPeakAlpha caps how far deriveBell may raise the wash's alpha. The
// bell is meant to be caught peripherally, not to black out the screen; past
// this the cure is worse than the invisible flash it fixes.
const bellMaxPeakAlpha uint8 = 60

// deriveBell resolves the bell wash and the alpha it peaks at. Color first: if
// the tinted wash clears minBellDist at the baseline alpha — true for the great
// majority of themes — nothing moves. Otherwise the wash is pushed toward the
// pole and, only if that is still not enough, the alpha is raised.
//
// Alpha is the last resort rather than the first because raising it makes the
// bell louder for every theme it touches, while pushing the color only makes it
// less tinted.
func deriveBell(th Theme, extreme gui.Color) (gui.Color, uint8) {
	bg := th.DefaultBG
	wash := pushOff(mixPct(extreme, th.DefaultFG, bellTint), bg,
		bellFlashPeakAlpha, minBellDist)
	if overlayDist(wash, bg, bellFlashPeakAlpha) >= minBellDist {
		return wash, bellFlashPeakAlpha
	}
	// pushOff already moved the color as far as it goes; the remaining knob is
	// how much of it lands on screen.
	for a := bellFlashPeakAlpha + 1; a <= int(bellMaxPeakAlpha); a++ {
		if overlayDist(wash, bg, uint8(a)) >= minBellDist {
			return wash, uint8(a)
		}
	}
	return wash, bellMaxPeakAlpha
}

// deriveOverlay computes the overlay palette for a theme. Pure function of the
// theme so it is trivially testable — see TestOverlayContrast, which is what
// actually holds the "checked against a light background" claim up.
func deriveOverlay(th Theme) overlayColors {
	dark := th.IsDark()

	// Both the wash and the thumb are positioned relative to DefaultFG, in
	// opposite directions: the wash sits past it, out at the extreme the theme
	// is heading for, and the thumb sits short of it, pulled back toward the
	// background. Chrome should be quieter than text; a flash should be
	// louder than it.
	extreme := overlayWhite
	if !dark {
		extreme = overlayBlack
	}

	bg := th.DefaultBG

	// Every wash and accent is pushed off the background far enough to clear
	// its floor. On the themes the blends were tuned against this changes
	// nothing — pushOff returns its input when the floor is already met — so
	// the tuned look is preserved and only the outliers move.
	//
	// The thumb needs it most: it is derived from DefaultFG, so a theme whose
	// own text barely separates from its background yields chrome that barely
	// separates from it either.
	thumb := pushOff(mixPct(th.DefaultFG, bg, thumbMix), bg, thumbAlpha, minChromeDist)

	bellWash, bellAlpha := deriveBell(th, extreme)

	ov := overlayColors{
		bellFlash:     bellWash,
		bellPeakAlpha: bellAlpha,
		thumb:         withAlpha(thumb, thumbAlpha),

		fail:     pushOff(accent(overlayFailHue, dark), bg, scrollbarTickIdleAlpha, minChromeDist),
		progress: pushOff(accent(overlayProgressHue, dark), bg, scrollbarTickIdleAlpha, minChromeDist),
		paused:   pushOff(accent(overlayPausedHue, dark), bg, scrollbarTickIdleAlpha, minChromeDist),
		indet:    pushOff(accent(overlayIndetHue, dark), bg, scrollbarTickIdleAlpha, minChromeDist),

		// The copy cursor's glyph is drawn in DefaultBG by drawCopyCursor
		// (the cell is inverted onto the cursor fill), so only the fill is
		// derived here — and it must stay far from DefaultBG for that glyph
		// to be legible, which is what darkening it on light themes buys.
		// minTextDist, not minCursorDist: the glyph on it is text being read,
		// and it is the stricter of the two floors the fill has to clear.
		copyCurFill: pushOff(accent(overlayCopyCurHue, dark), bg, 255, minTextDist),
	}

	// Hover must read as a response to the pointer, so it is derived from the
	// thumb that was actually chosen rather than from DefaultFG independently.
	// Deriving both from the theme and pushing each on its own could leave a
	// pushed idle thumb sitting further off the background than its own hover.
	hover := pushOff(mixPct(thumb, extreme, thumbMix-thumbHoverMix), bg,
		thumbHoverAlpha, minChromeDist)
	for pct := 0; pct <= 100; pct += 5 {
		cand := mixPct(hover, extreme, pct)
		if overlayDist(cand, bg, thumbHoverAlpha) > overlayDist(thumb, bg, thumbAlpha) {
			hover = cand
			break
		}
	}
	ov.thumbHover = withAlpha(hover, thumbHoverAlpha)

	ov.recordFill, ov.recordText = pillPair(overlayRecordHue, overlayRecordAlpha, dark)
	ov.badgeFill, ov.badgeText = pillPair(overlayBadgeHue, overlayBadgeAlpha, dark)
	ov.copyBarFill, ov.copyBarText = pillPair(overlayCopyBarHue, 255, dark)

	// The search bar has two fills (normal, no-match) sharing one text color.
	// The text is taken from the normal fill's pair: a query that stops
	// matching should change the bar's color, not restyle its label.
	ov.searchFill, ov.searchText = pillPair(overlaySearchHue, 255, dark)
	ov.searchNoMatchFill, _ = pillPair(overlayNoMatchHue, 255, dark)

	return ov
}

// rebuildOverlay refreshes the derived overlay palette. Called from
// rebuildPalette (theme swap) and SetDynColor (OSC 10/11 repaint) — the two
// paths that change what the overlays are drawn against.
func (g *grid) rebuildOverlay() {
	g.ov = deriveOverlay(g.Theme)
}
