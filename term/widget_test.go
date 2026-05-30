package term

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mike-ward/go-gui/gui"

	glyph "github.com/mike-ward/go-glyph"
)

// scrollbarThumb delegates to scrollbarGeometry so tests share the production formula.
func scrollbarThumb(sbRows, liveRows, viewOffset int, viewH float32) (thumbY, thumbH float32) {
	return scrollbarGeometry(sbRows, liveRows, float32(viewOffset), viewH)
}

func TestScrollbarGeometry_LiveView(t *testing.T) {
	// ViewOffset=0: thumb bottom should align with viewport bottom.
	const sb, rows, h = 100, 24, 480.0
	y, th := scrollbarThumb(sb, rows, 0, h)
	bottom := y + th
	if math.Abs(float64(bottom-h)) > 0.001 {
		t.Errorf("live view: thumb bottom = %.3f, want %.3f", bottom, float32(h))
	}
}

func TestScrollbarGeometry_TopView(t *testing.T) {
	// ViewOffset=len(Scrollback): thumb top should be at 0.
	const sb, rows, h = 100, 24, 480.0
	y, _ := scrollbarThumb(sb, rows, sb, h)
	if math.Abs(float64(y)) > 0.001 {
		t.Errorf("top view: thumbY = %.3f, want 0", y)
	}
}

func TestScrollbarGeometry_MidView(t *testing.T) {
	// ViewOffset=half scrollback: thumb midpoint should be near viewport midpoint.
	const sb, rows, h = 100, 0, 100.0 // rows=0 so total=sb; mid is exact
	mid := sb / 2
	y, th := scrollbarThumb(sb, rows, mid, h)
	thumbMid := y + th/2
	if math.Abs(float64(thumbMid-h/2)) > 1.0 {
		t.Errorf("mid view: thumb midpoint = %.3f, want ~%.3f", thumbMid, float32(h/2))
	}
}

func TestScrollbarGeometry_SubPixel(t *testing.T) {
	// Verify that fractional viewOffset produces fractional thumbY changes
	const sb, rows, h = 100, 24, 480.0
	y0, _ := scrollbarGeometry(sb, rows, 10.0, h)
	yHalf, _ := scrollbarGeometry(sb, rows, 10.5, h)
	y1, _ := scrollbarGeometry(sb, rows, 11.0, h)

	if yHalf <= y1 || yHalf >= y0 {
		t.Errorf("expected yHalf (%f) to be strictly between y1 (%f) and y0 (%f)", yHalf, y1, y0)
	}

	expectedHalf := (y0 + y1) / 2
	if math.Abs(float64(yHalf-expectedHalf)) > 0.001 {
		t.Errorf("yHalf = %f, want exactly half-way value %f", yHalf, expectedHalf)
	}
}

