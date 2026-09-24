package term

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// Regression tests for the parser review: each one reproduces a bug that was
// confirmed on the code before its fix.

// An OSC 52 copy is base64, so a 3 KB yank is already past the generic 4 KB
// OSC cap. The payload used to be cut at the cap and the truncated text still
// went to the clipboard, so nvim/tmux copies over ssh silently lost their tail.
func TestParser_OSC52_LargeCopyIsNotTruncated(t *testing.T) {
	g, p := newParserGrid(5, 40)
	p.SetClipboardWriteAllowed(true)
	var got []byte
	p.SetClipboardHandler(func(data []byte) { got = append([]byte(nil), data...) })

	text := strings.Repeat("0123456789abcdef", 500) // 8000 bytes
	b64 := base64.StdEncoding.EncodeToString([]byte(text))
	feed(t, g, p, []byte("\x1b]52;c;"+b64+"\x07"))
	if string(got) != text {
		t.Fatalf("clipboard len = %d, want %d", len(got), len(text))
	}
}

// Past the OSC 52 cap the copy is dropped, not cut short: a partial clipboard
// is worse than the old one, because the user pastes it without noticing.
func TestParser_OSC52_OverCapIsDropped(t *testing.T) {
	g, p := newParserGrid(5, 40)
	p.SetClipboardWriteAllowed(true)
	called := false
	p.SetClipboardHandler(func([]byte) { called = true })

	b64 := strings.Repeat("QUJD", maxOSC52Bytes/4+1) // valid base64, over the cap
	feed(t, g, p, []byte("\x1b]52;c;"+b64+"\x07"))
	if called {
		t.Fatal("onClipboard called with a truncated OSC 52 payload")
	}
	// The next, ordinary OSC must see the default cap and clean state again.
	feed(t, g, p, []byte("\x1b]52;c;aGk=\x07"))
	if !called {
		t.Fatal("OSC 52 after an oversized one was not delivered")
	}
}

// The OSC 8 registry used to fill at maxLinkEntries and then refuse every new
// URL until RIS, so a few `ls --hyperlink` runs killed links for the session.
// A full registry now drops the ids no cell refers to any more.
func TestGrid_InternLink_FullRegistryReclaimsDeadIDs(t *testing.T) {
	g, p := newParserGrid(4, 20)
	// One link that stays on screen: its id must survive the sweep.
	feed(t, g, p, []byte("\x1b]8;;https://kept.example\x07K\x1b]8;;\x07"))
	kept := g.At(0, 0).LinkID
	if kept == 0 {
		t.Fatal("kept link has no id")
	}
	// Fill the registry with URLs no cell uses.
	for i := range maxLinkEntries {
		g.internLink("https://dead.example/" + strconv.Itoa(i))
	}
	feed(t, g, p, []byte("\x1b]8;;https://new.example\x07N\x1b]8;;\x07"))
	id := g.At(0, 1).LinkID
	if id == 0 {
		t.Fatal("new link got id 0: full registry was not reclaimed")
	}
	if u := g.LinkURL(id); u != "https://new.example" {
		t.Fatalf("new link URL = %q", u)
	}
	if u := g.LinkURL(kept); u != "https://kept.example" {
		t.Fatalf("live link lost by the sweep: URL = %q", u)
	}
}

// Ids still referenced from scrollback and from the parked main screen are
// live too; the sweep must not hand them to a different URL.
func TestGrid_InternLink_SweepKeepsScrollbackAndMainScreen(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.ScrollbackCap = 10
	g.Scrollback.SetGeom(10, 10)
	feed(t, g, p, []byte("\x1b]8;;https://sb.example\x07S\x1b]8;;\x07\r\n\r\n\r\n"))
	sb := g.Scrollback.Row(0)[0].LinkID
	if sb == 0 {
		t.Fatal("scrollback row carries no link")
	}
	feed(t, g, p, []byte("\x1b]8;;https://main.example\x07M\x1b]8;;\x07"))
	main := g.At(g.CursorR, 0).LinkID
	feed(t, g, p, []byte("\x1b[?1049h"))
	for i := range maxLinkEntries {
		g.internLink("https://dead.example/" + strconv.Itoa(i))
	}
	if g.internLink("https://new.example") == 0 {
		t.Fatal("registry not reclaimed")
	}
	if u := g.LinkURL(sb); u != "https://sb.example" {
		t.Fatalf("scrollback link = %q after sweep", u)
	}
	if u := g.LinkURL(main); u != "https://main.example" {
		t.Fatalf("parked main-screen link = %q after sweep", u)
	}
}

