package term

import "unicode/utf8"

type parserState uint8

const (
	stGround parserState = iota
	stEsc
	stEscInter
	stCSI
	stDCS
	stDCSEsc
	stOSC    // collecting OSC payload, waiting for BEL or ESC \
	stOSCEsc // saw ESC inside OSC, waiting for terminating '\'
	stAPC    // collecting APC payload (ESC _ … ESC \) — used by Kitty Graphics Protocol
	stAPCEsc // saw ESC inside APC, waiting for terminating '\'
)

// Feed processes b, mutating the grid. Caller holds g.Mu.
// Feed parses b as a complete batch: it commits any trailing grapheme cluster
// before returning, so a single Feed renders everything it was given. Tests and
// direct callers use this. The streaming PTY reader uses feedChunk instead so a
// grapheme cluster straddling a read boundary is not committed half-assembled.
func (p *parser) Feed(b []byte) {
	p.feedChunk(b)
	p.g.FlushGrapheme()
}

// feedChunk parses b but leaves any trailing, still-growing grapheme cluster
// pending in the grid's assembler. The caller commits it (via Grid.FlushGrapheme)
// once the input burst has drained — see readLoop. Control bytes, cursor moves,
// and reports still flush the pending cluster mid-stream (so DSR/CPR are
// accurate); only a printable cluster at the very end of b is carried over.
func (p *parser) feedChunk(b []byte) {

	if p.utfLen > 0 {
		// Complete the partial UTF-8 sequence carried over from the previous
		// Feed call using bytes from b, without allocating. A UTF-8 sequence
		// is at most 4 bytes, so this loop runs at most 3 times.
		for len(b) > 0 {
			p.utf[p.utfLen] = b[0]
			p.utfLen++
			b = b[1:]
			if utf8.FullRune(p.utf[:p.utfLen]) {
				r, size := utf8.DecodeRune(p.utf[:p.utfLen])
				p.g.PutRune(r)
				// DecodeRune may consume fewer bytes than we accumulated (invalid
				// sequence). The unconsumed bytes were already removed from b —
				// prepend them back so the main loop sees them.
				if size < p.utfLen {
					tail := make([]byte, p.utfLen-size+len(b))
					copy(tail, p.utf[size:p.utfLen])
					copy(tail[p.utfLen-size:], b)
					b = tail
				}
				p.utfLen = 0
				break
			}
		}
		if p.utfLen > 0 {
			return // still incomplete; wait for more bytes
		}
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch p.state {
		case stGround:
			// Any control byte (incl. ESC) ends the current printable run:
			// commit the pending grapheme so cursor moves / reports / erases
			// act on the just-written cells.
			if c < 0x20 {
				p.g.FlushGrapheme()
			}
			switch {
			case c == 0x07:
				p.g.Bell()
				i++
			case c == 0x08:
				p.g.Backspace()
				i++
			case c == 0x09:
				p.g.Tab()
				i++
			case c == 0x0A:
				p.g.Newline()
				i++
			case c == 0x0D:
				p.g.CarriageReturn()
				i++
			case c == 0x0E:
				p.g.ActiveG = 1
				i++
			case c == 0x0F:
				p.g.ActiveG = 0
				i++
			case c == 0x1B:
				p.state = stEsc
				i++
			case c < 0x20:
				i++
			default:

				r, sz := utf8.DecodeRune(b[i:])
				if r == utf8.RuneError && sz == 1 && !utf8.FullRune(b[i:]) {
					n := copy(p.utf[:], b[i:])
					p.utfLen = n
					return
				}
				p.g.PutRune(r)
				i += sz
			}
		case stEsc:
			switch c {
			case '[':
				p.state = stCSI
				p.params = p.params[:0]
				p.paramSub = p.paramSub[:0]
				p.curP = 0
				p.hasP = false
				p.nextIsSub = false
				p.leader = 0
				p.intermediate = 0
			case ']':
				p.state = stOSC
				p.oscReset()
			case '_':
				p.state = stAPC
				p.apc = p.apc[:0]
			case 'P':
				p.state = stDCS
				p.dcs = p.dcs[:0]
			case '7':
				p.g.SaveCursor()
				p.state = stGround
			case '8':
				p.g.RestoreCursor()
				p.state = stGround
			case 'D':
				p.g.Newline()
				p.state = stGround
			case 'M':
				p.g.ReverseIndex()
				p.state = stGround
			case 'E':
				p.g.NextLine()
				p.state = stGround
			case 'H':
				p.g.SetTabStop()
				p.state = stGround
			case 'c':
				// RIS — full reset. terminfo rs1; `reset` and `tput init`
				// lead with it to recover a terminal a crashed app left in
				// raw/mouse-reporting mode.
				p.hardReset()
				p.state = stGround
			case '=':
				p.g.AppKeypad = true
				p.state = stGround
			case '>':
				p.g.AppKeypad = false
				p.state = stGround
			case '(', ')', '*', '+', '-', '.', '/':

				p.escInter = c
				p.state = stEscInter
			default:

				p.state = stGround
			}
			i++
		case stEscInter:

			switch p.escInter {
			case '(':
				p.g.CharsetG0 = c
			case ')':
				p.g.CharsetG1 = c
			}
			p.escInter = 0
			p.state = stGround
			i++
		case stCSI:
			switch {
			case c >= '<' && c <= '?' && p.leader == 0 && !p.hasP && len(p.params) == 0:

				p.leader = c
			case c >= '0' && c <= '9':
				p.curP = min(p.curP*10+int(c-'0'), maxCSIParamValue)
				p.hasP = true
			case c == ';':
				if len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.curP = 0
				p.hasP = false
				p.nextIsSub = false
			case c == ':':

				if len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.curP = 0
				p.hasP = false
				p.nextIsSub = true
			case c >= 0x40 && c <= 0x7E:
				if (p.hasP || len(p.params) > 0) && len(p.params) < maxCSIParams {
					p.params = append(p.params, p.curP)
					p.paramSub = append(p.paramSub, p.nextIsSub)
				}
				p.dispatchCSI(c)
				p.state = stGround
				p.curP = 0
				p.hasP = false
				p.leader = 0
				p.intermediate = 0
				p.nextIsSub = false
			case c >= 0x20 && c <= 0x2F:

				p.intermediate = c
			default:

			}
			i++
		case stDCS:
			switch c {
			case 0x1B:
				p.state = stDCSEsc
			default:
				if len(p.dcs) < maxDCSBytes {
					p.dcs = append(p.dcs, c)
				}
			}
			i++
		case stDCSEsc:
			if c == '\\' {
				p.dispatchDCS()
				p.state = stGround
				i++
			} else {
				p.dcs = p.dcs[:0]
				p.state = stEsc
			}
		case stOSC:
			switch c {
			case 0x07:
				p.dispatchOSC()
				p.oscReset()
				p.state = stGround
			case 0x1B:
				p.state = stOSCEsc
			default:
				lim := maxOSCBytes
				if p.oscIsImage {
					lim = maxOSC1337Bytes
				} else if len(p.osc) == 4 &&
					p.osc[0] == '1' && p.osc[1] == '3' &&
					p.osc[2] == '3' && p.osc[3] == '7' && c == ';' {
					p.oscIsImage = true
					lim = maxOSC1337Bytes
				}
				if len(p.osc) < lim {
					p.osc = append(p.osc, c)
				} else {
					// Record the truncation so handlers that care (OSC
					// 1337) can drop the sequence instead of decoding a
					// payload with its tail cut off.
					p.oscTrunc = true
				}
			}
			i++
		case stOSCEsc:
			if c == '\\' {
				p.dispatchOSC()
				p.oscReset()
				p.state = stGround
				i++
			} else {
				p.oscReset()
				p.state = stEsc
			}
		case stAPC:
			switch c {
			case 0x1B:
				p.state = stAPCEsc
			default:
				if len(p.apc) < maxAPCBytes {
					p.apc = append(p.apc, c)
				}
			}
			i++
		case stAPCEsc:
			if c == '\\' {
				p.dispatchAPC()
				p.apc = p.apc[:0]
				p.state = stGround
				i++
			} else {
				p.apc = p.apc[:0]
				p.state = stEsc
			}
		}
	}
	// No trailing FlushGrapheme here: a printable grapheme cluster at the end
	// of b may be the leading bytes of a cluster whose remaining code points
	// arrive in the next chunk (a ZWJ emoji split across a PTY read boundary).
	// Committing it now would write it as broken pieces. The caller flushes
	// once the burst drains; Feed (the batch wrapper) flushes immediately.
}

func appendReply(out []byte, body []byte) []byte {
	out = append(out, '\x1b', 'P')
	out = append(out, body...)
	out = append(out, '\x1b', '\\')
	return out
}
