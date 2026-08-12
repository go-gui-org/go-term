package term

import "github.com/go-gui-org/go-gui/gui"

// Theme holds the 16 ANSI base colors plus default fg/bg for a terminal
// color scheme. Indices 0–7 are standard ANSI; 8–15 are bright variants.
// The 240 extended colors (16–255) are computed and not themeable — a
// child app can still recolor any of the 256 entries at runtime via
// OSC 4, which lands in the grid's override layer (see palOverrides),
// not here.
type Theme struct {
	ANSI      [16]gui.Color
	DefaultFG gui.Color
	DefaultBG gui.Color
}

// DefaultTheme is the theme a new grid starts with and the one used when an
// embedder configures no themes at all. It is a VS Code Dark+ approximation,
// and it is the only theme defined in Go: every other shipped theme comes from
// the generated table behind [BundledThemes].
//
// It stays hand-written because it is load-bearing beyond being a choice —
// init() mirrors its ANSI entries into the 256-color table for resolve's
// legacy index fallback, so it must exist before any theme is selected.
//
// Read-only after init(); do not mutate. To customize, copy the struct:
// custom := DefaultTheme; custom.DefaultFG = myColor.
var DefaultTheme Theme

// palette holds the xterm 256-color table. Indices 0–15 mirror
// DefaultTheme.ANSI (for backwards-compat lookup); 16–231 are the
// 6×6×6 RGB cube; 232–255 are 24 grayscale steps. Theme.resolve uses
// Theme.ANSI for 0–15 and this table for 16–255.
var palette [256]gui.Color

func init() {
	DefaultTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(0, 0, 0),       // 0  black
			gui.RGB(205, 49, 49),   // 1  red
			gui.RGB(13, 188, 121),  // 2  green
			gui.RGB(229, 229, 16),  // 3  yellow
			gui.RGB(36, 114, 200),  // 4  blue
			gui.RGB(188, 63, 188),  // 5  magenta
			gui.RGB(17, 168, 205),  // 6  cyan
			gui.RGB(229, 229, 229), // 7  white
			gui.RGB(102, 102, 102), // 8  bright black
			gui.RGB(241, 76, 76),   // 9  bright red
			gui.RGB(35, 209, 139),  // 10 bright green
			gui.RGB(245, 245, 67),  // 11 bright yellow
			gui.RGB(59, 142, 234),  // 12 bright blue
			gui.RGB(214, 112, 214), // 13 bright magenta
			gui.RGB(41, 184, 219),  // 14 bright cyan
			gui.RGB(229, 229, 229), // 15 bright white
		},
		DefaultFG: gui.RGB(229, 229, 229),
		DefaultBG: gui.RGB(20, 20, 24),
	}

	// Mirror DefaultTheme ANSI 0–15 into palette so legacy palette-index
	// fallback in resolve (for unknown high-byte tags) stays consistent.
	for i := range 16 {
		palette[i] = DefaultTheme.ANSI[i]
	}
	// 16–231: 6×6×6 RGB cube. xterm step values per channel.
	levels := [6]uint8{0, 95, 135, 175, 215, 255}
	for r := range 6 {
		for g := range 6 {
			for b := range 6 {
				palette[16+36*r+6*g+b] = gui.RGB(levels[r], levels[g], levels[b])
			}
		}
	}
	// 232–255: 24 grayscale steps (8, 18, …, 238).
	for i := range 24 {
		v := uint8(8 + 10*i)
		palette[232+i] = gui.RGB(v, v, v)
	}
}

// resolve decodes a packed color value. Indices 0–15 use th.ANSI;
// indices 16–255 use the global xterm table. defaultColor returns def.
// Unknown high-byte tags fall through to palette[low byte] so a corrupt
// value renders as some valid color rather than panicking.
func (th *Theme) resolve(c uint32, def gui.Color) gui.Color {
	if c == defaultColor {
		return def
	}
	if c&0xFF000000 == colorRGB {
		return rgbToGUIColor(c)
	}
	idx := c & 0xFF
	if idx < 16 {
		return th.ANSI[idx]
	}
	return palette[idx]
}

