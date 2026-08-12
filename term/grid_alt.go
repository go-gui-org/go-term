package term

// Alt-screen state: the DECSET ?1049/?47 swap between the main buffer
// and a fresh blank one. EnterAlt parks the main screen (cells, cursor,
// SGR, scroll region, DECSC slot, and its image list) in mainSaved;
// ExitAlt restores it and drops whatever the alt screen placed.

// altSavedScreen captures everything needed to restore the main screen
// when ExitAlt is called: the cell buffer plus cursor/SGR/scroll-region
// state and the DECSC slot (so DECSC/DECRC inside the alt buffer don't
// clobber the main-buffer save).
type altSavedScreen struct {
	cells            []cell
	rowWrapped       []bool
	cursorR, cursorC int
	curFG, curBG     uint32
	curAttrs         uint16
	curULStyle       uint8
	curULColor       uint32
	charsetG0        byte
	charsetG1        byte
	activeG          uint8
	autoWrap         bool
	originMode       bool
	insertMode       bool
	top, bottom      int
	saved            savedCursor
	// graphics is the main screen's image list, parked here while the alt
	// screen is up (occludeMaxR is its matching occlusion bound). Images
	// belong to a screen the same way cells do; keeping them on the alt
	// screen is what left a sixel visible underneath yazi.
	//
	// maxGraphics is enforced per list, so a grid retains at most
	// 2*maxGraphics placements — one screen's worth on each side of the
	// swap, and no more no matter how often an app toggles ?1049.
	graphics    []graphic
	occludeMaxR int
}

// EnterAlt swaps the active cell buffer with a fresh blank one and
// stashes the main-screen state (cells, cursor, SGR, scroll region,
// DECSC slot) into mainSaved. While alt is active, scrollback writes
// are suppressed and ViewOffset is reset. No-op if already active.
//
// The DECSC save slot (g.saved) is also swapped so a DECSC/DECRC pair
// inside the alt buffer can't clobber the main-buffer save. ?1049
// callers typically SaveCursor *before* EnterAlt; that save lands in
// g.saved at call time and is correctly stashed here.
func (g *grid) EnterAlt() {
	if g.AltActive {
		return
	}
	g.mainSaved = altSavedScreen{
		cells:       g.Cells,
		rowWrapped:  g.RowWrapped,
		cursorR:     g.CursorR,
		cursorC:     g.CursorC,
		curFG:       g.CurFG,
		curBG:       g.CurBG,
		curAttrs:    g.CurAttrs,
		curULStyle:  g.CurULStyle,
		curULColor:  g.CurULColor,
		charsetG0:   g.CharsetG0,
		charsetG1:   g.CharsetG1,
		activeG:     g.ActiveG,
		autoWrap:    g.AutoWrap,
		originMode:  g.OriginMode,
		insertMode:  g.InsertMode,
		top:         g.Top,
		bottom:      g.Bottom,
		saved:       g.saved,
		graphics:    g.Graphics,
		occludeMaxR: g.occludeMaxR,
	}
	// The alt screen starts with no images of its own. Parking the main
	// screen's list is what hides a sixel behind a full-screen app and, in
	// the other direction, is what lets that app's own images be occluded
	// normally when it paints over them.
	g.Graphics = nil
	g.occludeMaxR = 0
	cells := make([]cell, g.Rows*g.Cols)
	blank := defaultCell()
	for i := range cells {
		cells[i] = blank
	}
	g.Cells = cells
	g.RowWrapped = make([]bool, g.Rows)
	g.CursorR, g.CursorC = 0, 0
	g.CurFG, g.CurBG, g.CurAttrs = defaultColor, defaultColor, 0
	g.CurULStyle = 0
	g.CurULColor = defaultColor
	g.CharsetG0 = charsetASCII
	g.CharsetG1 = charsetASCII
	g.ActiveG = 0
	g.AutoWrap = true
	g.OriginMode = false
	g.InsertMode = false
	g.Top, g.Bottom = 0, g.Rows-1
	g.saved = savedCursor{}
	g.AltActive = true
	g.ResetView()
	g.ClearSelection()
	g.markAllDirty()
}

// ExitAlt restores the main-screen state captured by EnterAlt: cells,
// cursor, SGR, scroll region, and DECSC slot. The alt buffer is dropped.
// No-op if not currently in alt.
func (g *grid) ExitAlt() {
	if !g.AltActive {
		return
	}
	g.Cells = g.mainSaved.cells
	g.RowWrapped = g.mainSaved.rowWrapped
	g.CursorR, g.CursorC = g.mainSaved.cursorR, g.mainSaved.cursorC
	g.CurFG = g.mainSaved.curFG
	g.CurBG = g.mainSaved.curBG
	g.CurAttrs = g.mainSaved.curAttrs
	g.CurULStyle = g.mainSaved.curULStyle
	g.CurULColor = g.mainSaved.curULColor
	g.CharsetG0 = g.mainSaved.charsetG0
	g.CharsetG1 = g.mainSaved.charsetG1
	g.ActiveG = g.mainSaved.activeG
	g.AutoWrap = g.mainSaved.autoWrap
	g.OriginMode = g.mainSaved.originMode
	g.InsertMode = g.mainSaved.insertMode
	g.Top, g.Bottom = g.mainSaved.top, g.mainSaved.bottom
	g.saved = g.mainSaved.saved
	// Images the alt screen placed die with it; the main screen's come back.
	g.Graphics = g.mainSaved.graphics
	g.occludeMaxR = g.mainSaved.occludeMaxR
	g.mainSaved = altSavedScreen{}
	g.AltActive = false
	g.ResetView()
	g.ClearSelection()
	g.markAllDirty()
}
