package term

import "strconv"

func (p *parser) dispatchCSI(final byte) {
	if p.leader != 0 {
		switch p.leader {
		case '?':
			switch final {
			case 'h':
				p.applyDECMode(true)
			case 'l':
				p.applyDECMode(false)
			case 'u':

				if p.onReply != nil {
					b := make([]byte, 0, 16)
					b = append(b, "\x1b[?"...)
					b = strconv.AppendUint(b, uint64(p.g.KittyKeyFlags), 10)
					b = append(b, 'u')
					p.onReply(b)
				}
			case 'n':
				// DECXCPR (CSI ? 6 n) — extended cursor position report. The
				// reply carries the private marker and, on a real VT, a page
				// number; xterm omits the page and clients accept that.
				if p.param(0, 0) == 6 && p.onReply != nil {
					row, col := p.g.CursorR+1, p.g.CursorC+1
					p.onReply([]byte("\x1b[?" + strconv.Itoa(row) + ";" +
						strconv.Itoa(col) + "R"))
				}
			case 'J':
				// DECSED — selective erase in display (protection honored).
				p.g.SelectiveEraseInDisplay(p.param(0, 0))
			case 'K':
				// DECSEL — selective erase in line.
				p.g.SelectiveEraseInLine(p.param(0, 0))
			case 'p':

				if p.intermediate == '$' && p.onReply != nil {
					n := p.param(0, 0)
					if n < 0 {
						n = 0
					}
					v := p.decModeState(n)
					b := make([]byte, 0, 24)
					b = append(b, "\x1b[?"...)
					b = strconv.AppendUint(b, uint64(n), 10)
					b = append(b, ';')
					b = strconv.AppendUint(b, uint64(v), 10)
					b = append(b, '$', 'y')
					p.onReply(b)
				}
			}
		case '>':

			switch final {
			case 'u':
				p.g.PushKittyKeyFlags(uint32(p.param(0, 0)))
			case 'c':

				if p.param(0, 0) == 0 && p.onReply != nil {
					p.onReply([]byte(da2Reply))
				}
			case 'q':
				// XTVERSION (CSI > q / CSI > 0 q): report name + version.
				if p.param(0, 0) == 0 && p.onReply != nil {
					p.onReply([]byte(xtversionReply))
				}
			}
		case '<':

			if final == 'u' {
				p.g.PopKittyKeyFlags(p.param(0, 1))
			}
		case '=':

			if final == 'u' {
				p.g.SetKittyKeyFlags(uint32(p.param(0, 0)))
			}
		}
		return
	}
	switch final {
	case 'h':
		p.applyMode(true)
	case 'l':
		p.applyMode(false)
	case 'm':
		p.applySGR()
	case 's':
		p.g.SaveCursor()
	case 'u':
		p.g.RestoreCursor()
	case 'A':
		p.g.CursorUp(p.param(0, 1))
	case 'B', 'e':
		p.g.CursorDown(p.param(0, 1))
	case 'C', 'a':
		p.g.CursorForward(p.param(0, 1))
	case 'D':
		p.g.CursorBack(p.param(0, 1))
	case 'E': // CNL — Cursor Next Line; column always resets to 0, not to scroll-region margin
		p.g.CursorDown(p.param(0, 1))
		p.g.MoveCursor(p.g.CursorR, 0)
	case 'F': // CPL — Cursor Preceding Line; same: column 0, not origin-mode relative
		p.g.CursorUp(p.param(0, 1))
		p.g.MoveCursor(p.g.CursorR, 0)
	case 'G', '`':
		p.g.MoveCursor(p.g.CursorR, p.param(0, 1)-1)
	case 'd':
		p.g.MoveCursorOrigin(p.param(0, 1)-1, p.g.CursorC)
	case 'H', 'f':
		p.g.MoveCursorOrigin(p.param(0, 1)-1, p.param(1, 1)-1)
	case 'J':
		p.g.EraseInDisplay(p.param(0, 0))
	case 'K':
		p.g.EraseInLine(p.param(0, 0))
	case 'X':
		p.g.EraseChars(p.param(0, 1))
	case 'I':
		p.g.TabForward(p.param(0, 1))
	case 'Z':
		p.g.TabBackward(p.param(0, 1))
	case 'b':
		// REP — repeat the preceding graphic character.
		p.g.RepeatLast(p.param(0, 1))
	case 'p':
		// DECSTR (CSI ! p) — soft terminal reset. The '$' intermediate form
		// (ANSI DECRQM) is handled below; no other CSI final 'p' is defined.
		switch p.intermediate {
		case '!':
			p.g.SoftReset()
		case '$':
			p.replyANSIDECRQM()
		}
	case 'r':
		// DECSTBM bare; DECCARA (change attributes in area) with '$'.
		if p.intermediate == '$' {
			p.g.ChangeAttrsRect(p.params)
			break
		}
		top := p.param(0, 1) - 1
		bot := p.param(1, p.g.Rows) - 1
		p.g.SetScrollRegion(top, bot)
	case 'L':
		p.g.InsertLines(p.param(0, 1))
	case 'M':
		p.g.DeleteLines(p.param(0, 1))
	case '@':
		p.g.InsertChars(p.param(0, 1))
	case 'P':
		p.g.DeleteChars(p.param(0, 1))
	case 'S':
		p.g.ScrollUp(p.param(0, 1))
	case 'T':
		p.g.ScrollDown(p.param(0, 1))
	case 'c':

		if p.param(0, 0) == 0 && p.onReply != nil {
			p.onReply([]byte(da1Reply))
		}
	case 'n':
		// DSR. 5 = "are you OK" (answer: terminal ready), 6 = CPR.
		if p.onReply == nil {
			break
		}
		switch p.param(0, 0) {
		case 5:
			p.onReply([]byte("\x1b[0n"))
		case 6:
			row, col := p.g.CursorR+1, p.g.CursorC+1
			p.onReply([]byte("\x1b[" + strconv.Itoa(row) + ";" + strconv.Itoa(col) + "R"))
		}
	case 't':
		// DECRARA (reverse attributes in area) with '$'; XTWINOPS bare.
		if p.intermediate == '$' {
			p.g.ReverseAttrsRect(p.params)
			break
		}
		// XTWINOPS. Only the read-only geometry queries and the title stack
		// are honored — window manipulation ops (move/resize/raise…) are
		// ignored, an embedded widget must not let the app drive the host
		// window. Cell pixel sizes come from the widget's measurement
		// (CellPxW/CellPxH, 0 before the first frame); a 0 reply is valid and
		// clients fall back.
		px := func(f float32) int { return int(f + 0.5) }
		switch p.param(0, 0) {
		case 14: // report text-area size in pixels: CSI 4 ; height ; width t
			if p.onReply != nil {
				h, w := px(float32(p.g.Rows)*p.g.CellPxH), px(float32(p.g.Cols)*p.g.CellPxW)
				p.onReply([]byte("\x1b[4;" + strconv.Itoa(h) + ";" + strconv.Itoa(w) + "t"))
			}
		case 16: // report cell size in pixels: CSI 6 ; height ; width t
			if p.onReply != nil {
				h, w := px(p.g.CellPxH), px(p.g.CellPxW)
				p.onReply([]byte("\x1b[6;" + strconv.Itoa(h) + ";" + strconv.Itoa(w) + "t"))
			}
		case 22: // push the current title onto the stack
			p.pushTitle()
		case 23: // pop it back
			p.popTitle()
		}
	case 'g':

		switch p.param(0, 0) {
		case 0:
			p.g.ClearTabStop(false)
		case 3:
			p.g.ClearTabStop(true)
		}
	case 'q':

		switch p.intermediate {
		case ' ':
			p.g.ApplyDECSCUSR(p.param(0, 0))
		case '"':
			// DECSCA — select character protection attribute.
			p.g.SetProtection(p.param(0, 0))
		}
	case 'x':
		// DECFRA (fill area) with '$'; DECSACE (attribute change extent)
		// with '*'. A bare 'x' is DECREQTPARM, which this terminal ignores.
		switch p.intermediate {
		case '$':
			p.g.FillRect(paramAt(p.params, 0), paramAt(p.params, 1),
				paramAt(p.params, 2), paramAt(p.params, 3), paramAt(p.params, 4))
		case '*':
			p.g.SetRectExtent(p.param(0, 0))
		}
	case 'z':
		// DECERA — erase rectangular area.
		if p.intermediate == '$' {
			p.g.EraseRect(paramAt(p.params, 0), paramAt(p.params, 1),
				paramAt(p.params, 2), paramAt(p.params, 3))
		}
	case '{':
		// DECSERA — selective erase rectangular area (protection honored).
		if p.intermediate == '$' {
			p.g.SelectiveEraseRect(paramAt(p.params, 0), paramAt(p.params, 1),
				paramAt(p.params, 2), paramAt(p.params, 3))
		}
	case 'v':
		// DECCRA — copy rectangular area. Params 4 and 7 are the source and
		// destination page numbers; with one page they resolve to it whatever
		// they say, so they are parsed past rather than validated.
		if p.intermediate == '$' {
			p.g.CopyRect(paramAt(p.params, 0), paramAt(p.params, 1),
				paramAt(p.params, 2), paramAt(p.params, 3),
				paramAt(p.params, 5), paramAt(p.params, 6))
		}
	default:

	}
}