// palTable is a full 256-entry color table. Two live on the grid: the
// effective table read by the render path (grid.pal) and the sparse OSC 4
// override layer that feeds it (grid.palOverride). In the override layer an
// entry counts as set only when its Color reports IsSet, so clearing one
// (OSC 104) is a plain zeroing.
type palTable [256]gui.Color

// rebuildPalette recomputes the effective table from the theme, the static
// xterm table, and any OSC 4 overrides. Cheap (256 copies) and rare — it
// runs on grid creation, theme change, and palette reset, never per frame.
// Keeping the merge here is what lets resolveColor be a single indexed load
// in the per-cell hot path.
func (g *grid) rebuildPalette() {
	g.pal = palTable(palette)
	copy(g.pal[:16], g.Theme.ANSI[:])
	// Overlay colors hang off DefaultFG/DefaultBG, not off the indexed table,
	// but this is the one place every theme swap passes through — deriving
	// them here is what keeps them from going stale.
	g.rebuildOverlay()
	if g.palOverride == nil {
		return
	}
	for i, c := range g.palOverride {
		if c.IsSet() {
			g.pal[i] = c
		}
	}
}

// setTheme swaps the theme and refreshes the effective palette. Every
// assignment to g.Theme that changes ANSI colors must go through here —
// g.pal is derived state. (SetDynColor only touches DefaultFG/DefaultBG,
// which are not part of the indexed table, so it needs no rebuild.)
func (g *grid) setTheme(th Theme) {
	g.Theme = th
	g.rebuildPalette()
}

// resolveColor decodes a packed color value: defaultColor yields def, an
// rgbColor-tagged value unpacks directly, and everything else indexes the
// effective palette (so OSC 4 overrides need no extra branch here).
func (g *grid) resolveColor(c uint32, def gui.Color) gui.Color {
	if c == defaultColor {
		return def
	}
	if c&0xFF000000 == colorRGB {
		return rgbToGUIColor(c)
	}
	return g.pal[c&0xFF]
}

// fgOf resolves a cell's foreground to a Color, honoring inverse.
//
// DECSCNM (?5) is folded in as an XOR rather than a second branch: reverse
// video is defined as swapping foreground and background for the *whole*
// screen, which is per-cell inverse applied globally, so a cell that is
// already inverse comes out unreversed. Keeping it to one comparison also
// keeps resolveColor inlining here, which the foreground pass depends on.
func (g *grid) fgOf(c cell) gui.Color {
	if (c.Attrs&attrInverse != 0) != g.ReverseScreen {
		return g.resolveColor(c.BG, g.Theme.DefaultBG)
	}
	return g.resolveColor(c.FG, g.Theme.DefaultFG)
}

// bgOf resolves a cell's background to a Color, honoring inverse and DECSCNM.
func (g *grid) bgOf(c cell) gui.Color {
	if (c.Attrs&attrInverse != 0) != g.ReverseScreen {
		return g.resolveColor(c.FG, g.Theme.DefaultFG)
	}
	return g.resolveColor(c.BG, g.Theme.DefaultBG)
}

// defaultFG/defaultBG are the theme's default colors with DECSCNM applied.
// Used by the paint sites that work in theme colors directly rather than
// through a cell — the canvas fill, fillRun's skip test, the IME strip — all
// of which must agree with what bgOf resolves for a default cell or reverse
// video leaves the screen half-swapped.
func (g *grid) defaultFG() gui.Color {
	if g.ReverseScreen {
		return g.Theme.DefaultBG
	}
	return g.Theme.DefaultFG
}

func (g *grid) defaultBG() gui.Color {
	if g.ReverseScreen {
		return g.Theme.DefaultFG
	}
	return g.Theme.DefaultBG
}

