package term

import "strconv"

func splitSemis(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	out := make([][]byte, 0, 4)
	start := 0
	for i, c := range b {
		if c == ';' {
			out = append(out, b[start:i])
			start = i + 1
			// Cap AFTER appending so the invariant covers the tail append
			// below too: with exactly maxXTGETTCAPParts semicolons the old
			// pre-append check let the tail push the count to cap+1.
			if len(out) >= maxXTGETTCAPParts {
				return out
			}
		}
	}
	out = append(out, b[start:])
	return out
}

func (p *parser) replyDECRQSS(body []byte) {
	if p.onReply == nil {
		return
	}

	// Valid bodies are at most 2 bytes (" q") — guard rejects obvious
	// garbage quickly; the default case below handles any unrecognized body.
	if len(body) > 2 {
		p.onReply(appendReply(nil, []byte("0$r")))
		return
	}
	out := make([]byte, 0, 32)
	switch string(body) {
	case "m":
		out = appendReply(out, append([]byte("1$r"), []byte(p.currentSGRString())...))
	case "r":
		top := p.g.Top + 1
		bot := p.g.Bottom + 1
		out = appendReply(out, []byte("1$r"+strconv.Itoa(top)+";"+strconv.Itoa(bot)+"r"))
	case " q":
		out = appendReply(out, []byte("1$r"+strconv.Itoa(p.g.DECSCUSRParam())+" q"))
	case "\"q":
		// DECSCA — 1 when characters are being written protected, else 0.
		ps := 0
		if p.g.CurAttrs&attrProtected != 0 {
			ps = 1
		}
		out = appendReply(out, []byte("1$r"+strconv.Itoa(ps)+"\"q"))
	case "*x":
		// DECSACE — attribute change extent (0 = stream, 2 = rectangle).
		out = appendReply(out, []byte("1$r"+strconv.Itoa(int(p.g.RectExtent))+"*x"))
	default:
		out = appendReply(out, []byte("0$r"))
	}
	p.onReply(out)
}

