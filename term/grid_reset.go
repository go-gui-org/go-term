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

	// SGR + character sets back to defaults.
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
	g.KittyKeyFlags = 0
	g.kittyFlagStack = g.kittyFlagStack[:0]

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
	g.Marks = g.Marks[:0]
	g.Graphics = g.Graphics[:0]
	g.ClearSelection()
	g.ResetView()
	g.CursorR, g.CursorC = 0, 0
	g.markAllDirty()
}
