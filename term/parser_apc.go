package term

import (
	"encoding/base64"
	"image"
	"log"
	"os"
	"strconv"
)

// kittyEntry is one entry in the off-screen image store (kittyStore).
type kittyEntry struct {
	path string
	w, h int // pixel dimensions of the stored image
}

// kittyPending is an in-flight chunked transmission (m=1 seen, m=0 not yet).
// The opening chunk carries the control data — format, pixel dimensions,
// action — and the continuation chunks carry only `m=` and payload, so the
// opening params have to be remembered here or the final chunk decodes with
// protocol defaults (f=32, s=0, v=0) and the image is silently dropped.
type kittyPending struct {
	params    kgpParams
	b64       []byte
	truncated bool // maxKittyImageBytes was hit; suppresses repeat logging
}

// dispatchAPC processes a completed APC sequence (ESC _ payload ESC \).
// Only the Kitty Graphics Protocol (payload starting with 'G') is handled;
// everything else is silently dropped.
func (p *parser) dispatchAPC() {
	if len(p.apc) < 1 || p.apc[0] != 'G' {
		return
	}
	p.handleKittyGraphics(p.apc[1:])
}

// kgpParams holds the decoded key=value pairs from a KGP escape.
type kgpParams struct {
	deleteOp string // d=: delete specifier (when a=d)
	format   int    // f=: 32 RGBA, 24 RGB, 100 PNG; default 32
	widthPx  int    // s=: pixel width for raw formats
	heightPx int    // v=: pixel height for raw formats
	quiet    int    // q=: 0 reply always, 1 suppress OK, 2 always suppress
	imageID  uint32 // i=: image id (0 = anonymous)
	action   byte   // a=: 't' transmit, 'T' transmit+display, 'p' place, 'q' query, 'd' delete; default 'T'
	medium   byte   // t=: 'd' direct (default), 'f' file, 't' temp-file, 's' shared-memory
	more     bool   // m=: true when m=1 (more chunks follow)
	noCursor bool   // C=: true when C=1 (place without moving the cursor)
	// cols/rows are the c=/r= cell footprint the client asked for; 0 means
	// "unspecified, derive from the pixel size". Lowercase, and unrelated to
	// C= above.
	cols int
	rows int
	// placeID is p=, the placement id. Only meaningful for virtual placements,
	// where the placeholder cells can name it via their underline color.
	placeID uint32
	// virtual is U=1: create a *virtual* placement instead of putting the
	// image on screen. Nothing is drawn until the client writes U+10EEEE
	// placeholder cells naming the image — see grid_virtual.go.
	virtual bool
}

// parseIntKV parses a KGP integer key=value, logging on error.
// The bool indicates success; callers skip the assignment on false
// so the field retains its default.
func parseIntKV(val string, key byte) (int, bool) {
	n, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("term: KGP bad %c= value %q: %v", key, val, err)
		return 0, false
	}
	return n, true
}

// parseUint32KV parses a KGP uint32 key=value, logging on error.
// The bool indicates success; callers skip the assignment on false
// so the field retains its default.
func parseUint32KV(val string, key byte) (uint32, bool) {
	n, err := strconv.ParseUint(val, 10, 32)
	if err != nil {
		log.Printf("term: KGP bad %c= value %q: %v", key, val, err)
		return 0, false
	}
	return uint32(n), true
}

// handleKittyGraphics parses a Kitty Graphics Protocol payload (bytes after
// the leading 'G') and dispatches the appropriate action.
func (p *parser) handleKittyGraphics(payload []byte) {
	params, rawB64 := splitKGPPayload(payload)

	switch params.action {
	case 'q':
		p.kittyReply(params.imageID, params.quiet, true)
	case 't', 'T':
		// File ('f'), temp-file ('t'), and shared-memory ('s') media all
		// require reading arbitrary host resources — not implemented; reject
		// explicitly so the client doesn't hang waiting for a response.
		if params.medium == 'f' || params.medium == 't' || params.medium == 's' {
			p.kittyReply(params.imageID, params.quiet, false)
			return
		}
		// Accumulate raw base64 text; decode only at end of chunks.
		p.kittyAccumulate(params, rawB64)
	case 'p':
		p.kittyPlace(params)
	case 'd':
		p.kittyDeleteID(params.imageID, params.deleteOp)
		p.kittyReply(params.imageID, params.quiet, true)
	default:
		// Unknown action: reply with error so the client doesn't hang.
		p.kittyReply(params.imageID, params.quiet, false)
	}
}

