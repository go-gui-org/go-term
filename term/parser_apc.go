package term

import (
	"encoding/base64"
	"image"
	"os"
	"strconv"
)

// kittyEntry is one entry in the off-screen image store (kittyStore).
type kittyEntry struct {
	path string
	w, h int // pixel dimensions of the stored image
}

// dispatchAPC processes a completed APC sequence (ESC _ payload ESC \).
// Only the Kitty Graphics Protocol (payload starting with 'G') is handled;
// everything else is silently dropped.
func (p *Parser) dispatchAPC() {
	if len(p.apc) < 1 || p.apc[0] != 'G' {
		return
	}
	p.handleKittyGraphics(p.apc[1:])
}

// kgpParams holds the decoded key=value pairs from a KGP escape.
type kgpParams struct {
	action   byte   // a=: 't' transmit, 'T' transmit+display, 'p' place, 'q' query, 'd' delete; default 'T'
	format   int    // f=: 32 RGBA, 24 RGB, 100 PNG; default 32
	medium   byte   // t=: 'd' direct (default), 'f' file, 't' temp-file, 's' shared-memory
	widthPx  int    // s=: pixel width for raw formats
	heightPx int    // v=: pixel height for raw formats
	more     bool   // m=: true when m=1 (more chunks follow)
	imageID  uint32 // i=: image id (0 = anonymous)
	quiet    int    // q=: 0 reply always, 1 suppress OK, 2 always suppress
	deleteOp string // d=: delete specifier (when a=d)
}

// handleKittyGraphics parses a Kitty Graphics Protocol payload (bytes after
// the leading 'G') and dispatches the appropriate action.
func (p *Parser) handleKittyGraphics(payload []byte) {
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
		p.kittyPlace(params.imageID, params.quiet)
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
			params.format, _ = strconv.Atoi(val)
		case 't':
			if len(val) == 1 {
				params.medium = val[0]
			}
		case 's':
			params.widthPx, _ = strconv.Atoi(val)
		case 'v':
			params.heightPx, _ = strconv.Atoi(val)
		case 'm':
			params.more = val == "1"
		case 'i':
			n, _ := strconv.ParseUint(val, 10, 32)
			params.imageID = uint32(n)
		case 'q':
			params.quiet, _ = strconv.Atoi(val)
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
func (p *Parser) kittyAccumulate(params kgpParams, rawB64 []byte) {
	id := params.imageID
	if p.kittyChunks == nil {
		p.kittyChunks = make(map[uint32][]byte)
	}
	if len(rawB64) > 0 {
		cur, known := p.kittyChunks[id]
		// Refuse new IDs when the pending-chunk table is full to prevent a
		// DoS where a malicious server opens many IDs with m=1 and never
		// finalises them.
		if !known && len(p.kittyChunks) >= maxKittyPendingChunks {
			return
		}
		if len(cur)+len(rawB64) <= maxKittyImageBytes {
			p.kittyChunks[id] = append(cur, rawB64...)
		}
	}
	if params.more {
		return
	}

	assembledB64 := p.kittyChunks[id]
	delete(p.kittyChunks, id)

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
		if p.kittyStore == nil {
			p.kittyStore = make(map[uint32]kittyEntry)
		}
		// Evict one entry when at capacity, skipping paths still referenced
		// by Grid.Graphics (evicting a rendered image deletes its file and
		// causes broken renders for the rest of the session).
		if len(p.kittyStore) >= maxKittyStoreEntries {
			for evictID, e := range p.kittyStore {
				inUse := false
				for _, gr := range p.g.Graphics {
					if gr.Src == e.path {
						inUse = true
						break
					}
				}
				if !inUse {
					_ = os.Remove(e.path)
					delete(p.kittyStore, evictID)
					break
				}
			}
		}
		p.kittyStore[id] = kittyEntry{path: path, w: b.Dx(), h: b.Dy()}
	}

	if params.action == 'T' || id == 0 {
		_, rows := p.g.AddGraphic(path, b.Dx(), b.Dy())
		for range rows {
			p.g.Newline()
		}
	}

	p.kittyReply(id, params.quiet, true)
}

// kittyPlace retrieves a previously transmitted image by id and renders
// it at the current cursor position.
func (p *Parser) kittyPlace(id uint32, quiet int) {
	if p.kittyStore == nil {
		p.kittyReply(id, quiet, false)
		return
	}
	e, ok := p.kittyStore[id]
	if !ok || e.path == "" {
		p.kittyReply(id, quiet, false)
		return
	}
	_, rows := p.g.AddGraphic(e.path, e.w, e.h)
	for range rows {
		p.g.Newline()
	}
	p.kittyReply(id, quiet, true)
}

// kittyDeleteID removes an image from kittyStore. Op "a"/"A" clears all.
// Empty op (no d= key) defaults to delete-by-ID per the KGP spec.
func (p *Parser) kittyDeleteID(id uint32, op string) {
	if p.kittyStore == nil {
		return
	}
	if op == "a" || op == "A" {
		for _, e := range p.kittyStore {
			_ = os.Remove(e.path)
		}
		p.kittyStore = make(map[uint32]kittyEntry)
		return
	}
	if e, ok := p.kittyStore[id]; ok {
		_ = os.Remove(e.path)
		delete(p.kittyStore, id)
	}
}

// kittyReply sends a KGP response. quiet: 0=always, 1=suppress OK, 2=always suppress.
func (p *Parser) kittyReply(id uint32, quiet int, ok bool) {
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
func (p *Parser) kittyDecodeImage(params kgpParams, raw []byte) *image.NRGBA {
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