// applyDECMode handles DEC private mode set/reset (CSI ? Pn h / l).
// Only the modes the widget honors are wired; unknown modes are
// silently dropped so apps that probe many modes don't break.
func (p *parser) applyDECMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 25:
			p.g.CursorVisible = set
		case 47, 1047:
			if set {
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
			}
		case 1049:
			if set {
				p.g.SaveCursor()
				p.g.EnterAlt()
			} else {
				p.g.ExitAlt()
				p.g.RestoreCursor()
			}
		case 2004:
			p.g.BracketedPaste = set
		case 1004:
			p.g.FocusReporting = set
		case 2026:
			// Mode 2026 synchronized updates: DECSET *begins* a sync
			// block (repaints suppressed), DECRST ends it and flushes.
			// There is no separate capability handshake in the CSI form —
			// treating h as "enable only" would silently render torn
			// frames for every modern client. SyncOutput gates the legacy
			// iTerm2 DCS =1s/=2s form; DECRQM reports SyncActive (see
			// decModeState) so a watchdog-forced end is observable.
			p.g.SyncOutput = set
			if set {
				p.g.BeginSync()
			} else {
				p.g.EndSync()
			}
		case 2027:
			// Grapheme clustering is unconditional (PutRune always clusters),
			// so DECSET/DECRST 2027 are accepted as no-ops. decModeState
			// reports it permanently set.
		case 7:
			p.g.AutoWrap = set
		case 6:
			p.g.OriginMode = set
			if set && p.g.regionValid() {
				p.g.CursorR, p.g.CursorC = p.g.Top, 0
			} else if !set {
				p.g.CursorR, p.g.CursorC = 0, 0
			}
		case 1:
			p.g.AppCursorKeys = set
		case 66:
			p.g.AppKeypad = set
		case 1000:
			p.g.MouseTrack = set
		case 1002:
			p.g.MouseTrackBtn = set
		case 1003:
			p.g.MouseTrackAny = set
		case 1006:
			p.g.MouseSGR = set
		case 1016:
			p.g.MouseSGRPixels = set
		}
	}
}

