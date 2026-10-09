package term

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// osc7501 wraps a report body in OSC 7501 framing with an ST terminator.
func osc7501(body string) []byte { return []byte("\x1b]7501;" + body + "\x1b\\") }

// b64 is standard padded base64 of s, the form the protocol's examples use.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// statusByID returns the stored record for id, or false.
func statusByID(g *grid, id string) (statusRec, bool) {
	for _, r := range g.status {
		if r.id == id {
			return r, true
		}
	}
	return statusRec{}, false
}

func TestOSC7501_FeatureDetectionReply(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, osc7501("?"))
	if string(reply) != statusReply {
		t.Fatalf("reply = %q, want %q", reply, statusReply)
	}
	if len(g.status) != 0 {
		t.Fatal("the query must not create a record")
	}
	// BEL terminator works too.
	reply = nil
	feed(t, g, p, []byte("\x1b]7501;?\x07"))
	if string(reply) != statusReply {
		t.Fatalf("BEL-terminated reply = %q", reply)
	}
}

// The protocol's own headline example.
func TestOSC7501_BlockedReport(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=blocked:kind=permission:app=terraform:"+
		"msg=QXBwbHkgMyB0byBhZGQsIDEgdG8gY2hhbmdlLCAwIHRvIGRlc3Ryb3k/"))
	r, ok := statusByID(g, "")
	if !ok {
		t.Fatal("no root record")
	}
	want := statusRec{
		state: statusBlocked, kind: "permission", progress: -1, app: "terraform",
		msg: "Apply 3 to add, 1 to change, 0 to destroy?",
	}
	r.touched = 0
	if r != want {
		t.Fatalf("record = %+v, want %+v", r, want)
	}
}

// A report replaces its record completely: keys it leaves out are gone.
func TestOSC7501_ReportReplacesRecord(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=working:app=brew:progress=40:msg="+b64("Installing")))
	feed(t, g, p, osc7501("state=done"))
	r, _ := statusByID(g, "")
	if r.state != statusDone || r.app != "" || r.msg != "" || r.progress != -1 {
		t.Fatalf("record = %+v, want a bare done", r)
	}
	if len(g.status) != 1 {
		t.Fatalf("records = %d, want 1", len(g.status))
	}
}

func TestOSC7501_StateRules(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		stored bool
	}{
		{"no state", "app=x", false},
		{"unknown state", "state=sleeping", false},
		{"empty state", "state=", false},
		{"idle", "state=idle", true},
		{"error", "state=error", true},
		// Last value wins, so a known state after an unknown one counts.
		{"repeated key", "state=bogus:state=working", true},
		// Whitespace around keys and values is removed.
		{"whitespace", " state = working ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(2, 10)
			feed(t, g, p, osc7501(tt.body))
			if got := len(g.status) == 1; got != tt.stored {
				t.Fatalf("stored = %v, want %v", got, tt.stored)
			}
		})
	}
}

// Malformed pairs are skipped and the rest of the report still applies.
func TestOSC7501_MalformedPairsSkipped(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("junk:=x:APP=y:app=ok!:state=working:app=good"))
	r, ok := statusByID(g, "")
	if !ok || r.state != statusWorking || r.app != "good" {
		t.Fatalf("record = %+v, ok = %v", r, ok)
	}
}

func TestOSC7501_KindAndProgressScoping(t *testing.T) {
	tests := []struct {
		body     string
		kind     string
		progress int8
	}{
		{"state=blocked:kind=auth:progress=10", "auth", 10},
		{"state=blocked:kind=nope", "", -1},
		{"state=working:kind=auth:progress=100", "", 100},
		{"state=working:progress=0", "", 0},
		{"state=working:progress=101", "", -1},
		{"state=working:progress=-1", "", -1},
		{"state=working:progress=5.5", "", -1},
		{"state=done:progress=50", "", -1},
		{"state=idle:kind=question", "", -1},
	}
	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			g, p := newParserGrid(2, 10)
			feed(t, g, p, osc7501(tt.body))
			r, ok := statusByID(g, "")
			if !ok || r.kind != tt.kind || r.progress != tt.progress {
				t.Fatalf("record = %+v, want kind %q progress %d", r, tt.kind, tt.progress)
			}
		})
	}
}

func TestOSC7501_AppOutsideSetIsAbsent(t *testing.T) {
	g, p := newParserGrid(2, 10)
	// "a,b" is inside the value set but outside app's own set.
	feed(t, g, p, osc7501("state=idle:app=a,b"))
	r, ok := statusByID(g, "")
	if !ok || r.app != "" {
		t.Fatalf("record = %+v, ok = %v; want stored with no app", r, ok)
	}
}