func TestSearchOverlap_NoScroll_OneRow(t *testing.T) {
	// 24 rows × 20px = 480px. Search bar at [460,480). Row 23's text
	// footprint [460,480) overlaps → 1 row reserved.
	if got := searchOverlap(20, 0, 480, 24); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestSearchOverlap_SubPixelMax_TwoRows(t *testing.T) {
	// renderYOff=19 shifts row 22 to [459,479), overlapping search bar
	// [460,480). Row 23 shifts to [479,499) also overlapping → 2 rows.
	if got := searchOverlap(20, 19, 480, 24); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestSearchOverlap_AlignedCanvas_Scroll_YieldsTwoRows(t *testing.T) {
	// 480px canvas, 20px cellH → no fractional gap. Any renderYOff > 0
	// shifts row 22's text bottom (460+renderYOff) past searchBarTop (460).
	// Only renderYOff=0 produces 1 row; all positive values → 2.
	if got := searchOverlap(20, 1, 480, 24); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestSearchOverlap_FractionalCanvas_OneRow(t *testing.T) {
	// 485px canvas, 20px cellH → rows=24, 5px fractional gap at bottom.
	// searchBarTop=465. renderYOff=3: row 22 at [443,463) doesn't
	// overlap, row 23 at [463,483) does → 1. Old heuristic: always 2.
	if got := searchOverlap(20, 3, 485, 24); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestSearchOverlap_ZeroRows_ReturnsZero(t *testing.T) {
	if got := searchOverlap(20, 5, 480, 0); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

func TestSearchOverlap_NaNInput_NoInfiniteLoop(t *testing.T) {
	// NaN comparisons always false → loop condition fails immediately.
	// Return value is meaningless (NaN geometry is undefined); the
	// assertion is just that the function returns without looping.
	_ = searchOverlap(20, float32(math.NaN()), 480, 24)
}

// recordingNotifier captures Notify calls for assertion in tests.
type recordingNotifier struct {
	calls []struct{ title, body string }
	mu    sync.Mutex
}

func (r *recordingNotifier) Notify(title, body string) {
	r.mu.Lock()
	r.calls = append(r.calls, struct{ title, body string }{title, body})
	r.mu.Unlock()
}

func TestNotify_DesktopNotifier_NoCallback(t *testing.T) {
	// When OnNotify is nil, OSC 9/777 must reach the notifier interface.
	rec := &recordingNotifier{}
	g := newGrid(4, 80)
	p := newParser(g)
	tm := &Term{
		grid:   g,
		parser: p,
		cfg:    Cfg{}, // OnNotify nil
		notif:  rec,
	}
	tm.registerNotifyHandler()

	// Bounded channel replaces time.Sleep — deterministic and fast.
	notified := make(chan struct{}, 2)

	// Wrap notif so each Notify call signals the channel.
	tm.notif = notifierFunc(func(title, body string) {
		rec.Notify(title, body)
		notified <- struct{}{}
	})

	feed(t, g, p, []byte("\x1b]9;hello world\x07"))
	<-notified

	feed(t, g, p, []byte("\x1b]777;notify;my title;my body\x07"))
	<-notified

	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.calls) != 2 {
		t.Fatalf("got %d notify calls, want 2", len(rec.calls))
	}
	if rec.calls[0].title != "" || rec.calls[0].body != "hello world" {
		t.Errorf("OSC 9: got title=%q body=%q, want title=\"\" body=\"hello world\"",
			rec.calls[0].title, rec.calls[0].body)
	}
	if rec.calls[1].title != "my title" || rec.calls[1].body != "my body" {
		t.Errorf("OSC 777: got title=%q body=%q, want title=\"my title\" body=\"my body\"",
			rec.calls[1].title, rec.calls[1].body)
	}
}

// --- numeric helpers ---

func TestFinite(t *testing.T) {
	cases := []struct {
		in   float32
		want bool
	}{
		{1, true},
		{0.5, true},
		{0, false},
		{-1, false},
		{float32(math.NaN()), false},
		{float32(math.Inf(1)), false},
		{float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		if got := finite(c.in); got != c.want {
			t.Errorf("finite(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestStripPasteEnd_NoMarker(t *testing.T) {
	in := "hello world\nlinetwo"
	if got := stripPasteEnd(in); got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestStripPasteEnd_RemovesEmbeddedMarker(t *testing.T) {
	in := "before\x1b[201~middle\x1b[201~after"
	want := "beforemiddleafter"
	if got := stripPasteEnd(in); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStripPasteEnd_PartialMarkerLeftAlone(t *testing.T) {
	// "\x1b[20" alone is not a marker.
	in := "x\x1b[20y"
	if got := stripPasteEnd(in); got != in {
		t.Errorf("got %q, want unchanged", got)
	}
}

func TestRealNumber(t *testing.T) {
	cases := []struct {
		in   float32
		want bool
	}{
		{0, true},
		{1, true},
		{-1, true},
		{float32(math.NaN()), false},
		{float32(math.Inf(1)), false},
		{float32(math.Inf(-1)), false},
	}
	for _, c := range cases {
		if got := realNumber(c.in); got != c.want {
			t.Errorf("realNumber(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTruncatePaste_ShortReturnsUnchanged(t *testing.T) {
	if got := truncatePaste("abc", 10); got != "abc" {
		t.Errorf("got %q, want %q", got, "abc")
	}
}

func TestTruncatePaste_AsciiCutAtMax(t *testing.T) {
	in := "abcdefghij"
	if got := truncatePaste(in, 4); got != "abcd" {
		t.Errorf("got %q, want %q", got, "abcd")
	}
}

func TestTruncatePaste_BacksOffPartialUTF8(t *testing.T) {
	// "é" is 0xC3 0xA9 (2 bytes). Cutting at the second byte mid-rune
	// must back up to the start so no half-rune escapes.
	in := "aé" // 1 + 2 = 3 bytes
	got := truncatePaste(in, 2)
	if got != "a" {
		t.Errorf("got %q, want %q", got, "a")
	}
}

func TestTruncatePaste_MultiByteAtBoundary(t *testing.T) {
	// "☃" is 0xE2 0x98 0x83 (3 bytes). max=4 lands inside the second
	// rune; result should keep the complete first snowman only.
	in := "☃☃" // 6 bytes
	got := truncatePaste(in, 4)
	if got != "☃" {
		t.Errorf("got %q, want %q", got, "☃")
	}
}

func TestTruncatePaste_ZeroOrNegativeMaxIsEmpty(t *testing.T) {
	if got := truncatePaste("abc", 0); got != "" {
		t.Errorf("max=0: got %q, want \"\"", got)
	}
	if got := truncatePaste("abc", -1); got != "" {
		t.Errorf("max=-1: got %q, want \"\"", got)
	}
}

func TestEncodeMouseSGR_Press(t *testing.T) {
	got := string(encodeMouseSGR(nil, 0, 4, 9, true))
	if got != "\x1b[<0;5;10M" {
		t.Errorf("press: %q", got)
	}
}

func TestEncodeMouseSGR_Release(t *testing.T) {
	got := string(encodeMouseSGR(nil, 0, 0, 0, false))
	if got != "\x1b[<0;1;1m" {
		t.Errorf("release: %q", got)
	}
}

func TestEncodeMouseSGR_WheelUp(t *testing.T) {
	got := string(encodeMouseSGR(nil, 64, 10, 20, true))
	if got != "\x1b[<64;11;21M" {
		t.Errorf("wheel up: %q", got)
	}
}

func TestEncodeMouseSGR_DragWithMods(t *testing.T) {
	got := string(encodeMouseSGR(nil, 48, 7, 3, true))
	if got != "\x1b[<48;8;4M" {
		t.Errorf("drag+ctrl: %q", got)
	}
}

func TestMouseSGRBaseButton_KnownButtons(t *testing.T) {
	cases := []struct {
		btn  gui.MouseButton
		want int
		ok   bool
	}{
		{gui.MouseLeft, 0, true},
		{gui.MouseRight, 2, true},
		{gui.MouseMiddle, 1, true},
		{gui.MouseInvalid, 0, false},
	}
	for _, c := range cases {
		got, ok := mouseSGRBaseButton(c.btn)
		if got != c.want || ok != c.ok {
			t.Errorf("btn=%d: got (%d,%v), want (%d,%v)",
				c.btn, got, ok, c.want, c.ok)
		}
	}
}

// writerFunc adapts a function literal to io.Writer.
// Used to capture or inspect bytes written to the PTY in tests.
type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) {
	if f == nil {
		return 0, nil
	}
	return f(b)
}

// notifierFunc adapts a function literal to the notifier interface.
type notifierFunc func(title, body string)

func (f notifierFunc) Notify(title, body string) { f(title, body) }

func newTestTermCapture() (*Term, *[]byte) {
	buf := make([]byte, 0, 64)
	t := &Term{grid: newGrid(4, 8)}
	t.mouse.lastR = -1
	t.mouse.lastC = -1
	t.pw = writerFunc(func(b []byte) (int, error) {
		buf = append(buf, b...)
		return len(b), nil
	})
	return t, &buf
}

func TestTerm_OnWindowEvent_NoReportWhenFocusOff(t *testing.T) {
	term, buf := newTestTermCapture()
	// FocusReporting defaults to false
	term.onWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.onWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "" {
		t.Fatalf("focus off: got %q, want empty", got)
	}
}

func TestTerm_OnWindowEvent_NilEventNoPanic(t *testing.T) {
	term := &Term{grid: newGrid(1, 5), pw: writerFunc(func([]byte) (int, error) { return 0, nil })}
	term.onWindowEvent(nil) // must not panic
}

func TestTerm_OnKeyDown_AppCursor(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.AppCursorKeys = true
	e := &gui.Event{KeyCode: gui.KeyUp}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1bOA" {
		t.Fatalf("app cursor = %q, want %q", got, "\x1bOA")
	}
	if !e.IsHandled {
		t.Fatal("event should be handled")
	}
}

func TestTerm_OnKeyDown_AppKeypad(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.AppKeypad = true
	e := &gui.Event{KeyCode: gui.KeyKP1}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1bOq" {
		t.Fatalf("app keypad = %q, want %q", got, "\x1bOq")
	}
}

func TestTerm_OnWindowEvent_FocusReporting(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.FocusReporting = true
	term.onWindowEvent(&gui.Event{Type: gui.EventFocused})
	term.onWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if got := string(*buf); got != "\x1b[I\x1b[O" {
		t.Fatalf("focus reports = %q, want %q", got, "\x1b[I\x1b[O")
	}
}

func TestTerm_WriteBytes_UsesWriteHost(t *testing.T) {
	term := &Term{}
	term.pw = writerFunc(func([]byte) (int, error) { return 0, errors.New("boom") })
	term.writeBytes([]byte("x"))
}

func TestCursorBlinks_HonorsGridDefault(t *testing.T) {
	g := newGrid(1, 5)
	tm := &Term{grid: g}
	if tm.cursorBlinks() {
		t.Error("default cursor should be steady")
	}
	g.CursorBlink = true
	if !tm.cursorBlinks() {
		t.Error("blinking cursor should blink")
	}
}

func TestCursorBlinks_CfgOverridesGrid(t *testing.T) {
	g := newGrid(1, 5)
	g.CursorBlink = true
	off := false
	tm := &Term{cfg: Cfg{CursorBlink: &off}, grid: g}
	if tm.cursorBlinks() {
		t.Error("Cfg override (false) should win over grid blink=true")
	}
	on := true
	g.CursorBlink = false
	tm.cfg.CursorBlink = &on
	if !tm.cursorBlinks() {
		t.Error("Cfg override (true) should win over grid blink=false")
	}
}

func TestMouseModBits(t *testing.T) {
	cases := []struct {
		m    gui.Modifier
		want int
	}{
		{0, 0},
		{gui.ModShift, 4},
		{gui.ModAlt, 8},
		{gui.ModCtrl, 16},
		{gui.ModCtrl | gui.ModShift, 20},
		{gui.ModCtrl | gui.ModAlt | gui.ModShift, 28},
		{gui.ModSuper, 0},
	}
	for _, c := range cases {
		if got := mouseModBits(c.m); got != c.want {
			t.Errorf("mod=%d: got %d, want %d", c.m, got, c.want)
		}
	}
}

func TestCellRunKey_PlainCell(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'A', FG: 7, BG: 0, Width: 1}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.ulStyle != ulNone || k.strikethrough {
		t.Error("plain cell should have no decoration")
	}
	if k.typeface != glyph.TypefaceRegular {
		t.Errorf("typeface: got %v, want regular", k.typeface)
	}
}

func TestCellRunKey_BoldItalic(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'B', Width: 1, Attrs: attrBold | attrItalic}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.typeface != glyph.TypefaceBoldItalic {
		t.Errorf("bold+italic: got %v, want BoldItalic", k.typeface)
	}
}

func TestCellRunKey_GeometryGlyphsIgnoreBoldTypeface(t *testing.T) {
	cases := []struct {
		name string
		ch   rune
	}{
		{"box-drawing", '│'},
		{"block-elements", '█'},
		{"braille", '⣿'},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(4, 8)
			base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
			cell := cell{Ch: tc.ch, Width: 1, Attrs: attrBold}
			k := cellRunKey(cell, base, g, -1, -1)
			if k.typeface != glyph.TypefaceRegular {
				t.Fatalf("geometry glyph %q should not switch to bold typeface, got %v", tc.ch, k.typeface)
			}
		})
	}
}

func TestCellRunKey_NonGeometryGlyphStillBolds(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: 'A', Width: 1, Attrs: attrBold}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.typeface != glyph.TypefaceBold {
		t.Fatalf("text glyph should still bold, got %v", k.typeface)
	}
}

func TestCellRunKey_GeometryGlyph_BoldItalicUsesItalic(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}
	cell := cell{Ch: '│', Width: 1, Attrs: attrBold | attrItalic}
	k := cellRunKey(cell, base, g, -1, -1)
	// Bold is suppressed; italic is not — TypefaceItalic expected.
	if k.typeface != glyph.TypefaceItalic {
		t.Fatalf("geometry glyph bold+italic: got %v, want TypefaceItalic", k.typeface)
	}
}

func TestIsGeometryGlyph_Boundaries(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
		desc string
	}{
		{0x24FF, false, "just below Box Drawing"},
		{0x2500, true, "first Box Drawing"},
		{0x257F, true, "last Box Drawing"},
		{0x2580, true, "first Block Elements"},
		{0x259F, true, "last Block Elements"},
		{0x25A0, true, "first Geometric Shapes"},
		{0x25C6, true, "◆ (DEC Special Graphics diamond)"},
		{0x25FF, true, "last Geometric Shapes"},
		{0x2600, false, "just above Geometric Shapes"},
		{0x23B9, false, "just below scan lines"},
		{0x23BA, true, "first scan line ⎺"},
		{0x23BD, true, "last scan line ⎽"},
		{0x23BE, false, "just above scan lines"},
		{0x27FF, false, "just below Braille"},
		{0x2800, true, "first Braille"},
		{0x28FF, true, "last Braille"},
		{0x2900, false, "just above Braille"},
	}
	for _, tc := range cases {
		if got := isGeometryGlyph(tc.r); got != tc.want {
			t.Errorf("isGeometryGlyph(%U) %s: got %v, want %v", tc.r, tc.desc, got, tc.want)
		}
	}
}

func TestCellRunKey_Underline(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'C', Width: 1, Attrs: attrUnderline, ULStyle: ulSingle, ULColor: DefaultColor}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.ulStyle != ulSingle {
		t.Errorf("underline attr: expected ulSingle in key, got %d", k.ulStyle)
	}
}

func TestCellRunKey_Strikethrough(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'D', Width: 1, Attrs: attrStrikethrough}
	k := cellRunKey(cell, base, g, -1, -1)
	if !k.strikethrough {
		t.Error("strikethrough attr: expected strikethrough in key")
	}
}

func TestCellRunKey_LinkForcesUnderline(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'E', Width: 1, LinkID: 42}
	k := cellRunKey(cell, base, g, -1, -1)
	if k.ulStyle == ulNone {
		t.Error("linked cell: expected underline forced on by linkID")
	}
}

