package term

import (
	"bytes"
	"strconv"
	"testing"
)

func TestParser_CSIParamCountCappedAt32(t *testing.T) {
	g, p := newParserGrid(1, 5)
	input := []byte("\x1b[")
	for range 100 {
		input = append(input, '1', ';')
	}
	input = append(input, '0', 'm')
	feed(t, g, p, input)
	if len(p.params) > maxCSIParams {
		t.Errorf("params grew past cap: %d", len(p.params))
	}
}

func TestParser_CSIParamValueCapped(t *testing.T) {
	g, p := newParserGrid(1, 5)

	input := append([]byte("\x1b["), bytes.Repeat([]byte("9"), 30)...)
	input = append(input, 'm')
	feed(t, g, p, input)
	for i, v := range p.params {
		if v > maxCSIParamValue {
			t.Errorf("param[%d]=%d exceeds cap %d", i, v, maxCSIParamValue)
		}
	}
}

func TestParser_SGR_Reset(t *testing.T) {
	g, p := newParserGrid(1, 1)
	g.CurFG = 5
	g.CurBG = 6
	g.CurAttrs = attrBold | attrUnderline
	feed(t, g, p, []byte("\x1b[m"))
	if g.CurFG != DefaultColor || g.CurBG != DefaultColor || g.CurAttrs != 0 {
		t.Errorf("SGR reset failed: fg=%d bg=%d attrs=%d",
			g.CurFG, g.CurBG, g.CurAttrs)
	}
}

