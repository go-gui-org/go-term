package term

import (
	"encoding/base64"
	"math"
	"path"
	"strconv"
	"strings"
)

// oscExitStatus parses the optional exit status out of an OSC 133 D payload
// ("D", "D;0", "D;1;aid=17"). Returns markExitUnknown when the status is
// absent or unparsable — shells vary in what they emit, and a missing status
// must never read as success. Out-of-range values clamp to the int16 max,
// which is still non-zero and so still reads as a failure.
func oscExitStatus(pt string) int16 {
	if len(pt) < 2 || pt[1] != ';' {
		return markExitUnknown
	}
	field := pt[2:]
	if i := strings.IndexByte(field, ';'); i >= 0 {
		field = field[:i] // trailing OSC 133 keys such as aid=/err=
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return markExitUnknown
	}
	if n < 0 {
		// A negative status is not meaningful; treat it as "reported, but
		// not a success" rather than discarding the fact a command ended.
		return math.MaxInt16
	}
	if n > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(n)
}

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
			if p.onCommand != nil {
				p.onCommand('C', markExitUnknown)
			}
		case 'D':
			exit := oscExitStatus(pt)
			p.g.AddCommandEnd(exit)
			if p.onCommand != nil {
				p.onCommand('D', exit)
			}
		}
	case 22:
		// OSC 22 — mouse cursor shape. sanitizeOSCString strips the control
		// characters; the name table rejects everything it does not know, so a
		// hostile payload can only ever be ignored.
		if s, ok := parsePointerShape(sanitizeOSCString(pt)); ok {
			p.g.PointerShape = s
		}
	case 9:
		// OSC 9 is two unrelated commands sharing a number. ConEmu's progress
		// report (9;4;state;value) must be split off *before* the notification
		// path, or `cargo build` pops a desktop notification reading "4;1;50".
		if rest, ok := strings.CutPrefix(pt, "4;"); ok {
			p.handleOSC94(rest)
			return
		}
		// A bare "9;4" carries no state. It is still a progress report — the
		// trailing fields are optional in ConEmu's own description — and
		// handleOSC94 reads the missing state as 0, the clear. Falling through
		// would pop a desktop notification whose body is the digit 4.
		if pt == "4" {
			p.handleOSC94("")
			return
		}
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

// handleOSC94 implements OSC 9;4 — ConEmu-style progress reporting. pt is the
// payload *after* the "4;" subtype, i.e. "state" or "state;value".
//
// States: 0 remove, 1 normal (value 0..100), 2 error, 3 indeterminate,
// 4 paused. A value is only meaningful in states 1/2/4; state 0 clears both so
// a stale percentage cannot resurface with the next state change.
//
// Malformed input clears rather than being ignored: the alternative is a
// progress bar stuck at whatever the last parseable report said, which is
// worse than none. Called with g.Mu held.
func (p *parser) handleOSC94(pt string) {
	stateStr, valStr, _ := strings.Cut(pt, ";")
	state, err := strconv.Atoi(stateStr)
	if err != nil || state < 0 || state > 4 {
		state = 0
	}
	val := 0
	if state != 0 && state != 3 {
		// An unparseable or absent value is 0, not a reason to drop the state:
		// "9;4;2" (error, no percentage) is a report real tools send.
		if v, err := strconv.Atoi(valStr); err == nil && v > 0 {
			val = min(v, 100)
		}
	}
	if p.g.ProgressState == uint8(state) && p.g.ProgressValue == uint8(val) {
		return // no visual change; don't invalidate the tessellation cache
	}
	p.g.ProgressState, p.g.ProgressValue = uint8(state), uint8(val)
	// Progress dirties no row, so this is the only thing that gets the frame
	// repainted — see grid.OverlayVersion.
	p.g.OverlayVersion++
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

// dimKind tags how an iTerm2 width=/height= value should be interpreted.
type dimKind uint8

const (
	dimAuto    dimKind = iota // unset or "auto": use the image's natural size
	dimCells                  // bare "N": N character cells
	dimPixels                 // "Npx": N pixels
	dimPercent                // "N%": percent of the text area
)

// dimSpec is one parsed width=/height= value.
type dimSpec struct {
	val  int
	kind dimKind
}

// iterm2FileArgs holds the parsed key=value list of an OSC 1337 File=
// payload. Unset keys keep the defaults set by parseOSC1337Args — note
// preserveAR defaults to true, matching the iTerm2 spec.
type iterm2FileArgs struct {
	name       string // decoded and sanitized to a bare filename
	size       int64  // declared byte count; -1 when absent
	width      dimSpec
	height     dimSpec
	preserveAR bool
	inline     bool
}

// maxDownloadName caps the sanitized filename. 255 bytes is the per-component
// limit on every filesystem go-term runs on.
const maxDownloadName = 255

// defaultDownloadName is used when name= is absent, undecodable, or sanitizes
// away to nothing. A transfer with a hostile name still lands, just under a
// boring one.
const defaultDownloadName = "download"

// parseOSC1337Args parses the semicolon-separated key=value list that
// precedes the base64 payload of an OSC 1337 File= sequence. Unknown keys are
// ignored and a malformed value falls back to that key's default rather than
// rejecting the whole sequence — iTerm2 is lenient here and real emitters
// lean on it.
func parseOSC1337Args(args string) iterm2FileArgs {
	a := iterm2FileArgs{size: -1, preserveAR: true}
	for rest := args; rest != ""; {
		var field string
		field, rest, _ = strings.Cut(rest, ";")
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "name":
			a.name = sanitizeDownloadName(val)
		case "size":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
				a.size = n
			}
		case "width":
			a.width = parseDimSpec(val)
		case "height":
			a.height = parseDimSpec(val)
		case "preserveAspectRatio":
			a.preserveAR = val != "0"
		case "inline":
			a.inline = val == "1"
		}
	}
	return a
}