// themeIsDark reports whether a background color reads as dark, using integer
// Rec.601 luma (299R + 587G + 114B, scaled by 1000). Float-free to match the
// rest of the color math here; the threshold is the midpoint, which is what
// kitty and Ghostty use for the same question.
func themeIsDark(c gui.Color) bool {
	return 299*int(c.R)+587*int(c.G)+114*int(c.B) < 128*1000
}

// IsDark reports whether the theme reads as a dark color scheme, by the luma
// of its DefaultBG.
//
// Exported because an embedder has the same question the emulator does — a
// host that themes its own chrome (window borders, tab bar) has to match the
// pane it wraps, and deriving that from a copy of the luma rule would let the
// chrome disagree with what DSR ?996 tells the child. This is a snapshot of
// the theme as declared; a child that repainted the background with OSC 11
// changes what the *grid* reports, not this.
func (t Theme) IsDark() bool {
	return themeIsDark(t.DefaultBG)
}

// colorSchemeDark is the single source of truth for what DSR ?996 reports and
// what the mode-2031 notification announces.
//
// It reads Theme.DefaultBG, which OSC 11 writes through (see SetDynColor), so a
// child that repainted the background is answered about the background it
// actually set. It deliberately does *not* go through defaultBG: that folds in
// DECSCNM, and reverse video is a transient render toggle, not a change of
// color scheme — reporting a flip for it would have every subscribed app
// re-theme itself twice per `tput flash`.
func (g *grid) colorSchemeDark() bool {
	return themeIsDark(g.Theme.DefaultBG)
}

// colorSchemeReport builds the CSI ? 997 ; Ps n report: 1 = dark, 2 = light.
// Shared by the DSR ?996 answer and the unsolicited mode-2031 notification so
// the two can never disagree about the encoding.
func colorSchemeReport(dark bool) []byte {
	if dark {
		return []byte("\x1b[?997;1n")
	}
	return []byte("\x1b[?997;2n")
}

// Selection highlight tint. Terminal.app-style: a selected cell keeps its own
// foreground color and its background is blended a fixed fraction toward one
// of the theme's default colors. Blending toward a theme color (rather than a
// fixed gray) makes the tint lighten on dark themes and darken on light ones,
// and keeps syntax colors readable instead of inverting them away.
//
// Expressed as an integer percentage so the blend stays allocation- and
// float-free in the per-cell draw path — the same mixPct the overlay palette
// uses (palette_overlay.go), so there is one blend rule in the package rather
// than two that can round differently.
const selTintPct = 30

// chanDist is the sum of per-channel absolute differences between two colors —
// a cheap, float-free stand-in for perceptual distance, adequate for the one
// question selectionBG asks: which of two endpoints is farther away.
func chanDist(a, b gui.Color) int {
	return absDiff8(a.R, b.R) + absDiff8(a.G, b.G) + absDiff8(a.B, b.B)
}

// absDiff8 is |a-b| for two 8-bit channels, computed in int so the subtraction
// cannot wrap.
func absDiff8(a, b uint8) int {
	if a > b {
		return int(a) - int(b)
	}
	return int(b) - int(a)
}

// selectionBG returns the highlight background for a cell whose resolved
// background is bg.
//
// The blend target is whichever theme default — foreground or background — is
// farther from bg, which guarantees the tint is actually visible. Ordinary
// text (bg == DefaultBG) blends toward DefaultFG, the Terminal.app behavior.
// Reverse-video cells (bg == DefaultFG: a vim status line, a man-page heading,
// or a search match, whose highlight is an inverse) would otherwise be blended
// with themselves and come out unchanged, showing no selection at all; those
// blend back toward DefaultBG instead.
func (g *grid) selectionBG(bg gui.Color) gui.Color {
	return selectionTint(g.Theme, bg)
}

// selectionTint is the blend itself, split out so the exported
// [Theme.SelectionBG] and the per-cell draw path cannot round differently.
func selectionTint(th Theme, bg gui.Color) gui.Color {
	target := th.DefaultFG
	if chanDist(bg, target) < chanDist(bg, th.DefaultBG) {
		target = th.DefaultBG
	}
	return mixPct(bg, target, selTintPct)
}