func xtgettcapValue(name string) (string, bool) {
	switch name {

	// ── Terminal identity ──
	case "TN", "name":
		return "xterm-256color", true

	// ── Boolean caps (empty value = present) ──
	// ccc is deliberately absent: palette redefinition (OSC 4/104) is not
	// implemented, so "can change colors" must not be advertised.
	case "AX", "am", "bce", "fullkbd", "hs", "km", "mir", "mc5i",
		"msgr", "npc", "xenl", "XT":
		return "", true

	// ── SGR / video attributes ──
	case "bold":
		return "\x1b[1m", true
	case "dim":
		return "\x1b[2m", true
	case "sitm":
		return "\x1b[3m", true
	case "ritm":
		return "\x1b[23m", true
	case "smso":
		return "\x1b[7m", true
	case "rmso":
		return "\x1b[27m", true
	case "smul":
		return "\x1b[4m", true
	case "rmul":
		return "\x1b[24m", true
	case "rev":
		return "\x1b[7m", true
	case "sgr0":
		return "\x1b(B\x1b[m", true
	case "Smulx":
		return "\x1b[4:%p1%dm", true
	case "Setulc":
		return "\x1b[58:2::%p1%{65536}%/%d:%p1%{256}%/%{256}%m%d:%p1%{256}%m%dm", true
	case "sgr":
		return "\x1b[0%?%p6%t;1%;%?%p5%t;2%;%?%p2%t;4%;" +
			"%?%p1%p3%|%t;7%;%?%p4%t;5%;%?%p7%t;8%;" +
			"m%?%p9%t\x1b(0%e\x1b(B%;", true

	// ── Character set ──
	case "smacs":
		return "\x1b(0", true
	case "rmacs":
		return "\x1b(B", true
	case "acsc":
		return "``aaffggiijjkkllmmnnooppqqrrssttuuvvwwxxyyzz{{||}}~~", true

	// ── Cursor control ──
	case "civis":
		return "\x1b[?25l", true
	case "cnorm":
		return "\x1b[?12l\x1b[?25h", true
	case "cvvis":
		return "\x1b[?12;25h", true
	case "Ss":
		return "\x1b[%p1%d q", true
	case "Se":
		return "\x1b[2 q", true

	// ── Screen management ──
	case "clear":
		return "\x1b[H\x1b[2J", true
	case "home":
		return "\x1b[H", true
	case "cup":
		return "\x1b[%i%p1%d;%p2%dH", true
	case "hpa":
		return "\x1b[%i%p1%dG", true
	case "vpa":
		return "\x1b[%i%p1%dd", true
	case "csr":
		return "\x1b[%i%p1%d;%p2%dr", true
	case "ed":
		return "\x1b[J", true
	case "el":
		return "\x1b[K", true
	case "el1":
		return "\x1b[1K", true
	case "E3":
		return "\x1b[3J", true

	// ── Cursor movement ──
	case "cub":
		return "\x1b[%p1%dD", true
	case "cub1":
		return "\x08", true
	case "cud":
		return "\x1b[%p1%dB", true
	case "cud1":
		return "\n", true
	case "cuf":
		return "\x1b[%p1%dC", true
	case "cuf1":
		return "\x1b[C", true
	case "cuu":
		return "\x1b[%p1%dA", true
	case "cuu1":
		return "\x1b[A", true

	// ── Character / line insertion and deletion ──
	case "dch":
		return "\x1b[%p1%dP", true
	case "dch1":
		return "\x1b[P", true
	case "dl":
		return "\x1b[%p1%dM", true
	case "dl1":
		return "\x1b[M", true
	case "ich":
		return "\x1b[%p1%d@", true
	case "ich1":
		return "\x1b[@", true
	case "il":
		return "\x1b[%p1%dL", true
	case "il1":
		return "\x1b[L", true
	// ech (CSI Ps X) is deliberately absent: dispatchCSI has no 'X' case,
	// and ncurses uses ech for clear optimizations when advertised —
	// a silently dropped ECH would leave stale cells on screen.
	case "indn":
		return "\x1b[%p1%dS", true
	case "rin":
		return "\x1b[%p1%dT", true

	// ── Scroll / index ──
	case "ind":
		return "\n", true
	case "ri":
		return "\x1bM", true

	// ── Color ──
	// initc / oc / ccc are deliberately absent: OSC 4 (set palette entry)
	// and OSC 104 (reset palette) are not implemented by parser_osc.go.
	case "RGB":
		return "8/8/8", true
	case "Co", "colors":
		return "256", true
	case "pairs":
		return "32767", true
	case "op":
		return "\x1b[39;49m", true
	case "setab":
		return "\x1b[%?%p1%{8}%<%t4%p1%d" +
			"%e%p1%{16}%<%t10%p1%{8}%-%d" +
			"%e48;5;%p1%d%;m", true
	case "setaf":
		return "\x1b[%?%p1%{8}%<%t3%p1%d" +
			"%e%p1%{16}%<%t9%p1%{8}%-%d" +
			"%e38;5;%p1%d%;m", true
	case "setrgbb":
		return "\x1b[48:2:%p1%d:%p2%d:%p3%dm", true
	case "setrgbf":
		return "\x1b[38:2:%p1%d:%p2%d:%p3%dm", true

	// ── Modes ──
	case "smam":
		return "\x1b[?7h", true
	case "rmam":
		return "\x1b[?7l", true
	case "smcup":
		return "\x1b[?1049h", true
	case "rmcup":
		return "\x1b[?1049l", true
	case "smir":
		return "\x1b[4h", true
	case "rmir":
		return "\x1b[4l", true
	case "smkx":
		return "\x1b[?1h\x1b=", true
	case "rmkx":
		return "\x1b[?1l\x1b>", true

	// ── Tab stops ──
	case "ht":
		return "\t", true
	case "hts":
		return "\x1bH", true
	case "tbc":
		return "\x1b[3g", true
	case "it":
		return "8", true

	// ── Misc control ──
	// flash is deliberately absent: it needs DECSCNM (mode 5), which
	// applyDECMode ignores; BEL already triggers the widget's visual
	// flash overlay, so apps falling back to bel get a flash anyway.
	case "bel":
		return "\x07", true
	case "cr":
		return "\r", true
	case "sc":
		return "\x1b7", true
	case "rc":
		return "\x1b8", true

	// ── Status line ──
	case "tsl":
		return "\x1b]2;", true
	case "fsl":
		return "\x07", true
	case "dsl":
		return "\x1b]2;\x07", true

	// ── Focus reporting ──
	case "fe":
		return "\x1b[?1004h", true
	case "fd":
		return "\x1b[?1004l", true
	case "kxIN":
		return "\x1b[I", true
	case "kxOUT":
		return "\x1b[O", true

	// ── Bracketed paste ──
	case "BE":
		return "\x1b[?2004h", true
	case "BD":
		return "\x1b[?2004l", true
	case "PE":
		return "\x1b[201~", true
	case "PS":
		return "\x1b[200~", true

	// ── Synchronized updates ──
	case "Sync":
		return "\x1b[?2026%?%p1%{1}%=%th%el%;", true

	// ── Mouse ──
	case "kmous":
		return "\x1b[<", true
	case "xm":
		// Mouse-report format description, verbatim from ncurses
		// xterm+sm+1006 (SGR 1006 encoding: final 'M' press / 'm' release).
		return "\x1b[<%i%p3%d;%p1%d;%p2%d;%?%p4%tM%em%;", true
	case "XM":
		return "\x1b[?1006;1000%?%p1%{1}%=%th%el%;", true

	// ── Clipboard ──
	case "Ms":
		return "\x1b]52;%p1%s;%p2%s\x07", true

	// ── Terminal queries / reports ──
	case "RV":
		return "\x1b[>c", true
	case "XR":
		return "\x1b[>0q", true
	case "u6":
		return "\x1b[%i%d;%dR", true
	case "u7":
		return "\x1b[6n", true
	case "u8":
		return "\x1b[?%[;0123456789]c", true
	case "u9":
		return "\x1b[c", true
	case "xr":
		// XTVERSION response pattern (ncurses report+version); must match
		// xtversionReply: DCS > | printable-text ST.
		return "\x1bP>|[ -~]+\x1b\\", true

	// ── Keyboard: basic keys ──
	case "kbs":
		return "\x7f", true
	case "kcbt":
		return "\x1b[Z", true
	case "kent":
		return "\x1bOM", true
	case "kcuu1":
		return "\x1b[A", true
	case "kcud1":
		return "\x1b[B", true
	case "kcub1":
		return "\x1b[D", true
	case "kcuf1":
		return "\x1b[C", true
	case "khome":
		return "\x1b[H", true
	case "kend":
		return "\x1b[F", true
	case "kich1":
		return "\x1b[2~", true
	case "kdch1":
		return "\x1b[3~", true
	case "kpp":
		return "\x1b[5~", true
	case "knp":
		return "\x1b[6~", true
	case "kind":
		return "\x1b[1;2B", true
	case "kri":
		return "\x1b[1;2A", true

	// ── Keyboard: function keys ──
	case "kf1":
		return "\x1bOP", true
	case "kf2":
		return "\x1bOQ", true
	case "kf3":
		return "\x1bOR", true
	case "kf4":
		return "\x1bOS", true
	case "kf5":
		return "\x1b[15~", true
	case "kf6":
		return "\x1b[17~", true
	case "kf7":
		return "\x1b[18~", true
	case "kf8":
		return "\x1b[19~", true
	case "kf9":
		return "\x1b[20~", true
	case "kf10":
		return "\x1b[21~", true
	case "kf11":
		return "\x1b[23~", true
	case "kf12":
		return "\x1b[24~", true

	// ── Keyboard: modified arrow keys (Shift/Ctrl/Shift+Ctrl only;
	//   Alt is ESC-prefixed in legacy mode so 3/4/7 variants are omitted) ──
	case "kUP":
		return "\x1b[1;2A", true
	case "kUP5":
		return "\x1b[1;5A", true
	case "kUP6":
		return "\x1b[1;6A", true
	case "kDN":
		return "\x1b[1;2B", true
	case "kDN5":
		return "\x1b[1;5B", true
	case "kDN6":
		return "\x1b[1;6B", true
	case "kLFT":
		return "\x1b[1;2D", true
	case "kLFT5":
		return "\x1b[1;5D", true
	case "kLFT6":
		return "\x1b[1;6D", true
	case "kRIT":
		return "\x1b[1;2C", true
	case "kRIT5":
		return "\x1b[1;5C", true
	case "kRIT6":
		return "\x1b[1;6C", true
	case "kHOM5":
		return "\x1b[1;5H", true
	case "kEND5":
		return "\x1b[1;5F", true

	// ── Keyboard: modified pgup/pgdn (Shift only) ──
	case "kNXT":
		return "\x1b[6;2~", true
	case "kPRV":
		return "\x1b[5;2~", true

	// ── Keyboard: modified function keys (Shift/Ctrl only) ──
	case "kIC":
		return "\x1b[2;2~", true
	case "kIC5":
		return "\x1b[2;5~", true
	case "kIC6":
		return "\x1b[2;6~", true
	case "kDC":
		return "\x1b[3;2~", true
	case "kDC5":
		return "\x1b[3;5~", true
	case "kDC6":
		return "\x1b[3;6~", true

	// ── Custom query caps ──
	case "query-os-name":
		return "\x1b]0;?\x07", true

	default:
		return "", false
	}
}

