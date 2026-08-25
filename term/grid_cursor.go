package term

// CursorStyle selects the cursor glyph: filled block, baseline underline, or
// vertical bar at the leading edge of the cell. It is both the internal grid
// state DECSCUSR writes and the public spelling an embedder configures through
// Cfg.CursorStyle — one type, so the two cannot drift.
type CursorStyle uint8

// String names the style the way the config file spells it, so a log line
// and a config key read the same.
func (s CursorStyle) String() string {
	switch s {
	case CursorStyleUnderline:
		return "underline"
	case CursorStyleBar:
		return "bar"
	default:
		return "block"
	}
}

// ApplyDECSCUSR applies the DECSCUSR (CSI Ps SP q) parameter,
// setting cursor shape + blink. Unknown values fall back to the
// xterm default (blinking block, matching Ps=0/1).
//
// A locked cursor drops the sequence outright rather than recording it for
// later: the user asked for one cursor, and DECRQSS must keep reporting what
// is actually on screen (see DECSCUSRParam).
func (g *grid) ApplyDECSCUSR(ps int) {
	if g.cursorLocked {
		return
	}
	switch ps {
	case 0, 1:
		g.cursorShape, g.CursorBlink = CursorStyleBlock, true
	case 2:
		g.cursorShape, g.CursorBlink = CursorStyleBlock, false
	case 3:
		g.cursorShape, g.CursorBlink = CursorStyleUnderline, true
	case 4:
		g.cursorShape, g.CursorBlink = CursorStyleUnderline, false
	case 5:
		g.cursorShape, g.CursorBlink = CursorStyleBar, true
	case 6:
		g.cursorShape, g.CursorBlink = CursorStyleBar, false
	default:
		g.cursorShape, g.CursorBlink = CursorStyleBlock, true
	}
}

// setCursorDefaults records the user's cursor settings and applies the shape
// and blink to the live cursor. The defaults are what HardReset restores, so a
// configured cursor survives `reset`; locked drops every later DECSCUSR.
//
// Caller holds Mu.
func (g *grid) setCursorDefaults(style CursorStyle, blink, locked bool) {
	g.defaultShape, g.defaultBlink, g.cursorLocked = style, blink, locked
	g.cursorShape, g.CursorBlink = style, blink
	g.markDirty(g.CursorR)
}

// savedCursor holds the snapshot taken by SaveCursor (DECSC / CSI s).
// Stores position and SGR state per VT100 spec. attrs carries the DECSCA
// protection bit along with the SGR attributes, which VT510 requires DECSC
// to save. Zero value means no snapshot has been taken yet (valid == false).
type savedCursor struct {
	r, c       int
	fg, bg     uint32
	attrs      uint16
	ulStyle    uint8
	ulColor    uint32
	charsetG0  byte
	charsetG1  byte
	activeG    uint8
	autoWrap   bool
	originMode bool
	insertMode bool
	valid      bool
}

// MoveCursor sets the cursor to (r,c), clamped to grid bounds. Used by
// CSI cursor-position sequences which are 1-based; callers convert.
func (g *grid) MoveCursor(r, c int) {
	if r < 0 {
		r = 0
	}
	if r >= g.Rows {
		r = g.Rows - 1
	}
	if c < 0 {
		c = 0
	}
	if c >= g.Cols {
		c = g.Cols - 1
	}
	g.markDirty(g.CursorR)
	g.CursorR, g.CursorC = r, c
	g.markDirty(r)
}

// MoveCursorOrigin applies DECOM semantics: r is relative to Top when
// OriginMode is enabled, and the row is clamped to the active scroll
// region. Column handling remains full-width.
func (g *grid) MoveCursorOrigin(r, c int) {
	if !g.OriginMode || !g.regionValid() {
		g.MoveCursor(r, c)
		return
	}
	r += g.Top
	if r < g.Top {
		r = g.Top
	}
	if r > g.Bottom {
		r = g.Bottom
	}
	if c < 0 {
		c = 0
	}
	if c >= g.Cols {
		c = g.Cols - 1
	}
	g.markDirty(g.CursorR)
	g.CursorR, g.CursorC = r, c
	g.markDirty(r)
}

// CursorUp/Down/Forward/Back move the cursor by n cells, clamped.
func (g *grid) CursorUp(n int) {
	r := g.CursorR - n
	if g.OriginMode && g.regionValid() && g.CursorR >= g.Top && g.CursorR <= g.Bottom && r < g.Top {
		r = g.Top
	}
	g.MoveCursor(r, g.CursorC)
}

func (g *grid) CursorDown(n int) {
	r := g.CursorR + n
	if g.OriginMode && g.regionValid() && g.CursorR >= g.Top && g.CursorR <= g.Bottom && r > g.Bottom {
		r = g.Bottom
	}
	g.MoveCursor(r, g.CursorC)
}

func (g *grid) CursorForward(n int) { g.MoveCursor(g.CursorR, g.settledCol()+n) }

func (g *grid) CursorBack(n int) { g.MoveCursor(g.CursorR, g.settledCol()-n) }

// settledCol is the cursor's real column, with the deferred-wrap state
// collapsed. putCell encodes "wrap pending" by leaving CursorC at Cols: the
// glyph that filled the last column has been written, and the wrap happens
// only when the next one arrives. DEC terminals keep that as a separate flag
// with the cursor still *on* the last column, so every cursor-relative
// operation has to read it that way — otherwise it computes from a column that
// does not exist. A backspace out of the pending state is the case that shows:
// it must land one column left of the right margin, not on it.
//
// Deliberately not applied to the erase operations, which have their own
// documented handling of the pending column (see eraseInLine).
func (g *grid) settledCol() int {
	if g.CursorC >= g.Cols {
		return max(g.Cols-1, 0)
	}
	return g.CursorC
}

// SaveCursor snapshots cursor position and SGR state. Implements
// DECSC (ESC 7) and CSI s. Subsequent SaveCursor calls overwrite.
func (g *grid) SaveCursor() {
	g.saved = savedCursor{
		r:          g.CursorR,
		c:          g.CursorC,
		fg:         g.CurFG,
		bg:         g.CurBG,
		attrs:      g.CurAttrs,
		ulStyle:    g.CurULStyle,
		ulColor:    g.CurULColor,
		charsetG0:  g.CharsetG0,
		charsetG1:  g.CharsetG1,
		activeG:    g.ActiveG,
		autoWrap:   g.AutoWrap,
		originMode: g.OriginMode,
		insertMode: g.InsertMode,
		valid:      true,
	}
}

// RestoreCursor restores the snapshot from SaveCursor. If no save has
// occurred, homes the cursor and resets SGR per VT100 spec.
func (g *grid) RestoreCursor() {
	if !g.saved.valid {
		g.MoveCursor(0, 0)
		g.CurFG, g.CurBG, g.CurAttrs = defaultColor, defaultColor, 0
		g.CurULStyle = 0
		g.CurULColor = defaultColor
		return
	}
	g.MoveCursor(g.saved.r, g.saved.c)
	g.CurFG = g.saved.fg
	g.CurBG = g.saved.bg
	g.CurAttrs = g.saved.attrs
	g.CurULStyle = g.saved.ulStyle
	g.CurULColor = g.saved.ulColor
	g.CharsetG0 = g.saved.charsetG0
	g.CharsetG1 = g.saved.charsetG1
	g.ActiveG = g.saved.activeG
	g.AutoWrap = g.saved.autoWrap
	g.OriginMode = g.saved.originMode
	g.InsertMode = g.saved.insertMode
}