// splitKGPPayload splits a raw KGP body (after 'G') into parsed params and
// the raw base64 payload. Format: "k=v,k=v,...;base64data".
func splitKGPPayload(payload []byte) (kgpParams, []byte) {
	params := kgpParams{action: 'T', format: 32, medium: 'd'}

	semi := -1
	for i, b := range payload {
		if b == ';' {
			semi = i
			break
		}
	}
	var kvSection, b64Section []byte
	if semi >= 0 {
		kvSection = payload[:semi]
		b64Section = payload[semi+1:]
	} else {
		kvSection = payload
	}

	for len(kvSection) > 0 {
		end := len(kvSection)
		for i, b := range kvSection {
			if b == ',' {
				end = i
				break
			}
		}
		kv := kvSection[:end]
		if end < len(kvSection) {
			kvSection = kvSection[end+1:]
		} else {
			kvSection = nil
		}
		if len(kv) < 3 || kv[1] != '=' {
			continue
		}
		key := kv[0]
		val := string(kv[2:])
		switch key {
		case 'a':
			if len(val) == 1 {
				params.action = val[0]
			}
		case 'f':
			if n, ok := parseIntKV(val, 'f'); ok {
				params.format = n
			}
		case 't':
			if len(val) == 1 {
				params.medium = val[0]
			}
		case 's':
			if n, ok := parseIntKV(val, 's'); ok {
				params.widthPx = n
			}
		case 'v':
			if n, ok := parseIntKV(val, 'v'); ok {
				params.heightPx = n
			}
		case 'm':
			params.more = val == "1"
		case 'C':
			// Cursor movement policy. C=1 means "do not move the cursor";
			// any other value (including the C=0 default) leaves the normal
			// post-placement movement in place. Note the key is case-
			// sensitive: lowercase 'c' is the placement column count, which
			// this parser does not implement.
			params.noCursor = val == "1"
		case 'U':
			// Unicode placeholder placement. U=1 makes the placement virtual:
			// no cells, no cursor movement, nothing on screen until the client
			// prints U+10EEEE placeholder cells.
			params.virtual = val == "1"
		case 'c':
			// Placement width in cells. Lowercase — C= above is the cursor
			// movement policy, and the two are unrelated. Clamped here rather
			// than downstream: kgpCellRect derives the missing dimension by
			// multiplying this out in float64, and an unclamped c= turns that
			// into a float too large to convert back to int.
			if n, ok := parseIntKV(val, 'c'); ok && n >= 0 {
				params.cols = min(n, MaxGridDim)
			}
		case 'r':
			if n, ok := parseIntKV(val, 'r'); ok && n >= 0 {
				params.rows = min(n, MaxGridDim)
			}
		case 'p':
			if n, ok := parseUint32KV(val, 'p'); ok {
				params.placeID = n
			}
		case 'i':
			if n, ok := parseUint32KV(val, 'i'); ok {
				params.imageID = n
			}
		case 'q':
			if n, ok := parseIntKV(val, 'q'); ok {
				params.quiet = n
			}
		case 'd':
			params.deleteOp = val
		}
	}
	return params, b64Section
}