func (p *parser) replyXTGETTCAP(body []byte) {
	if p.onReply == nil {
		return
	}
	parts := splitSemis(body)
	if len(parts) == 0 {
		p.onReply(appendReply(nil, []byte("0+r")))
		return
	}
	payload := make([]byte, 0, len(body)+32)
	payload = append(payload, "1+r"...)
	for i, part := range parts {
		name, ok := decodeHexBytes(part)
		if !ok {
			p.onReply(appendReply(nil, append([]byte("0+r"), part...)))
			return
		}
		value, ok := xtgettcapValue(name)
		if !ok {
			p.onReply(appendReply(nil, append([]byte("0+r"), part...)))
			return
		}
		if i > 0 {
			payload = append(payload, ';')
		}
		payload = append(payload, part...)
		payload = append(payload, '=')
		payload = append(payload, encodeHexBytes(value)...)
	}
	p.onReply(appendReply(nil, payload))
}

func (p *parser) dispatchDCS() {
	if len(p.dcs) < 2 {

		if len(p.dcs) == 1 && p.dcs[0] == 'q' {
			p.handleSixel(nil)
		}
		return
	}
	switch {
	case p.dcs[0] == '$' && p.dcs[1] == 'q':
		p.replyDECRQSS(p.dcs[2:])
	case p.dcs[0] == '+' && p.dcs[1] == 'q':
		p.replyXTGETTCAP(p.dcs[2:])
	case p.g.SyncOutput && len(p.dcs) >= 3 && p.dcs[0] == '=' && p.dcs[2] == 's':
		switch p.dcs[1] {
		case '1':
			p.g.BeginSync()
		case '2':
			p.g.EndSync()
		}
	default:

		if q := indexSixelFinal(p.dcs); q >= 0 {
			p.handleSixel(p.dcs[q+1:])
		}
	}
}

// indexSixelFinal returns the index of the 'q' final byte that
// introduces a Sixel data stream, or -1 if the payload prefix is not a
// valid sixel param list. The prefix may be empty (bare 'q') or a
// sequence of digits / semicolons.
func indexSixelFinal(dcs []byte) int {
	for i, b := range dcs {
		switch {
		case b == 'q':
			return i
		case b >= '0' && b <= '9', b == ';':

		default:
			return -1
		}
	}
	return -1
}

// handleSixel decodes a Sixel payload (bytes after the 'q' introducer)
// and stashes the resulting image as a grid graphic anchored at the
// cursor. Cursor advances past the image's vertical extent so following
// text starts below it (xterm convention). Decode failures are silent.
func (p *parser) handleSixel(data []byte) {
	img := decodeSixel(data)
	if img == nil {
		return
	}
	b := img.Bounds()
	path := encodePNGFile(img, p.graphicsDir)
	if path == "" {
		return
	}
	_, rows := p.g.AddGraphic(path, b.Dx(), b.Dy())
	for range rows {
		p.g.Newline()
	}
}
