package term

import (
	"strings"
	"unicode/utf8"
)

// OSC 7501 — the Program Status Protocol. A program reports what it is doing
// (idle, working, waiting on the user, done, failed) as a list of key=value
// pairs; the terminal keeps one record per id. Spec:
// https://www.superlogical.com/rex/docs/build/program-status (revision 0.3).
//
//	OSC 7501 ; state=blocked:kind=permission:app=terraform:msg=<base64> ST
//	OSC 7501 ; ? ST    feature detection; the reply is the same sequence
//
// The parser only validates and stores. What a record looks like on screen is
// the embedder's business (Term.ProgramStatus, Cfg.OnProgramStatus).

// statusReply is the fixed answer to the feature detection query. It is the
// only thing the protocol ever writes back: ids, titles and messages are never
// echoed, so a report cannot be turned into input for the child.
const statusReply = "\x1b]7501;?\x1b\\"

// handleOSC7501 parses one report and applies it to the grid. pt is the body
// after "7501;". Called with g.Mu held.
//
// Every check runs before the store is touched: a report that breaks a limit,
// carries undecodable base64, or decodes to a control character is dropped
// whole, never half-applied.
func (p *parser) handleOSC7501(pt string) {
	// The whole-sequence cap. A payload over the generic OSC cap was already
	// cut short (oscTrunc); the length test covers the 4 bytes of OSC/ST
	// framing around a payload just under it. 3 assumes the shorter BEL
	// terminator, which errs towards accepting.
	if p.oscTrunc || len(p.osc)+3 > maxStatusSeqBytes {
		return
	}
	if strings.TrimSpace(pt) == "?" {
		if p.onReply != nil {
			p.onReply([]byte(statusReply))
		}
		return
	}

	// Raw values, last one wins. Empty string and "absent" are the same for
	// every key: an empty state is unrecognized, an empty id is the root, and
	// an empty msg or app says nothing.
	var state, id, kind, progress, app, title, msg string
	for rest := pt; rest != ""; {
		var pair string
		pair, rest, _ = strings.Cut(rest, ":")
		key, val, ok := strings.Cut(pair, "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if len(key) > maxStatusKeyBytes {
			return // a limit, not a malformed pair: drop the report
		}
		// Malformed pairs are skipped and the rest still applies.
		if !ok || !isStatusKey(key) || !isStatusValue(val) {
			continue
		}
		switch key {
		case "state":
			state = val
		case "id":
			id = val
		case "kind":
			kind = val
		case "progress":
			progress = val
		case "app":
			app = val
		case "title":
			title = val
		case "msg":
			msg = val
		}
		// Unknown keys are ignored: that is how the protocol is extended.
	}

	st, isClear, ok := parseStatusState(state)
	if !ok {
		return // no state, or one added after this revision
	}
	// An invalid id is ignored rather than read as the root: falling back
	// would let a malformed id overwrite the root record.
	if !validStatusID(id) {
		return
	}
	// Limits and text checks run even for a clear, which uses neither: the
	// protocol discards a report that breaks one whole, whatever its state.
	if len(app) > maxStatusAppBytes || len(msg) > maxStatusMsgEncoded ||
		len(title) > maxStatusTitleEncoded {
		return
	}
	rec := statusRec{id: id, state: st, progress: -1}
	if rec.title, ok = decodeStatusText(title, maxStatusTitleDecoded); !ok {
		return
	}
	if rec.msg, ok = decodeStatusText(msg, maxStatusMsgDecoded); !ok {
		return
	}
	if isClear {
		p.g.clearStatus(id)
		return
	}
	// The remaining keys degrade to absent instead of failing the report.
	if isStatusApp(app) {
		rec.app = app
	}
	if st == statusBlocked {
		switch kind {
		case "permission", "question", "auth":
			rec.kind = kind
		}
	}
	if st == statusWorking || st == statusBlocked {
		rec.progress = parseStatusProgress(progress)
	}
	p.g.setStatus(rec)
}

