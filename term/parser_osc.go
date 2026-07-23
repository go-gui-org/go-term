package term

import (
	"encoding/base64"
	"path"
	"strconv"
	"strings"
)

// notifyMax caps notification strings before handing them to onNotify.
// Prevents subprocess arg-length overflows and large heap allocations
// from hostile OSC payloads.
const notifyMax = 512

func encodeHexBytes(s string) []byte {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(s)*2)
	for i := 0; i < len(s); i++ {
		b := s[i]
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0F])
	}
	return out
}

func decodeHexBytes(b []byte) (string, bool) {
	if len(b)%2 != 0 {
		return "", false
	}
	out := make([]byte, len(b)/2)
	for i := 0; i < len(b); i += 2 {
		hi := fromHexNibble(b[i])
		lo := fromHexNibble(b[i+1])
		if hi < 0 || lo < 0 {
			return "", false
		}
		out[i/2] = byte(hi<<4 | lo)
	}
	return string(out), true
}

func fromHexNibble(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	default:
		return -1
	}
}

// dispatchOSC parses the accumulated OSC payload as "Ps;Pt" and
// dispatches recognized commands. Anything malformed or unknown is
// silently dropped (xterm behavior). Called with g.Mu held.
func (p *parser) dispatchOSC() {
	if len(p.osc) == 0 {
		return
	}
	sep := -1
	for i, b := range p.osc {
		if b == ';' {
			sep = i
			break
		}
	}
	if sep <= 0 {
		// Bare "OSC 104 ST" resets the whole palette — the one command
		// with no argument. Everything else requires a ";Pt" payload.
		if string(p.osc) == "104" {
			p.g.ResetPalette()
		}
		return
	}
	ps := 0
	for i := range sep {
		c := p.osc[i]
		if c < '0' || c > '9' {
			return
		}
		ps = ps*10 + int(c-'0')
		if ps > 1<<20 {
			return
		}
	}
	pt := string(p.osc[sep+1:])
	switch ps {
	case 0, 1, 2:
		// Tracked as well as forwarded so XTWINOPS 22/23 (title stack) has
		// something to push.
		p.curTitle = pt
		if p.onTitle != nil {
			p.onTitle(pt)
		}
	case 7:
		// Accept standard file:// URIs and bare absolute paths (common in
		// zsh/fish integrations). Other schemes are rejected. Control
		// characters are stripped by sanitizeOSCString. Both forms are
		// path-cleaned so embedders don't receive traversal strings.
		if strings.HasPrefix(pt, "file://") || strings.HasPrefix(pt, "/") {
			cwd := sanitizeOSCString(pt)
			if strings.HasPrefix(cwd, "file://") {
				// file://[host]/path — clean the path portion only.
				rest := cwd[len("file://"):]
				if slash := strings.IndexByte(rest, '/'); slash >= 0 {
					cwd = "file://" + rest[:slash] + path.Clean(rest[slash:])
				}
			} else if strings.HasPrefix(cwd, "/") {
				cwd = path.Clean(cwd)
			}
			p.g.Cwd = cwd
		}
	case 4:
		p.handleOSC4(pt)
	case 104:
		p.handleOSC104(pt)
	case 10, 11, 12:

		if pt == "?" {
			r, g, b := p.g.dynColorRGB(ps)
			reply := "\x1b]" + strconv.Itoa(ps) + ";rgb:" +
				oscHexWord(r) + "/" + oscHexWord(g) + "/" + oscHexWord(b) + "\x1b\\"
			if p.onReply != nil {
				p.onReply([]byte(reply))
			}
			return
		}
		if c, ok := parseXColor(pt); ok {
			p.g.SetDynColor(ps, c)
		}
	case 8:

		semiIdx := strings.IndexByte(pt, ';')
		if semiIdx < 0 {
			return
		}
		uri := pt[semiIdx+1:]
		if uri == "" {
			p.g.CurLinkID = 0
		} else {
			p.g.CurLinkID = p.g.internLink(uri)
		}
	case 133:

		if len(pt) == 0 {
			return
		}
		switch pt[0] {
		case 'A':
			p.g.AddMark(markPromptStart)
		case 'B':
			p.g.AddMark(markCommandStart)
		case 'C':
			p.g.AddMark(markOutputStart)
		case 'D':
			p.g.AddMark(markCommandEnd)
		}
	case 9:
		// iTerm2-style notification: OSC 9 ; message BEL — body only, no title.
		if p.onNotify != nil {
			p.onNotify("", truncatePaste(pt, notifyMax))
		}
	case 52:

		semiIdx := strings.IndexByte(pt, ';')
		if semiIdx < 0 {
			return
		}
		b64 := pt[semiIdx+1:]
		if b64 == "?" {
			return
		}
		data, err := decodeBase64String(b64)
		if err != nil {
			return
		}
		if p.allowClipboardWrite && p.onClipboard != nil {
			p.onClipboard(data)
		}
	case 777:
		// libnotify-style: OSC 777 ; notify ; title ; body BEL
		parts := strings.SplitN(pt, ";", 3)
		if len(parts) < 2 || parts[0] != "notify" {
			return
		}
		title, body := "", parts[1]
		if len(parts) == 3 {
			title, body = parts[1], parts[2]
		}
		if p.onNotify != nil {
			p.onNotify(truncatePaste(title, notifyMax), truncatePaste(body, notifyMax))
		}
	case 1337:
		p.handleOSC1337(pt)
	}
}