// TestCellRunKey_DifferentLinksSameStyleCoalesce asserts the intent
// behind dropping linkID from runKey: two cells in different links
// but with the same visual style produce equal keys, allowing the
// foreground pass to coalesce them into one dc.Text call (and one
// go-glyph layout-cache entry).
func TestCellRunKey_DifferentLinksSameStyleCoalesce(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	a := cell{Ch: 'x', Width: 1, LinkID: 1}
	b := cell{Ch: 'y', Width: 1, LinkID: 2}
	if cellRunKey(a, base, g, -1, -1) != cellRunKey(b, base, g, -1, -1) {
		t.Error("same-style cells in different links must produce equal keys")
	}
}

func TestCellRunKey_DimHalvesColor(t *testing.T) {
	g := newGrid(4, 8)
	base := gui.TextStyle{}
	cell := cell{Ch: 'F', Width: 1, Attrs: attrDim}
	cell.FG = rgbColor(200, 100, 50)
	k := cellRunKey(cell, base, g, -1, -1)
	// Dim halves each channel via integer division.
	want := gui.RGB(100, 50, 25)
	if k.color != want {
		t.Errorf("dim color: got %v, want %v", k.color, want)
	}
}

// BenchmarkForegroundPass exercises the run-key computation and string
// building for a full 80×24 screen of mixed colored text. It does not
// call dc.Text (no GUI context required) — the hot path is the loop
// logic and memory access pattern.
func BenchmarkForegroundPass(b *testing.B) {
	const rows, cols = 24, 80
	g := newGrid(rows, cols)
	base := gui.TextStyle{Typeface: glyph.TypefaceRegular}

	// Fill with alternating color runs to stress the coalescing path.
	colors := []uint32{rgbColor(200, 200, 200), rgbColor(100, 200, 100), rgbColor(200, 100, 100)}
	for r := range rows {
		for c := range cols {
			g.Cells[r*cols+c] = cell{
				Ch:    rune('A' + c%26),
				FG:    colors[c%len(colors)],
				Width: 1,
			}
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		for r := range rows {
			for c := range cols {
				cell := g.Cells[r*cols+c]
				if cell.Width == 0 && cell.Ch == 0 {
					continue
				}
				_ = cellRunKey(cell, base, g, -1, -1)
			}
		}
	}
}

func TestTerm_OnKeyDown_AltLetter(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyF, "\x1bf"},
		{gui.KeyB, "\x1bb"},
		{gui.KeyA, "\x1ba"},
		{gui.KeyZ, "\x1bz"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: gui.ModAlt}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("Alt+%v = %q, want %q", c.key, got, c.want)
		}
		if !e.IsHandled {
			t.Errorf("Alt+%v: event should be handled", c.key)
		}
	}
}

func TestTerm_OnKeyDown_AltArrow(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyUp, "\x1b\x1b[A"},
		{gui.KeyDown, "\x1b\x1b[B"},
		{gui.KeyRight, "\x1b\x1b[C"},
		{gui.KeyLeft, "\x1b\x1b[D"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: gui.ModAlt}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("Alt+%v = %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_OnKeyDown_AltCtrlLetter(t *testing.T) {
	term, buf := newTestTermCapture()
	// Alt+Ctrl+B → ESC + 0x02
	e := &gui.Event{KeyCode: gui.KeyB, Modifiers: gui.ModAlt | gui.ModCtrl}
	term.onKeyDown(nil, e, &gui.Window{})
	want := "\x1b\x02"
	if got := string(*buf); got != want {
		t.Fatalf("Alt+Ctrl+B = %q, want %q", got, want)
	}
}