func TestOSC7501_IDGrammar(t *testing.T) {
	deep := strings.Repeat("a/", maxStatusIDDepth) + "a" // one level too deep
	tests := []struct {
		id    string
		valid bool
	}{
		{"build", true},
		{"build/test", true},
		{strings.TrimSuffix(strings.Repeat("a/", maxStatusIDDepth), "/"), true},
		{strings.Repeat("x", maxStatusIDSegment), true},
		{strings.Repeat("x", maxStatusIDSegment+1), false},
		{deep, false},
		{"/build", false},
		{"build/", false},
		{"a//b", false},
		{"a,b", false},
		{"a=b", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			g, p := newParserGrid(2, 10)
			feed(t, g, p, osc7501("state=idle"))
			feed(t, g, p, osc7501("state=working:id="+tt.id))
			_, ok := statusByID(g, tt.id)
			if ok != tt.valid {
				t.Fatalf("stored = %v, want %v", ok, tt.valid)
			}
			// An invalid id must never fall back to overwriting the root.
			if root, _ := statusByID(g, ""); root.state != statusIdle {
				t.Fatalf("root overwritten: %+v", root)
			}
		})
	}
}

func TestOSC7501_TextValidation(t *testing.T) {
	tests := []struct {
		name   string
		msg    string // already encoded
		stored bool
		want   string
	}{
		{"plain", b64("hello"), true, "hello"},
		{"unpadded", strings.TrimRight(b64("hi"), "="), true, "hi"},
		// In the value set, but no valid base64 is one character long.
		{"bad base64", "A", false, ""},
		{"C0", b64("a\nb"), false, ""},
		{"DEL", b64("a\x7fb"), false, ""},
		{"C1", b64("a\u0085b"), false, ""},
		{"invalid utf8", base64.StdEncoding.EncodeToString([]byte{0xff, 0xfe}), false, ""},
		// Bidi overrides are legal text, disarmed rather than refused.
		{"bidi override", b64("ab\u202Ecd\u2066"), true, "abcd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(2, 10)
			feed(t, g, p, osc7501("state=done:msg="+tt.msg))
			r, ok := statusByID(g, "")
			if ok != tt.stored {
				t.Fatalf("stored = %v, want %v", ok, tt.stored)
			}
			if ok && r.msg != tt.want {
				t.Fatalf("msg = %q, want %q", r.msg, tt.want)
			}
		})
	}
}

// A report that breaks a limit is discarded whole, even when the rest of it is
// fine — and must not disturb what is already stored.
func TestOSC7501_LimitsDiscardWhole(t *testing.T) {
	tests := []struct{ name, body string }{
		{"long key", "state=done:" + strings.Repeat("k", maxStatusKeyBytes+1) + "=1"},
		{"long app", "state=done:app=" + strings.Repeat("a", maxStatusAppBytes+1)},
		{"msg encoded", "state=done:msg=" + strings.Repeat("A", maxStatusMsgEncoded+4)},
		{"msg decoded", "state=done:msg=" + b64(strings.Repeat("x", maxStatusMsgDecoded+1))},
		{"title encoded", "state=done:title=" + strings.Repeat("A", maxStatusTitleEncoded+4)},
		{"title decoded", "state=done:title=" + b64(strings.Repeat("x", maxStatusTitleDecoded+1))},
		{"id total", "state=done:id=" + strings.Repeat("abcdefghijklmnop/", 8)[:maxStatusIDBytes+1]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(2, 10)
			feed(t, g, p, osc7501("state=idle"))
			feed(t, g, p, osc7501(tt.body))
			if len(g.status) != 1 || g.status[0].state != statusIdle {
				t.Fatalf("records = %+v, want only the idle root", g.status)
			}
		})
	}
	// The largest values that fit are accepted.
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=done:msg="+b64(strings.Repeat("x", maxStatusMsgDecoded))+
		":title="+b64(strings.Repeat("y", maxStatusTitleDecoded))))
	if r, ok := statusByID(g, ""); !ok || len(r.msg) != maxStatusMsgDecoded ||
		len(r.title) != maxStatusTitleDecoded {
		t.Fatalf("max-size report not stored: ok = %v", ok)
	}
}

// A sequence past the generic OSC cap is truncated by the parser; the report
// must be dropped, not applied with its tail cut off.
func TestOSC7501_OversizeSequenceDropped(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=done:pad="+strings.Repeat("A", maxOSCBytes)))
	if len(g.status) != 0 {
		t.Fatal("over-cap report was applied")
	}
}

func TestOSC7501_ClearSubtree(t *testing.T) {
	g, p := newParserGrid(2, 10)
	for _, id := range []string{"", "build", "build/test", "build/test/unit", "buildx", "deploy"} {
		body := "state=working"
		if id != "" {
			body += ":id=" + id
		}
		feed(t, g, p, osc7501(body))
	}
	feed(t, g, p, osc7501("state=clear:id=build"))
	ids := make([]string, 0, len(g.status))
	for _, r := range g.status {
		ids = append(ids, strconv.Quote(r.id))
	}
	// "buildx" shares a prefix with "build" but is not beneath it.
	if got := strings.Join(ids, ","); got != `"","buildx","deploy"` {
		t.Fatalf("after clear build: %s", got)
	}
	// A clear with no id removes every record.
	feed(t, g, p, osc7501("state=clear"))
	if len(g.status) != 0 {
		t.Fatalf("after bare clear: %d records", len(g.status))
	}
}

