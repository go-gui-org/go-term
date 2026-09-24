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
	// kittyOpenDropped marks that transmission as refused (oversize, or the
	// pending table was full): its continuations must be discarded rather than
	// routed to whatever id was open before it. kittyPendingBytes is the total
	// base64 buffered across kittyChunks, bounded by maxKittyPendingBytes.
	kittyOpenID       uint32
	kittyOpenDropped  bool
	kittyPendingBytes int
	// kittyPendingSeq stamps each pending transmission with its insertion
	// order so a full table evicts the oldest (see evictOldestPending).
	kittyPendingSeq uint64

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

	// onCommand, if non-nil, is invoked for the OSC 133 marks that bracket a
	// command's execution: 'C' when output starts and 'D' when it ends, with
	// the reported exit status. It is how the widget times a command without
	// putting a timestamp on every mark. Runs under grid.Mu like the rest.
	onCommand func(kind byte, exit int16)

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
	// unless oscLim names a larger cap (OSC 1337, OSC 52).
	osc []byte
	dcs []byte

	// apc accumulates the payload of the in-progress APC (Application
	// Program Command). Used by the Kitty Graphics Protocol (payload
	// starts with 'G'). Capped at maxAPCBytes per-chunk; chunked images
	// accumulate base64 text in kittyChunks.
	apc      []byte
	apcTrunc bool // true once a byte was dropped for exceeding the APC cap
	curP     int  // value being accumulated
	utfLen   int

	utf                 [4]byte // UTF-8 carry-over between Feed calls
	state               parserState
	hasP                bool // any digit seen for curP
	nextIsSub           bool // pending: next param pushed will be marked as sub-param
	leader              byte // optional CSI private leader: one of < = > ?
	intermediate        byte // last intermediate byte (0x20..0x2F) seen, 0 if none
	escInter            byte // ESC intermediate introducer like '(' in ESC(B
	oscLim              int  // payload cap picked from the OSC number; 0 = maxOSCBytes
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
	p.oscLim = 0
	p.oscTrunc = false
}

// dcsReset starts a fresh DCS payload.
func (p *parser) dcsReset() {
	p.dcs = resetPayload(p.dcs, maxDCSRetain)
	p.dcsTrunc = false
}

// apcReset starts a fresh APC payload.
func (p *parser) apcReset() {
	p.apc = resetPayload(p.apc, maxAPCBytes)
	p.apcTrunc = false
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

// SetCommandHandler registers a callback for the OSC 133 C and D marks.
// Called while grid.Mu is held, immediately after the mark is recorded — so
// the handler may read the grid (commandText) but must not re-lock it, and
// must not block.
func (p *parser) SetCommandHandler(fn func(kind byte, exit int16)) { p.onCommand = fn }

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
//
// The in-flight KGP state goes with it. `reset` is the only recovery a user
// has, and without this an abandoned chunked transfer keeps its slot (and its
// buffered base64) for the life of the pane, so a stream that fills the
// pending table denies every later image permanently.
func (p *parser) hardReset() {
	p.g.HardReset()
	p.titleStack = p.titleStack[:0]
	p.kittyResetTransfers()
	p.kittyDropStore()
}

// newParser binds a parser to a grid. Callers must hold g.Mu while calling
// Feed.
func newParser(g *grid) *parser {
	return &parser{g: g, params: make([]int, 0, 8), paramSub: make([]bool, 0, 8)}
}

// currentSGRString renders the current SGR state as the parameter string a
// DECRQSS "m" reply carries ("1;3;38;2;255;0;0m"). It must cover every
// attribute applySGR can set: a client that saves the reply and replays it to
// restore its pen otherwise loses what was left out. "0m" is the reply only
// when nothing is set.
func (p *parser) currentSGRString() string {
	g := p.g
	params := make([]byte, 0, 32)
	// Attribute bits in SGR-number order, so the reply reads like the SGR that
	// produced it.
	for _, a := range [...]struct {
		bit uint16
		sgr int
	}{
		{attrBold, 1}, {attrDim, 2}, {attrItalic, 3},
	} {
		if g.CurAttrs&a.bit != 0 {
			params = appendSGRParam(params, a.sgr)
		}
	}
	if g.CurAttrs&attrUnderline != 0 {
		params = appendSGRParam(params, 4)
		// A plain underline is "4"; the other shapes need the colon form
		// (4:2 double … 4:5 dashed), since "4;3" would read as underline+italic.
		if g.CurULStyle > ulSingle {
			params = append(params, ':')
			params = strconv.AppendInt(params, int64(g.CurULStyle), 10)
		}
	}
	for _, a := range [...]struct {
		bit uint16
		sgr int
	}{
		{attrBlink, 5}, {attrInverse, 7}, {attrConceal, 8},
		{attrStrikethrough, 9}, {attrOverline, 53},
	} {
		if g.CurAttrs&a.bit != 0 {
			params = appendSGRParam(params, a.sgr)
		}
	}
	params = appendSGRColor(params, g.CurFG, 30, 90, 38)
	params = appendSGRColor(params, g.CurBG, 40, 100, 48)
	// SGR 58 has no short palette form, so base -1 sends every index through
	// the 58;5;n shape.
	params = appendSGRColor(params, g.CurULColor, -1, -1, 58)
	if len(params) == 0 {
		return "0m"
	}
	return string(params) + "m"
}

// appendSGRColor appends the SGR parameters that select color c, or nothing
// for defaultColor. Palette 0–7 use base+n and 8–15 bright+n-8 when base >= 0;
// every other palette index is ext;5;n and a truecolor value ext;2;r;g;b.
func appendSGRColor(params []byte, c uint32, base, bright, ext int) []byte {
	switch c >> 24 {
	case 0x00:
		v := int(c & 0xFF)
		switch {
		case base >= 0 && v <= 7:
			return appendSGRParam(params, base+v)
		case base >= 0 && v <= 15:
			return appendSGRParam(params, bright+v-8)
		}
		for _, n := range [...]int{ext, 5, v} {
			params = appendSGRParam(params, n)
		}
	case 0x01:
		for _, n := range [...]int{ext, 2, int(c>>16) & 0xFF, int(c>>8) & 0xFF, int(c) & 0xFF} {
			params = appendSGRParam(params, n)
		}
	}
	return params
}

// appendSGRParam appends n to a ';'-separated SGR parameter list.
func appendSGRParam(params []byte, n int) []byte {
	if len(params) > 0 {
		params = append(params, ';')
	}
	return strconv.AppendInt(params, int64(n), 10)
}