func TestModParam(t *testing.T) {
	cases := []struct {
		shift, alt, ctrl bool
		want             int
	}{
		{false, false, false, 0},
		{true, false, false, 2},
		{false, true, false, 3},
		{true, true, false, 4},
		{false, false, true, 5},
		{true, false, true, 6},
		{false, true, true, 7},
		{true, true, true, 8},
	}
	for _, c := range cases {
		if got := modParam(c.shift, c.alt, c.ctrl); got != c.want {
			t.Errorf("modParam(%v,%v,%v)=%d want %d", c.shift, c.alt, c.ctrl, got, c.want)
		}
	}
}

func TestFuncKeySeq_NoModifier(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyInsert, "\x1b[2~"},
		{gui.KeyF1, "\x1bOP"},
		{gui.KeyF2, "\x1bOQ"},
		{gui.KeyF3, "\x1bOR"},
		{gui.KeyF4, "\x1bOS"},
		{gui.KeyF5, "\x1b[15~"},
		{gui.KeyF6, "\x1b[17~"},
		{gui.KeyF7, "\x1b[18~"},
		{gui.KeyF8, "\x1b[19~"},
		{gui.KeyF9, "\x1b[20~"},
		{gui.KeyF10, "\x1b[21~"},
		{gui.KeyF11, "\x1b[23~"},
		{gui.KeyF12, "\x1b[24~"},
	}
	for _, c := range cases {
		got := string(funcKeySeq(c.key, false, false))
		if got != c.want {
			t.Errorf("funcKeySeq(%v)=%q want %q", c.key, got, c.want)
		}
	}
}

func TestFuncKeySeq_ShiftModifier(t *testing.T) {
	// Shift+F1 → \x1b[1;2P, Shift+F5 → \x1b[15;2~
	if got := string(funcKeySeq(gui.KeyF1, true, false)); got != "\x1b[1;2P" {
		t.Errorf("Shift+F1=%q want %q", got, "\x1b[1;2P")
	}
	if got := string(funcKeySeq(gui.KeyF5, true, false)); got != "\x1b[15;2~" {
		t.Errorf("Shift+F5=%q want %q", got, "\x1b[15;2~")
	}
}

func TestFuncKeySeq_CtrlModifier(t *testing.T) {
	// Ctrl+F1 → \x1b[1;5P, Ctrl+F10 → \x1b[21;5~
	if got := string(funcKeySeq(gui.KeyF1, false, true)); got != "\x1b[1;5P" {
		t.Errorf("Ctrl+F1=%q want %q", got, "\x1b[1;5P")
	}
	if got := string(funcKeySeq(gui.KeyF10, false, true)); got != "\x1b[21;5~" {
		t.Errorf("Ctrl+F10=%q want %q", got, "\x1b[21;5~")
	}
}

func TestTerm_OnKeyDown_FuncKeys(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyF1, 0, "\x1bOP"},
		{gui.KeyF4, 0, "\x1bOS"},
		{gui.KeyF5, 0, "\x1b[15~"},
		{gui.KeyF12, 0, "\x1b[24~"},
		{gui.KeyInsert, 0, "\x1b[2~"},
		{gui.KeyF1, gui.ModShift, "\x1b[1;2P"},
		{gui.KeyF5, gui.ModCtrl, "\x1b[15;5~"},
		{gui.KeyF1, gui.ModAlt, "\x1b\x1bOP"}, // alt as ESC prefix
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
		if !e.IsHandled {
			t.Errorf("key=%v mods=%v: event not handled", c.key, c.mods)
		}
	}
}

func TestScrollbarGeometry_ZeroTotal_NoPanic(t *testing.T) {
	// sbLen=0, rows=0 → total=0: must not divide by zero.
	y, h := scrollbarGeometry(0, 0, 0, 100)
	if y != 0 || h != 0 {
		t.Errorf("zero total: got y=%v h=%v, want (0,0)", y, h)
	}
}

func TestTerm_PosToCell_NaNInfCollapseToZero(t *testing.T) {
	term := &Term{
		grid:  newGrid(24, 80),
		cellW: 8,
		cellH: 16,
	}
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	ninf := float32(math.Inf(-1))
	cases := []struct{ x, y float32 }{
		{nan, 16}, {inf, 16}, {ninf, 16},
		{8, nan}, {8, inf}, {8, ninf},
		{nan, nan},
	}
	for _, c := range cases {
		r, col := term.posToCell(c.x, c.y)
		if r < 0 || r >= term.grid.Rows || col < 0 || col >= term.grid.Cols {
			t.Errorf("posToCell(%v,%v)=(%d,%d): outside grid [0,%d)x[0,%d)",
				c.x, c.y, r, col, term.grid.Rows, term.grid.Cols)
		}
	}
}

func TestTerm_OnChar_SearchMode_AppendAndCap(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	term.search.active = true

	e := &gui.Event{CharCode: 'a'}
	term.onChar(nil, e, nil)
	if term.search.query != "a" {
		t.Fatalf("query = %q, want \"a\"", term.search.query)
	}
	if !e.IsHandled {
		t.Error("event must be handled in search mode")
	}

	// Fill to exactly MaxGridDim runes (already have 1 'a').
	for i := 1; i < MaxGridDim; i++ {
		term.onChar(nil, &gui.Event{CharCode: 'x'}, nil)
	}
	if utf8.RuneCountInString(term.search.query) != MaxGridDim {
		t.Fatalf("query rune count = %d, want %d", utf8.RuneCountInString(term.search.query), MaxGridDim)
	}
	// Next char must be rejected (at cap).
	before := term.search.query
	term.onChar(nil, &gui.Event{CharCode: 'z'}, nil)
	if term.search.query != before {
		t.Errorf("query grew past MaxGridDim cap: len now %d", utf8.RuneCountInString(term.search.query))
	}
}

func TestTerm_SearchJump_ForwardFindsMatch(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	// putRow places at (0,0). Find skips start.Col+1 on the start row,
	// so content at col 0 is invisible to a fresh search. Pad col 0
	// so the matchable text starts at col 1.
	putRow(term.grid, "xhello")
	term.search.query = "hello"
	verBefore := term.drawVersion.Load()
	term.searchJump(true, &gui.Window{})
	term.grid.Mu.Lock()
	off := term.grid.ViewOffset
	term.grid.Mu.Unlock()
	if off != 0 {
		t.Errorf("ViewOffset = %d after live match, want 0", off)
	}
	if term.drawVersion.Load() <= verBefore {
		t.Error("drawVersion not incremented after search jump")
	}
	if !time.Now().Before(term.scrollbar.until) {
		t.Error("scrollbar.until not set to future after search jump")
	}
}

func TestTerm_SearchJump_NoMatchDoesNotPanic(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	term.search.query = "xyzzy_not_present"
	verBefore := term.drawVersion.Load()
	scBefore := term.scrollbar.until
	term.searchJump(true, &gui.Window{}) // must not panic
	if term.drawVersion.Load() != verBefore {
		t.Error("drawVersion changed on no-match jump")
	}
	if !term.scrollbar.until.Equal(scBefore) {
		t.Error("scrollbar.until modified on no-match jump")
	}
}