// SelectionBG returns the highlight background this theme gives ordinary
// selected text — a cell sitting on the theme's own default background.
//
// Exported for an embedder drawing chrome that has to match what the pane
// paints: the theme browser's preview uses it to show a real selection rather
// than an approximation of one. Cells with a non-default background (reverse
// video, a colored run) resolve differently; that case stays internal.
func (t Theme) SelectionBG() gui.Color {
	return selectionTint(t, t.DefaultBG)
}

// highlightSelected rewrites a cell so it renders as selected. The cell's
// resolved foreground (inverse already applied) is frozen into FG as a direct
// RGB value and BG becomes the blended selection tint, so every downstream
// consumer — bgOf, cellRunKey, emitCell — sees ordinary colors and needs no
// selection-specific branch. attrInverse is cleared because it has already
// been resolved here; leaving it set would swap the two colors back.
func (g *grid) highlightSelected(c cell) cell {
	fg := g.fgOf(c)
	bg := g.selectionBG(g.bgOf(c))
	c.Attrs &^= attrInverse
	c.FG = rgbColor(fg.R, fg.G, fg.B)
	c.BG = rgbColor(bg.R, bg.G, bg.B)
	return c
}

// SetPaletteColor overrides palette entry idx with an OSC 4 color. c must
// be an rgbColor-tagged packed value. The override layer is allocated on
// first use, so sessions that never see OSC 4 pay nothing. Marks all rows
// dirty so the next render picks up the change. Called from the parser
// while Mu is held.
func (g *grid) SetPaletteColor(idx uint8, c uint32) {
	if g.palOverride == nil {
		g.palOverride = new(palTable)
	}
	col := rgbToGUIColor(c)
	g.palOverride[idx] = col
	g.pal[idx] = col
	g.markAllDirty()
}

// ResetPaletteColor drops the OSC 4 override for one index (OSC 104 ; idx),
// restoring the theme / static table color. Called under Mu.
func (g *grid) ResetPaletteColor(idx uint8) {
	if g.palOverride == nil {
		return
	}
	g.palOverride[idx] = gui.Color{}
	if idx < 16 {
		g.pal[idx] = g.Theme.ANSI[idx]
	} else {
		g.pal[idx] = palette[idx]
	}
	g.markAllDirty()
}

// ResetPalette drops every OSC 4 override (bare OSC 104, and RIS). Frees
// the override layer outright. Called under Mu.
func (g *grid) ResetPalette() {
	if g.palOverride == nil {
		return
	}
	g.palOverride = nil
	g.rebuildPalette()
	g.markAllDirty()
}

// paletteColorRGB returns the effective color components for palette index
// idx: OSC 4 override, else the theme's ANSI color (0–15), else the static
// xterm table. Backs OSC 4 queries. Called under Mu.
func (g *grid) paletteColorRGB(idx uint8) (r, gr, b uint8) {
	c := g.pal[idx]
	return c.R, c.G, c.B
}

// fg resolves a cell's foreground to a Color, honoring inverse.
func (th *Theme) fg(c cell) gui.Color {
	if c.Attrs&attrInverse != 0 {
		return th.resolve(c.BG, th.DefaultBG)
	}
	return th.resolve(c.FG, th.DefaultFG)
}

// bg resolves a cell's background to a Color, honoring inverse.
func (th *Theme) bg(c cell) gui.Color {
	if c.Attrs&attrInverse != 0 {
		return th.resolve(c.FG, th.DefaultFG)
	}
	return th.resolve(c.BG, th.DefaultBG)
}

// rgbToGUIColor unpacks a colorRGB-tagged uint32 into a gui.Color.
// Used by grid.SetDynColor so grid doesn't call gui.RGB directly.
func rgbToGUIColor(c uint32) gui.Color {
	return gui.RGB(uint8(c>>16), uint8(c>>8), uint8(c))
}