// kittyAccumulate appends raw base64 chunk text for the given image ID.
// Per the KGP spec, chunks are concatenated as base64 text then decoded
// once at the end (m=0), so splits at arbitrary byte boundaries are valid.
// When m=0, decodes the assembled base64, writes PNG, optionally places.
func (p *parser) kittyAccumulate(params kgpParams, rawB64 []byte) {
	id := params.imageID
	// Continuation chunks carry no i= key. Route them to the transmission the
	// opening chunk started rather than to anonymous id 0, which would mix two
	// concurrent transfers together.
	if id == 0 && p.kittyOpenID != 0 {
		id = p.kittyOpenID
	}
	// A transmission refused at its opening chunk keeps its slot in kittyOpenID
	// so that its payload-only continuations are dropped here. Without this they
	// route to the *previous* open id and either resurrect the refused image
	// under anonymous id 0 or corrupt an unrelated in-flight transfer.
	if p.kittyOpenDropped && id == p.kittyOpenID {
		if !params.more {
			p.kittyOpenID = 0
			p.kittyOpenDropped = false
		}
		return
	}

	pend, known := p.kittyChunks[id]

	// A self-contained transmission — no m=1 opened this id and none opens it
	// now — decodes straight from the chunk in hand. Keeping it out of the
	// pending table matters: otherwise a table filled by abandoned chunked
	// transfers would swallow ordinary single-shot images.
	if !known && !params.more {
		params.imageID = id
		p.kittyFinish(params, id, rawB64)
		return
	}

	if !known {
		// Opening chunk: its control data governs the whole transmission, so
		// record it *before* looking at the payload. The spec allows an opener
		// that carries control data only — no ';' section at all — and chafa
		// sends exactly that (`a=T,f=32,s=…,v=…,m=1,q=2` then payload-only
		// continuations). Deferring the record until a chunk had payload lost
		// the opener's f=/s=/v=/q=, so the final decode saw width 0, returned
		// nil, and the whole image came back EINVAL.
		if p.kittyChunks == nil {
			p.kittyChunks = make(map[uint32]kittyPending)
		}
		// Refuse new IDs when the pending-chunk table is full to prevent a
		// DoS where a malicious server opens many IDs with m=1 and never
		// finalises them. Reply so the client doesn't wait on a dead transfer.
		if len(p.kittyChunks) >= maxKittyPendingChunks {
			p.kittyRefuse(id, params)
			return
		}
		// A raw transmission states its pixel dimensions up front. Reject an
		// oversize one here rather than buffering tens of MB of base64 that
		// kittyRawToNRGBA is going to refuse anyway.
		if (params.format == 32 || params.format == 24) &&
			(params.widthPx > maxSixelWidth || params.heightPx > maxSixelHeight) {
			log.Printf("term: KGP image %d×%d exceeds %d×%d, rejected",
				params.widthPx, params.heightPx, maxSixelWidth, maxSixelHeight)
			p.kittyRefuse(id, params)
			return
		}
		pend.params = params
		p.putKittyPending(id, pend)
	}
	if len(rawB64) > 0 {
		// Two ceilings: one transmission may not exceed maxKittyImageBytes, and
		// all in-flight transmissions together may not exceed
		// maxKittyPendingBytes — otherwise maxKittyPendingChunks abandoned
		// transfers could each sit on a full-size buffer.
		if len(pend.b64)+len(rawB64) > maxKittyImageBytes ||
			p.kittyPendingBytes+len(rawB64) > maxKittyPendingBytes {
			// Silently truncating leaves the client staring at nothing when it
			// asked for quiet (q=2), so leave a trace for whoever debugs it —
			// once per transmission, not once per dropped chunk.
			if !pend.truncated {
				log.Printf("term: KGP transmission %d exceeds the %d-byte per-image "+
					"or %d-byte total base64 budget, truncated",
					id, maxKittyImageBytes, maxKittyPendingBytes)
				pend.truncated = true
				p.putKittyPending(id, pend)
			}
		} else {
			pend.b64 = append(pend.b64, rawB64...)
			p.putKittyPending(id, pend)
		}
	}
	// Reaching here means this id is being accepted, so it owns the open slot
	// outright — any earlier refusal it might have inherited is over.
	p.kittyOpenDropped = false
	if params.more {
		p.kittyOpenID = id
		return
	}
	p.kittyOpenID = 0
	p.dropKittyPending(id)

	// Decode with the opening chunk's control data — q= included, since
	// continuation chunks do not repeat it and a client that opened with q=2
	// expects silence. A closing chunk that states its own non-zero q= still
	// wins; 0 is indistinguishable from absent, so it cannot un-quiet.
	quiet := pend.params.quiet
	if params.quiet != 0 {
		quiet = params.quiet
	}
	params = pend.params
	params.quiet = quiet
	params.imageID = id
	if pend.truncated {
		// A truncated buffer cannot decode to anything but garbage, so skip the
		// decode rather than spend a second full-size allocation and an image
		// decode on it. The client still gets its answer.
		p.kittyReply(id, params.quiet, false)
		return
	}
	p.kittyFinish(params, id, pend.b64)
}