// handleOSC4 implements OSC 4 — palette set and query. The payload is a
// sequence of "index ; spec" pairs, e.g. "1;#ff0000;2;rgb:00/ff/00". A spec
// of "?" queries the current color instead of setting it. Processing stops
// at the first malformed pair (xterm behavior); pairs applied before it
// stand. Called with g.Mu held.
func (p *parser) handleOSC4(pt string) {
	for rest := pt; rest != ""; {
		idxStr, tail, ok := strings.Cut(rest, ";")
		if !ok {
			return // trailing index with no spec
		}
		spec, next, hasNext := strings.Cut(tail, ";")
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 || idx > 255 {
			return
		}
		if spec == "?" {
			p.replyPaletteColor(uint8(idx))
		} else if c, ok := parseXColor(spec); ok {
			p.g.SetPaletteColor(uint8(idx), c)
		} else {
			return
		}
		if !hasNext {
			return
		}
		rest = next
	}
}

// replyPaletteColor answers an OSC 4 query with the index's effective
// color, in the same "rgb:RRRR/GGGG/BBBB" form the OSC 10/11 replies use.
func (p *parser) replyPaletteColor(idx uint8) {
	if p.onReply == nil {
		return
	}
	r, g, b := p.g.paletteColorRGB(idx)
	p.onReply([]byte("\x1b]4;" + strconv.Itoa(int(idx)) + ";rgb:" +
		oscHexWord(r) + "/" + oscHexWord(g) + "/" + oscHexWord(b) + "\x1b\\"))
}

// handleOSC104 implements OSC 104 — palette reset. An empty payload resets
// every entry; otherwise each ';'-separated index is reset individually.
// Stops at the first non-numeric or out-of-range index. Called with g.Mu
// held. (The no-argument form "OSC 104 ST" is handled in dispatchOSC,
// which otherwise requires a ';' before the payload.)
func (p *parser) handleOSC104(pt string) {
	if pt == "" {
		p.g.ResetPalette()
		return
	}
	for rest := pt; rest != ""; {
		idxStr, next, hasNext := strings.Cut(rest, ";")
		idx, err := strconv.Atoi(idxStr)
		if err != nil || idx < 0 || idx > 255 {
			return
		}
		p.g.ResetPaletteColor(uint8(idx))
		if !hasNext {
			return
		}
		rest = next
	}
}

