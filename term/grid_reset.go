package term

// Terminal reset paths.
//
// Two levels exist, and terminfo drives both: `reset`/`tput init` send rs1
// (ESC c, RIS) followed by rs2/is2 (which begins with CSI ! p, DECSTR). A
// terminal that ignores them leaves stale SGR, a stale scroll region, a stale
// origin mode, or a stuck alt screen behind after any app that dies badly.

// SoftReset implements DECSTR (CSI ! p). It restores the state a fresh
// terminal starts in *without* touching the screen contents, the scrollback,
// or the alt-screen selection — an app running `tput init` mid-session must
// not lose what is on screen.
//
// Divergence from VT510, which specifies autowrap OFF: modern emulators
// (xterm's effective behavior, kitty, ghostty) reset it to ON, which is also
// the terminfo `am` default. Turning it off here would silently break line
// wrapping for every program that runs `reset`, since neither rs2 nor is2
// re-enables it.
func (g *grid) SoftReset() {
	g.FlushGrapheme()

	// SGR + character sets back to defaults. Zeroing CurAttrs also drops
	// DECSCA protection, which VT510's reset table requires ("character
	// attribute → normal, erasable by DECSEL and DECSED").
	g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, 0
	g.CurULStyle, g.CurULColor = ulNone, DefaultColor
	g.CurLinkID = 0
	g.CharsetG0, g.CharsetG1, g.ActiveG = charsetASCII, charsetASCII, 0

	// Modes. The cursor itself does not move (DECSTR resets the *saved*
	// cursor, not the active one); a following RestoreCursor homes it.
	g.CursorVisible = true
	g.OriginMode = false
	g.InsertMode = false
	g.AutoWrap = true
	g.AppCursorKeys = false
	g.AppKeypad = false
	g.saved = savedCursor{}

	// Scroll region back to the full screen.
	g.Top, g.Bottom = 0, g.Rows-1

	// A synchronized-update block open at reset time would suppress repaints
	// indefinitely; end it so the screen is live again.
	g.SyncOutput = false
	if g.SyncActive {
		g.EndSync()
	}
	g.markAllDirty()
}

// ScreenAlignment implements DECALN (ESC # 8): fill every cell of the screen
// with 'E' in the default attributes, reset the scroll region to the full
// screen, and home the cursor. Originally a CRT alignment pattern; today it
// survives as the fastest way for a test program to put known content in every
// cell, which is exactly how vttest uses it.
//
// Nothing goes to scrollback: this overwrites the screen in place.
func (g *grid) ScreenAlignment() {
	g.FlushGrapheme()

	// Default attributes, not the current SGR — DECALN's pattern is defined
	// as the plain character, and vttest reads the result visually.
	fill := defaultCell()
	fill.Ch = 'E'
	for i := range g.Cells {
		g.Cells[i] = fill
	}
	// Flat fill, so occlusion is not carried by eraseSpan — see ClearAll.
	if len(g.Graphics) != 0 {
		g.occludeGraphics(0, g.Rows, 0, g.Cols)
	}
	for r := range g.RowWrapped {
		g.RowWrapped[r] = false
	}
	g.Top, g.Bottom = 0, g.Rows-1
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
}

// SetColumnMode implements DECCOLM (DEC ?3). cols is 80 or 132 to pin the
// grid's width, or 0 to release the pin and let the width follow the window
// again. Per DEC STD 070 and xterm, switching column mode also erases the
// screen, homes the cursor and resets the scroll region — applications rely on
// that (vttest draws each of its boxes immediately after the switch and
// expects a blank canvas).
//
// The screen is erased *before* the resize so the discarded content is not
// first reflowed into scrollback: DECCOLM is a mode switch, not a window
// resize, and the text it drops was never meant to be history.
func (g *grid) SetColumnMode(cols int) {
	if g.ColumnMode == cols {
		return
	}
	g.FlushGrapheme()
	g.ColumnMode = cols

	g.ClearAll()
	// Drop the wrap flags with the text: a stale one would make the reflow
	// inside Resize splice together rows that no longer hold anything.
	for r := range g.RowWrapped {
		g.RowWrapped[r] = false
	}
	// Only when the width actually moves: Resize runs the full reflow, which
	// costs ~100µs and ~700 KB of arena regardless of how little content
	// there is, and a child can drive this path with 6 bytes of input. A
	// window already at the requested width (the common `?3l` on an 80-column
	// pane) must not pay it.
	if cols > 0 && cols != g.Cols {
		g.Resize(g.Rows, cols)
	}
	// Resize already normalises the region, but the release path (cols == 0)
	// does not resize here — the widget restores the window-derived width on
	// its next frame — so reset the region unconditionally.
	g.Top, g.Bottom = 0, g.Rows-1
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
}

