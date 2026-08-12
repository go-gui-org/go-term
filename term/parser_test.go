package term

import (
	"testing"
)

func feed(t *testing.T, g *grid, p *parser, b []byte) {
	t.Helper()
	g.Mu.Lock()
	defer g.Mu.Unlock()
	p.Feed(b)
}

func newParserGrid(rows, cols int) (*grid, *parser) {
	g := newGrid(rows, cols)
	return g, newParser(g)
}

func TestParser_C0Bytes(t *testing.T) {
	g, p := newParserGrid(2, 5)
	g.Put('x')
	g.Put('y')
	feed(t, g, p, []byte{0x07})
	if g.CursorC != 2 {
		t.Errorf("BEL moved cursor: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x08})
	if g.CursorC != 1 {
		t.Errorf("BS: %d", g.CursorC)
	}
	g.CursorC = 0
	feed(t, g, p, []byte{0x09})
	if g.CursorC != 4 {
		t.Errorf("TAB: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0D})
	if g.CursorC != 0 {
		t.Errorf("CR: %d", g.CursorC)
	}
	feed(t, g, p, []byte{0x0A})
	if g.CursorR != 1 {
		t.Errorf("LF: %d", g.CursorR)
	}

	feed(t, g, p, []byte{0x01, 0x02, 0x05})
	if g.CursorR != 1 || g.CursorC != 0 {
		t.Errorf("other C0 should not move: r=%d c=%d", g.CursorR, g.CursorC)
	}
}

func TestParser_UTF8SplitAcrossFeeds(t *testing.T) {
	cases := []struct {
		name  string
		parts [][]byte
		want  rune
	}{
		{"2-byte split 1+1", [][]byte{{0xC3}, {0xA9}}, 0x00E9},
		{"3-byte split 1+2", [][]byte{{0xE2}, {0x98, 0x83}}, 0x2603},
		{"3-byte split 2+1", [][]byte{{0xE2, 0x98}, {0x83}}, 0x2603},
		{"4-byte split 1+3", [][]byte{{0xF0}, {0x9F, 0x98, 0x80}}, 0x1F600},
		{"4-byte split 2+2", [][]byte{{0xF0, 0x9F}, {0x98, 0x80}}, 0x1F600},
		{"4-byte split 3+1", [][]byte{{0xF0, 0x9F, 0x98}, {0x80}}, 0x1F600},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, p := newParserGrid(1, 5)
			for _, part := range c.parts {
				feed(t, g, p, part)
			}
			if g.At(0, 0).Ch != c.want {
				t.Errorf("got %U, want %U", g.At(0, 0).Ch, c.want)
			}
		})
	}
}

func TestParser_InvalidUTF8YieldsReplacement(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte{0xFF})
	if g.At(0, 0).Ch != 0xFFFD {
		t.Errorf("invalid byte should produce U+FFFD, got %U", g.At(0, 0).Ch)
	}
}

// Regression: invalid UTF-8 carry-over (leader split across chunks) followed
// by an ESC byte in the next chunk must not silently drop the ESC. The leader
// produces U+FFFD at the current cursor position, then the escape sequence is
// processed normally.
func TestParser_UTF8CarryOverFollowedByEscape(t *testing.T) {
	// 0xE0 is a 3-byte leader; next chunk starts with ESC [ A (cursor up).
	// Cursor starts at row 2. Put(FFFD) places it there, then cursor-up moves to row 1.
	g, p := newParserGrid(3, 5)
	g.MoveCursor(2, 0)
	feed(t, g, p, []byte{0xE0})           // partial UTF-8 — stored as carry-over
	feed(t, g, p, []byte{0x1B, '[', 'A'}) // ESC [ A = cursor up 1
	if g.At(2, 0).Ch != 0xFFFD {
		t.Errorf("invalid leader should produce U+FFFD at (2,0), got %U", g.At(2, 0).Ch)
	}
	if g.CursorR != 1 {
		t.Errorf("ESC [ A after carry-over: cursor row = %d, want 1", g.CursorR)
	}
}

func TestParser_ESCNonBracketIgnored(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b("))
	if g.CursorC != 0 {
		t.Errorf("ESC ( should be swallowed: cursor=%d", g.CursorC)
	}
	if p.state != stEscInter {
		t.Errorf("state should await ESC intermediate final: %d", p.state)
	}
}

func TestParser_ESCCharsetDesignationSwallowed(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b(BX"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Fatalf("ESC(B leaked into grid: got %q want %q", got, 'X')
	}
	if g.CursorC != 1 {
		t.Fatalf("cursor after ESC(BX = %d, want 1", g.CursorC)
	}
}

func TestParser_RestoreWithoutSaveResets(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(2, 3)
	g.CurFG = paletteColor(5)
	g.CurAttrs = attrUnderline
	feed(t, g, p, []byte("\x1b8"))
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("home: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurFG != defaultColor || g.CurAttrs != 0 {
		t.Errorf("SGR not reset: fg=%#x attrs=%d", g.CurFG, g.CurAttrs)
	}
}

func TestParser_IND_RI_NEL(t *testing.T) {
	g, p := newParserGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[2;4r"))
	g.CursorR = 3
	feed(t, g, p, []byte("\x1bD"))
	if g.At(1, 0).Ch != 'C' || g.At(2, 0).Ch != 'D' || g.At(3, 0).Ch != ' ' {
		t.Errorf("IND scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR = 1
	feed(t, g, p, []byte("\x1bM"))
	if g.At(1, 0).Ch != ' ' || g.At(2, 0).Ch != 'C' || g.At(3, 0).Ch != 'D' {
		t.Errorf("RI scroll wrong: %q %q %q",
			g.At(1, 0).Ch, g.At(2, 0).Ch, g.At(3, 0).Ch)
	}
	g.CursorR, g.CursorC = 1, 1
	feed(t, g, p, []byte("\x1bE"))
	if g.CursorC != 0 || g.CursorR != 2 {
		t.Errorf("NEL cursor: %d,%d", g.CursorR, g.CursorC)
	}
}

func TestParser_MouseModes_Toggle(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[?1000;1006h"))
	if !g.MouseTrack || !g.MouseSGR {
		t.Errorf("?1000;1006h: track=%v sgr=%v", g.MouseTrack, g.MouseSGR)
	}
	feed(t, g, p, []byte("\x1b[?1002h"))
	if !g.MouseTrackBtn {
		t.Error("?1002h not set")
	}
	feed(t, g, p, []byte("\x1b[?1003h"))
	if !g.MouseTrackAny {
		t.Error("?1003h not set")
	}
	feed(t, g, p, []byte("\x1b[?1000;1002;1003;1006l"))
	if g.MouseTrack || g.MouseTrackBtn || g.MouseTrackAny || g.MouseSGR {
		t.Errorf("reset failed: track=%v btn=%v any=%v sgr=%v",
			g.MouseTrack, g.MouseTrackBtn, g.MouseTrackAny, g.MouseSGR)
	}
}

func TestParser_MouseReporting_Aggregate(t *testing.T) {
	g, _ := newParserGrid(1, 5)
	if g.MouseReporting() {
		t.Error("default should be off")
	}
	g.MouseTrack = true
	if !g.MouseReporting() {
		t.Error("MouseTrack should imply reporting")
	}
	g.MouseTrack = false
	g.MouseTrackAny = true
	if !g.MouseReporting() {
		t.Error("MouseTrackAny should imply reporting")
	}
}

func TestParser_BEL_IncrementsBellCount(t *testing.T) {
	g, p := newParserGrid(5, 20)
	feed(t, g, p, []byte{0x07})
	if g.BellCount != 1 {
		t.Fatalf("BellCount after BEL = %d, want 1", g.BellCount)
	}
	feed(t, g, p, []byte{0x07, 0x07})
	if g.BellCount != 3 {
		t.Fatalf("BellCount after 3 BELs = %d, want 3", g.BellCount)
	}
}

func TestParser_BEL_DoesNotMoveCursor(t *testing.T) {
	g, p := newParserGrid(5, 20)
	g.Put('A')
	feed(t, g, p, []byte{0x07})
	if g.CursorC != 1 {
		t.Errorf("BEL moved cursor: col = %d, want 1", g.CursorC)
	}
}

func TestParser_BEL_InOSCTerminatesPayload(t *testing.T) {

	g, p := newParserGrid(5, 40)
	var title string
	p.SetTitleHandler(func(s string) { title = s })
	feed(t, g, p, []byte("\x1b]0;hello\x07"))
	if title != "hello" {
		t.Errorf("OSC title = %q, want %q", title, "hello")
	}

	if g.BellCount != 0 {
		t.Errorf("OSC-terminator BEL incremented BellCount = %d, want 0", g.BellCount)
	}
}

func TestParser_HTS_SetTabStop(t *testing.T) {
	g, p := newParserGrid(1, 80)
	g.Mu.Lock()
	g.CursorC = 12
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))
	g.Mu.Lock()
	got := g.TabStops[12]
	g.Mu.Unlock()
	if !got {
		t.Error("ESC H: tab stop not set at col 12")
	}
}

func TestParser_TBC_ClearAtCursor(t *testing.T) {
	g, p := newParserGrid(1, 80)

	g.Mu.Lock()
	g.CursorC = 8
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1b[g"))
	g.Mu.Lock()
	got := g.TabStops[8]
	g.Mu.Unlock()
	if got {
		t.Error("CSI g: stop at col 8 should be cleared")
	}
}

func TestParser_TBC_ClearAll(t *testing.T) {
	g, p := newParserGrid(1, 80)
	feed(t, g, p, []byte("\x1b[3g"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	for c := range MaxGridDim {
		if g.TabStops[c] {
			t.Errorf("CSI 3g: stop still set at col %d", c)
		}
	}
}

func TestParser_HTS_TBC_RoundTrip(t *testing.T) {
	g, p := newParserGrid(1, 80)

	feed(t, g, p, []byte("\x1b[3g"))
	g.Mu.Lock()
	g.CursorC = 5
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))
	g.Mu.Lock()
	g.CursorC = 10
	g.Mu.Unlock()
	feed(t, g, p, []byte("\x1bH"))

	g.Mu.Lock()
	defer g.Mu.Unlock()

	for c := range 20 {
		want := c == 5 || c == 10
		if g.TabStops[c] != want {
			t.Errorf("col %d: TabStops=%v, want %v", c, g.TabStops[c], want)
		}
	}

	g.CursorC = 0
	g.Tab()
	if g.CursorC != 5 {
		t.Errorf("Tab from 0: got %d, want 5", g.CursorC)
	}
	g.Tab()
	if g.CursorC != 10 {
		t.Errorf("Tab from 5: got %d, want 10", g.CursorC)
	}
	g.Tab()
	if g.CursorC != g.Cols-1 {
		t.Errorf("Tab from 10 (no more stops): got %d, want %d", g.CursorC, g.Cols-1)
	}
}

func TestParser_KittyKeyPush(t *testing.T) {
	g, p := newParserGrid(4, 8)

	feed(t, g, p, []byte("\x1b[>1u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after CSI>1u: flags=%d, want 1", g.KittyKeyFlags)
	}

	feed(t, g, p, []byte("\x1b[>2u"))
	if g.KittyKeyFlags != 3 {
		t.Fatalf("after CSI>2u: flags=%d, want 3", g.KittyKeyFlags)
	}
	if len(g.kittyFlagStack) != 2 {
		t.Fatalf("stack depth=%d, want 2", len(g.kittyFlagStack))
	}
}

func TestParser_KittyKeyPop(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[>2u"))
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after pop: flags=%d, want 1", g.KittyKeyFlags)
	}
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 0 {
		t.Fatalf("after second pop: flags=%d, want 0", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeyPopN(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[>2u"))
	feed(t, g, p, []byte("\x1b[>4u"))
	feed(t, g, p, []byte("\x1b[<2u"))
	if g.KittyKeyFlags != 1 {
		t.Fatalf("after pop 2: flags=%d, want 1", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeyPopEmpty(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.KittyKeyFlags = 7
	feed(t, g, p, []byte("\x1b[<1u"))
	if g.KittyKeyFlags != 0 {
		t.Fatalf("pop empty: flags=%d, want 0", g.KittyKeyFlags)
	}
}

func TestParser_KittyKeySet(t *testing.T) {
	g, p := newParserGrid(4, 8)
	feed(t, g, p, []byte("\x1b[>1u"))
	feed(t, g, p, []byte("\x1b[=5u"))
	if g.KittyKeyFlags != 5 {
		t.Fatalf("after CSI=5u: flags=%d, want 5", g.KittyKeyFlags)
	}

	if len(g.kittyFlagStack) != 1 {
		t.Fatalf("stack depth=%d, want 1 (set does not push)", len(g.kittyFlagStack))
	}
}

func TestParser_KittyKeyQuery(t *testing.T) {
	g, p := newParserGrid(4, 8)
	g.KittyKeyFlags = 3
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b[?u"))
	want := "\x1b[?3u"
	if string(got) != want {
		t.Fatalf("query reply: got %q, want %q", got, want)
	}
}

func TestParser_KittyKeyQueryZero(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var got []byte
	p.SetReplyHandler(func(b []byte) { got = append(got, b...) })
	feed(t, g, p, []byte("\x1b[?u"))
	want := "\x1b[?0u"
	if string(got) != want {
		t.Fatalf("query zero: got %q, want %q", got, want)
	}
}

// ---- Benchmarks ----

func BenchmarkParserFeed_PlainText(b *testing.B) {
	g := newGrid(24, 80)
	p := newParser(g)
	input := make([]byte, 4096)
	for i := range input {
		input[i] = byte('a' + i%26)
	}
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		g.Mu.Lock()
		p.Feed(input)
		g.Mu.Unlock()
	}
}

func BenchmarkParserFeed_SGR(b *testing.B) {
	g := newGrid(24, 80)
	p := newParser(g)
	// interleave SGR color sequences with text
	input := make([]byte, 0, 4096)
	for len(input) < 4000 {
		input = append(input, "\x1b[31;1mhello\x1b[0m "...)
	}
	input = input[:4096]
	b.SetBytes(int64(len(input)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		g.Mu.Lock()
		p.Feed(input)
		g.Mu.Unlock()
	}
}

func TestCurrentSGRString_AllPaths(t *testing.T) {
	tests := []struct {
		name string
		fg   uint32
		bg   uint32
		attr uint16
		want string
	}{
		{"default", defaultColor, defaultColor, 0, "0m"},
		{"bold", defaultColor, defaultColor, attrBold, "1m"},
		{"underline", defaultColor, defaultColor, attrUnderline, "4m"},
		{"inverse", defaultColor, defaultColor, attrInverse, "7m"},
		{"bold+underline", defaultColor, defaultColor, attrBold | attrUnderline, "1;4m"},
		{"fg_pal0", paletteColor(0), defaultColor, 0, "30m"},
		{"fg_pal7", paletteColor(7), defaultColor, 0, "37m"},
		{"fg_pal8", paletteColor(8), defaultColor, 0, "90m"},
		{"fg_pal15", paletteColor(15), defaultColor, 0, "97m"},
		{"fg_256", paletteColor(200), defaultColor, 0, "38;5;200m"},
		{"fg_rgb", rgbColor(10, 20, 30), defaultColor, 0, "38;2;10;20;30m"},
		{"bg_pal0", defaultColor, paletteColor(0), 0, "40m"},
		{"bg_pal7", defaultColor, paletteColor(7), 0, "47m"},
		{"bg_pal8", defaultColor, paletteColor(8), 0, "100m"},
		{"bg_pal15", defaultColor, paletteColor(15), 0, "107m"},
		{"bg_256", defaultColor, paletteColor(200), 0, "48;5;200m"},
		{"bg_rgb", defaultColor, rgbColor(10, 20, 30), 0, "48;2;10;20;30m"},
		{"fg_rgb_bold", rgbColor(10, 20, 30), defaultColor, attrBold, "1;38;2;10;20;30m"},
	}
	for _, tt := range tests {
		g := newGrid(2, 10)
		g.CurFG = tt.fg
		g.CurBG = tt.bg
		g.CurAttrs = tt.attr
		p := newParser(g)
		got := p.currentSGRString()
		if got != tt.want {
			t.Errorf("currentSGRString %s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestParser_ESCRestartsSequenceInProgress: ESC is not an intermediate byte —
// it abandons whatever sequence is being collected and opens a new one.
// Absorbing it instead splices the next sequence's bytes onto the abandoned
// one, so a truncated write (a child killed mid-sequence, a `printf` cut off)
// silently applies parameters nobody sent.
func TestParser_ESCRestartsSequenceInProgress(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			// Absorbed, this parsed as CSI 1;2 m — two SGR params from one
			// sequence — and then printed nothing. Restarted, the abandoned
			// `CSI 1` is dropped and `CSI 2 m` stands on its own.
			name:  "esc_inside_csi_params",
			input: "\x1b[1\x1b[2mAB",
			want:  "AB   ",
		},
		{
			// ESC inside a charset-designator sequence (stEscInter).
			name:  "esc_inside_charset_designator",
			input: "\x1b(\x1b[2mAB",
			want:  "AB   ",
		},
		{
			// ESC ESC: the second restarts, so `c` is still read as a final
			// (RIS) rather than printed as a literal.
			name:  "double_esc_keeps_next_byte_as_final",
			input: "AB\x1b\x1bc",
			want:  "     ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(2, 5)
			feed(t, g, p, []byte(tt.input))
			if got := rowText(g, 0); got != tt.want {
				t.Errorf("row = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestParser_GroundStateFormFeedAndVerticalTab: VT and FF are line feeds on a
// terminal. They were previously dropped in ground state, so `printf '\f'`
// (and anything emitting FF as a newline) left the cursor where it was.
func TestParser_GroundStateFormFeedAndVerticalTab(t *testing.T) {
	for _, c := range []byte{0x0B, 0x0C} {
		g, p := newParserGrid(4, 5)
		g.Put('x')
		feed(t, g, p, []byte{c})
		if g.CursorR != 1 {
			t.Errorf("control %#x: row = %d, want 1", c, g.CursorR)
		}
		// A line feed does not carry the cursor to column zero.
		if g.CursorC != 1 {
			t.Errorf("control %#x: col = %d, want 1", c, g.CursorC)
		}
	}
}

// TestParser_PendingWrapIsCollapsedForCursorOps: deferred wrap is stored as
// CursorC == Cols, a column that does not exist. Every cursor-relative
// operation has to read it as "on the last column" or it computes from the
// phantom one and the whole row drifts.
func TestParser_PendingWrapIsCollapsedForCursorOps(t *testing.T) {
	tests := []struct {
		name  string
		input string // fed after the row is filled into pending wrap
		want  int    // resulting CursorC
	}{
		{"cursor_back", "\x1b[1D", 3},
		{"cursor_forward", "\x1b[1C", 4},
		{"tab", "\t", 4},
		{"backspace", "\x08", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(1, 5)
			feed(t, g, p, []byte("abcde")) // fills the row; wrap now pending
			if g.CursorC != g.Cols {
				t.Fatalf("setup: CursorC = %d, want pending wrap at %d",
					g.CursorC, g.Cols)
			}
			feed(t, g, p, []byte(tt.input))
			if g.CursorC != tt.want {
				t.Errorf("CursorC = %d, want %d", g.CursorC, tt.want)
			}
		})
	}
}

// CPR must report the settled column too: an application that asks "where am
// I?" after filling a row would otherwise be told column Cols+1, which is off
// the screen it just measured.
func TestParser_CPRReportsSettledColumn(t *testing.T) {
	for _, seq := range []string{"\x1b[6n", "\x1b[?6n"} {
		g, p := newParserGrid(1, 5)
		var reply []byte
		p.onReply = func(b []byte) { reply = append(reply, b...) }
		feed(t, g, p, []byte("abcde"))
		feed(t, g, p, []byte(seq))
		if want := "5R"; len(reply) < 2 || string(reply[len(reply)-2:]) != want {
			t.Errorf("%q reply = %q, want it to end in %q", seq, reply, want)
		}
	}
}

// TestParser_DECCOLM_GatedAndReported covers the ?40 gate, the pin it enables,
// the release when permission is revoked, and what DECRQM reports throughout.
func TestParser_DECCOLM_GatedAndReported(t *testing.T) {
	g, p := newParserGrid(4, 80)

	// Ungated: inert.
	feed(t, g, p, []byte("\x1b[?3h"))
	if g.ColumnMode != 0 {
		t.Fatalf("DECCOLM applied without ?40: mode = %d", g.ColumnMode)
	}
	if got := p.decModeState(3); got != boolState(false) {
		t.Errorf("DECRQM ?3 = %d, want reset", got)
	}

	feed(t, g, p, []byte("\x1b[?40h"))
	if !g.AllowColumnMode || p.decModeState(40) != boolState(true) {
		t.Fatal("?40h did not grant column-switch permission")
	}

	feed(t, g, p, []byte("\x1b[?3h"))
	if g.ColumnMode != 132 || g.Cols != 132 {
		t.Fatalf("mode = %d, cols = %d, want 132/132", g.ColumnMode, g.Cols)
	}
	if got := p.decModeState(3); got != boolState(true) {
		t.Errorf("DECRQM ?3 at 132 = %d, want set", got)
	}

	feed(t, g, p, []byte("\x1b[?3l"))
	if g.ColumnMode != 80 || g.Cols != 80 {
		t.Fatalf("mode = %d, cols = %d, want 80/80", g.ColumnMode, g.Cols)
	}
	// 80 is DECCOLM's reset state, so it reports reset even though pinned.
	if got := p.decModeState(3); got != boolState(false) {
		t.Errorf("DECRQM ?3 at 80 = %d, want reset", got)
	}

	// Revoking permission releases the pin, so a crashed app cannot strand
	// the pane at a fixed width.
	feed(t, g, p, []byte("\x1b[?40l"))
	if g.AllowColumnMode || g.ColumnMode != 0 {
		t.Fatalf("?40l left allow = %v, mode = %d", g.AllowColumnMode, g.ColumnMode)
	}
}

// DECRQM must report DECSCNM, which is how an application discovers it is
// running on a light background before choosing its colors.
func TestParser_DECSCNM_Reported(t *testing.T) {
	g, p := newParserGrid(2, 5)
	if got := p.decModeState(5); got != boolState(false) {
		t.Errorf("DECRQM ?5 = %d, want reset", got)
	}
	feed(t, g, p, []byte("\x1b[?5h"))
	if got := p.decModeState(5); got != boolState(true) {
		t.Errorf("DECRQM ?5 after ?5h = %d, want set", got)
	}
}