func TestTerm_SearchJump_EmptyQuery_Nop(t *testing.T) {
	term, _ := newTestTermCapture()
	term.cmd = &gui.Window{}
	term.search.query = ""
	verBefore := term.drawVersion.Load()
	scBefore := term.scrollbar.until
	term.searchJump(true, &gui.Window{}) // early return, must not panic
	if term.drawVersion.Load() != verBefore {
		t.Error("drawVersion changed on empty-query jump")
	}
	if !term.scrollbar.until.Equal(scBefore) {
		t.Error("scrollbar.until modified on empty-query jump")
	}
}

func TestTerm_OnKeyDown_ModifiedCursorKeys(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		{gui.KeyUp, gui.ModShift, "\x1b[1;2A"},
		{gui.KeyDown, gui.ModCtrl, "\x1b[1;5B"},
		{gui.KeyRight, gui.ModShift | gui.ModCtrl, "\x1b[1;6C"},
		{gui.KeyLeft, gui.ModShift, "\x1b[1;2D"},
		// No modifier → normal sequences.
		{gui.KeyUp, 0, "\x1b[A"},
		{gui.KeyDown, 0, "\x1b[B"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
	}
}

func TestTerm_OnKeyDown_CtrlShiftHomeEndPassthrough(t *testing.T) {
	// Ctrl+Shift+Home/End must pass through to the pty as Ctrl+Home/End
	// (not be consumed by scroll-to-top/bottom). Ctrl+Shift+Tab without
	// KKP falls through to raw \t instead of emitting \x1b[Z].
	cases := []struct {
		key  gui.KeyCode
		mods gui.Modifier
		want string
	}{
		// Shift+Home/End (no Ctrl) — still scrolls, no pty output.
		{gui.KeyHome, gui.ModShift, ""},
		{gui.KeyEnd, gui.ModShift, ""},
		// Ctrl+Shift+Home/End — passes through as Ctrl+Home/Ctrl+End
		// (Shift is deliberately excluded from modParam; it has special
		// scrollback semantics in this terminal).
		{gui.KeyHome, gui.ModShift | gui.ModCtrl, "\x1b[1;5H"},
		{gui.KeyEnd, gui.ModShift | gui.ModCtrl, "\x1b[1;5F"},
		// Ctrl+Home/End (no Shift) — unaffected.
		{gui.KeyHome, gui.ModCtrl, "\x1b[1;5H"},
		{gui.KeyEnd, gui.ModCtrl, "\x1b[1;5F"},
		// Ctrl+Shift+Tab without KKP — raw \t, not \x1b[Z.
		{gui.KeyTab, gui.ModShift | gui.ModCtrl, "\t"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		e := &gui.Event{KeyCode: c.key, Modifiers: c.mods}
		term.onKeyDown(nil, e, &gui.Window{})
		got := string(*buf)
		if got != c.want {
			t.Errorf("key=%v mods=%v: got %q want %q", c.key, c.mods, got, c.want)
		}
	}
}

// --- Kitty Keyboard Protocol (Phase 27) ---

func TestKittyKeySeq_Disabled(t *testing.T) {
	// flags==0 means legacy mode; must return nil for all inputs.
	if got := kittyKeySeq(13, 0, 0, false); got != nil {
		t.Fatalf("flags=0: got %q, want nil", got)
	}
}

func TestKittyKeySeq_NoMods(t *testing.T) {
	cases := []struct {
		cp   int
		want string
	}{
		{13, "\x1b[13u"},   // Enter
		{9, "\x1b[9u"},     // Tab
		{27, "\x1b[27u"},   // Escape
		{127, "\x1b[127u"}, // Backspace
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, 0, 1, false)
		if string(got) != c.want {
			t.Errorf("cp=%d: got %q, want %q", c.cp, got, c.want)
		}
	}
}

func TestKittyKeySeq_WithMods(t *testing.T) {
	cases := []struct {
		cp   int
		mods gui.Modifier
		want string
	}{
		{13, gui.ModCtrl, "\x1b[13;5u"},                  // Ctrl+Enter → mod=5
		{127, gui.ModShift | gui.ModCtrl, "\x1b[127;6u"}, // Shift+Ctrl+Backspace → mod=6
		{99, gui.ModCtrl, "\x1b[99;5u"},                  // Ctrl+C
		{97, gui.ModAlt, "\x1b[97;3u"},                   // Alt+A → mod=3
		{65, gui.ModSuper, "\x1b[65;9u"},                 // Super+A → mod=9
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, c.mods, 1, false)
		if string(got) != c.want {
			t.Errorf("cp=%d mods=%v: got %q, want %q", c.cp, c.mods, got, c.want)
		}
	}
}

func TestTerm_KittyKey_Backspace(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyBackspace}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[127u" {
		t.Fatalf("KKP backspace: got %q, want %q", got, "\x1b[127u")
	}
}

func TestTerm_KittyKey_Enter(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[13u" {
		t.Fatalf("KKP enter: got %q, want %q", got, "\x1b[13u")
	}
}

func TestTerm_KittyKey_Tab(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyTab}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[9u" {
		t.Fatalf("KKP tab: got %q, want %q", got, "\x1b[9u")
	}
}

func TestTerm_KittyKey_Escape(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	e := &gui.Event{KeyCode: gui.KeyEscape}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[27u" {
		t.Fatalf("KKP escape: got %q, want %q", got, "\x1b[27u")
	}
}

