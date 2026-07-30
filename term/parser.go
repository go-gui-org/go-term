package term

import (
	"strconv"
)

// parser is a VT/xterm-compatible state machine. It handles C0 controls;
// ESC sequences (cursor save/restore, IND/RI/NEL, charset selection, tab
// stops, keypad mode, RIS); CSI (SGR with 16/256/truecolor fg/bg, underline
// color/extended underlines, blink/conceal, cursor movement/positioning,
// erase in line/display, scroll regions, IL/DL/ICH/DCH, REP, DECSCUSR, DA1,
// DECSC/DECRC, DECSTR, tab clear, title stack, cursor/status reports,
// Kitty Keyboard Protocol, mode set/reset/query); OSC (window
// title, CWD, hyperlinks, desktop notifications, dynamic colors, clipboard,
// semantic shell marks, iTerm2 inline images); DCS (DECRQSS, XTGETTCAP,
// sixel graphics, synchronized updates); and APC (Kitty Graphics Protocol).
// Unrecognized escape sequences are silently consumed.
type parser struct {
	g           *grid
	kittyChunks map[uint32]kittyPending // partial transmissions: id → chunk state
	kittyStore  map[uint32]kittyEntry   // off-screen cache: image id → entry
	// kittyOpenID is the id of the transmission an m=1 chunk left open, so the
	// continuation chunks — which carry no i= key — land in the right entry.
	kittyOpenID uint32

	// onTitle, if non-nil, is invoked for OSC 0/1/2 (window title).
	// onReply, if non-nil, is invoked when the parser needs to write
	// bytes back toward the application (e.g. DA1 response).
	// onClipboard, if non-nil, is invoked for OSC 52 clipboard-write
	// requests. onNotify, if non-nil, is invoked for OSC 9 and OSC 777
	// desktop-notification requests. onDownload, if non-nil, is invoked for
	// OSC 1337 File= transfers that are not inline images. All run while
	// grid.Mu is held — handlers must not re-enter the grid.
	onTitle     func(string)
	onReply     func([]byte)
	onClipboard func([]byte)
	onNotify    func(title, body string)
	onDownload  func(name string, data []byte)

	// curTitle mirrors the last title reported via OSC 0/1/2 and titleStack
	// holds the ones pushed by XTWINOPS 22 (CSI 22 t), popped by 23. vim and
	// tmux bracket their session with a push/pop pair and rely on the pop to
	// put the shell's title back. Icon name and window title share one stack —
	// the widget surfaces a single title.
	curTitle   string
	titleStack []string

	// graphicsDir is the directory where decoded Sixel PNGs are written.
	// Empty = os.TempDir(). Set via SetGraphicsDir; the widget creates a
	// per-Term subdirectory and removes it on Close.
	graphicsDir string
	params      []int  // SGR params accumulated in current CSI
	paramSub    []bool // paramSub[i] true when params[i] was colon-separated from params[i-1]

	// osc accumulates the payload of the in-progress OSC (Operating
	// System Command). Reset on entry to stOSC; capped at maxOSCBytes
	// unless oscIsImage is set (OSC 1337), in which case maxOSC1337Bytes.
	osc []byte
	dcs []byte

	// apc accumulates the payload of the in-progress APC (Application
	// Program Command). Used by the Kitty Graphics Protocol (payload
	// starts with 'G'). Capped at maxAPCBytes per-chunk; chunked images
	// accumulate base64 text in kittyChunks.
	apc    []byte
	curP   int // value being accumulated
	utfLen int

	utf                 [4]byte // UTF-8 carry-over between Feed calls
	state               parserState
	hasP                bool // any digit seen for curP
	nextIsSub           bool // pending: next param pushed will be marked as sub-param
	leader              byte // optional CSI private leader: one of < = > ?
	intermediate        byte // last intermediate byte (0x20..0x2F) seen, 0 if none
	escInter            byte // ESC intermediate introducer like '(' in ESC(B
	oscIsImage          bool // true once "1337;" prefix detected
	oscTrunc            bool // true once a byte was dropped for exceeding the OSC cap
	dcsTrunc            bool // true once a byte was dropped for exceeding the DCS cap
	allowClipboardWrite bool
}

// SetGraphicsDir tells the parser where to write decoded Sixel images.
// Empty string falls back to os.TempDir(). The widget creates a private
// subdir per Term so cleanup on Close removes only its own files.
func (p *parser) SetGraphicsDir(dir string) { p.graphicsDir = dir }

// resetPayload readies an escape-payload buffer for reuse. Image-carrying
// sequences (OSC 1337, sixel over DCS) can grow their backing array to tens
// of MB; truncating alone would pin that array for the parser's lifetime, so
// anything grown past retain is dropped and the next sequence starts from a
// small allocation again. Buffers at or under retain are kept, so the common
// case — and every frame of a sixel animation — stays allocation-free.
func resetPayload(buf []byte, retain int) []byte {
	if cap(buf) > retain {
		return nil
	}
	return buf[:0]
}

func (p *parser) oscReset() {
	p.osc = resetPayload(p.osc, maxOSCBytes)
	p.oscIsImage = false
	p.oscTrunc = false
}

// dcsReset starts a fresh DCS payload.
func (p *parser) dcsReset() {
	p.dcs = resetPayload(p.dcs, maxDCSRetain)
	p.dcsTrunc = false
}