// HardReset implements RIS (ESC c) — the power-on state. Everything SoftReset
// does, plus: leave the alt screen, wipe the screen and scrollback, clear
// images and shell marks, restore default tab stops, and drop every host-set
// reporting mode (mouse, bracketed paste, focus, Kitty keyboard). This is what
// `reset` relies on to recover a terminal left in raw/mouse-reporting mode by
// a crashed application.
//
// Not reset: the embedder's Theme (including OSC 10/11 overrides, which the
// grid cannot distinguish from an embedder-supplied theme) and the window
// title. Both belong to the widget, not the cell buffer. OSC 4 palette
// overrides *are* reset — they live in their own layer, so dropping them
// cannot take an embedder-supplied color with them.
func (g *grid) HardReset() {
	g.ExitAlt() // no-op when the main screen is already active
	g.SoftReset()

	// Reporting modes an application may have left enabled.
	g.MouseTrack, g.MouseTrackBtn, g.MouseTrackAny = false, false, false
	g.MouseSGR, g.MouseSGRPixels = false, false
	g.BracketedPaste = false
	g.FocusReporting = false
	g.ColorSchemeUpdates = false
	g.KittyKeyFlags = 0
	g.kittyFlagStack = g.kittyFlagStack[:0]

	// OSC 22 pointer shape and OSC 9;4 progress. Both are child-set decorations
	// with no reset sequence of their own, so `reset` is the only way out of a
	// pane left showing a crosshair or a stuck progress bar by a killed job.
	g.PointerShape = pointerDefault
	g.ProgressState, g.ProgressValue = 0, 0
	g.OverlayVersion++

	// DECSACE back to the power-on stream extent. DECSTR leaves it alone —
	// VT510's soft-reset table does not list it.
	g.RectExtent = 0

	// DECSCNM. A pane left in reverse video by a crashed app is exactly the
	// kind of state `reset` exists to clear.
	g.ReverseScreen = false

	// DECCOLM: release the column pin and revoke permission to set it again.
	// Deliberately not in SoftReset — VT510's soft-reset table does not list
	// DECCOLM, and xterm's DECSTR leaves the column count alone. Assigned
	// directly rather than through SetColumnMode because the surrounding
	// HardReset already erases the screen and homes the cursor; the widget's
	// next frame restores the window-derived width.
	g.AllowColumnMode = false
	g.ColumnMode = 0

	// Cursor appearance (DECSCUSR / OSC 12).
	g.cursorShape = cursorBlock
	g.CursorBlink = false
	g.CursorColor = DefaultColor

	// OSC 4 palette overrides. Unlike OSC 10/11 these are unambiguously
	// child-set, so a power-on reset drops them (xterm/kitty do the same).
	g.ResetPalette()
	g.lastGraphic, g.lastGraphicID, g.lastGraphicW = 0, 0, 0
	g.gphBuf = g.gphBuf[:0]

	// Default tab stops: every eight columns.
	for c := range MaxGridDim {
		g.TabStops[c] = c >= 8 && c%8 == 0
	}

	// Screen + history. Dropping scrollback shifts the content-row coordinate
	// space, so marks, graphics and any selection (all content-row based) go
	// with it — the same bookkeeping ED 3 performs.
	for i := range g.Cells {
		g.Cells[i] = defaultCell()
	}
	for r := range g.RowWrapped {
		g.RowWrapped[r] = false
	}
	if g.Scrollback.Len() > 0 {
		g.Scrollback.DropBacking()
	}
	// The reflow scratch follows the ring: nothing reflows on a cleared
	// screen, so its blocks only retain memory here.
	g.reflowArena = rowArena{}
	g.Marks = g.Marks[:0]
	g.marksVer++
	g.Graphics = g.Graphics[:0]
	// Virtual placements go too: RIS wipes the screen, so every placeholder
	// cell that could have named one is gone as well.
	g.deleteAllVirtualImages()
	g.ClearSelection()
	g.ResetView()
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
}