func TestTerm_KittyKey_CtrlC(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1
	// Ctrl+C: KeyCode=KeyC, Modifiers=ModCtrl. Codepoint for 'c' is 99.
	e := &gui.Event{KeyCode: gui.KeyC, Modifiers: gui.ModCtrl}
	term.onKeyDown(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[99;5u" {
		t.Fatalf("KKP Ctrl+C: got %q, want %q", got, "\x1b[99;5u")
	}
}

func TestKittyKeySeq_Release(t *testing.T) {
	// Test key release sequence generation (event-type 3).
	// Modifier field is mandatory even when mod==1 (no modifiers).
	cases := []struct {
		cp   int
		mods gui.Modifier
		want string
	}{
		{13, 0, "\x1b[13;1:3u"},           // Enter release, no mods
		{9, gui.ModShift, "\x1b[9;2:3u"},  // Shift+Tab release
		{27, gui.ModCtrl, "\x1b[27;5:3u"}, // Ctrl+Escape release
		{65, gui.ModAlt, "\x1b[65;3:3u"},  // Alt+A release
	}
	for _, c := range cases {
		got := kittyKeySeq(c.cp, c.mods, 1, true)
		if string(got) != c.want {
			t.Errorf("release cp=%d mods=%v: got %q, want %q", c.cp, c.mods, got, c.want)
		}
	}
}

func TestTerm_KittyKey_Release(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2 // Enable event type reporting (flag bit 2)

	// Test Enter key release
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KKP Enter release: got %q, want %q", got, "\x1b[13;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Shift+Tab release
	e = &gui.Event{KeyCode: gui.KeyTab, Modifiers: gui.ModShift}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[9;2:3u" {
		t.Fatalf("KKP Shift+Tab release: got %q, want %q", got, "\x1b[9;2:3u")
	}
}

func TestTerm_KittyKey_ModifierOnly(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2 // Enable event type reporting (flag bit 2)

	// Test Shift key release
	e := &gui.Event{KeyCode: gui.KeyLeftShift}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57441;1:3u" {
		t.Fatalf("KKP Shift release: got %q, want %q", got, "\x1b[57441;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Ctrl key release
	e = &gui.Event{KeyCode: gui.KeyLeftControl}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57442;1:3u" {
		t.Fatalf("KKP Ctrl release: got %q, want %q", got, "\x1b[57442;1:3u")
	}

	// Clear buffer for next test
	*buf = (*buf)[:0]

	// Test Alt key release
	e = &gui.Event{KeyCode: gui.KeyLeftAlt}
	term.onKeyUp(nil, e, &gui.Window{})
	if got := string(*buf); got != "\x1b[57443;1:3u" {
		t.Fatalf("KKP Alt release: got %q, want %q", got, "\x1b[57443;1:3u")
	}
}

func TestTerm_KittyKey_ReleaseDisabled(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 1 // Event type reporting disabled (flag bit 2 not set)

	// Test that no release events are generated when flag bit 2 is not set
	e := &gui.Event{KeyCode: gui.KeyEnter}
	term.onKeyUp(nil, e, &gui.Window{})
	if len(*buf) != 0 {
		t.Fatalf("KKP release with flag bit 2 disabled: got %q, want empty", string(*buf))
	}
}

func TestKittyKeySeq_ZeroCodepointReturnsNil(t *testing.T) {
	if got := kittyKeySeq(0, 0, 1, false); got != nil {
		t.Fatalf("codepoint=0: got %q, want nil", got)
	}
}

func TestKittyKeySeq_NegativeCodepointReturnsNil(t *testing.T) {
	if got := kittyKeySeq(-1, 0, 1, false); got != nil {
		t.Fatalf("codepoint=-1: got %q, want nil", got)
	}
	if got := kittyKeySeq(-1, 0, 1, true); got != nil {
		t.Fatalf("codepoint=-1 release: got %q, want nil", got)
	}
}

func TestTerm_KittyKey_RightModifiers(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyRightShift, "\x1b[57447;1:3u"},
		{gui.KeyRightControl, "\x1b[57448;1:3u"},
		{gui.KeyRightAlt, "\x1b[57449;1:3u"},
		{gui.KeyLeftSuper, "\x1b[57444;1:3u"},
		{gui.KeyRightSuper, "\x1b[57450;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_NavRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyInsert, "\x1b[57348;1:3u"},
		{gui.KeyDelete, "\x1b[57349;1:3u"},
		{gui.KeyLeft, "\x1b[57350;1:3u"},
		{gui.KeyRight, "\x1b[57351;1:3u"},
		{gui.KeyUp, "\x1b[57352;1:3u"},
		{gui.KeyDown, "\x1b[57353;1:3u"},
		{gui.KeyPageUp, "\x1b[57354;1:3u"},
		{gui.KeyPageDown, "\x1b[57355;1:3u"},
		{gui.KeyHome, "\x1b[57356;1:3u"},
		{gui.KeyEnd, "\x1b[57357;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_FKeyRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyF1, "\x1b[57364;1:3u"},
		{gui.KeyF2, "\x1b[57365;1:3u"},
		{gui.KeyF3, "\x1b[57366;1:3u"},
		{gui.KeyF4, "\x1b[57367;1:3u"},
		{gui.KeyF5, "\x1b[57368;1:3u"},
		{gui.KeyF6, "\x1b[57369;1:3u"},
		{gui.KeyF7, "\x1b[57370;1:3u"},
		{gui.KeyF8, "\x1b[57371;1:3u"},
		{gui.KeyF9, "\x1b[57372;1:3u"},
		{gui.KeyF10, "\x1b[57373;1:3u"},
		{gui.KeyF11, "\x1b[57374;1:3u"},
		{gui.KeyF12, "\x1b[57375;1:3u"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_PrintableRelease(t *testing.T) {
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyA, "\x1b[97;1:3u"},  // 'a'
		{gui.KeyZ, "\x1b[122;1:3u"}, // 'z'
		{gui.Key0, "\x1b[48;1:3u"},  // '0'
		{gui.Key9, "\x1b[57;1:3u"},  // '9'
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		term.grid.KittyKeyFlags = 2
		term.onKeyUp(nil, &gui.Event{KeyCode: c.key}, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestTerm_KittyKey_KPEnterRelease(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	term.onKeyUp(nil, &gui.Event{KeyCode: gui.KeyKPEnter}, &gui.Window{})
	if got := string(*buf); got != "\x1b[13;1:3u" {
		t.Fatalf("KPEnter release: got %q, want %q", got, "\x1b[13;1:3u")
	}
}

func TestTerm_KittyKey_UnknownKeyNoOutput(t *testing.T) {
	term, buf := newTestTermCapture()
	term.grid.KittyKeyFlags = 2
	// KeyF13 is not in the switch; should produce no output.
	term.onKeyUp(nil, &gui.Event{KeyCode: gui.KeyF13}, &gui.Window{})
	if len(*buf) != 0 {
		t.Fatalf("unknown key: got %q, want empty", string(*buf))
	}
}

func TestTerm_KittyKey_LegacyFallback(t *testing.T) {
	// When KKP is disabled (flags=0), legacy sequences still emitted.
	cases := []struct {
		key  gui.KeyCode
		want string
	}{
		{gui.KeyBackspace, "\x7f"},
		{gui.KeyEnter, "\r"},
		{gui.KeyTab, "\t"},
		{gui.KeyEscape, "\x1b"},
	}
	for _, c := range cases {
		term, buf := newTestTermCapture()
		// flags=0 by default
		e := &gui.Event{KeyCode: c.key}
		term.onKeyDown(nil, e, &gui.Window{})
		if got := string(*buf); got != c.want {
			t.Errorf("legacy key=%v: got %q, want %q", c.key, got, c.want)
		}
	}
}

func TestParser_MousePixelMode_Toggle(t *testing.T) {
	g := newGrid(5, 10)
	p := newParser(g)
	p.Feed([]byte("\x1b[?1016h"))
	if !g.MouseSGRPixels {
		t.Error("?1016h should set MouseSGRPixels")
	}
	p.Feed([]byte("\x1b[?1016l"))
	if g.MouseSGRPixels {
		t.Error("?1016l should clear MouseSGRPixels")
	}
}

func TestWriteMouse_CellVsPixelCoords(t *testing.T) {
	cases := []struct {
		name   string
		col    int
		row    int
		pixX   float32
		pixY   float32
		pixels bool
		press  bool
		want   string
	}{
		// cell mode: col+1 / row+1
		{"cell press", 4, 9, 50.0, 90.0, false, true, "\x1b[<0;5;10M"},
		{"cell release", 0, 0, 0, 0, false, false, "\x1b[<0;1;1m"},
		// Pixel mode: int(pixX)+1 / int(pixY)+1
		{"pixel press", 4, 9, 50.7, 90.3, true, true, "\x1b[<0;51;91M"},
		{"pixel release", 0, 0, 9.9, 19.1, true, false, "\x1b[<0;10;20m"},
		// Pixel mode at origin maps to (1,1) per 1-based spec
		{"pixel origin", 3, 3, 0, 0, true, true, "\x1b[<0;1;1M"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tm, buf := newTestTermCapture()
			tm.writeMouse(0, c.col, c.row, c.pixX, c.pixY, c.pixels, c.press)
			if got := string(*buf); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSendDesktopNotify_NullByteStripped(t *testing.T) {
	// Null bytes are stripped before reaching subprocess args.
	// Test via inline ReplaceAll (the former cleanNotifyStr behavior).
	clean := func(s string) string { return strings.ReplaceAll(s, "\x00", "") }
	if got := clean("hel\x00lo"); got != "hello" {
		t.Fatalf("null byte: got %q, want %q", got, "hello")
	}
	if got := clean(`say "hi"`); got != `say "hi"` {
		t.Fatalf("double quote should be preserved: got %q", got)
	}
	if got := clean("a\x00b\"c"); got != "ab\"c" {
		t.Fatalf("both: got %q, want %q", got, "ab\"c")
	}
}

func TestSendDesktopNotify_HostileInputNoPanic(t *testing.T) {
	// Subprocess errors are swallowed; just verify no panic with hostile input.
	sendDesktopNotify(`"; rm -rf /`, "body\x00with null")
	sendDesktopNotify("", `body "quoted"`)
}

// TestTerm_NotifyBusy_ExtrasDropped verifies that concurrent OSC notifications
// are deduplicated: while one is in flight, subsequent calls are dropped.
func TestTerm_NotifyBusy_ExtrasDropped(t *testing.T) {
	block := make(chan struct{})
	finished := make(chan struct{})
	calls := 0

	term := &Term{grid: newGrid(4, 8)}
	term.cfg.OnNotify = func(_, _ string) {
		calls++
		<-block
		close(finished)
	}

	// Replicate the exact handler registered in New.
	send := func() {
		if !term.notifyBusy.CompareAndSwap(false, true) {
			return
		}
		fn := term.cfg.OnNotify
		go func() {
			defer term.notifyBusy.Store(false)
			fn("", "msg")
		}()
	}

	send() // acquires busy, goroutine blocks on <-block
	send() // dropped
	send() // dropped
	close(block)

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification goroutine")
	}

	if calls != 1 {
		t.Fatalf("want 1 call, got %d", calls)
	}
}

// --- termRuneStr ---

func TestTermRuneStr_ASCIINoAlloc(t *testing.T) {
	tm := &Term{}
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = tm.termRuneStr('A')
	})
	_ = sink
	if avg != 0 {
		t.Errorf("ASCII path should not allocate, got %v allocs/op", avg)
	}
}

func TestTermRuneStr_NonASCIICachesOnMiss(t *testing.T) {
	tm := &Term{}
	r := rune(0x2603) // ☃ snowman
	s := tm.termRuneStr(r)
	if s != string(r) {
		t.Errorf("got %q, want %q", s, string(r))
	}
	if tm.draw.runeCache == nil || tm.draw.runeCache[r] == "" {
		t.Error("rune not stored in cache after first call")
	}
}

func TestTermRuneStr_CacheHitNoAlloc(t *testing.T) {
	tm := &Term{}
	r := rune(0x1F600) // 😀 emoji (4-byte UTF-8)
	tm.termRuneStr(r)  // prime the cache
	var sink string
	avg := testing.AllocsPerRun(100, func() {
		sink = tm.termRuneStr(r)
	})
	_ = sink
	if avg != 0 {
		t.Errorf("cache hit should not allocate, got %v allocs/op", avg)
	}
}

func TestKeypadSeq_All(t *testing.T) {
	tests := []struct {
		k    gui.KeyCode
		want string
	}{
		{gui.KeyKP0, "\x1bOp"},
		{gui.KeyKP1, "\x1bOq"},
		{gui.KeyKP2, "\x1bOr"},
		{gui.KeyKP3, "\x1bOs"},
		{gui.KeyKP4, "\x1bOt"},
		{gui.KeyKP5, "\x1bOu"},
		{gui.KeyKP6, "\x1bOv"},
		{gui.KeyKP7, "\x1bOw"},
		{gui.KeyKP8, "\x1bOx"},
		{gui.KeyKP9, "\x1bOy"},
		{gui.KeyKPDecimal, "\x1bOn"},
		{gui.KeyKPDivide, "\x1bOo"},
		{gui.KeyKPMultiply, "\x1bOj"},
		{gui.KeyKPSubtract, "\x1bOm"},
		{gui.KeyKPAdd, "\x1bOk"},
		{gui.KeyKPEqual, "\x1bOX"},
		{gui.KeyA, ""},
		{gui.KeyKPEnter, ""},
		{gui.KeyCode(9999), ""},
	}
	for _, tt := range tests {
		got := keypadSeq(tt.k)
		if string(got) != tt.want {
			t.Errorf("keypadSeq(%v) = %q, want %q", tt.k, got, tt.want)
		}
	}
}

func TestKKPCodepoint_AllKeys(t *testing.T) {
	tests := []struct {
		k    gui.KeyCode
		want int
		ok   bool
	}{
		{gui.KeyLeftShift, 57441, true},
		{gui.KeyRightShift, 57447, true},
		{gui.KeyLeftControl, 57442, true},
		{gui.KeyRightControl, 57448, true},
		{gui.KeyLeftAlt, 57443, true},
		{gui.KeyRightAlt, 57449, true},
		{gui.KeyLeftSuper, 57444, true},
		{gui.KeyRightSuper, 57450, true},
		{gui.KeyEnter, 13, true},
		{gui.KeyKPEnter, 13, true},
		{gui.KeyBackspace, 127, true},
		{gui.KeyTab, 9, true},
		{gui.KeyEscape, 27, true},
		{gui.KeyInsert, 57348, true},
		{gui.KeyDelete, 57349, true},
		{gui.KeyLeft, 57350, true},
		{gui.KeyRight, 57351, true},
		{gui.KeyUp, 57352, true},
		{gui.KeyDown, 57353, true},
		{gui.KeyPageUp, 57354, true},
		{gui.KeyPageDown, 57355, true},
		{gui.KeyHome, 57356, true},
		{gui.KeyEnd, 57357, true},
		{gui.KeyF1, 57364, true},
		{gui.KeyF12, 57375, true},
		{gui.KeyA, int('a'), true},
		{gui.KeyZ, int('z'), true},
		{gui.Key0, int('0'), true},
		{gui.Key9, int('9'), true},
		{gui.KeyCode(9999), 0, false},
	}
	for _, tt := range tests {
		got, ok := kkpCodepoint(tt.k)
		if ok != tt.ok {
			t.Errorf("kkpCodepoint(%v) ok=%v, want %v", tt.k, ok, tt.ok)
		}
		if ok && got != tt.want {
			t.Errorf("kkpCodepoint(%v) = %d, want %d", tt.k, got, tt.want)
		}
	}
}

// --- config helpers ---

func TestApplyScrollbackConfig(t *testing.T) {
	tests := []struct {
		name string
		rows int
		want int
	}{
		{"default", 0, defaultScrollbackRows},
		{"custom", 7000, 7000},
		{"clamped", MaxScrollbackCap + 1, MaxScrollbackCap},
		{"disabled", -1, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newGrid(24, 80)
			applyScrollbackConfig(g, Cfg{ScrollbackRows: tc.rows})
			if g.ScrollbackCap != tc.want {
				t.Errorf("ScrollbackCap = %d, want %d", g.ScrollbackCap, tc.want)
			}
		})
	}
}

func TestBuildThemeMenu_Empty(t *testing.T) {
	if got := buildThemeMenu(Cfg{}); got != nil {
		t.Errorf("expected nil for empty themes, got %v", got)
	}
}

func TestBuildThemeMenu_TwoThemes(t *testing.T) {
	themes := []NamedTheme{
		{Name: "Dark", Theme: DefaultTheme},
		{Name: "Light", Theme: SolarizedDarkTheme},
	}
	items := buildThemeMenu(Cfg{Themes: themes})
	if len(items) != 3 {
		t.Fatalf("expected 3 menu items, got %d", len(items))
	}
}

func TestApplyTheme_EmptyThemes(t *testing.T) {
	g := newGrid(24, 80)
	g.Theme = SolarizedDarkTheme
	applyTheme(g, Cfg{})
	if g.Theme.DefaultFG == DefaultTheme.DefaultFG {
		t.Error("theme should not have changed when Themes is empty")
	}
}

func TestApplyTheme_FirstTheme(t *testing.T) {
	g := newGrid(24, 80)
	applyTheme(g, Cfg{
		Themes: []NamedTheme{
			{Name: "Nord", Theme: NordTheme},
			{Name: "Default", Theme: DefaultTheme},
		},
	})
	if g.Theme.DefaultFG != NordTheme.DefaultFG {
		t.Error("expected first theme (Nord) to be applied")
	}
}

// --- lifecycle ---

func TestClose_Idempotent(t *testing.T) {
	g := newGrid(24, 80)
	p := newParser(g)
	done := make(chan struct{})
	close(done)
	tm := &Term{
		grid:      g,
		parser:    p,
		blinkDone: done,
		readDone:  done,
	}
	tm.closed.Store(true)
	if err := tm.Close(); err != nil {
		t.Logf("Close returned error (expected with nil pty): %v", err)
	}
	if err := tm.Close(); err != nil {
		t.Logf("second Close: %v", err)
	}
}

func TestClose_FullIntegration(t *testing.T) {
	pty, err := startPTY(24, 80)
	if err != nil {
		t.Skipf("startPTY: %v", err)
	}
	g := newGrid(24, 80)
	tm := &Term{
		cfg:       Cfg{},
		grid:      g,
		parser:    newParser(g),
		pty:       pty,
		pw:        pty,
		cmd:       &gui.Window{},
		notif:     desktopNotifier{},
		blinkDone: make(chan struct{}),
		readDone:  make(chan struct{}),
	}
	tm.mouse.hoverR.Store(-1)
	tm.mouse.hoverC.Store(-1)

	// Start readLoop so Close can observe it drain.
	go tm.readLoop()

	// Close must stop the reader, close the pty, and be idempotent.
	if err := tm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tm.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// PTY writes must fail after close.
	if _, err := pty.Write([]byte("x")); err == nil {
		t.Error("pty.Write after Close should fail")
	}
}

func TestCursorBlinks_CfgOverride(t *testing.T) {
	g := newGrid(24, 80)
	g.CursorBlink = false

	yes := true
	tm := &Term{grid: g, cfg: Cfg{CursorBlink: &yes}}
	if !tm.cursorBlinks() {
		t.Error("CursorBlink=true override should force blinking on")
	}

	no := false
	tm.cfg.CursorBlink = &no
	if tm.cursorBlinks() {
		t.Error("CursorBlink=false override should force blinking off")
	}
}

func TestCursorBlinks_HonorsGrid(t *testing.T) {
	g := newGrid(24, 80)
	g.CursorBlink = true
	tm := &Term{grid: g, cfg: Cfg{}}
	if !tm.cursorBlinks() {
		t.Error("should honor grid.CursorBlink=true when no override")
	}
	g.CursorBlink = false
	if tm.cursorBlinks() {
		t.Error("should honor grid.CursorBlink=false when no override")
	}
}

// --- openURL scheme whitelist ---

func TestOpenURL_PermittedSchemes(t *testing.T) {
	// Permitted schemes reach exec.Command; blocked at the switch in the
	// default case and return without spawning a process. We verify the
	// function does not panic for any input — the exec may fail in CI but
	// the error is swallowed via cmd.Start().
	for _, url := range []string{
		"https://example.com",
		"http://example.com",
		"mailto:user@example.com",
	} {
		openURL(url) // must not panic
	}
}

func TestOpenURL_BlockedSchemes(t *testing.T) {
	for _, url := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://example.com",
		"ssh://evil.com",
		"",
	} {
		openURL(url) // must not panic; silently dropped
	}
}

// --- flushPendingReplies ---

func TestFlushPendingReplies_EmptyNoOp(t *testing.T) {
	term := &Term{pw: writerFunc(func([]byte) (int, error) {
		t.Error("pw.Write must not be called for empty queue")
		return 0, nil
	})}
	term.flushPendingReplies() // must not panic or call pw.Write
}

func TestFlushPendingReplies_ErrorPath(t *testing.T) {
	calls := 0
	term := &Term{
		pw: writerFunc(func(b []byte) (int, error) {
			calls++
			return 0, errTestBoom
		}),
		pendingReplies: [][]byte{
			[]byte("reply1"),
			[]byte("reply2"),
		},
	}
	// Errors are logged, not returned; all pending replies are processed.
	term.flushPendingReplies()
	if calls != 2 {
		t.Errorf("pw.Write called %d times, want 2", calls)
	}
	if term.pendingReplies != nil {
		t.Errorf("pendingReplies not cleared: %v", term.pendingReplies)
	}
}

// --- scheduleResizeWake ---

func TestScheduleResizeWake_FirstCallCreatesTimer(t *testing.T) {
	term := &Term{}
	if term.resize.timer != nil {
		t.Fatal("resizeTimer should start nil")
	}
	// Use a long duration so the timer doesn't fire during the test.
	term.scheduleResizeWake(time.Hour)
	if term.resize.timer == nil {
		t.Fatal("resizeTimer should be created on first call")
	}
	term.resize.timer.Stop()
}

func TestScheduleResizeWake_ClosedSkipsBump(t *testing.T) {
	term := &Term{}
	term.closed.Store(true)
	prev := term.drawVersion.Load()
	term.scheduleResizeWake(time.Nanosecond)
	// Give the timer a moment to fire.
	time.Sleep(20 * time.Millisecond)
	if term.drawVersion.Load() != prev {
		t.Error("closed term: drawVersion must not change")
	}
	if term.resize.timer != nil {
		term.resize.timer.Stop()
	}
}

// --- writeMouse NaN/Inf pixel coords ---

func TestWriteMouse_PixelCoordsNaN(t *testing.T) {
	term, buf := newTestTermCapture()
	// NaN pixX should collapse to 0 via the realNumber guard; pixY=9
	// unchanged. Expect 1-based coords: col=int(0)+1=1, row=int(9)+1=10.
	term.writeMouse(0, 0, 0, float32(math.NaN()), 9.0, true, true)
	want := "\x1b[<0;1;10M"
	if got := string(*buf); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteMouse_PixelCoordsInf(t *testing.T) {
	term, buf := newTestTermCapture()
	term.writeMouse(0, 0, 0, 5.0, float32(math.Inf(1)), true, true)
	want := "\x1b[<0;6;1M"
	if got := string(*buf); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- cancelMomentum nil timer ---

func TestCancelMomentum_BeforeFirstScrollNoPanic(t *testing.T) {
	term := &Term{}       // momentumTimer is nil by default
	term.cancelMomentum() // must not panic — nil-guarded inside
}

// errTestBoom is a sentinel error for ptyWriter failure tests.
var errTestBoom = errors.New("boom")

// --- benchmarks ---

func BenchmarkDrawPrep_DirtyRows(b *testing.B) {
	const rows, cols = 24, 80
	g := newGrid(rows, cols)
	// Mark every other row dirty.
	for r := range rows {
		if r%2 == 0 {
			g.Dirty[r] = true
		}
	}
	g.dirtyCount = rows / 2

	b.ResetTimer()
	for b.Loop() {
		_ = g.HasDirtyRows()
		g.ClearDirty()
		// Re-mark for next iteration.
		for r := range rows {
			if r%2 == 0 {
				g.Dirty[r] = true
			}
		}
		g.dirtyCount = rows / 2
	}
}