// parseDimSpec parses one iTerm2 dimension: "auto", "N" (cells), "Npx"
// (pixels) or "N%" (percent of the text area). Anything else — including a
// zero or negative count — reads as auto.
func parseDimSpec(s string) dimSpec {
	kind := dimCells
	switch {
	case s == "" || s == "auto":
		return dimSpec{}
	case strings.HasSuffix(s, "px"):
		kind, s = dimPixels, s[:len(s)-2]
	case strings.HasSuffix(s, "%"):
		kind, s = dimPercent, s[:len(s)-1]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return dimSpec{}
	}
	return dimSpec{val: n, kind: kind}
}

// sanitizeDownloadName turns the base64-encoded name= value into a bare
// filename safe to join onto a download directory. Everything up to the last
// path separator is dropped (both flavors — the sender picks the separator,
// not us), control bytes are stripped, and the traversal names are refused.
// Returns defaultDownloadName rather than "" so a transfer is never lost to a
// hostile name.
func sanitizeDownloadName(b64 string) string {
	raw, err := decodeBase64String(b64)
	if err != nil {
		return defaultDownloadName
	}
	name := sanitizeOSCString(string(raw))
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return defaultDownloadName
	}
	if len(name) > maxDownloadName {
		name = name[:maxDownloadName]
	}
	return name
}

// handleOSC1337 implements the iTerm2 File= protocol, which carries both
// inline images (inline=1) and plain file transfers (inline=0).
// Payload format: File=[key=value;...]:base64data
//
// Transfers are handed to onDownload, which is nil unless the host opted in —
// writing files on the say-so of arbitrary terminal output is the host's call,
// not the parser's.
func (p *parser) handleOSC1337(pt string) {
	const prefix = "File="
	if !strings.HasPrefix(pt, prefix) {
		return
	}
	// A payload that hit the OSC cap lost its tail; decoding it would yield
	// either a base64 error or, worse, a silently truncated file.
	if p.oscTrunc {
		return
	}
	rest := pt[len(prefix):]
	before, b64, ok := strings.Cut(rest, ":")
	if !ok {
		return
	}
	args := parseOSC1337Args(before)

	// Declared size over the wire cap can't be honest — drop before spending
	// a decode on it. The real length is re-checked implicitly by the cap on
	// p.osc itself.
	if args.size > maxOSC1337Bytes {
		return
	}
	if !args.inline {
		if p.onDownload == nil {
			return
		}
		raw, err := decodeBase64String(b64)
		if err != nil {
			return
		}
		name := args.name
		if name == "" {
			name = defaultDownloadName
		}
		p.onDownload(name, raw)
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
	src := encodePNGFile(img, p.graphicsDir)
	if src == "" {
		return
	}
	cols, rows := p.resolveGraphicCells(&args, b.Dx(), b.Dy())
	_, rows = p.g.addGraphicCells(src, b.Dx(), b.Dy(), cols, rows)
	for range rows {
		p.g.Newline()
	}
}

// resolveGraphicCells converts the width=/height= specs into an explicit cell
// footprint for an image of imgW×imgH pixels. Returns 0,0 when the natural
// size should be used — either both axes are auto or the cell metrics aren't
// measured yet — which addGraphicCells reads as "derive it yourself".
func (p *parser) resolveGraphicCells(a *iterm2FileArgs, imgW, imgH int) (int, int) {
	cw, ch := p.g.CellPxW, p.g.CellPxH
	if cw <= 0 || ch <= 0 || imgW <= 0 || imgH <= 0 {
		return 0, 0
	}
	if a.width.kind == dimAuto && a.height.kind == dimAuto {
		return 0, 0
	}

	// Work in fractional cells throughout and round up only at the end, so a
	// chain of conversions doesn't accumulate rounding error.
	toCells := func(d dimSpec, cellPx float32, axisCells int) float64 {
		switch d.kind {
		case dimCells:
			return float64(d.val)
		case dimPixels:
			return float64(d.val) / float64(cellPx)
		case dimPercent:
			return float64(d.val) / 100 * float64(axisCells)
		}
		return -1 // auto
	}
	w := toCells(a.width, cw, p.g.Cols)
	h := toCells(a.height, ch, p.g.Rows)

	// Natural size in cells, the reference for aspect-preserving fits.
	natW := float64(imgW) / float64(cw)
	natH := float64(imgH) / float64(ch)

	switch {
	case !a.preserveAR:
		// Free scaling: each unspecified axis keeps its natural extent.
		if w < 0 {
			w = natW
		}
		if h < 0 {
			h = natH
		}
	case w < 0:
		// Only height given (both-auto returned above), so h is valid here.
		w = h * natW / natH
	case h < 0:
		h = w * natH / natW
	default:
		// Both axes given: fit inside the box without overflowing either.
		scale := min(w/natW, h/natH)
		w, h = natW*scale, natH*scale
	}

	cols := int(math.Ceil(w))
	rows := int(math.Ceil(h))
	return max(cols, 1), max(rows, 1)
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