// parseStatusState maps the state value. isClear is the one value that is not
// a state but a removal. ok is false for anything this revision does not know,
// so a state added later never turns into idle on this terminal.
func parseStatusState(s string) (st statusState, isClear, ok bool) {
	switch s {
	case "idle":
		return statusIdle, false, true
	case "working":
		return statusWorking, false, true
	case "done":
		return statusDone, false, true
	case "blocked":
		return statusBlocked, false, true
	case "error":
		return statusError, false, true
	case "clear":
		return 0, true, true
	}
	return 0, false, false
}

// isStatusKey reports whether k matches the key grammar [a-z]+.
func isStatusKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 'a' || k[i] > 'z' {
			return false
		}
	}
	return true
}

// isStatusValue reports whether v is inside the value set [A-Za-z0-9_.,+/=-]*.
// The set has no ':' or ';', which is why nothing in the protocol needs
// escaping.
func isStatusValue(v string) bool {
	for i := 0; i < len(v); i++ {
		if !isStatusAlnum(v[i]) && strings.IndexByte("_.,+/=-", v[i]) < 0 {
			return false
		}
	}
	return true
}

func isStatusAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// isStatusNameByte is the set [A-Za-z0-9_.+-] shared by app and id segments.
func isStatusNameByte(c byte) bool {
	return isStatusAlnum(c) || c == '_' || c == '.' || c == '+' || c == '-'
}

// isStatusApp reports whether a matches [A-Za-z0-9_.+-]{1,32}.
func isStatusApp(a string) bool {
	if a == "" || len(a) > maxStatusAppBytes {
		return false
	}
	for i := 0; i < len(a); i++ {
		if !isStatusNameByte(a[i]) {
			return false
		}
	}
	return true
}

// validStatusID reports whether id is "" (the root) or a "/"-separated path of
// 1..maxStatusIDDepth segments, each [A-Za-z0-9_.+-]{1,32}, maxStatusIDBytes
// in total. The depth and total length are protocol limits; enforcing them here
// drops the report the same way the grammar check does.
func validStatusID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > maxStatusIDBytes {
		return false
	}
	depth, seg := 1, 0
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == '/' {
			if seg == 0 {
				return false // empty segment: leading, trailing, or doubled '/'
			}
			depth, seg = depth+1, 0
			continue
		}
		if !isStatusNameByte(c) {
			return false
		}
		seg++
		if seg > maxStatusIDSegment {
			return false
		}
	}
	return seg > 0 && depth <= maxStatusIDDepth
}

// parseStatusProgress reads progress as an integer 0..100. Anything else —
// a sign, a fraction, 101 — is treated as absent, which means indeterminate.
func parseStatusProgress(s string) int8 {
	if s == "" || len(s) > 3 {
		return -1
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	if n > 100 {
		return -1
	}
	return int8(n)
}

// decodeStatusText decodes a base64 msg or title. ok is false when the report
// must be dropped: bad base64, decoded text over maxLen bytes, text that is not
// UTF-8, or text holding a control character (C0, DEL, or C1). The protocol
// requires refusing those rather than stripping them.
//
// Invisible formatting characters (bidi overrides and isolates, directional
// marks) are removed rather than refused. They are legal text, but outside the
// grid — in a tab bar or a notification — an override can make a message read
// backwards or hide part of it, and the protocol asks terminals to disarm them.
// Doing it here means every embedder gets disarmed text.
func decodeStatusText(b64 string, maxLen int) (string, bool) {
	if b64 == "" {
		return "", true
	}
	raw, err := decodeBase64String(b64)
	if err != nil || len(raw) > maxLen || !utf8.Valid(raw) {
		return "", false
	}
	strip := false
	for _, r := range string(raw) {
		if r < 0x20 || r >= 0x7F && r <= 0x9F {
			return "", false
		}
		if isBidiFormat(r) {
			strip = true
		}
	}
	if !strip {
		return string(raw), true
	}
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range string(raw) {
		if !isBidiFormat(r) {
			b.WriteRune(r)
		}
	}
	return b.String(), true
}

// isBidiFormat reports the invisible characters that change text direction:
// LRM/RLM, the Arabic letter mark, the embeddings and overrides (U+202A–202E),
// and the isolates (U+2066–2069).
func isBidiFormat(r rune) bool {
	return r == 0x200E || r == 0x200F || r == 0x061C ||
		r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069
}