// The kitty spec replies only when the client sent an id. An anonymous
// command used to answer "ESC _ G i=0;OK ESC \", which lands in the shell's
// input line as text.
func TestParser_APC_KittyNoIDNoReply(t *testing.T) {
	h := newAPCHelper(t)
	h.feedAPC("a=d;")
	h.feedAPC("a=z;")
	if len(h.replies) != 0 {
		t.Fatalf("replies = %q; want none for commands without an id", h.replies)
	}
}

// OSC 110/111/112 undo OSC 10/11/12. Without them a program that set the
// background and reset it on exit left the pane in its colors.
func TestParser_OSC11x_ResetsDynamicColors(t *testing.T) {
	g, p := newParserGrid(2, 10)
	fg, bg := g.Theme.DefaultFG, g.Theme.DefaultBG
	feed(t, g, p, []byte("\x1b]10;#00ff00\x07\x1b]11;#ff0000\x07\x1b]12;#0000ff\x07"))
	if g.Theme.DefaultBG == bg || g.CursorColor == defaultColor {
		t.Fatal("OSC 10/11/12 did not apply; the test would prove nothing")
	}
	// Bare form for 110, trailing-';' form for 111, ST terminator for 112.
	feed(t, g, p, []byte("\x1b]110\x07\x1b]111;\x07\x1b]112\x1b\\"))
	if g.Theme.DefaultFG != fg {
		t.Errorf("fg after OSC 110 = %v, want %v", g.Theme.DefaultFG, fg)
	}
	if g.Theme.DefaultBG != bg {
		t.Errorf("bg after OSC 111 = %v, want %v", g.Theme.DefaultBG, bg)
	}
	if g.CursorColor != defaultColor {
		t.Errorf("cursor color after OSC 112 = %#x, want default", g.CursorColor)
	}
}

// OSC 111 restores the embedder's theme, not the one the grid started with.
func TestParser_OSC111_RestoresEmbedderTheme(t *testing.T) {
	g, p := newParserGrid(2, 10)
	th := g.Theme
	th.DefaultBG.R ^= 0x40
	g.setTheme(th)
	feed(t, g, p, []byte("\x1b]11;#ff0000\x07\x1b]111\x07"))
	if g.Theme.DefaultBG != th.DefaultBG {
		t.Fatalf("bg after OSC 111 = %v, want embedder %v", g.Theme.DefaultBG, th.DefaultBG)
	}
}

// A CSI whose intermediate byte is not one the final takes is a different
// control function (SL is "CSI Ps SP @", SR is "CSI Ps SP A"). It used to run
// the plain one, so SL inserted blanks and SR moved the cursor.
func TestParser_CSI_UnknownIntermediateIgnored(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("abcdef\x1b[1;1H"))
	feed(t, g, p, []byte("\x1b[2 @"))
	if got := rowText(g, 0); got != "abcdef  " {
		t.Errorf("row after CSI 2 SP @ = %q; SL must not run ICH", got)
	}
	feed(t, g, p, []byte("\x1b[3;1H\x1b[2 A"))
	if g.CursorR != 2 {
		t.Errorf("cursor row after CSI 2 SP A = %d, want 2; SR must not run CUU", g.CursorR)
	}
	// "CSI Ps SP t" is DECSWBV, not XTWINOPS: no title push may happen.
	p.curTitle = "shell"
	feed(t, g, p, []byte("\x1b[22 t"))
	if len(p.titleStack) != 0 {
		t.Errorf("CSI 22 SP t pushed the title; must not run XTWINOPS")
	}
	// Finals that do take an intermediate still work.
	feed(t, g, p, []byte("\x1b[6 q"))
	if g.cursorShape != CursorStyleBar {
		t.Errorf("DECSCUSR stopped working: shape = %v", g.cursorShape)
	}
}