// putKittyPending stores pend under id, keeping kittyPendingBytes in step with
// the table. Every write to kittyChunks outside the tests goes through here (or
// dropKittyPending) so the aggregate byte count cannot drift from the map.
func (p *parser) putKittyPending(id uint32, pend kittyPending) {
	p.kittyPendingBytes += len(pend.b64) - len(p.kittyChunks[id].b64)
	p.kittyChunks[id] = pend
}

// dropKittyPending removes id's pending state and returns its bytes to the
// shared budget.
func (p *parser) dropKittyPending(id uint32) {
	p.kittyPendingBytes -= len(p.kittyChunks[id].b64)
	if p.kittyPendingBytes < 0 {
		// Only reachable if something bypassed putKittyPending; clamp rather
		// than let a negative count hand out unbounded headroom.
		p.kittyPendingBytes = 0
	}
	delete(p.kittyChunks, id)
}

// kittyRefuse rejects a transmission at its opening chunk: it answers the
// client so it doesn't wait on a dead transfer, and — when the chunk left the
// transfer open (m=1) — marks the id so the payload-only continuations still
// coming are discarded instead of being attributed to another transmission.
func (p *parser) kittyRefuse(id uint32, params kgpParams) {
	p.kittyReply(id, params.quiet, false)
	if params.more {
		p.kittyOpenID = id
		p.kittyOpenDropped = true
	}
}

// kittyFinish decodes one complete transmission's base64 text, stores the
// resulting PNG under id when id is non-zero, and places it when the action
// asks for it. params must already carry the *opening* chunk's control data.
func (p *parser) kittyFinish(params kgpParams, id uint32, assembledB64 []byte) {
	var raw []byte
	if len(assembledB64) > 0 {
		// Decode []byte directly to avoid a string copy of a potentially large buffer.
		// len(assembledB64) is always a safe upper bound for both padded and raw
		// base64 (raw with len%4!=0 decodes to more bytes than StdEncoding.DecodedLen
		// estimates, which would silently truncate the output).
		dst := make([]byte, len(assembledB64))
		n, err := base64.StdEncoding.Decode(dst, assembledB64)
		if err != nil {
			n, err = base64.RawStdEncoding.Decode(dst, assembledB64)
		}
		if err == nil {
			raw = dst[:n]
		}
	}

	img := p.kittyDecodeImage(params, raw)
	if img == nil {
		p.kittyReply(id, params.quiet, false)
		return
	}
	path := encodePNGFile(img, p.graphicsDir)
	if path == "" {
		p.kittyReply(id, params.quiet, false)
		return
	}

	b := img.Bounds()
	if id != 0 {
		p.kittyStoreImage(id, path, b.Dx(), b.Dy())
	}

	if params.action == 'T' || id == 0 {
		if !p.kittyDisplay(path, b.Dx(), b.Dy(), id, params) {
			p.kittyReply(id, params.quiet, false)
			return
		}
	}

	p.kittyReply(id, params.quiet, true)
}

