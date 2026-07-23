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

// Predefined themes. These are read-only after init(); do not mutate.
// DefaultTheme is applied when a new grid is created. To customize, copy
// the struct: custom := DefaultTheme; custom.DefaultFG = myColor.
var (
	DefaultTheme         Theme // VS Code Dark+ approximation
	GruvboxTheme         Theme // Gruvbox Dark
	NordTheme            Theme // Nord
	SolarizedDarkTheme   Theme // Solarized Dark
	DraculaTheme         Theme // Dracula
	CatppuccinMochaTheme Theme // Catppuccin Mocha
	TokyoNightTheme      Theme // Tokyo Night
	MonokaiTheme         Theme // Monokai
	OneDarkTheme         Theme // One Dark
	RosePineTheme        Theme // Rosé Pine
	KanagawaTheme        Theme // Kanagawa
	AyuDarkTheme         Theme // Ayu Dark
	EverforestTheme      Theme // Everforest
	GitHubDarkTheme      Theme // GitHub Dark
)

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

	// https://github.com/morhetz/gruvbox
	GruvboxTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(40, 40, 40),    // 0  bg0_h
			gui.RGB(204, 36, 29),   // 1  red
			gui.RGB(152, 151, 26),  // 2  green
			gui.RGB(215, 153, 33),  // 3  yellow
			gui.RGB(69, 133, 136),  // 4  blue
			gui.RGB(177, 98, 134),  // 5  magenta
			gui.RGB(104, 157, 106), // 6  cyan
			gui.RGB(168, 153, 132), // 7  fg4
			gui.RGB(146, 131, 116), // 8  fg3
			gui.RGB(251, 73, 52),   // 9  bright red
			gui.RGB(184, 187, 38),  // 10 bright green
			gui.RGB(250, 189, 47),  // 11 bright yellow
			gui.RGB(131, 165, 152), // 12 bright blue
			gui.RGB(211, 134, 155), // 13 bright magenta
			gui.RGB(142, 192, 124), // 14 bright cyan
			gui.RGB(235, 219, 178), // 15 fg1
		},
		DefaultFG: gui.RGB(235, 219, 178),
		DefaultBG: gui.RGB(40, 40, 40),
	}

	// https://www.nordtheme.com
	NordTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(46, 52, 64),    // 0  nord0
			gui.RGB(191, 97, 106),  // 1  nord11
			gui.RGB(163, 190, 140), // 2  nord14
			gui.RGB(235, 203, 139), // 3  nord13
			gui.RGB(129, 161, 193), // 4  nord9
			gui.RGB(180, 142, 173), // 5  nord15
			gui.RGB(136, 192, 208), // 6  nord8
			gui.RGB(229, 233, 240), // 7  nord4
			gui.RGB(76, 86, 106),   // 8  nord3
			gui.RGB(191, 97, 106),  // 9  bright red
			gui.RGB(163, 190, 140), // 10 bright green
			gui.RGB(235, 203, 139), // 11 bright yellow
			gui.RGB(129, 161, 193), // 12 bright blue
			gui.RGB(180, 142, 173), // 13 bright magenta
			gui.RGB(143, 188, 187), // 14 nord7
			gui.RGB(236, 239, 244), // 15 nord6
		},
		DefaultFG: gui.RGB(229, 233, 240),
		DefaultBG: gui.RGB(46, 52, 64),
	}

	// https://ethanschoonover.com/solarized
	SolarizedDarkTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(7, 54, 66),     // 0  base02
			gui.RGB(220, 50, 47),   // 1  red
			gui.RGB(133, 153, 0),   // 2  green
			gui.RGB(181, 137, 0),   // 3  yellow
			gui.RGB(38, 139, 210),  // 4  blue
			gui.RGB(211, 54, 130),  // 5  magenta
			gui.RGB(42, 161, 152),  // 6  cyan
			gui.RGB(238, 232, 213), // 7  base2
			gui.RGB(0, 43, 54),     // 8  base03
			gui.RGB(203, 75, 22),   // 9  orange
			gui.RGB(88, 110, 117),  // 10 base01
			gui.RGB(101, 123, 131), // 11 base00
			gui.RGB(131, 148, 150), // 12 base0
			gui.RGB(108, 113, 196), // 13 violet
			gui.RGB(147, 161, 161), // 14 base1
			gui.RGB(253, 246, 227), // 15 base3
		},
		DefaultFG: gui.RGB(131, 148, 150),
		DefaultBG: gui.RGB(0, 43, 54),
	}

	// https://draculatheme.com
	DraculaTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(33, 34, 44),    // 0  bg_dark
			gui.RGB(255, 85, 85),   // 1  red
			gui.RGB(80, 250, 123),  // 2  green
			gui.RGB(241, 250, 140), // 3  yellow
			gui.RGB(189, 147, 249), // 4  purple
			gui.RGB(255, 121, 198), // 5  pink
			gui.RGB(139, 233, 253), // 6  cyan
			gui.RGB(248, 248, 242), // 7  fg_light
			gui.RGB(98, 114, 164),  // 8  comment
			gui.RGB(255, 110, 110), // 9  bright red
			gui.RGB(105, 255, 148), // 10 bright green
			gui.RGB(255, 255, 165), // 11 bright yellow
			gui.RGB(214, 172, 255), // 12 bright purple
			gui.RGB(255, 146, 223), // 13 bright pink
			gui.RGB(164, 255, 255), // 14 bright cyan
			gui.RGB(255, 255, 255), // 15 bright white
		},
		DefaultFG: gui.RGB(248, 248, 242),
		DefaultBG: gui.RGB(40, 42, 54),
	}

	// https://catppuccin.com
	CatppuccinMochaTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(69, 71, 90),    // 0  surface1
			gui.RGB(243, 139, 168), // 1  red
			gui.RGB(166, 227, 161), // 2  green
			gui.RGB(249, 226, 175), // 3  yellow
			gui.RGB(137, 180, 250), // 4  blue
			gui.RGB(245, 194, 231), // 5  pink
			gui.RGB(148, 226, 213), // 6  teal
			gui.RGB(186, 194, 222), // 7  subtext1
			gui.RGB(88, 91, 112),   // 8  surface2
			gui.RGB(243, 139, 168), // 9  bright red
			gui.RGB(166, 227, 161), // 10 bright green
			gui.RGB(249, 226, 175), // 11 bright yellow
			gui.RGB(137, 180, 250), // 12 bright blue
			gui.RGB(245, 194, 231), // 13 bright pink
			gui.RGB(148, 226, 213), // 14 bright teal
			gui.RGB(166, 173, 200), // 15 subtext0
		},
		DefaultFG: gui.RGB(205, 214, 244),
		DefaultBG: gui.RGB(30, 30, 46),
	}

	// https://github.com/folke/tokyonight.nvim
	TokyoNightTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(26, 27, 39),    // 0  bg
			gui.RGB(197, 59, 83),   // 1  red
			gui.RGB(158, 206, 106), // 2  green
			gui.RGB(224, 175, 104), // 3  yellow
			gui.RGB(122, 162, 247), // 4  blue
			gui.RGB(173, 142, 230), // 5  magenta
			gui.RGB(125, 207, 255), // 6  cyan
			gui.RGB(169, 177, 214), // 7  fg_dim
			gui.RGB(65, 73, 107),   // 8  gutter
			gui.RGB(247, 118, 142), // 9  bright red
			gui.RGB(185, 242, 124), // 10 bright green
			gui.RGB(224, 175, 104), // 11 bright yellow
			gui.RGB(122, 162, 247), // 12 bright blue
			gui.RGB(185, 154, 247), // 13 bright magenta
			gui.RGB(125, 207, 255), // 14 bright cyan
			gui.RGB(192, 202, 245), // 15 fg
		},
		DefaultFG: gui.RGB(192, 202, 245),
		DefaultBG: gui.RGB(26, 27, 39),
	}

	// https://monokai.pro
	MonokaiTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(39, 40, 34),    // 0  bg
			gui.RGB(249, 38, 114),  // 1  red
			gui.RGB(166, 226, 46),  // 2  green
			gui.RGB(244, 191, 117), // 3  yellow
			gui.RGB(102, 217, 239), // 4  blue
			gui.RGB(174, 129, 255), // 5  purple
			gui.RGB(161, 239, 228), // 6  cyan
			gui.RGB(248, 248, 242), // 7  fg
			gui.RGB(117, 113, 94),  // 8  comment
			gui.RGB(249, 38, 114),  // 9  bright red
			gui.RGB(166, 226, 46),  // 10 bright green
			gui.RGB(244, 191, 117), // 11 bright yellow
			gui.RGB(102, 217, 239), // 12 bright blue
			gui.RGB(174, 129, 255), // 13 bright purple
			gui.RGB(161, 239, 228), // 14 bright cyan
			gui.RGB(249, 248, 245), // 15 bright white
		},
		DefaultFG: gui.RGB(248, 248, 242),
		DefaultBG: gui.RGB(39, 40, 34),
	}

	// https://github.com/atom/one-dark-syntax
	OneDarkTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(40, 44, 52),    // 0  bg
			gui.RGB(224, 108, 117), // 1  red
			gui.RGB(152, 195, 121), // 2  green
			gui.RGB(229, 192, 123), // 3  yellow
			gui.RGB(97, 175, 239),  // 4  blue
			gui.RGB(198, 120, 221), // 5  purple
			gui.RGB(86, 182, 194),  // 6  cyan
			gui.RGB(171, 178, 191), // 7  fg
			gui.RGB(92, 99, 112),   // 8  gutter
			gui.RGB(224, 108, 117), // 9  bright red
			gui.RGB(152, 195, 121), // 10 bright green
			gui.RGB(229, 192, 123), // 11 bright yellow
			gui.RGB(97, 175, 239),  // 12 bright blue
			gui.RGB(198, 120, 221), // 13 bright purple
			gui.RGB(86, 182, 194),  // 14 bright cyan
			gui.RGB(190, 198, 212), // 15 bright white
		},
		DefaultFG: gui.RGB(171, 178, 191),
		DefaultBG: gui.RGB(40, 44, 52),
	}

	// https://rosepinetheme.com
	RosePineTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(38, 35, 58),    // 0  overlay
			gui.RGB(235, 111, 146), // 1  love
			gui.RGB(49, 116, 143),  // 2  pine
			gui.RGB(246, 193, 119), // 3  gold
			gui.RGB(156, 207, 216), // 4  foam
			gui.RGB(196, 167, 231), // 5  iris
			gui.RGB(235, 188, 186), // 6  rose
			gui.RGB(224, 222, 244), // 7  text
			gui.RGB(110, 106, 134), // 8  muted
			gui.RGB(235, 111, 146), // 9  bright red
			gui.RGB(49, 116, 143),  // 10 bright green
			gui.RGB(246, 193, 119), // 11 bright yellow
			gui.RGB(156, 207, 216), // 12 bright blue
			gui.RGB(196, 167, 231), // 13 bright magenta
			gui.RGB(235, 188, 186), // 14 bright cyan
			gui.RGB(224, 222, 244), // 15 bright white
		},
		DefaultFG: gui.RGB(224, 222, 244),
		DefaultBG: gui.RGB(25, 23, 36),
	}

	// https://github.com/rebelot/kanagawa.nvim
	KanagawaTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(9, 6, 24),      // 0  sumiInk0
			gui.RGB(195, 64, 67),   // 1  autumnRed
			gui.RGB(118, 148, 106), // 2  autumnGreen
			gui.RGB(192, 163, 110), // 3  carpYellow
			gui.RGB(126, 156, 216), // 4  crystalBlue
			gui.RGB(149, 127, 184), // 5  oniViolet
			gui.RGB(106, 149, 137), // 6  springGreen
			gui.RGB(200, 192, 147), // 7  oldWhite
			gui.RGB(114, 113, 105), // 8  fujiGray
			gui.RGB(232, 36, 36),   // 9  samuraiRed
			gui.RGB(152, 187, 108), // 10 boatYellow2 / springGreen
			gui.RGB(230, 195, 132), // 11 bright yellow
			gui.RGB(127, 180, 202), // 12 springBlue
			gui.RGB(147, 138, 169), // 13 springViolet2
			gui.RGB(122, 168, 159), // 14 waveAqua1
			gui.RGB(220, 215, 186), // 15 lightWhite
		},
		DefaultFG: gui.RGB(220, 215, 186),
		DefaultBG: gui.RGB(31, 31, 40),
	}

	// https://github.com/ayu-theme/ayu-colors
	AyuDarkTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(10, 14, 20),    // 0  dark bg
			gui.RGB(255, 51, 51),   // 1  red
			gui.RGB(194, 217, 76),  // 2  green
			gui.RGB(255, 143, 64),  // 3  yellow
			gui.RGB(89, 194, 255),  // 4  blue
			gui.RGB(255, 180, 84),  // 5  orange/magenta
			gui.RGB(149, 230, 203), // 6  cyan
			gui.RGB(179, 177, 173), // 7  fg
			gui.RGB(77, 85, 102),   // 8  gutter
			gui.RGB(255, 51, 51),   // 9  bright red
			gui.RGB(194, 217, 76),  // 10 bright green
			gui.RGB(255, 143, 64),  // 11 bright yellow
			gui.RGB(89, 194, 255),  // 12 bright blue
			gui.RGB(255, 180, 84),  // 13 bright magenta
			gui.RGB(149, 230, 203), // 14 bright cyan
			gui.RGB(230, 225, 207), // 15 bright white
		},
		DefaultFG: gui.RGB(179, 177, 173),
		DefaultBG: gui.RGB(10, 14, 20),
	}

	// https://github.com/sainnhe/everforest
	EverforestTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(43, 51, 57),    // 0  bg_dim
			gui.RGB(230, 126, 128), // 1  red
			gui.RGB(167, 192, 128), // 2  green
			gui.RGB(219, 188, 127), // 3  yellow
			gui.RGB(127, 187, 179), // 4  blue
			gui.RGB(214, 153, 182), // 5  purple
			gui.RGB(131, 192, 146), // 6  aqua
			gui.RGB(211, 198, 170), // 7  fg
			gui.RGB(75, 86, 92),    // 8  gray0
			gui.RGB(230, 126, 128), // 9  bright red
			gui.RGB(167, 192, 128), // 10 bright green
			gui.RGB(219, 188, 127), // 11 bright yellow
			gui.RGB(127, 187, 179), // 12 bright blue
			gui.RGB(214, 153, 182), // 13 bright magenta
			gui.RGB(131, 192, 146), // 14 bright cyan
			gui.RGB(229, 223, 197), // 15 light gray
		},
		DefaultFG: gui.RGB(211, 198, 170),
		DefaultBG: gui.RGB(39, 46, 51),
	}

	// https://primer.style/primitives/colors
	GitHubDarkTheme = Theme{
		ANSI: [16]gui.Color{
			gui.RGB(72, 79, 88),    // 0  gray
			gui.RGB(248, 81, 73),   // 1  red
			gui.RGB(63, 185, 80),   // 2  green
			gui.RGB(210, 153, 34),  // 3  yellow
			gui.RGB(88, 166, 255),  // 4  blue
			gui.RGB(188, 140, 255), // 5  purple
			gui.RGB(57, 210, 192),  // 6  cyan
			gui.RGB(201, 209, 217), // 7  fg
			gui.RGB(110, 118, 129), // 8  gray dim
			gui.RGB(255, 123, 114), // 9  bright red
			gui.RGB(86, 211, 100),  // 10 bright green
			gui.RGB(227, 179, 65),  // 11 bright yellow
			gui.RGB(121, 192, 255), // 12 bright blue
			gui.RGB(210, 168, 255), // 13 bright purple
			gui.RGB(86, 212, 221),  // 14 bright cyan
			gui.RGB(240, 246, 252), // 15 bright white
		},
		DefaultFG: gui.RGB(201, 209, 217),
		DefaultBG: gui.RGB(13, 17, 23),
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
// indices 16–255 use the global xterm table. DefaultColor returns def.
// Unknown high-byte tags fall through to palette[low byte] so a corrupt
// value renders as some valid color rather than panicking.
func (th *Theme) resolve(c uint32, def gui.Color) gui.Color {
	if c == DefaultColor {
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

// resolveColor decodes a packed color value: DefaultColor yields def, an
// rgbColor-tagged value unpacks directly, and everything else indexes the
// effective palette (so OSC 4 overrides need no extra branch here).
func (g *grid) resolveColor(c uint32, def gui.Color) gui.Color {
	if c == DefaultColor {
		return def
	}
	if c&0xFF000000 == colorRGB {
		return rgbToGUIColor(c)
	}
	return g.pal[c&0xFF]
}

// fgOf resolves a cell's foreground to a Color, honoring inverse.
func (g *grid) fgOf(c cell) gui.Color {
	if c.Attrs&attrInverse != 0 {
		return g.resolveColor(c.BG, g.Theme.DefaultBG)
	}
	return g.resolveColor(c.FG, g.Theme.DefaultFG)
}

// bgOf resolves a cell's background to a Color, honoring inverse.
func (g *grid) bgOf(c cell) gui.Color {
	if c.Attrs&attrInverse != 0 {
		return g.resolveColor(c.FG, g.Theme.DefaultFG)
	}
	return g.resolveColor(c.BG, g.Theme.DefaultBG)
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