// With origin mode on, CPR reports the row relative to the top margin, the
// same coordinates CUP takes (xterm). It used to report the absolute row.
func TestParser_CPR_OriginMode(t *testing.T) {
	g, p := newParserGrid(10, 10)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1b[5;8r\x1b[?6h\x1b[2;3H\x1b[6n\x1b[?6n"))
	want := []string{"\x1b[2;3R", "\x1b[?2;3R"}
	if len(replies) != 2 || replies[0] != want[0] || replies[1] != want[1] {
		t.Fatalf("replies = %q, want %q", replies, want)
	}
}

// DECRQSS "m" must describe every attribute SGR can set. Italic,
// strikethrough and friends were left out, and with only those set the reply
// claimed "0m".
func TestCurrentSGRString_AllAttributes(t *testing.T) {
	cases := []struct {
		sgr, want string
	}{
		{"3;9", "3;9m"},
		{"2;5;8;53", "2;5;8;53m"},
		{"4:3", "4:3m"},
		{"21", "4:2m"},
		{"4;58;2;1;2;3", "4;58;2;1;2;3m"},
		{"4;58;5;200", "4;58;5;200m"},
	}
	for _, c := range cases {
		g, p := newParserGrid(1, 5)
		feed(t, g, p, []byte("\x1b["+c.sgr+"m"))
		if got := p.currentSGRString(); got != c.want {
			t.Errorf("SGR %s: DECRQSS = %q, want %q", c.sgr, got, c.want)
		}
	}
}

// XTGETTCAP withheld ech, the palette caps and flash on the grounds that they
// were unimplemented; all three are implemented now. Alt+arrow sends
// CSI 1;3X, so the Alt arrow caps are advertised as well.
func TestXTGETTCAP_ImplementedCapsAdvertised(t *testing.T) {
	for _, name := range []string{"ech", "ccc", "initc", "oc", "flash", "kUP3", "kDN3", "kLFT3", "kRIT3"} {
		if _, ok := xtgettcapValue(name); !ok {
			t.Errorf("xtgettcapValue(%q) not advertised", name)
		}
	}
}

// A sweep that frees nothing backs off for linkSweepBackoff refused calls
// instead of rescanning the screen and scrollback per new URL, then tries
// again. The id it hands out after that must not collide with a live one.
func TestGrid_InternLink_SweepBackoffThenReclaims(t *testing.T) {
	g := newGrid(64, 64) // 4096 cells: one per registry slot
	for i := range maxLinkEntries {
		id := g.internLink("https://live.example/" + strconv.Itoa(i))
		g.row(i / 64)[i%64].LinkID = id
	}
	if id := g.internLink("https://new.example/0"); id != 0 {
		t.Fatalf("all ids live: got id %d, want 0", id)
	}
	if g.linkSweepWait != linkSweepBackoff {
		t.Fatalf("linkSweepWait = %d, want %d", g.linkSweepWait, linkSweepBackoff)
	}
	// Free one cell's id. During the backoff it stays unclaimed.
	freed := g.row(0)[5].LinkID
	g.row(0)[5].LinkID = 0
	for i := range linkSweepBackoff {
		if id := g.internLink("https://wait.example/" + strconv.Itoa(i)); id != 0 {
			t.Fatalf("call %d in backoff got id %d, want 0", i, id)
		}
	}
	id := g.internLink("https://after.example")
	if id == 0 {
		t.Fatal("after backoff the freed slot was not reclaimed")
	}
	if u := g.LinkURL(freed); u != "" {
		t.Fatalf("freed id %d still maps to %q", freed, u)
	}
	for r := range g.Rows {
		for c, cl := range g.row(r) {
			if cl.LinkID == id {
				t.Fatalf("new id %d collides with live cell (%d,%d)", id, r, c)
			}
		}
	}
	if u := g.LinkURL(g.row(0)[6].LinkID); u != "https://live.example/6" {
		t.Fatalf("live neighbor URL = %q", u)
	}
}