// kittyDisplay shows the image at path: normally by registering a placement at
// the cursor and advancing past it (one Newline per row it covers), or — when
// the client asked for a virtual placement (U=1) — by recording it in the
// grid's virtual store, where it waits for placeholder cells to name it.
// noCursor (C=1) skips the advance, leaving the client's own positioning
// intact. Shared by the display half of a transmission (a=T) and the place
// action (a=p), so the cursor and footprint policies have one home rather than
// one per call site. Reports false when the request was rejected.
func (p *parser) kittyDisplay(
	path string, widthPx, heightPx int, id uint32, params kgpParams,
) bool {
	cols, rows := kgpCellRect(params.cols, params.rows,
		widthPx, heightPx, p.g.CellPxW, p.g.CellPxH)

	if params.virtual {
		// A virtual placement is addressed only by its image id, so one
		// without an i= key can never be displayed. Refusing tells the client
		// something is wrong instead of silently swallowing the image.
		if id == 0 {
			return false
		}
		if cols <= 0 || rows <= 0 {
			// Neither c= nor r= was given. The spec expects both for a virtual
			// placement; fall back to the pixel-derived footprint so a client
			// that omits them still gets a sensibly sized image.
			cols, rows = p.g.cellsForPixels(widthPx, heightPx)
		}
		p.g.addVirtualImage(id, virtualImage{
			Src: path, WidthPx: widthPx, HeightPx: heightPx,
			Cols: cols, Rows: rows, PlacementID: params.placeID,
		})
		return true
	}

	_, placedRows := p.g.AddGraphicKitty(path, widthPx, heightPx, cols, rows, id)
	if params.noCursor {
		return true
	}
	for range placedRows {
		p.g.Newline()
	}
	return true
}

// kittyStoreImage records the PNG at path in the off-screen store under id,
// evicting one entry first when the store is at capacity. Eviction prefers an
// image not currently on screen so a visible picture's file doesn't vanish
// under the renderer, but falls back to any entry — otherwise the store would
// freeze permanently once every stored image is displayed.
func (p *parser) kittyStoreImage(id uint32, path string, w, h int) {
	if p.kittyStore == nil {
		p.kittyStore = make(map[uint32]kittyEntry)
	}
	if len(p.kittyStore) >= maxKittyStoreEntries {
		var fallbackID uint32
		for evictID, e := range p.kittyStore {
			fallbackID = evictID
			if p.g.graphicsUseSrc(e.path) {
				continue
			}
			_ = os.Remove(e.path)
			delete(p.kittyStore, evictID)
			fallbackID = 0
			break
		}
		if fallbackID != 0 {
			evicted := p.kittyStore[fallbackID].path
			_ = os.Remove(evicted)
			delete(p.kittyStore, fallbackID)
			// Drop the placements too, or the renderer keeps trying to draw
			// a file that is no longer there.
			p.g.deleteGraphicsBySrc(evicted)
		}
	}
	p.kittyStore[id] = kittyEntry{path: path, w: w, h: h}
}

// kittyPlace retrieves a previously transmitted image by id and renders
// it at the current cursor position. C=1 (noCursor) suppresses the cursor
// advance so the caller's own positioning survives the placement.
func (p *parser) kittyPlace(params kgpParams) {
	id, quiet := params.imageID, params.quiet
	if p.kittyStore == nil {
		p.kittyReply(id, quiet, false)
		return
	}
	e, ok := p.kittyStore[id]
	if !ok || e.path == "" {
		p.kittyReply(id, quiet, false)
		return
	}
	if !p.kittyDisplay(e.path, e.w, e.h, id, params) {
		p.kittyReply(id, quiet, false)
		return
	}
	p.kittyReply(id, quiet, true)
}