// handleOSC1337 implements the iTerm2 inline image protocol.
// Payload format: File=[key=value;...]:base64data
// Only renders when inline=1 is present; all other cases drop silently.
func (p *parser) handleOSC1337(pt string) {
	const prefix = "File="
	if !strings.HasPrefix(pt, prefix) {
		return
	}
	rest := pt[len(prefix):]
	before, after, ok := strings.Cut(rest, ":")
	if !ok {
		return
	}
	args, b64 := before, after

	if !strings.Contains(";"+args+";", ";inline=1;") {
		return
	}

	raw, err := decodeBase64String(b64)
	if err != nil {
		return
	}
	img := decodeImageBytes(raw)
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

// parseXColor parses an X11 color string into a packed rgbColor.
// Accepts "rgb:H/H/H" through "rgb:HHHH/HHHH/HHHH" and the "#" forms
// #RGB, #RRGGBB, #RRRGGGBBB, #RRRRGGGGBBBB. Color *names* are not
// supported — programs that recolor the palette emit hex.
func parseXColor(s string) (uint32, bool) {
	if strings.HasPrefix(s, "rgb:") {
		parts := strings.SplitN(s[4:], "/", 3)
		if len(parts) != 3 {
			return 0, false
		}
		var ch [3]uint8
		for i, p := range parts {
			v, ok := parseHexChannel(p)
			if !ok {
				return 0, false
			}
			ch[i] = v
		}
		return rgbColor(ch[0], ch[1], ch[2]), true
	}
	if len(s) > 1 && s[0] == '#' {
		// Equal-width channels: #RGB, #RRGGBB, #RRRGGGBBB, #RRRRGGGGBBBB.
		digits := s[1:]
		w := len(digits) / 3
		if w == 0 || w > 4 || len(digits)%3 != 0 {
			return 0, false
		}
		var ch [3]uint8
		for i := range ch {
			v, ok := parseHexChannel(digits[i*w : (i+1)*w])
			if !ok {
				return 0, false
			}
			ch[i] = v
		}
		return rgbColor(ch[0], ch[1], ch[2]), true
	}
	return 0, false
}

// parseHexChannel converts a 1–4 digit hex channel to 8 bits, scaling the
// way X11 does: narrower values are widened, wider ones truncated to their
// most significant byte.
func parseHexChannel(p string) (uint8, bool) {
	if len(p) == 0 || len(p) > 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(p, 16, 64)
	if err != nil {
		return 0, false
	}
	switch len(p) {
	case 1:
		return uint8(n * 0x11), true
	case 2:
		return uint8(n), true
	case 3:
		return uint8(n >> 4), true
	default:
		return uint8(n >> 8), true
	}
}

// sanitizeOSCString strips ASCII control characters (0x00–0x1F, 0x7F) from
// an OSC payload string before storing it in a grid field. Prevents hostile
// terminal sequences from embedding control bytes in caller-visible state.
func sanitizeOSCString(s string) string {
	for i := 0; i < len(s); i++ {
		if b := s[i]; b < 0x20 || b == 0x7F {
			var buf strings.Builder
			buf.Grow(len(s))
			buf.WriteString(s[:i])
			for j := i + 1; j < len(s); j++ {
				if b2 := s[j]; b2 >= 0x20 && b2 != 0x7F {
					buf.WriteByte(b2)
				}
			}
			return buf.String()
		}
	}
	return s
}

// decodeBase64String decodes a base64-encoded string, trying standard
// encoding first then raw (unpadded) encoding. When len(s)%4 != 0 the
// input is definitely unpadded, so we skip the StdEncoding attempt
// (and its allocation) entirely.
func decodeBase64String(s string) ([]byte, error) {
	if len(s)%4 != 0 {
		return base64.RawStdEncoding.DecodeString(s)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(s)
	}
	return data, err
}

// oscHexWord expands an 8-bit color component to a 4-hex-digit string
// by repeating the byte (e.g. 0xAB → "abab"), matching xterm convention.
func oscHexWord(n uint8) string {
	v := uint16(n)<<8 | uint16(n)
	const hx = "0123456789abcdef"
	return string([]byte{hx[v>>12], hx[(v>>8)&0xF], hx[(v>>4)&0xF], hx[v&0xF]})
}