// A new shell prompt drops working and blocked records; done, error and idle
// stay for the user to find.
func TestOSC7501_PromptDropsTransient(t *testing.T) {
	g, p := newParserGrid(2, 10)
	for _, s := range []string{"idle", "working", "done", "blocked", "error"} {
		feed(t, g, p, osc7501("state="+s+":id="+s))
	}
	feed(t, g, p, []byte("\x1b]133;A\x07"))
	for _, s := range []string{"idle", "done", "error"} {
		if _, ok := statusByID(g, s); !ok {
			t.Errorf("%s record dropped at prompt", s)
		}
	}
	for _, s := range []string{"working", "blocked"} {
		if _, ok := statusByID(g, s); ok {
			t.Errorf("%s record survived the prompt", s)
		}
	}
}

// RIS removes every record; DECSTR and the alternate screen leave them alone.
func TestOSC7501_ResetAndAltScreen(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=done"))
	feed(t, g, p, []byte("\x1b[?1049h\x1b[!p\x1b[?1049l"))
	if len(g.status) != 1 {
		t.Fatal("DECSTR or alt screen removed a record")
	}
	feed(t, g, p, []byte("\x1bc"))
	if len(g.status) != 0 {
		t.Fatal("RIS kept a record")
	}
}

// Past the cap, the record updated least recently makes room.
func TestOSC7501_EvictsLeastRecentlyUpdated(t *testing.T) {
	g, p := newParserGrid(2, 10)
	for i := range maxStatusRecords {
		feed(t, g, p, osc7501("state=working:id=r"+strconv.Itoa(i)))
	}
	// Touch r0 so r1 becomes the oldest.
	feed(t, g, p, osc7501("state=done:id=r0"))
	feed(t, g, p, osc7501("state=working:id=new"))
	if len(g.status) != maxStatusRecords {
		t.Fatalf("records = %d, want %d", len(g.status), maxStatusRecords)
	}
	if _, ok := statusByID(g, "r1"); ok {
		t.Error("r1 (least recently updated) was not evicted")
	}
	for _, id := range []string{"r0", "new"} {
		if _, ok := statusByID(g, id); !ok {
			t.Errorf("%s missing", id)
		}
	}
}

// StatusVersion is what tells the embedder to look. It must move on every
// change and stay put when nothing changed — a prompt with no transient record
// to drop, a clear of an id that does not exist.
func TestOSC7501_StatusVersion(t *testing.T) {
	g, p := newParserGrid(2, 10)
	v := g.StatusVersion
	feed(t, g, p, []byte("\x1b]133;A\x07"))
	feed(t, g, p, osc7501("state=clear:id=nothing"))
	feed(t, g, p, osc7501("state=bogus"))
	if g.StatusVersion != v {
		t.Fatal("version moved with no change")
	}
	feed(t, g, p, osc7501("state=working"))
	if g.StatusVersion == v {
		t.Fatal("version did not move on a report")
	}
	v = g.StatusVersion
	feed(t, g, p, []byte("\x1b]133;A\x07"))
	if g.StatusVersion == v {
		t.Fatal("version did not move when the prompt dropped a record")
	}
}

// The whole-sequence cap is checked on its own, not left to the generic OSC
// cap: a report whose payload is just inside the OSC buffer but whose framed
// sequence passes maxStatusSeqBytes is dropped.
func TestOSC7501_SequenceCapBoundary(t *testing.T) {
	// payload is "7501;" + body; with the shortest (BEL) terminator the
	// sequence is payload + 3 bytes.
	body := func(payload int) string {
		head := "state=done:pad="
		return head + strings.Repeat("A", payload-len("7501;")-len(head))
	}
	fits := maxStatusSeqBytes - 3
	for _, tt := range []struct {
		payload int
		stored  bool
	}{{fits, true}, {fits + 1, false}} {
		g, p := newParserGrid(2, 10)
		feed(t, g, p, osc7501(body(tt.payload)))
		if _, ok := statusByID(g, ""); ok != tt.stored {
			t.Fatalf("payload %d: stored = %v, want %v", tt.payload, ok, tt.stored)
		}
	}
}

// A clear that breaks a limit is discarded like any other report: it must not
// remove the records it names.
func TestOSC7501_ClearBreakingLimitIgnored(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=working:id=job"))
	feed(t, g, p, osc7501("state=clear:id=job:app="+strings.Repeat("a", maxStatusAppBytes+1)))
	feed(t, g, p, osc7501("state=clear:id=job:msg=A"))
	if _, ok := statusByID(g, "job"); !ok {
		t.Fatal("invalid clear removed the record")
	}
}

// A repeated key takes its last value; an unknown key is ignored.
func TestOSC7501_RepeatedKeyLastWins(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, osc7501("state=idle:future=1:state=blocked:kind=auth:kind=question"))
	r, ok := statusByID(g, "")
	if !ok || r.state != statusBlocked || r.kind != "question" {
		t.Fatalf("record = %+v, ok = %v", r, ok)
	}
}