// kittyDeleteID handles the delete action (a=d). The d= key selects what goes;
// with no d= key at all the spec says "delete all images visible on screen",
// i.e. the same as d=a. Case carries meaning: the lowercase variant removes
// only the on-screen placements and keeps the stored image data so the client
// can re-display it with a=p, the uppercase variant frees the data too.
//
// Implemented selectors:
//
//	a/A  every placement on screen (what yazi and other previewers send to
//	     clear a preview: ESC _ G q=2,a=d,d=A ESC \)
//	i/I  the placements of the image named by i=
//
// The remaining selectors (n, c, f, p, q, x, y, z) are ignored rather than
// falling through to delete-by-id, which would drop images the client never
// named.
func (p *parser) kittyDeleteID(id uint32, op string) {
	sel := byte('a') // no d= key means "all visible placements"
	if op != "" {
		sel = op[0]
	}
	freeData := sel >= 'A' && sel <= 'Z'
	sel |= 0x20 // fold to lowercase; only the ASCII letters above matter

	switch sel {
	case 'a':
		p.g.deleteGraphics(0, true)
		// Virtual placements are not "visible on screen" — they own no cells —
		// so the lowercase selector leaves them alone. Only freeing the image
		// data (uppercase) can take them, since without the file there is
		// nothing left to draw.
		if freeData {
			p.g.deleteAllVirtualImages()
			for _, e := range p.kittyStore {
				_ = os.Remove(e.path)
			}
			p.kittyStore = nil
		}
	case 'i':
		p.g.deleteGraphics(id, false)
		// d=i names one image explicitly, so its virtual placement goes too:
		// the client asked for that image to stop being displayed, and for a
		// virtual placement the placement *is* the display.
		p.g.deleteVirtualImage(id)
		if freeData {
			if e, ok := p.kittyStore[id]; ok {
				_ = os.Remove(e.path)
				delete(p.kittyStore, id)
			}
		}
	}
}

// kittyReply sends a KGP response. quiet: 0=always, 1=suppress OK, 2=always suppress.
func (p *parser) kittyReply(id uint32, quiet int, ok bool) {
	if p.onReply == nil {
		return
	}
	if quiet == 2 || (quiet == 1 && ok) {
		return
	}
	msg := "OK"
	if !ok {
		msg = "EINVAL:unsupported"
	}
	buf := make([]byte, 0, 32)
	buf = append(buf, '\x1b', '_', 'G')
	buf = append(buf, 'i', '=')
	buf = strconv.AppendUint(buf, uint64(id), 10)
	buf = append(buf, ';')
	buf = append(buf, msg...)
	buf = append(buf, '\x1b', '\\')
	p.onReply(buf)
}

// kittyDecodeImage converts raw bytes into NRGBA based on params.format.
func (p *parser) kittyDecodeImage(params kgpParams, raw []byte) *image.NRGBA {
	if len(raw) == 0 {
		return nil
	}
	switch params.format {
	case 100:
		return decodeImageBytes(raw)
	case 32:
		return kittyRawToNRGBA(raw, params.widthPx, params.heightPx, 4)
	case 24:
		return kittyRawToNRGBA(raw, params.widthPx, params.heightPx, 3)
	default:
		return decodeImageBytes(raw)
	}
}

// kittyRawToNRGBA constructs image.NRGBA from raw pixel bytes.
func kittyRawToNRGBA(raw []byte, w, h, bpp int) *image.NRGBA {
	if w <= 0 || h <= 0 || bpp < 3 || bpp > 4 {
		return nil
	}
	if w > maxSixelWidth || h > maxSixelHeight {
		return nil
	}
	if len(raw) < w*h*bpp {
		return nil
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			src := (y*w + x) * bpp
			dst := (y*w + x) * 4
			img.Pix[dst+0] = raw[src+0]
			img.Pix[dst+1] = raw[src+1]
			img.Pix[dst+2] = raw[src+2]
			if bpp == 4 {
				img.Pix[dst+3] = raw[src+3]
			} else {
				img.Pix[dst+3] = 0xFF
			}
		}
	}
	return img
}