// SetTitleHandler registers a callback for OSC 0/1/2. Pass nil to
// disable. Called while grid.Mu is held.
func (p *parser) SetTitleHandler(fn func(string)) { p.onTitle = fn }

// SetReplyHandler registers a callback for parser-originated host
// writes (DA1 today; future: cursor position reports, etc.). Called
// while grid.Mu is held.
func (p *parser) SetReplyHandler(fn func([]byte)) { p.onReply = fn }

// SetClipboardHandler registers a callback for OSC 52 clipboard-write
// requests. data is the decoded (raw) clipboard payload. Pass nil to
// disable. Called while grid.Mu is held. OSC 52 writes are ignored unless
// SetClipboardWriteAllowed(true) is also called.
func (p *parser) SetClipboardHandler(fn func([]byte)) { p.onClipboard = fn }

// SetClipboardWriteAllowed controls whether OSC 52 write requests may invoke
// the registered clipboard handler. Disabled by default.
func (p *parser) SetClipboardWriteAllowed(ok bool) { p.allowClipboardWrite = ok }

// SetNotifyHandler registers a callback for OSC 9 and OSC 777 desktop
// notifications. title may be empty (OSC 9 carries body only). Called
// while grid.Mu is held — the handler must not block; fire a goroutine
// for any slow work (e.g. exec).
func (p *parser) SetNotifyHandler(fn func(title, body string)) { p.onNotify = fn }

// SetDownloadHandler registers a callback for OSC 1337 File= transfers that
// are not inline images (iTerm2's imgcat -d, it2dl). name is sanitized down
// to a bare filename — never a path — and data is the decoded payload, freshly
// allocated and not retained by the parser. Pass nil to disable, which is the
// default: file transfers drop silently unless the host opts in. Called while
// grid.Mu is held — the handler must not block; hand disk writes to a
// goroutine.
func (p *parser) SetDownloadHandler(fn func(name string, data []byte)) { p.onDownload = fn }

// pushTitle saves the current title (XTWINOPS CSI 22 t).
func (p *parser) pushTitle() {
	if len(p.titleStack) >= maxTitleStack {
		return
	}
	p.titleStack = append(p.titleStack, p.curTitle)
}

// popTitle restores the most recently pushed title (XTWINOPS CSI 23 t) and
// republishes it through onTitle. No-op on an empty stack.
func (p *parser) popTitle() {
	if len(p.titleStack) == 0 {
		return
	}
	title := p.titleStack[len(p.titleStack)-1]
	p.titleStack = p.titleStack[:len(p.titleStack)-1]
	p.curTitle = title
	if p.onTitle != nil {
		p.onTitle(title)
	}
}

// hardReset performs RIS (ESC c): the grid returns to its power-on state and
// the parser drops the escape-level state it owns. The current title is kept —
// the widget owns the window title, and RIS gives no replacement to show.
func (p *parser) hardReset() {
	p.g.HardReset()
	p.titleStack = p.titleStack[:0]
}

// newParser binds a parser to a grid. Callers must hold g.Mu while calling
// Feed.
func newParser(g *grid) *parser {
	return &parser{g: g, params: make([]int, 0, 8), paramSub: make([]bool, 0, 8)}
}

func (p *parser) currentSGRString() string {
	if p.g.CurFG == DefaultColor && p.g.CurBG == DefaultColor &&
		p.g.CurAttrs&attrVisual == 0 {
		return "0m"
	}
	params := make([]byte, 0, 32)
	appendParam := func(s string) {
		if len(params) > 0 {
			params = append(params, ';')
		}
		params = append(params, s...)
	}
	if p.g.CurAttrs&attrBold != 0 {
		appendParam("1")
	}
	if p.g.CurAttrs&attrUnderline != 0 {
		appendParam("4")
	}
	if p.g.CurAttrs&attrInverse != 0 {
		appendParam("7")
	}
	switch p.g.CurFG >> 24 {
	case 0x00:
		v := int(p.g.CurFG & 0xFF)
		switch {
		case v <= 7:
			appendParam(strconv.Itoa(v + 30))
		case v <= 15:
			appendParam(strconv.Itoa(v - 8 + 90))
		default:
			appendParam("38")
			appendParam("5")
			appendParam(strconv.Itoa(v))
		}
	case 0x01:
		appendParam("38")
		appendParam("2")
		appendParam(strconv.Itoa(int((p.g.CurFG >> 16) & 0xFF)))
		appendParam(strconv.Itoa(int((p.g.CurFG >> 8) & 0xFF)))
		appendParam(strconv.Itoa(int(p.g.CurFG & 0xFF)))
	}
	switch p.g.CurBG >> 24 {
	case 0x00:
		v := int(p.g.CurBG & 0xFF)
		switch {
		case v <= 7:
			appendParam(strconv.Itoa(v + 40))
		case v <= 15:
			appendParam(strconv.Itoa(v - 8 + 100))
		default:
			appendParam("48")
			appendParam("5")
			appendParam(strconv.Itoa(v))
		}
	case 0x01:
		appendParam("48")
		appendParam("2")
		appendParam(strconv.Itoa(int((p.g.CurBG >> 16) & 0xFF)))
		appendParam(strconv.Itoa(int((p.g.CurBG >> 8) & 0xFF)))
		appendParam(strconv.Itoa(int(p.g.CurBG & 0xFF)))
	}
	if len(params) == 0 {
		return "0m"
	}
	return string(params) + "m"
}