func (p *parser) applyMode(set bool) {
	for _, n := range p.params {
		switch n {
		case 4:
			p.g.InsertMode = set
		}
	}
}

// replyANSIDECRQM answers the non-private DECRQM form (CSI Ps $ p) with
// DECRPM (CSI Ps ; Pv $ y). Only ANSI modes the emulator models get a real
// state; anything else reports 0 (unrecognized), which is what a client needs
// to hear before falling back.
func (p *parser) replyANSIDECRQM() {
	if p.onReply == nil {
		return
	}
	n := p.param(0, 0)
	if n < 0 {
		n = 0
	}
	v := 0
	switch n {
	case 4: // IRM — insert/replace
		v = boolState(p.g.InsertMode)
	case 20: // LNM — LF never implies CR here, and cannot be turned on
		v = 4 // PERMANENTLY_RESET
	}
	b := make([]byte, 0, 24)
	b = append(b, "\x1b["...)
	b = strconv.AppendUint(b, uint64(n), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(v), 10)
	b = append(b, '$', 'y')
	p.onReply(b)
}

// decModeState returns the current state of a DEC private mode:
// 1 = set, 2 = reset, 0 = unrecognized. Used for DECRQM replies.
func (p *parser) decModeState(n int) int {
	switch n {
	case 1:
		return boolState(p.g.AppCursorKeys)
	case 6:
		return boolState(p.g.OriginMode)
	case 7:
		return boolState(p.g.AutoWrap)
	case 25:
		return boolState(p.g.CursorVisible)
	case 47, 1047, 1049:
		return boolState(p.g.AltActive)
	case 66:
		return boolState(p.g.AppKeypad)
	case 1000:
		return boolState(p.g.MouseTrack)
	case 1002:
		return boolState(p.g.MouseTrackBtn)
	case 1003:
		return boolState(p.g.MouseTrackAny)
	case 1004:
		return boolState(p.g.FocusReporting)
	case 1006:
		return boolState(p.g.MouseSGR)
	case 1016:
		return boolState(p.g.MouseSGRPixels)
	case 2004:
		return boolState(p.g.BracketedPaste)
	case 2026:
		// Report whether a synchronized-update block is currently open —
		// per the mode-2026 spec ("currently in synchronized output
		// mode"), and matching kitty/alacritty after a timeout-forced
		// end. SyncOutput alone would go stale when the widget's
		// watchdog force-ends a block.
		return boolState(p.g.SyncActive)
	case 2027:
		return 3 // PERMANENTLY_SET — grapheme clustering is always on
	default:
		return 0
	}
}