func TestParser_SGR_BoldUnderlineInverse(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[1;4;7m"))
	if g.CurAttrs != attrBold|attrUnderline|attrInverse {
		t.Errorf("attrs: %d", g.CurAttrs)
	}
	feed(t, g, p, []byte("\x1b[22;24;27m"))
	if g.CurAttrs != 0 {
		t.Errorf("clear: %d", g.CurAttrs)
	}
}

func TestParser_SGR_FG_BG(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[31;42m"))
	if g.CurFG != 1 || g.CurBG != 2 {
		t.Errorf("fg/bg: %d %d", g.CurFG, g.CurBG)
	}
	feed(t, g, p, []byte("\x1b[39;49m"))
	if g.CurFG != DefaultColor || g.CurBG != DefaultColor {
		t.Errorf("default: %d %d", g.CurFG, g.CurBG)
	}
}

func TestParser_SGR_Bright(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[91;102m"))
	if g.CurFG != 9 || g.CurBG != 10 {
		t.Errorf("bright: %d %d", g.CurFG, g.CurBG)
	}
}

func TestParser_SGR38_5Swallowed(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;200;31m"))
	if g.CurFG != 1 {
		t.Errorf("trailing SGR after 38;5 not applied: fg=%d", g.CurFG)
	}
}

func TestParser_SGR38_2Swallowed(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;2;100;200;50;31m"))
	if g.CurFG != 1 {
		t.Errorf("trailing SGR after 38;2 not applied: fg=%d", g.CurFG)
	}
}

func TestParser_SGR256_AppliesPaletteIndex(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;200m"))
	if got, want := g.CurFG, paletteColor(200); got != want {
		t.Errorf("38;5;200 fg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[48;5;17m"))
	if got, want := g.CurBG, paletteColor(17); got != want {
		t.Errorf("48;5;17 bg: got %#x want %#x", got, want)
	}
}

func TestParser_SGR256_OutOfRangeClamps(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;5;9999m"))
	if got, want := g.CurFG, paletteColor(255); got != want {
		t.Errorf("clamped 256-color: got %#x want %#x", got, want)
	}
}

func TestParser_SGRTruecolor_AppliesRGB(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38;2;255;100;0m"))
	if got, want := g.CurFG, rgbColor(255, 100, 0); got != want {
		t.Errorf("38;2 fg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[48;2;10;20;30m"))
	if got, want := g.CurBG, rgbColor(10, 20, 30); got != want {
		t.Errorf("48;2 bg: got %#x want %#x", got, want)
	}
}

func TestParser_SGRTruecolor_ChannelsClamp(t *testing.T) {
	g, p := newParserGrid(1, 1)

	feed(t, g, p, []byte("\x1b[38;2;300;500;128m"))
	if got, want := g.CurFG, rgbColor(255, 255, 128); got != want {
		t.Errorf("clamped channels: got %#x want %#x", got, want)
	}
}

func TestParser_SGR38_NoSelectorIsNoop(t *testing.T) {

	g, p := newParserGrid(1, 1)
	g.CurFG = paletteColor(7)
	feed(t, g, p, []byte("\x1b[38m"))
	if got, want := g.CurFG, paletteColor(7); got != want {
		t.Errorf("bare 38 should not change FG: got %#x want %#x", got, want)
	}
}

func TestParser_SGR_UnknownExtendedSelectorConsumesRest(t *testing.T) {
	g, p := newParserGrid(1, 1)
	g.CurFG = paletteColor(7)

	feed(t, g, p, []byte("\x1b[38;9;1;2;3;4m"))
	if got, want := g.CurFG, paletteColor(7); got != want {
		t.Errorf("unknown selector should not change FG: got %#x want %#x", got, want)
	}
}

func TestParser_SGR38_2Truncated(t *testing.T) {
	g, p := newParserGrid(1, 1)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on truncated 38;2: %v", r)
		}
	}()
	feed(t, g, p, []byte("\x1b[38;2;1m"))
}

func TestParser_CSI_CursorMoves(t *testing.T) {
	g, p := newParserGrid(5, 5)
	feed(t, g, p, []byte("\x1b[3;4H"))
	if g.CursorR != 2 || g.CursorC != 3 {
		t.Errorf("H: %d %d", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[A"))
	if g.CursorR != 1 {
		t.Errorf("A: %d", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[2B"))
	if g.CursorR != 3 {
		t.Errorf("B: %d", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[2C"))
	if g.CursorC != 4 {
		t.Errorf("C: %d", g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[2D"))
	if g.CursorC != 2 {
		t.Errorf("D: %d", g.CursorC)
	}
}

func TestParser_CSI_EraseInDisplayLine(t *testing.T) {
	g, p := newParserGrid(2, 3)
	g.At(0, 0).Ch = 'a'
	g.At(0, 1).Ch = 'b'
	g.At(0, 2).Ch = 'c'
	g.MoveCursor(0, 1)
	feed(t, g, p, []byte("\x1b[K"))
	if g.At(0, 0).Ch != 'a' || g.At(0, 1).Ch != ' ' || g.At(0, 2).Ch != ' ' {
		t.Errorf("EL 0: %v %v %v",
			g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch)
	}
	g.At(1, 0).Ch = 'x'
	g.MoveCursor(0, 0)
	feed(t, g, p, []byte("\x1b[2J"))
	for _, c := range g.Cells {
		if c.Ch != ' ' {
			t.Fatalf("ED 2 left content: %v", c.Ch)
		}
	}
}

func TestParser_CSI_UnknownDropped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.Put('z')
	// CSI V has no assigned meaning; CSI Z (CBT) used to stand in here, but
	// it is implemented now and no longer exercises the drop path.
	feed(t, g, p, []byte("\x1b[V"))
	if g.At(0, 0).Ch != 'z' || g.CursorC != 1 {
		t.Errorf("unknown CSI mutated state: %v cursor=%d",
			g.At(0, 0).Ch, g.CursorC)
	}
}

func TestParser_CursorSaveRestore_ESC78(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(2, 4)
	feed(t, g, p, []byte("\x1b[31m"))
	feed(t, g, p, []byte("\x1b7"))
	g.MoveCursor(0, 0)
	feed(t, g, p, []byte("\x1b[32m"))
	feed(t, g, p, []byte("\x1b8"))
	if g.CursorR != 2 || g.CursorC != 4 {
		t.Errorf("cursor not restored: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("FG not restored: %#x", g.CurFG)
	}
}

func TestParser_CursorSaveRestore_CSIsu(t *testing.T) {
	g, p := newParserGrid(5, 10)
	g.MoveCursor(3, 7)
	g.CurAttrs = attrBold
	feed(t, g, p, []byte("\x1b[s"))
	g.MoveCursor(0, 0)
	g.CurAttrs = 0
	feed(t, g, p, []byte("\x1b[u"))
	if g.CursorR != 3 || g.CursorC != 7 {
		t.Errorf("CSI u: r=%d c=%d", g.CursorR, g.CursorC)
	}
	if g.CurAttrs != attrBold {
		t.Errorf("CSI u attrs: %d", g.CurAttrs)
	}
}

func TestParser_DECCharset_SOAndSI(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b)0\x0ex\x0fl"))
	if got := g.At(0, 0).Ch; got != '│' {
		t.Fatalf("SO x = %q, want %q", got, '│')
	}
	if got := g.At(0, 1).Ch; got != 'l' {
		t.Fatalf("SI l = %q, want %q", got, 'l')
	}
}

func TestParser_DECCharset_SaveRestore(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b)0\x0e\x1b7"))
	feed(t, g, p, []byte("\x0f"))
	feed(t, g, p, []byte("\x1b8x"))
	if got := g.At(0, 0).Ch; got != '│' {
		t.Fatalf("restored charset x = %q, want %q", got, '│')
	}
}

func TestParser_DECCharset_G0Designation_Translates(t *testing.T) {
	g, p := newParserGrid(1, 4)

	feed(t, g, p, []byte("\x1b(0x"))
	if got := g.At(0, 0).Ch; got != '│' {
		t.Fatalf("ESC(0 x = %q, want '│'", got)
	}
}

func TestParser_DECCharset_RedesignateG1ToASCII(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b)0\x0ex"))
	if got := g.At(0, 0).Ch; got != '│' {
		t.Fatalf("DEC G1 x = %q, want '│'", got)
	}
	feed(t, g, p, []byte("\x1b)Bx"))
	if got := g.At(0, 1).Ch; got != 'x' {
		t.Fatalf("ASCII G1 x = %q, want 'x'", got)
	}
}

func TestParser_DEC25_CursorVisibility(t *testing.T) {
	g, p := newParserGrid(2, 5)
	if !g.CursorVisible {
		t.Fatal("default CursorVisible should be true")
	}
	feed(t, g, p, []byte("\x1b[?25l"))
	if g.CursorVisible {
		t.Errorf("?25l: still visible")
	}
	feed(t, g, p, []byte("\x1b[?25h"))
	if !g.CursorVisible {
		t.Errorf("?25h: still hidden")
	}
}

func TestParser_DEC2004_BracketedPaste(t *testing.T) {
	g, p := newParserGrid(2, 5)
	if g.BracketedPaste {
		t.Fatal("default should be off")
	}
	feed(t, g, p, []byte("\x1b[?2004h"))
	if !g.BracketedPaste {
		t.Errorf("?2004h: still off")
	}
	feed(t, g, p, []byte("\x1b[?2004l"))
	if g.BracketedPaste {
		t.Errorf("?2004l: still on")
	}
}

func TestParser_DEC7_AutoWrap(t *testing.T) {
	g, p := newParserGrid(1, 3)
	if !g.AutoWrap {
		t.Fatal("default autowrap should be on")
	}
	feed(t, g, p, []byte("\x1b[?7l"))
	if g.AutoWrap {
		t.Fatal("?7l should disable autowrap")
	}
	feed(t, g, p, []byte("abcd"))
	if got := string([]rune{g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch}); got != "abd" {
		t.Fatalf("nowrap overwrite = %q, want %q", got, "abd")
	}
	if g.CursorC != 2 {
		t.Fatalf("nowrap cursor = %d, want 2", g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[?7h"))
	if !g.AutoWrap {
		t.Fatal("?7h should enable autowrap")
	}
}

func TestParser_DEC6_OriginMode(t *testing.T) {
	g, p := newParserGrid(6, 5)
	feed(t, g, p, []byte("\x1b[2;5r"))
	feed(t, g, p, []byte("\x1b[?6h"))
	if !g.OriginMode {
		t.Fatal("?6h should enable origin mode")
	}
	if g.CursorR != 1 || g.CursorC != 0 {
		t.Fatalf("origin home = %d,%d, want 1,0", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[2;3H"))
	if g.CursorR != 2 || g.CursorC != 2 {
		t.Fatalf("origin CUP = %d,%d, want 2,2", g.CursorR, g.CursorC)
	}
	feed(t, g, p, []byte("\x1b[99B"))
	if g.CursorR != 4 {
		t.Fatalf("origin CUD clamp = %d, want 4", g.CursorR)
	}
	feed(t, g, p, []byte("\x1b[?6l"))
	if g.OriginMode {
		t.Fatal("?6l should disable origin mode")
	}
}

func TestParser_CSI4_InsertMode(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("abcd"))
	g.CursorR, g.CursorC = 0, 1
	feed(t, g, p, []byte("\x1b[4h"))
	if !g.InsertMode {
		t.Fatal("CSI 4 h should enable insert mode")
	}
	feed(t, g, p, []byte("X"))
	got := string([]rune{g.At(0, 0).Ch, g.At(0, 1).Ch, g.At(0, 2).Ch, g.At(0, 3).Ch})
	if got != "aXbc" {
		t.Fatalf("IRM row = %q, want %q", got, "aXbc")
	}
	feed(t, g, p, []byte("\x1b[4l"))
	if g.InsertMode {
		t.Fatal("CSI 4 l should disable insert mode")
	}
}

func TestParser_DECMode_FocusSyncCursorKeypad(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[?1004;2026;1h\x1b="))
	if !g.FocusReporting || !g.SyncOutput || !g.AppCursorKeys || !g.AppKeypad {
		t.Fatalf("mode set failed: focus=%v sync=%v ckm=%v keypad=%v",
			g.FocusReporting, g.SyncOutput, g.AppCursorKeys, g.AppKeypad)
	}
	// DECSET 2026 is itself "begin synchronized update" in the CSI form —
	// there is no separate capability handshake.
	if !g.SyncActive {
		t.Fatal("DECSET 2026 should begin a sync block")
	}
	feed(t, g, p, []byte("\x1bP=1s\x1b\\"))
	if !g.SyncActive {
		t.Fatal("sync begin not set")
	}
	feed(t, g, p, []byte("\x1bP=2s\x1b\\"))
	if g.SyncActive {
		t.Fatal("sync end not cleared")
	}
	feed(t, g, p, []byte("\x1b[?1004;2026;1l\x1b>"))
	if g.FocusReporting || g.SyncOutput || g.SyncActive || g.AppCursorKeys || g.AppKeypad {
		t.Fatalf("mode reset failed: focus=%v sync=%v active=%v ckm=%v keypad=%v",
			g.FocusReporting, g.SyncOutput, g.SyncActive, g.AppCursorKeys, g.AppKeypad)
	}
}

func TestParser_DECPrivateResetBetweenSequences(t *testing.T) {

	g, p := newParserGrid(2, 5)
	feed(t, g, p, []byte("\x1b[?25l"))
	feed(t, g, p, []byte("\x1b[31m"))
	if g.CursorVisible {
		t.Fatal("?25l should still be in effect")
	}
	if g.CurFG != paletteColor(1) {
		t.Errorf("SGR after DEC mode: fg=%#x", g.CurFG)
	}
}

func TestParser_NonDECPrivateLeaderDoesNotFallThroughToSGR(t *testing.T) {

	g, p := newParserGrid(2, 5)
	feed(t, g, p, []byte("\x1b[>4;1m"))
	if g.CurAttrs != 0 {
		t.Fatalf("CSI > 4;1m changed attrs: %#x", g.CurAttrs)
	}
	feed(t, g, p, []byte("\x1b[K"))
	for c := range g.Cols {
		cell := g.At(0, c)
		if cell.Attrs != 0 {
			t.Fatalf("erased cell %d kept attrs %#x", c, cell.Attrs)
		}
	}
}

func TestParser_DECSTBM_SetAndReset(t *testing.T) {
	g, p := newParserGrid(10, 4)
	feed(t, g, p, []byte("\x1b[3;7r"))
	if g.Top != 2 || g.Bottom != 6 {
		t.Errorf("DECSTBM 3;7 → %d..%d, want 2..6", g.Top, g.Bottom)
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("DECSTBM did not home cursor")
	}

	feed(t, g, p, []byte("\x1b[r"))
	if g.Top != 0 || g.Bottom != 9 {
		t.Errorf("bare DECSTBM reset failed: %d..%d", g.Top, g.Bottom)
	}
}

func TestParser_InsertDeleteLines(t *testing.T) {
	g, p := newParserGrid(5, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D', 'E'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[2;4r"))
	g.CursorR = 2
	feed(t, g, p, []byte("\x1b[L"))

	want := []rune{'A', 'B', ' ', 'C', 'E'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after IL row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}

	feed(t, g, p, []byte("\x1b[2M"))
	want = []rune{'A', 'B', ' ', ' ', 'E'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after DL row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
}

func TestParser_InsertDeleteChars(t *testing.T) {
	g, p := newParserGrid(1, 6)
	for c := range g.Cols {
		g.At(0, c).Ch = rune('a' + c)
	}
	g.CursorC = 2
	feed(t, g, p, []byte("\x1b[2@"))
	want := []rune{'a', 'b', ' ', ' ', 'c', 'd'}
	for i, w := range want {
		if g.At(0, i).Ch != w {
			t.Errorf("after ICH col %d = %q, want %q", i, g.At(0, i).Ch, w)
		}
	}
	g.CursorC = 0
	feed(t, g, p, []byte("\x1b[3P"))

	want = []rune{' ', 'c', 'd', ' ', ' ', ' '}
	for i, w := range want {
		if g.At(0, i).Ch != w {
			t.Errorf("after DCH col %d = %q, want %q", i, g.At(0, i).Ch, w)
		}
	}
}

func TestParser_SU_SD(t *testing.T) {
	g, p := newParserGrid(4, 2)
	for i, ch := range []rune{'A', 'B', 'C', 'D'} {
		for c := range g.Cols {
			g.At(i, c).Ch = ch
		}
	}
	feed(t, g, p, []byte("\x1b[S"))
	want := []rune{'B', 'C', 'D', ' '}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after SU row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
	feed(t, g, p, []byte("\x1b[T"))
	want = []rune{' ', 'B', 'C', 'D'}
	for i, w := range want {
		if g.At(i, 0).Ch != w {
			t.Errorf("after SD row %d = %q, want %q", i, g.At(i, 0).Ch, w)
		}
	}
}

func TestParser_DEC47_AltScreen(t *testing.T) {
	g, p := newParserGrid(2, 3)
	feed(t, g, p, []byte("hi"))
	feed(t, g, p, []byte("\x1b[?47h"))
	if !g.AltActive {
		t.Fatal("?47h: AltActive should be true")
	}
	feed(t, g, p, []byte("\x1b[?47l"))
	if g.AltActive {
		t.Fatal("?47l: AltActive should be false")
	}
	if g.At(0, 0).Ch != 'h' || g.At(0, 1).Ch != 'i' {
		t.Errorf("main not restored: %q%q", g.At(0, 0).Ch, g.At(0, 1).Ch)
	}
}

func TestParser_DEC1047_AltScreen(t *testing.T) {
	g, p := newParserGrid(2, 3)
	feed(t, g, p, []byte("ab"))
	feed(t, g, p, []byte("\x1b[?1047h"))
	if !g.AltActive {
		t.Fatal("?1047h: AltActive should be true")
	}
	feed(t, g, p, []byte("\x1b[?1047l"))
	if g.AltActive {
		t.Fatal("?1047l: AltActive should be false")
	}
	if g.At(0, 0).Ch != 'a' {
		t.Errorf("main row 0 col 0 = %q, want a", g.At(0, 0).Ch)
	}
}

func TestParser_DEC1049_SavesAndRestoresCursor(t *testing.T) {
	g, p := newParserGrid(4, 6)
	feed(t, g, p, []byte("hello\r\nworld"))
	mainR, mainC := g.CursorR, g.CursorC
	feed(t, g, p, []byte("\x1b[?1049h"))
	if !g.AltActive {
		t.Fatal("?1049h: AltActive should be true")
	}
	if g.CursorR != 0 || g.CursorC != 0 {
		t.Errorf("alt entry: cursor not homed: %d,%d", g.CursorR, g.CursorC)
	}

	feed(t, g, p, []byte("\x1b[3;3HALT"))
	feed(t, g, p, []byte("\x1b[s"))
	feed(t, g, p, []byte("\x1b[?1049l"))
	if g.AltActive {
		t.Fatal("?1049l: AltActive should be false")
	}
	if g.CursorR != mainR || g.CursorC != mainC {
		t.Errorf("?1049l: cursor not restored: got %d,%d want %d,%d",
			g.CursorR, g.CursorC, mainR, mainC)
	}
	row1 := []rune{'w', 'o', 'r', 'l', 'd'}
	for i, w := range row1 {
		if g.At(1, i).Ch != w {
			t.Errorf("main row 1 col %d = %q, want %q",
				i, g.At(1, i).Ch, w)
		}
	}
}

func TestParser_DEC1049_SuppressesScrollback(t *testing.T) {
	g, p := newParserGrid(2, 3)
	g.ScrollbackCap = 50
	feed(t, g, p, []byte("\x1b[?1049h"))
	for range 8 {
		feed(t, g, p, []byte("x\r\n"))
	}
	if g.Scrollback.Len() != 0 {
		t.Errorf("scrollback grew under ?1049: %d rows",
			g.Scrollback.Len())
	}
	feed(t, g, p, []byte("\x1b[?1049l"))
	for range 5 {
		feed(t, g, p, []byte("y\r\n"))
	}
	if g.Scrollback.Len() == 0 {
		t.Errorf("scrollback inert after ?1049l")
	}
}

func TestParser_DECRQSS_Replies(t *testing.T) {
	g, p := newParserGrid(6, 8)
	g.CurAttrs = attrBold | attrUnderline
	g.CurFG = paletteColor(2)
	g.Top, g.Bottom = 1, 4
	g.ApplyDECSCUSR(6)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP$qm\x1b\\"))
	feed(t, g, p, []byte("\x1bP$qr\x1b\\"))
	feed(t, g, p, []byte("\x1bP$q q\x1b\\"))
	want := []string{
		"\x1bP1$r1;4;32m\x1b\\",
		"\x1bP1$r2;5r\x1b\\",
		"\x1bP1$r6 q\x1b\\",
	}
	if len(replies) != len(want) {
		t.Fatalf("DECRQSS reply count = %d, want %d", len(replies), len(want))
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Fatalf("reply[%d] = %q, want %q", i, replies[i], want[i])
		}
	}
}

func TestParser_DECSCUSR_AllPs(t *testing.T) {
	cases := []struct {
		ps    int
		shape cursorShape
		blink bool
	}{
		{0, cursorBlock, true},
		{1, cursorBlock, true},
		{2, cursorBlock, false},
		{3, cursorUnderline, true},
		{4, cursorUnderline, false},
		{5, cursorBar, true},
		{6, cursorBar, false},
		{99, cursorBlock, true},
	}
	for _, c := range cases {
		g, p := newParserGrid(1, 5)

		seq := append([]byte("\x1b["), []byte(strconv.Itoa(c.ps))...)
		seq = append(seq, ' ', 'q')
		feed(t, g, p, seq)
		if g.cursorShape != c.shape || g.CursorBlink != c.blink {
			t.Errorf("Ps=%d: shape=%d blink=%v, want shape=%d blink=%v",
				c.ps, g.cursorShape, g.CursorBlink, c.shape, c.blink)
		}
	}
}

func TestParser_DECSCUSR_RequiresSpaceIntermediate(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.cursorShape = cursorBar
	g.CursorBlink = false

	feed(t, g, p, []byte("\x1b[2q"))
	if g.cursorShape != cursorBar || g.CursorBlink != false {
		t.Errorf("CSI 2 q (no SP) clobbered shape=%d blink=%v",
			g.cursorShape, g.CursorBlink)
	}
}

func TestParser_DECSCUSR_DefaultParam(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b[ q"))
	if g.cursorShape != cursorBlock || !g.CursorBlink {
		t.Errorf("default DECSCUSR: shape=%d blink=%v",
			g.cursorShape, g.CursorBlink)
	}
}

func TestCurrentSGRString_AllDefault(t *testing.T) {
	_, p := newParserGrid(1, 5)
	if got := p.currentSGRString(); got != "0m" {
		t.Errorf("all-default = %q, want %q", got, "0m")
	}
}

func TestCurrentSGRString_AttrInverse(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.CurAttrs = attrInverse
	if got := p.currentSGRString(); got != "7m" {
		t.Errorf("inverse = %q, want %q", got, "7m")
	}
}

func TestCurrentSGRString_BrightPalette(t *testing.T) {
	cases := []struct {
		fg   uint32
		want string
	}{
		{paletteColor(8), "90m"},
		{paletteColor(15), "97m"},
	}
	for _, c := range cases {
		g, p := newParserGrid(1, 5)
		g.CurFG = c.fg
		if got := p.currentSGRString(); got != c.want {
			t.Errorf("fg=%#x: got %q, want %q", c.fg, got, c.want)
		}
	}
}

func TestCurrentSGRString_TruecolorFGBG(t *testing.T) {
	g, p := newParserGrid(1, 5)
	g.CurFG = rgbColor(255, 100, 0)
	g.CurBG = rgbColor(0, 128, 255)
	want := "38;2;255;100;0;48;2;0;128;255m"
	if got := p.currentSGRString(); got != want {
		t.Errorf("truecolor = %q, want %q", got, want)
	}
}

func TestParser_DECRQSS_LongBodyReturnsNotOk(t *testing.T) {

	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP$qfoobar\x1b\\"))
	want := "\x1bP0$r\x1b\\"
	if len(replies) != 1 || replies[0] != want {
		t.Fatalf("long body DECRQSS = %q, want %q", replies, want)
	}
}

func TestParser_CursorSaveRestore_PreservesAutoWrapOriginInsert(t *testing.T) {
	g, p := newParserGrid(5, 8)
	g.AutoWrap = false
	g.OriginMode = true
	g.InsertMode = true
	feed(t, g, p, []byte("\x1b7"))
	g.AutoWrap = true
	g.OriginMode = false
	g.InsertMode = false
	feed(t, g, p, []byte("\x1b8"))
	if g.AutoWrap {
		t.Error("AutoWrap should be restored to false")
	}
	if !g.OriginMode {
		t.Error("OriginMode should be restored to true")
	}
	if !g.InsertMode {
		t.Error("InsertMode should be restored to true")
	}
}

func TestParser_SGR_NewAttrs_Set(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want uint16
	}{
		{"dim", "\x1b[2m", attrDim},
		{"italic", "\x1b[3m", attrItalic},
		{"strikethrough", "\x1b[9m", attrStrikethrough},
		{"bold+dim", "\x1b[1m\x1b[2m", attrBold | attrDim},
		{"bold+italic", "\x1b[1m\x1b[3m", attrBold | attrItalic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(1, 1)
			feed(t, g, p, []byte(tt.seq))
			if g.CurAttrs&tt.want != tt.want {
				t.Errorf("attrs=%08b want %08b set", g.CurAttrs, tt.want)
			}
		})
	}
}

func TestParser_SGR_NewAttrs_Clear(t *testing.T) {
	tests := []struct {
		name     string
		setSeq   string
		clearSeq string
		bits     uint16
	}{
		{"dim via 22", "\x1b[2m", "\x1b[22m", attrDim},
		{"bold+dim via 22", "\x1b[1m\x1b[2m", "\x1b[22m", attrBold | attrDim},
		{"italic via 23", "\x1b[3m", "\x1b[23m", attrItalic},
		{"strikethrough via 29", "\x1b[9m", "\x1b[29m", attrStrikethrough},
		{"all via SGR 0", "\x1b[1m\x1b[2m\x1b[3m\x1b[9m", "\x1b[0m", attrBold | attrDim | attrItalic | attrStrikethrough},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(1, 1)
			feed(t, g, p, []byte(tt.setSeq))
			if g.CurAttrs&tt.bits != tt.bits {
				t.Errorf("after set: attrs=%08b want %08b set", g.CurAttrs, tt.bits)
			}
			feed(t, g, p, []byte(tt.clearSeq))
			if g.CurAttrs&tt.bits != 0 {
				t.Errorf("after clear: attrs=%08b want %08b clear", g.CurAttrs, tt.bits)
			}
		})
	}
}

func TestParser_SGR4_NoSubparam_SingleUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[4m"))
	if g.CurAttrs&attrUnderline == 0 {
		t.Error("SGR 4: attrUnderline not set")
	}
	if g.CurULStyle != ulSingle {
		t.Errorf("SGR 4: CurULStyle = %d, want ulSingle (%d)", g.CurULStyle, ulSingle)
	}
}

func TestParser_SGR4_ColonSubparam_Styles(t *testing.T) {
	cases := []struct {
		seq     string
		style   uint8
		hasAttr bool
	}{
		{"\x1b[4:0m", ulNone, false},
		{"\x1b[4:1m", ulSingle, true},
		{"\x1b[4:2m", ulDouble, true},
		{"\x1b[4:3m", ulCurly, true},
		{"\x1b[4:4m", ulDotted, true},
		{"\x1b[4:5m", ulDashed, true},
	}
	for _, c := range cases {
		g, p := newParserGrid(2, 10)

		g.CurAttrs |= attrUnderline
		g.CurULStyle = ulSingle
		feed(t, g, p, []byte(c.seq))
		if g.CurULStyle != c.style {
			t.Errorf("seq %q: CurULStyle = %d, want %d", c.seq, g.CurULStyle, c.style)
		}
		if c.hasAttr && g.CurAttrs&attrUnderline == 0 {
			t.Errorf("seq %q: attrUnderline not set", c.seq)
		}
		if !c.hasAttr && g.CurAttrs&attrUnderline != 0 {
			t.Errorf("seq %q: attrUnderline should be cleared", c.seq)
		}
	}
}

func TestParser_SGR21_DoubleUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[21m"))
	if g.CurAttrs&attrUnderline == 0 {
		t.Error("SGR 21: attrUnderline not set")
	}
	if g.CurULStyle != ulDouble {
		t.Errorf("SGR 21: CurULStyle = %d, want ulDouble (%d)", g.CurULStyle, ulDouble)
	}
}

func TestParser_SGR24_ClearsUnderline(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurAttrs |= attrUnderline
	g.CurULStyle = ulCurly
	g.CurULColor = rgbColor(255, 0, 0)
	feed(t, g, p, []byte("\x1b[24m"))
	if g.CurAttrs&attrUnderline != 0 {
		t.Error("SGR 24: attrUnderline should be cleared")
	}
	if g.CurULStyle != ulNone {
		t.Errorf("SGR 24: CurULStyle = %d, want 0", g.CurULStyle)
	}
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 24: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
}

func TestParser_SGR58_ULColor_RGB(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[58;2;255;128;0m"))
	want := rgbColor(255, 128, 0)
	if g.CurULColor != want {
		t.Errorf("SGR 58 RGB: CurULColor = %#x, want %#x", g.CurULColor, want)
	}
}

func TestParser_SGR58_ULColor_Palette(t *testing.T) {
	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[58;5;196m"))
	want := paletteColor(196)
	if g.CurULColor != want {
		t.Errorf("SGR 58 palette: CurULColor = %#x, want %#x", g.CurULColor, want)
	}
}

func TestParser_SGR59_ResetsULColor(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurULColor = rgbColor(0, 255, 0)
	feed(t, g, p, []byte("\x1b[59m"))
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 59: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
}

func TestParser_SGRReset_ClearsULState(t *testing.T) {
	g, p := newParserGrid(2, 10)
	g.CurULStyle = ulCurly
	g.CurULColor = rgbColor(100, 200, 50)
	g.CurAttrs |= attrUnderline
	feed(t, g, p, []byte("\x1b[0m"))
	if g.CurULStyle != ulNone {
		t.Errorf("SGR 0: CurULStyle = %d, want 0", g.CurULStyle)
	}
	if g.CurULColor != DefaultColor {
		t.Errorf("SGR 0: CurULColor = %#x, want DefaultColor", g.CurULColor)
	}
	if g.CurAttrs&attrUnderline != 0 {
		t.Error("SGR 0: attrUnderline should be cleared")
	}
}

func TestParser_SGR4_Semicolon_NotSubparam(t *testing.T) {

	g, p := newParserGrid(2, 10)
	feed(t, g, p, []byte("\x1b[4;3m"))
	if g.CurULStyle != ulSingle {
		t.Errorf("4;3m: CurULStyle = %d, want ulSingle (semicolon ≠ colon)", g.CurULStyle)
	}
	if g.CurAttrs&attrItalic == 0 {
		t.Error("4;3m: attrItalic should also be set")
	}
}

func TestParser_DA2(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[>c"))
	want := []byte("\x1b[>0;0;0c")
	if !bytes.Equal(reply, want) {
		t.Errorf("DA2 reply = %q, want %q", reply, want)
	}
}

func TestParser_DECRQM_Set(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	// Set bracketed paste (2004) then query.
	feed(t, g, p, []byte("\x1b[?2004h"))
	// Reset reply buffer so we only see the DECRQM reply.
	reply = nil
	feed(t, g, p, []byte("\x1b[?2004$p"))
	want := []byte("\x1b[?2004;1$y")
	if !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2004 set reply = %q, want %q", reply, want)
	}
}

func TestParser_DECRQM_Reset(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	// Set and then reset cursor visible (25), then query.
	feed(t, g, p, []byte("\x1b[?25h"))
	feed(t, g, p, []byte("\x1b[?25l"))
	reply = nil
	feed(t, g, p, []byte("\x1b[?25$p"))
	want := []byte("\x1b[?25;2$y")
	if !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 25 reset reply = %q, want %q", reply, want)
	}
}

func TestParser_DECRQM_Unrecognized(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[?9999$p"))
	want := []byte("\x1b[?9999;0$y")
	if !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 9999 unrecognized reply = %q, want %q", reply, want)
	}
}

func TestParser_DECRQM_Grapheme2027(t *testing.T) {
	// Mode 2027 (grapheme clustering) is always on; DECRQM reports it
	// permanently set (value 3), and DECSET/DECRST are no-ops.
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[?2027$p"))
	want := []byte("\x1b[?2027;3$y")
	if !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2027 reply = %q, want %q", reply, want)
	}
	// A reset attempt must not change the permanently-set report.
	reply = nil
	feed(t, g, p, []byte("\x1b[?2027l\x1b[?2027$p"))
	if !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2027 after reset = %q, want %q", reply, want)
	}
}

func TestParser_DECRQM_Sync2026(t *testing.T) {
	// Mode 2026 reports whether a synchronized-update block is currently
	// open (SyncActive), not merely that DECSET was ever seen — after a
	// forced end (widget watchdog) an app querying must observe reset.
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[?2026$p"))
	if want := []byte("\x1b[?2026;2$y"); !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2026 initial reply = %q, want %q", reply, want)
	}
	reply = nil
	feed(t, g, p, []byte("\x1b[?2026h\x1b[?2026$p"))
	if want := []byte("\x1b[?2026;1$y"); !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2026 in-block reply = %q, want %q", reply, want)
	}
	// Simulate the widget watchdog force-ending the block: the mode is
	// still "supported" but no block is open, so the report is reset.
	g.EndSync()
	reply = nil
	feed(t, g, p, []byte("\x1b[?2026$p"))
	if want := []byte("\x1b[?2026;2$y"); !bytes.Equal(reply, want) {
		t.Errorf("DECRQM 2026 after forced end = %q, want %q", reply, want)
	}
}

func TestParser_XTWINOPS_PixelGeometry(t *testing.T) {
	g, p := newParserGrid(24, 80)
	g.CellPxW, g.CellPxH = 8.0, 16.0
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })

	// CSI 16 t -> cell size: CSI 6 ; height ; width t
	feed(t, g, p, []byte("\x1b[16t"))
	if want := []byte("\x1b[6;16;8t"); !bytes.Equal(reply, want) {
		t.Errorf("CSI 16t reply = %q, want %q", reply, want)
	}

	// CSI 14 t -> text-area size: CSI 4 ; rows*h ; cols*w t
	reply = nil
	feed(t, g, p, []byte("\x1b[14t"))
	if want := []byte("\x1b[4;384;640t"); !bytes.Equal(reply, want) {
		t.Errorf("CSI 14t reply = %q, want %q", reply, want)
	}

	// Unhandled window op (e.g. raise, CSI 5 t) must not reply.
	reply = nil
	feed(t, g, p, []byte("\x1b[5t"))
	if len(reply) != 0 {
		t.Errorf("CSI 5t should not reply, got %q", reply)
	}
}

func TestParser_XTVERSION(t *testing.T) {
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[>q"))
	want := []byte("\x1bP>|go-term(" + termVersion + ")\x1b\\")
	if !bytes.Equal(reply, want) {
		t.Errorf("XTVERSION reply = %q, want %q", reply, want)
	}
}

func TestParser_XTVERSION_NonZeroParam(t *testing.T) {
	// CSI > 1 q is not an XTVERSION request; must not reply.
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[>1q"))
	if len(reply) != 0 {
		t.Errorf("CSI > 1 q should not reply, got %q", reply)
	}
}

func TestParser_DECRQM_NoDollar(t *testing.T) {
	// CSI ? 25 p without $ intermediate must not trigger DECRQM. Mode 25
	// has no meaning as a bare CSI final, so it falls through
	// silently and no reply is emitted.
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[?25p"))
	if len(reply) != 0 {
		t.Errorf("DECRQM without $ should not reply, got %q", reply)
	}
}

func TestParser_DA2_NonZeroParam(t *testing.T) {
	// DA2 with Ps != 0 must not reply (matches DA1 behavior).
	g, p := newParserGrid(2, 10)
	var reply []byte
	p.SetReplyHandler(func(b []byte) { reply = append(reply, b...) })
	feed(t, g, p, []byte("\x1b[>1c"))
	if len(reply) != 0 {
		t.Errorf("DA2 with Ps=1 should not reply, got %q", reply)
	}
}

func TestParser_DA2_NoHandler(t *testing.T) {
	// DA2 without a reply handler must not panic.
	g, p := newParserGrid(2, 10)
	// No SetReplyHandler call — p.onReply is nil.
	feed(t, g, p, []byte("\x1b[>c"))
	// No panic = pass.
}

func TestParser_DECRQM_NoHandler(t *testing.T) {
	// DECRQM without a reply handler must not panic.
	g, p := newParserGrid(2, 10)
	// No SetReplyHandler call — p.onReply is nil.
	feed(t, g, p, []byte("\x1b[?2004$p"))
	// No panic = pass.
}

// ── DECSCA + VT420 rectangular area operations (issue #71) ────────────────

func TestParser_DECSCA_SetsAndClearsProtection(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b[1\"q"))
	if g.CurAttrs&attrProtected == 0 {
		t.Fatal("CSI 1 \" q did not set protection")
	}
	// Characters written now carry the bit.
	feed(t, g, p, []byte("ab"))
	if g.At(0, 0).Attrs&attrProtected == 0 || g.At(0, 1).Attrs&attrProtected == 0 {
		t.Error("protection not applied to written cells")
	}
	// SGR reset must not clear DECSCA — it is not a visual attribute.
	feed(t, g, p, []byte("\x1b[0m"))
	if g.CurAttrs&attrProtected == 0 {
		t.Error("SGR 0 cleared DECSCA")
	}
	feed(t, g, p, []byte("\x1b[m"))
	if g.CurAttrs&attrProtected == 0 {
		t.Error("bare SGR cleared DECSCA")
	}
	feed(t, g, p, []byte("\x1b[0\"q"))
	if g.CurAttrs&attrProtected != 0 {
		t.Error("CSI 0 \" q did not clear protection")
	}
}

func TestParser_DECSCA_SavedByDECSCAndClearedByDECSTR(t *testing.T) {
	g, p := newParserGrid(1, 4)
	feed(t, g, p, []byte("\x1b[1\"q\x1b7\x1b[0\"q"))
	if g.CurAttrs&attrProtected != 0 {
		t.Fatal("protection still set after DECSCA 0")
	}
	feed(t, g, p, []byte("\x1b8")) // DECRC restores the saved DECSCA state
	if g.CurAttrs&attrProtected == 0 {
		t.Error("DECRC did not restore DECSCA")
	}
	feed(t, g, p, []byte("\x1b[!p")) // DECSTR
	if g.CurAttrs&attrProtected != 0 {
		t.Error("DECSTR left DECSCA set")
	}
}

func TestParser_DECSEL_DECSED_HonorProtection(t *testing.T) {
	g, p := newParserGrid(2, 4)
	// Row 0: "ab" unprotected, "CD" protected.
	feed(t, g, p, []byte("ab\x1b[1\"qCD\x1b[0\"q"))
	feed(t, g, p, []byte("\x1b[1;1H\x1b[?2K")) // DECSEL 2 on row 0
	if got := rowText(g, 0); got != "  CD" {
		t.Errorf("DECSEL row = %q, want \"  CD\"", got)
	}
	feed(t, g, p, []byte("\x1b[?2J")) // DECSED 2
	if got := rowText(g, 0); got != "  CD" {
		t.Errorf("DECSED row = %q, want \"  CD\"", got)
	}
	feed(t, g, p, []byte("\x1b[2J")) // plain ED ignores protection
	if got := rowText(g, 0); got != "    " {
		t.Errorf("ED row = %q, want blank", got)
	}
}

func TestParser_RectOps_Dispatch(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			// DECERA over rows 1-2, cols 2-3.
			name:  "DECERA",
			input: "\x1b[1;2;2;3$z",
			want:  []string{"X  X", "X  X", "XXXX"},
		},
		{
			// DECFRA fills rows 2-3, cols 1-2 with '*'.
			name:  "DECFRA",
			input: "\x1b[42;2;1;3;2$x",
			want:  []string{"XPXX", "**XX", "**XX"},
		},
		{
			// DECSERA leaves the protected cell at (0,1) standing; the rest
			// of row 1 clears.
			name:  "DECSERA",
			input: "\x1b[1;1;1;4$\x7b",
			want:  []string{" P  ", "XXXX", "XXXX"},
		},
		{
			// DECCRA copies row 1 cols 1-2 down to row 3 col 3.
			name:  "DECCRA",
			input: "\x1b[1;1;1;2;1;3;3;1$v",
			want:  []string{"XPXX", "XXXX", "XXXP"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newParserGrid(3, 4)
			fillGrid(g, 'X')
			// A protected 'P' at (0,1) so the selective forms are observable.
			g.Cells[1] = cell{Ch: 'P', FG: DefaultColor, BG: DefaultColor,
				ULColor: DefaultColor, Width: 1, Attrs: attrProtected}
			feed(t, g, p, []byte(tt.input))
			wantRows(t, g, tt.want)
		})
	}
}

func TestParser_DECCARA_DECRARA_Dispatch(t *testing.T) {
	g, p := newParserGrid(2, 4)
	fillGrid(g, 'X')
	// Rectangle extent, then bold rows 1-2 cols 2-3.
	feed(t, g, p, []byte("\x1b[2*x\x1b[1;2;2;3;1$r"))
	if g.RectExtent != 2 {
		t.Fatalf("DECSACE not applied: %d", g.RectExtent)
	}
	for r := range 2 {
		for c := range 4 {
			bold := g.At(r, c).Attrs&attrBold != 0
			want := c == 1 || c == 2
			if bold != want {
				t.Errorf("DECCARA (%d,%d) bold=%v, want %v", r, c, bold, want)
			}
		}
	}
	// DECRARA toggles the same area back off.
	feed(t, g, p, []byte("\x1b[1;2;2;3;1$t"))
	for r := range 2 {
		for c := range 4 {
			if g.At(r, c).Attrs&attrBold != 0 {
				t.Errorf("DECRARA left (%d,%d) bold", r, c)
			}
		}
	}
}

func TestParser_RectIntermediates_DoNotShadowBareFinals(t *testing.T) {
	// 'r', 't' and 'x' are shared with DECSTBM, XTWINOPS and DECREQTPARM;
	// only the '$'/'*' intermediate forms may reach the rectangle ops.
	g, p := newParserGrid(6, 8)
	feed(t, g, p, []byte("\x1b[2;5r"))
	if g.Top != 1 || g.Bottom != 4 {
		t.Errorf("DECSTBM broken by DECCARA dispatch: %d..%d", g.Top, g.Bottom)
	}
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1b[16t")) // XTWINOPS cell-size report
	if len(replies) != 1 {
		t.Errorf("XTWINOPS broken by DECRARA dispatch: %q", replies)
	}
	if g.RectExtent != 0 {
		t.Errorf("bare final touched DECSACE: %d", g.RectExtent)
	}
}

func TestParser_DECRQSS_DECSCA_DECSACE(t *testing.T) {
	g, p := newParserGrid(4, 8)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	feed(t, g, p, []byte("\x1bP$q\"q\x1b\\"))
	feed(t, g, p, []byte("\x1b[1\"q\x1b[2*x"))
	feed(t, g, p, []byte("\x1bP$q\"q\x1b\\"))
	feed(t, g, p, []byte("\x1bP$q*x\x1b\\"))
	want := []string{
		"\x1bP1$r0\"q\x1b\\",
		"\x1bP1$r1\"q\x1b\\",
		"\x1bP1$r2*x\x1b\\",
	}
	if len(replies) != len(want) {
		t.Fatalf("reply count = %d (%q), want %d", len(replies), replies, len(want))
	}
	for i := range want {
		if replies[i] != want[i] {
			t.Errorf("reply[%d] = %q, want %q", i, replies[i], want[i])
		}
	}
}
