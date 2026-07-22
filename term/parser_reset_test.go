package term

import (
	"bytes"
	"testing"
)

// REP is what ncurses emits for runs of identical characters whenever the
// terminfo entry advertises `rep`; dropping it silently loses text.
func TestParser_REP_RepeatsLastGraphic(t *testing.T) {
	g, p := newParserGrid(1, 8)
	feed(t, g, p, []byte("a\x1b[3b"))
	if got, want := rowText(g, 0), "aaaa    "; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

func TestParser_REP_DefaultCountIsOne(t *testing.T) {
	g, p := newParserGrid(1, 8)
	feed(t, g, p, []byte("a\x1b[b"))
	if got, want := rowText(g, 0), "aa      "; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

// The pending grapheme must be committed before REP runs, otherwise the
// character being repeated is the one *before* the streaming cluster.
func TestParser_REP_RepeatsClusterNotBaseRune(t *testing.T) {
	g, p := newParserGrid(1, 10)
	const combined = "e\u0301" // 'e' + combining acute: one cluster, two runes
	feed(t, g, p, []byte(combined+"\x1b[2b"))
	for c := range 3 {
		cl := g.At(0, c)
		if cl.clusterID == 0 {
			t.Fatalf("col %d: clusterID = 0, want the multi-rune cluster", c)
		}
		if got := g.clusters[cl.clusterID]; got != combined {
			t.Errorf("col %d = %q, want %q", c, got, combined)
		}
	}
}

func TestParser_REP_NoopBeforeAnyGraphic(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b[4b"))
	if got := rowText(g, 0); got != "    " {
		t.Errorf("row = %q, want blanks", got)
	}
}

func TestParser_SGR_BlinkAndConceal(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b[5;8m"))
	if g.CurAttrs&attrBlink == 0 || g.CurAttrs&attrConceal == 0 {
		t.Fatalf("attrs = %d, want blink|conceal", g.CurAttrs)
	}
	feed(t, g, p, []byte("\x1b[25;28m"))
	if g.CurAttrs != 0 {
		t.Errorf("attrs = %d after 25;28m, want 0", g.CurAttrs)
	}
	// Rapid blink (6) is the same attribute as slow blink (5).
	feed(t, g, p, []byte("\x1b[6m"))
	if g.CurAttrs&attrBlink == 0 {
		t.Error("SGR 6 did not set blink")
	}
}

func TestParser_SGR_ConcealAppliesToCell(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b[8mpw"))
	if g.At(0, 0).Attrs&attrConceal == 0 {
		t.Error("conceal attribute not stored on the cell")
	}
	// The grid keeps the real text — only rendering hides it, so selection
	// copy and search still work like every other terminal.
	if got := rowText(g, 0)[:2]; got != "pw" {
		t.Errorf("cells = %q, want %q", got, "pw")
	}
}

func TestParser_DECSTR_SoftReset(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[1;31m\x1b[?6h\x1b[?25l\x1b[2;3r\x1b[4h"))
	feed(t, g, p, []byte("\x1b[!p"))
	if g.CurAttrs != 0 || g.CurFG != DefaultColor {
		t.Errorf("SGR survived DECSTR: attrs=%d fg=%d", g.CurAttrs, g.CurFG)
	}
	if g.OriginMode || !g.CursorVisible || g.InsertMode {
		t.Errorf("modes survived DECSTR: origin=%v vis=%v insert=%v",
			g.OriginMode, g.CursorVisible, g.InsertMode)
	}
	if g.Top != 0 || g.Bottom != g.Rows-1 {
		t.Errorf("scroll region = %d..%d, want full", g.Top, g.Bottom)
	}
}

// CSI ! p and CSI ? Ps $ p share the final byte; the intermediate decides.
func TestParser_DECSTR_DoesNotShadowDECRQM(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[?25$p"))
	if want := []byte("\x1b[?25;1$y"); !bytes.Equal(reply, want) {
		t.Errorf("DECRQM reply = %q, want %q", reply, want)
	}
	if !g.CursorVisible {
		t.Error("DECRQM query performed a soft reset")
	}
}

func TestParser_RIS_HardReset(t *testing.T) {
	g, p := newParserGrid(3, 6)
	feed(t, g, p, []byte("\x1b[?1000h\x1b[?2004h\x1b[1mhello"))
	feed(t, g, p, []byte("\x1bc"))
	if g.MouseTrack || g.BracketedPaste || g.CurAttrs != 0 {
		t.Errorf("state survived RIS: mouse=%v paste=%v attrs=%d",
			g.MouseTrack, g.BracketedPaste, g.CurAttrs)
	}
	if got := rowText(g, 0); got != "      " {
		t.Errorf("screen = %q, want blanks", got)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("cursor at %d,%d, want home", g.CursorR, g.CursorC)
	}
}

// ESC c must not be confused with the many other ESC-prefixed sequences, and
// text after it renders normally.
func TestParser_RIS_ResumesNormalOutput(t *testing.T) {
	g, p := newParserGrid(2, 6)
	feed(t, g, p, []byte("old\x1bcnew"))
	if got, want := rowText(g, 0), "new   "; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

func TestParser_TitleStack_PushPop(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var titles []string
	p.SetTitleHandler(func(s string) { titles = append(titles, s) })

	feed(t, g, p, []byte("\x1b]2;shell\x07"))    // shell sets its title
	feed(t, g, p, []byte("\x1b[22;0t"))          // vim pushes it
	feed(t, g, p, []byte("\x1b]2;vim: foo\x07")) // vim sets its own
	feed(t, g, p, []byte("\x1b[23;0t"))          // vim exits, pops

	want := []string{"shell", "vim: foo", "shell"}
	if len(titles) != len(want) {
		t.Fatalf("titles = %v, want %v", titles, want)
	}
	for i := range want {
		if titles[i] != want[i] {
			t.Errorf("titles[%d] = %q, want %q", i, titles[i], want[i])
		}
	}
}

func TestParser_TitleStack_PopEmptyIsNoop(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var titles []string
	p.SetTitleHandler(func(s string) { titles = append(titles, s) })
	feed(t, g, p, []byte("\x1b[23;0t"))
	if len(titles) != 0 {
		t.Errorf("pop on empty stack emitted %v", titles)
	}
}

// A pushing-without-popping app must not grow the stack without bound, and
// the first (shell) title must stay recoverable.
func TestParser_TitleStack_CappedKeepsOldest(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var titles []string
	p.SetTitleHandler(func(s string) { titles = append(titles, s) })
	feed(t, g, p, []byte("\x1b]2;first\x07"))
	for range maxTitleStack + 5 {
		feed(t, g, p, []byte("\x1b[22;0t"))
	}
	if len(p.titleStack) != maxTitleStack {
		t.Fatalf("stack depth = %d, want %d", len(p.titleStack), maxTitleStack)
	}
	if p.titleStack[0] != "first" {
		t.Errorf("oldest entry = %q, want %q", p.titleStack[0], "first")
	}
}

func TestParser_RIS_ClearsTitleStack(t *testing.T) {
	g, p := newParserGrid(2, 8)
	feed(t, g, p, []byte("\x1b]2;a\x07\x1b[22;0t\x1bc"))
	if len(p.titleStack) != 0 {
		t.Errorf("title stack survived RIS: %v", p.titleStack)
	}
}

func TestParser_DSR5_ReportsReady(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[5n"))
	if want := []byte("\x1b[0n"); !bytes.Equal(reply, want) {
		t.Errorf("DSR 5 reply = %q, want %q", reply, want)
	}
}

func TestParser_DECXCPR(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[3;5H\x1b[?6n"))
	if want := []byte("\x1b[?3;5R"); !bytes.Equal(reply, want) {
		t.Errorf("DECXCPR reply = %q, want %q", reply, want)
	}
}

// The private reply form must not be sent for the plain CPR query, or a client
// parsing "CSI r ; c R" chokes on the leading '?'.
func TestParser_CPR_StaysNonPrivate(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[2;2H\x1b[6n"))
	if want := []byte("\x1b[2;2R"); !bytes.Equal(reply, want) {
		t.Errorf("CPR reply = %q, want %q", reply, want)
	}
}

func TestParser_ANSIDECRQM(t *testing.T) {
	g, p := newParserGrid(2, 8)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })

	feed(t, g, p, []byte("\x1b[4h\x1b[4$p")) // IRM set, then query
	if want := []byte("\x1b[4;1$y"); !bytes.Equal(reply, want) {
		t.Errorf("IRM set reply = %q, want %q", reply, want)
	}
	reply = nil
	feed(t, g, p, []byte("\x1b[4l\x1b[4$p"))
	if want := []byte("\x1b[4;2$y"); !bytes.Equal(reply, want) {
		t.Errorf("IRM reset reply = %q, want %q", reply, want)
	}
	reply = nil
	feed(t, g, p, []byte("\x1b[20$p")) // LNM — cannot be enabled here
	if want := []byte("\x1b[20;4$y"); !bytes.Equal(reply, want) {
		t.Errorf("LNM reply = %q, want %q", reply, want)
	}
	reply = nil
	feed(t, g, p, []byte("\x1b[99$p"))
	if want := []byte("\x1b[99;0$y"); !bytes.Equal(reply, want) {
		t.Errorf("unknown mode reply = %q, want %q", reply, want)
	}
}