// boolState returns 1 if b is true, 2 if false (DECRPM state encoding).
func boolState(b bool) int {
	if b {
		return 1
	}
	return 2
}

// param returns params[i] or def if missing or zero (per VT semantics
// where "0" often means "1" for cursor moves).
func (p *parser) param(i, def int) int {
	if i >= len(p.params) {
		return def
	}
	if p.params[i] == 0 {
		return def
	}
	return p.params[i]
}

// applyExtendedColor handles SGR 38/48/58 sub-forms (;5;n and ;2;r;g;b)
// starting at params[i] (the 38/48/58 itself). target receives the result.
// Returns the new value of i; the outer loop's `i++` advances past the last
// param consumed. On truncation, returns len(params)-1.
func applyExtendedColor(params []int, i int, target *uint32) int {
	if i < 0 || i+1 >= len(params) {
		return len(params) - 1
	}
	switch params[i+1] {
	case 5:
		if i+2 >= len(params) {
			return len(params) - 1
		}
		*target = paletteColor(clampU8(params[i+2]))
		return i + 2
	case 2:
		if i+4 >= len(params) {
			return len(params) - 1
		}
		*target = rgbColor(
			clampU8(params[i+2]),
			clampU8(params[i+3]),
			clampU8(params[i+4]),
		)
		return i + 4
	default:
		return len(params) - 1
	}
}

// clampU8 saturates an int to 0..255.
func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func (p *parser) applySGR() {
	g := p.g
	// SGR reset clears the visual attributes but must leave DECSCA alone:
	// "DECSCA does not affect visual character attributes set by SGR", and
	// the converse holds too — only DECSCA, DECSTR and RIS change protection.
	if len(p.params) == 0 {

		g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, g.CurAttrs&attrProtected
		g.CurULStyle = 0
		g.CurULColor = DefaultColor
		return
	}
	for i := 0; i < len(p.params); i++ {
		n := p.params[i]
		switch {
		case n == 0:
			g.CurFG, g.CurBG, g.CurAttrs = DefaultColor, DefaultColor, g.CurAttrs&attrProtected
			g.CurULStyle = 0
			g.CurULColor = DefaultColor
		case n == 1:
			g.CurAttrs |= attrBold
		case n == 2:
			g.CurAttrs |= attrDim
		case n == 3:
			g.CurAttrs |= attrItalic
		case n == 4:

			ulStyle := ulSingle
			if i+1 < len(p.params) && i+1 < len(p.paramSub) && p.paramSub[i+1] {
				sub := p.params[i+1]
				i++
				if sub == 0 {

					g.CurAttrs &^= attrUnderline
					g.CurULStyle = 0
					continue
				}
				if sub < 1 || sub > 5 {
					continue
				}
				ulStyle = uint8(sub)
			}
			g.CurAttrs |= attrUnderline
			g.CurULStyle = ulStyle
		case n == 5, n == 6:
			// Slow (5) and rapid (6) blink share one attribute; no emulator
			// distinguishes the rates.
			g.CurAttrs |= attrBlink
		case n == 7:
			g.CurAttrs |= attrInverse
		case n == 8:
			g.CurAttrs |= attrConceal
		case n == 9:
			g.CurAttrs |= attrStrikethrough
		case n == 21:

			g.CurAttrs |= attrUnderline
			g.CurULStyle = ulDouble
		case n == 22:
			g.CurAttrs &^= attrBold | attrDim
		case n == 23:
			g.CurAttrs &^= attrItalic
		case n == 25:
			g.CurAttrs &^= attrBlink
		case n == 24:
			g.CurAttrs &^= attrUnderline
			g.CurULStyle = 0
			g.CurULColor = DefaultColor
		case n == 27:
			g.CurAttrs &^= attrInverse
		case n == 28:
			g.CurAttrs &^= attrConceal
		case n == 29:
			g.CurAttrs &^= attrStrikethrough
		case n >= 30 && n <= 37:
			g.CurFG = paletteColor(uint8(n - 30))
		case n == 39:
			g.CurFG = DefaultColor
		case n >= 40 && n <= 47:
			g.CurBG = paletteColor(uint8(n - 40))
		case n == 49:
			g.CurBG = DefaultColor
		case n >= 90 && n <= 97:
			g.CurFG = paletteColor(uint8(n - 90 + 8))
		case n >= 100 && n <= 107:
			g.CurBG = paletteColor(uint8(n - 100 + 8))
		case n == 38 || n == 48:

			target := &g.CurFG
			if n == 48 {
				target = &g.CurBG
			}
			i = applyExtendedColor(p.params, i, target)
		case n == 58:

			i = applyExtendedColor(p.params, i, &g.CurULColor)
		case n == 59:

			g.CurULColor = DefaultColor
		}
	}
}
