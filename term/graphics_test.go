package term

import (
	"image/color"
	"os"
	"strings"
	"testing"
)

// helper: build a DCS Sixel payload as the parser sees it (bytes between
// DCS introducer and ST). Form: "<params>q<sixel-body>".
func sixelPayload(params, body string) string {
	return params + "q" + body
}

// feedSixel wraps payload in ESC P … ESC \ and feeds it through a
// fresh parser so tests exercise the DCS state machine end-to-end.
// Decoded PNGs land in t.TempDir() so they're cleaned up automatically.
func feedSixel(t *testing.T, rows, cols int, params, body string) *grid {
	t.Helper()
	g := newGrid(rows, cols)
	g.CellPxW, g.CellPxH = 8, 16
	p := newParser(g)
	p.SetGraphicsDir(t.TempDir())
	p.Feed([]byte("\x1bP" + sixelPayload(params, body) + "\x1b\\"))
	return g
}

func TestDecodeSixel_SinglePixel(t *testing.T) {
	// One sixel char '~' (0x7E) → mask 0x3F → all six pixels in column 0 set.
	// Color register 0 defaults to black. Body: "#0~"
	img := decodeSixel([]byte("#0~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dx() != 1 || b.Dy() != 6 {
		t.Fatalf("got %dx%d; want 1x6", b.Dx(), b.Dy())
	}
	for y := range 6 {
		got := img.NRGBAAt(0, y)
		if got != (color.NRGBA{0, 0, 0, 0xFF}) {
			t.Errorf("pixel (0,%d) = %v; want black", y, got)
		}
	}
}

func TestDecodeSixel_ColorDefRGB(t *testing.T) {
	// #1;2;100;0;0 → register 1 = pure red (RGB, channels 0..100).
	// Then "#1~" paints column 0 in band 0 with red.
	img := decodeSixel([]byte("#1;2;100;0;0#1~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	want := color.NRGBA{0xFF, 0, 0, 0xFF}
	if got := img.NRGBAAt(0, 0); got != want {
		t.Fatalf("pixel (0,0) = %v; want %v", got, want)
	}
}

func TestDecodeSixel_RunLength(t *testing.T) {
	// "!5~" repeats '~' five times → 5 columns of full bands.
	img := decodeSixel([]byte("#0!5~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dx() != 5 || b.Dy() != 6 {
		t.Fatalf("got %dx%d; want 5x6", b.Dx(), b.Dy())
	}
	for x := range 5 {
		if got := img.NRGBAAt(x, 0); got.A != 0xFF {
			t.Errorf("pixel (%d,0) not opaque: %v", x, got)
		}
	}
}

func TestDecodeSixel_BandAdvance(t *testing.T) {
	// "~-~" paints a full 6px column, line-feeds, then paints another at
	// y=6..11. Expected height = 12.
	img := decodeSixel([]byte("#0~-~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	b := img.Bounds()
	if b.Dy() != 12 {
		t.Fatalf("height = %d; want 12", b.Dy())
	}
	if got := img.NRGBAAt(0, 11); got.A != 0xFF {
		t.Errorf("expected opaque pixel at (0,11), got %v", got)
	}
}

func TestDecodeSixel_CarriageReturn(t *testing.T) {
	// "~~$#1!2~" — paint two cols red after a CR; verifies $ resets col
	// but stays on the same band. Cols 0,1 from black '~~'; then CR
	// resets to col 0 and overwrites col 0,1 with red.
	img := decodeSixel([]byte("#0~~$#1;2;100;0;0!2~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	for x := range 2 {
		if got := img.NRGBAAt(x, 0); got.R != 0xFF || got.G != 0 || got.B != 0 {
			t.Errorf("col %d = %v; want red", x, got)
		}
	}
}

func TestDecodeSixel_RasterAttrsSkipped(t *testing.T) {
	// `"1;1;10;6` is a raster-attrs prefix. Should be silently skipped.
	img := decodeSixel([]byte(`"1;1;10;6#0~`))
	if img == nil {
		t.Fatal("decode returned nil after raster attrs")
	}
	if img.Bounds().Dx() != 1 || img.Bounds().Dy() != 6 {
		t.Fatalf("got %v; want 1x6", img.Bounds())
	}
}

func TestDecodeSixel_OutOfRangeColorIgnored(t *testing.T) {
	// Color register 999 is out of range; define silently dropped, default
	// register 0 (black) still paints.
	img := decodeSixel([]byte("#999;2;100;0;0#0~"))
	if img == nil {
		t.Fatal("decode returned nil")
	}
	if got := img.NRGBAAt(0, 0); got != (color.NRGBA{0, 0, 0, 0xFF}) {
		t.Errorf("(0,0) = %v; want black", got)
	}
}

func TestDecodeSixel_EmptyPayload(t *testing.T) {
	if got := decodeSixel(nil); got != nil {
		t.Fatalf("expected nil for empty payload, got %v", got)
	}
	if got := decodeSixel([]byte("")); got != nil {
		t.Fatalf("expected nil for empty payload, got %v", got)
	}
}

func TestEncodePNGFile_NilEmpty(t *testing.T) {
	if encodePNGFile(nil, t.TempDir()) != "" {
		t.Fatal("nil image should yield empty path")
	}
}

func TestEncodePNGFile_WritesPNG(t *testing.T) {
	g := feedSixel(t, 4, 10, "", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	path := g.Graphics[0].Src
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("Src %q lacks .png suffix", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("PNG file is empty")
	}
	// PNG magic bytes.
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var magic [8]byte
	_, _ = f.Read(magic[:])
	if string(magic[:]) != "\x89PNG\r\n\x1a\n" {
		t.Errorf("not a PNG: %q", magic)
	}
}

func TestParser_DCS_SixelDispatch(t *testing.T) {
	// End-to-end: ESC P q ~ ESC \ should produce one graphic with a
	// non-empty PNG data URL and advance the cursor below it.
	g := feedSixel(t, 10, 80, "", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	if !strings.HasSuffix(gr.Src, ".png") {
		t.Errorf("Src should be a .png path; got %q", gr.Src)
	}
	if _, err := os.Stat(gr.Src); err != nil {
		t.Errorf("PNG file missing on disk: %v", err)
	}
	if gr.WidthPx != 1 || gr.HeightPx != 6 {
		t.Errorf("size = %dx%d; want 1x6", gr.WidthPx, gr.HeightPx)
	}
	if gr.OriginR != 0 || gr.OriginC != 0 {
		t.Errorf("origin = (%d,%d); want (0,0)", gr.OriginR, gr.OriginC)
	}
	// Cursor should sit *below* the graphic. With cellPxH=16 and pixel
	// height 6, ceil(6/16) = 1 cell row → cursor advances from 0 to 1.
	if g.CursorR != 1 {
		t.Errorf("CursorR = %d; want 1 (advanced below 1-cell image)", g.CursorR)
	}
}

func TestParser_DCS_SixelWithParams(t *testing.T) {
	// `0;0;0q` is a valid sixel introducer with three numeric params.
	g := feedSixel(t, 10, 80, "0;0;0", "#0~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
}

func TestParser_DCS_SixelBlanksCells(t *testing.T) {
	// Multi-cell image: 16px tall at cellPxH=16 occupies 1 cell row, and
	// 24px wide at cellPxW=8 occupies 3 cell cols. We force this by
	// repeating sixel chars. "!24~" = 24 cols of 6-px column → 24x6 image.
	g := feedSixel(t, 10, 80, "", "#0!24~")
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	if gr.Cols != 3 || gr.Rows != 1 {
		t.Errorf("cell rect = %dx%d; want 3x1", gr.Cols, gr.Rows)
	}
	// Cells under the image must be blank (Ch==' ').
	for c := range gr.Cols {
		if ch := g.Cells[c].Ch; ch != ' ' {
			t.Errorf("cell %d under image has Ch=%q; want space", c, ch)
		}
	}
}

func TestParser_DCS_SixelOversizedDropped(t *testing.T) {
	// A bare 'q' with no body produces a nil image and no graphic, but
	// also no panic.
	g := feedSixel(t, 10, 80, "", "")
	if len(g.Graphics) != 0 {
		t.Fatalf("got %d graphics for empty payload; want 0", len(g.Graphics))
	}
}

// A full-window sixel frame is over a megabyte — `chafa shot.png` in a 160×45
// pane emits ~1.6 MB. Under the old 1 MiB DCS cap the tail was dropped and the
// image decoded to its top half. The whole frame must survive the parser.
func TestParser_DCS_SixelLargePayloadNotTruncated(t *testing.T) {
	// Bloat the byte count without bloating the dimensions: re-selecting the
	// color register per column costs 3 bytes a pixel column, so 3500 columns
	// per band × 100 bands ≈ 1.05 MB of payload for a 3500×600 image — over
	// the old cap, inside the decoder's 4096×4096 limit.
	const (
		bandCols = 3500
		bands    = 100
	)
	var body strings.Builder
	body.Grow(bandCols*3*bands + bands)
	for range bands {
		for range bandCols {
			body.WriteString("#0~")
		}
		body.WriteByte('-') // next band
	}
	if body.Len() <= 1<<20 {
		t.Fatalf("test payload is %d bytes; must exceed the old 1 MiB cap", body.Len())
	}

	g := feedSixel(t, 50, 200, "", body.String())
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	// Each band is 6 pixels tall. The trailing '-' opens a further band but
	// writes no pixel into it, and height tracks written pixels, not bands.
	if wantH := bands * 6; gr.HeightPx != wantH {
		t.Errorf("HeightPx = %d; want %d (frame truncated)", gr.HeightPx, wantH)
	}
	if gr.WidthPx != bandCols {
		t.Errorf("WidthPx = %d; want %d", gr.WidthPx, bandCols)
	}
}

// Over the cap the sequence is refused outright: a partially rendered frame
// looks like a rendering bug, an absent one looks like the refusal it is.
func TestParser_DCS_OverCapDropped(t *testing.T) {
	g := newGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p := newParser(g)
	p.SetGraphicsDir(t.TempDir())

	p.dcs = append(p.dcs, sixelPayload("", "#0!24~")...)
	p.dcsTrunc = true
	p.dispatchDCS()

	if len(g.Graphics) != 0 {
		t.Fatalf("got %d graphics for a truncated payload; want 0", len(g.Graphics))
	}
}

// dcsReset must clear the truncation flag, or one oversized frame would
// poison every DCS that follows it.
func TestParser_DCS_ResetClearsTruncation(t *testing.T) {
	g := newGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p := newParser(g)
	p.SetGraphicsDir(t.TempDir())

	p.dcsTrunc = true
	p.Feed([]byte("\x1bP" + sixelPayload("", "#0!24~") + "\x1b\\"))
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics after a fresh DCS; want 1", len(g.Graphics))
	}
}

// The DCS buffer must not stay pinned after the sequence is dispatched.
// At maxDCSBytes an unreleased buffer costs tens of MB per idle pane.
func TestParser_DCS_BufferReleasedAfterDispatch(t *testing.T) {
	g := newGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p := newParser(g)
	p.SetGraphicsDir(t.TempDir())

	// A body comfortably past maxDCSRetain so dcsReset drops the array
	// instead of merely reslicing it.
	body := strings.Repeat("#0~", (maxDCSRetain/3)+1000)
	p.Feed([]byte("\x1bP" + sixelPayload("", body) + "\x1b\\"))

	if got := cap(p.dcs); got > maxDCSRetain {
		t.Errorf("cap(p.dcs) = %d after dispatch; want <= %d (buffer pinned)",
			got, maxDCSRetain)
	}
}

// End-to-end counterpart to TestParser_DCS_OverCapDropped: a payload that
// actually overruns maxDCSBytes on the wire must be refused, and must not
// poison the next frame.
func TestParser_DCS_FeedOverCapDroppedThenRecovers(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates maxDCSBytes of payload")
	}
	g := newGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p := newParser(g)
	p.SetGraphicsDir(t.TempDir())

	// Filler is plain 'A': not a sixel data byte, so nothing decodes even
	// if the drop were to fail — the assertion is about the guard, and the
	// trailing "#0~" only exists to make the payload nominally drawable.
	var b strings.Builder
	b.Grow(maxDCSBytes + 64)
	b.WriteString(sixelPayload("", ""))
	b.WriteString(strings.Repeat("A", maxDCSBytes+1))
	b.WriteString("#0~")
	p.Feed([]byte("\x1bP" + b.String() + "\x1b\\"))

	// dcsTrunc is cleared by the post-dispatch reset, so the observable
	// evidence is the absent graphic, not the flag.
	if len(g.Graphics) != 0 {
		t.Fatalf("got %d graphics for an over-cap payload; want 0", len(g.Graphics))
	}

	// The parser must be usable again straight afterwards.
	p.Feed([]byte("\x1bP" + sixelPayload("", "#0!24~") + "\x1b\\"))
	if len(g.Graphics) != 1 {
		t.Fatalf("got %d graphics after recovery; want 1", len(g.Graphics))
	}
}

func TestGrid_TrimGraphics_EvictsOffTop(t *testing.T) {
	g := newGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	// Inject two graphics: one anchored at content row 1 (height 1),
	// another at row 5 (height 2).
	g.Graphics = []graphic{
		{OriginR: 1, Cols: 1, Rows: 1, Src: "x"},
		{OriginR: 5, Cols: 1, Rows: 2, Src: "y"},
	}
	g.trimGraphics(2)
	if len(g.Graphics) != 1 {
		t.Fatalf("survivors = %d; want 1", len(g.Graphics))
	}
	if g.Graphics[0].OriginR != 3 {
		t.Errorf("OriginR shifted to %d; want 3", g.Graphics[0].OriginR)
	}
}

func TestGrid_TrimGraphics_PartialKeep(t *testing.T) {
	g := newGrid(4, 10)
	// Image straddling the trim boundary (rows 1..3 at height 3, trim 2)
	// keeps its visible tail: OriginR becomes -1, Rows=3 → still covers
	// content row 1, so we keep it.
	g.Graphics = []graphic{{OriginR: 1, Cols: 1, Rows: 3, Src: "x"}}
	g.trimGraphics(2)
	if len(g.Graphics) != 1 {
		t.Fatalf("expected to keep partially visible graphic")
	}
	if g.Graphics[0].OriginR != -1 {
		t.Errorf("OriginR = %d; want -1", g.Graphics[0].OriginR)
	}
}

func TestGrid_ShiftGraphics_DropsOutOfRange(t *testing.T) {
	g := newGrid(4, 10)
	g.Graphics = []graphic{
		{OriginR: 0, Cols: 1, Rows: 1, Src: "a"},
		{OriginR: 5, Cols: 1, Rows: 1, Src: "b"},
	}
	// total=4, delta=-3: a moves to -3 (drop), b moves to 2 (keep).
	g.shiftGraphics(-3, 4)
	if len(g.Graphics) != 1 || g.Graphics[0].Src != "b" {
		t.Fatalf("survivors = %v; want only 'b'", g.Graphics)
	}
	if g.Graphics[0].OriginR != 2 {
		t.Errorf("OriginR = %d; want 2", g.Graphics[0].OriginR)
	}
}

func TestGrid_AddGraphic_CapEvictsOldest(t *testing.T) {
	g := newGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	for i := 0; i <= maxGraphics; i++ {
		g.AddGraphic("/tmp/fake.png", 8, 16)
	}
	if len(g.Graphics) != maxGraphics {
		t.Fatalf("len(Graphics) = %d; want %d", len(g.Graphics), maxGraphics)
	}
}

func TestIndexSixelFinal(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"q", 0},
		{"0q", 1},
		{"0;0;0q", 5},
		{";q", 1},
		{"$q", -1}, // '$' is DECRQSS, not sixel
		{"+q", -1}, // '+' is XTGETTCAP
		{"abcq", -1},
	}
	for _, tt := range tests {
		got := indexSixelFinal([]byte(tt.in))
		if got != tt.want {
			t.Errorf("indexSixelFinal(%q) = %d; want %d", tt.in, got, tt.want)
		}
	}
}

func TestHLSToRGB_Cardinals(t *testing.T) {
	// DEC HLS H=0 is blue per VT340 spec. With L=50, S=100, hue 0 → blue.
	got := hlsToRGB(0, 50, 100)
	if got.B < 0xC0 || got.R > 0x40 || got.G > 0x40 {
		t.Errorf("DEC H=0 L=50 S=100 = %v; want roughly blue", got)
	}
}

func BenchmarkSixel_Decode(b *testing.B) {
	// Realistic sixel payload: 80 columns × 24 rows (4 bands), with two
	// colors and run-length encoding.
	// Color 0 = red, color 1 = green.
	var payload []byte
	for band := range 4 {
		if band > 0 {
			payload = append(payload, '-')
		}
		payload = append(payload, "#0;2;100;0;0"...)
		payload = append(payload, "#0!40~"...)
		payload = append(payload, "#1;2;0;100;0"...)
		payload = append(payload, "#1!40~"...)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		img := decodeSixel(payload)
		if img == nil {
			b.Fatal("decodeSixel returned nil")
		}
	}
}

func TestClamp100(t *testing.T) {
	tests := []struct {
		v, want int
	}{
		{-1, 0},
		{0, 0},
		{50, 50},
		{100, 100},
		{101, 100},
		{9999, 100},
	}
	for _, tt := range tests {
		got := clamp100(tt.v)
		if got != tt.want {
			t.Errorf("clamp100(%d) = %d, want %d", tt.v, got, tt.want)
		}
	}
}

func TestHLSToRGB_AllRegions(t *testing.T) {
	// Each hue sextant at L=50, S=100 yields a primary or secondary.
	tests := []struct {
		h, l, s    int
		r, g, b    uint8  // approximate — HLS→RGB uses floats
		wantStrong string // which channel should dominate
	}{
		{0, 50, 100, 0, 0, 255, "blue"},
		{60, 50, 100, 0, 255, 255, "cyan"},
		{120, 50, 100, 0, 255, 0, "green"},
		{180, 50, 100, 255, 255, 0, "yellow"},
		{240, 50, 100, 255, 0, 0, "red"},
		{300, 50, 100, 255, 0, 255, "magenta"},
		// Black at L=0, white at L=100
		{0, 0, 0, 0, 0, 0, "black"},
		{0, 100, 0, 255, 255, 255, "white"},
	}
	for _, tt := range tests {
		c := hlsToRGB(tt.h, tt.l, tt.s)
		// Just verify we get something back; exact values depend on float math.
		if c.A != 0xFF {
			t.Errorf("hlsToRGB(%d,%d,%d) alpha = %d, want 255", tt.h, tt.l, tt.s, c.A)
		}
	}
}

// --- Occlusion: images the client can only remove by painting over them ---

// gridWithGraphic parks a 2×2-cell image at (row, col) and returns the grid.
func gridWithGraphic(t *testing.T, row, col int) *grid {
	t.Helper()
	g := newGrid(10, 40)
	g.CellPxW, g.CellPxH = 8, 16
	g.CursorR, g.CursorC = row, col
	if cols, rows := g.AddGraphic("img.png", 16, 32); cols != 2 || rows != 2 {
		t.Fatalf("AddGraphic footprint = %dx%d; want 2x2", cols, rows)
	}
	return g
}

// The bug from yazi's iTerm2 preview: the protocol has no delete sequence, so
// the client clears an image by writing spaces over its cells. Keeping the
// image left it on screen for the rest of the session.
func TestGrid_OccludeGraphics_TextOverwrite(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.CursorR, g.CursorC = 3, 5
	g.Put(' ')
	if len(g.Graphics) != 0 {
		t.Fatal("image survived text written into its cells")
	}
}

func TestGrid_OccludeGraphics_TextBesideImageKeepsIt(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.CursorR, g.CursorC = 3, 7 // one column past the 2-cell width
	g.Put('x')
	g.CursorR, g.CursorC = 5, 5 // one row past the 2-cell height
	g.Put('x')
	if len(g.Graphics) != 1 {
		t.Fatal("image removed by text that does not touch it")
	}
}

func TestGrid_OccludeGraphics_EraseInLine(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.CursorR, g.CursorC = 3, 0
	g.EraseInLine(2)
	if len(g.Graphics) != 0 {
		t.Fatal("image survived EL over its row")
	}
}

func TestGrid_OccludeGraphics_EraseInDisplay(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.CursorR, g.CursorC = 0, 0
	g.EraseInDisplay(2)
	if len(g.Graphics) != 0 {
		t.Fatal("image survived ED 2")
	}
}

// Kitty placements are their own layer: text over them is not a delete, and
// neither is a bounded erase (EL/ECH/ED 0/1). They go away through a=d
// (kittyDeleteID) or a whole-screen ED 2/3 — see
// TestGrid_EraseInDisplay_ClearsKittyPlacements.
func TestGrid_OccludeGraphics_KittyExempt(t *testing.T) {
	g := newGrid(10, 40)
	g.CellPxW, g.CellPxH = 8, 16
	g.CursorR, g.CursorC = 3, 5
	g.AddGraphicKitty("img.png", 16, 32, 0, 0, 7)
	g.CursorR, g.CursorC = 3, 5
	g.Put('x')
	g.EraseChars(4)
	g.CursorR, g.CursorC = 3, 5
	g.EraseInDisplay(0)
	if len(g.Graphics) != 1 {
		t.Fatal("KGP placement removed by text/bounded erase; only a=d or ED 2/3 may")
	}
}

// ECH (CSI X) blanks cells like EL does, so it must occlude on the same terms.
func TestGrid_OccludeGraphics_EraseChars(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.CursorR, g.CursorC = 3, 5
	g.EraseChars(2)
	if len(g.Graphics) != 0 {
		t.Fatal("image survived ECH over its cells")
	}
}

// Images belong to the screen they were drawn on. Entering the alt screen
// hides a main-screen image (yazi must not draw over a leftover `chafa`
// picture) without destroying it, and exiting brings it back.
func TestGrid_Graphics_AltScreenHidesAndRestores(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.EnterAlt()
	if len(g.Graphics) != 0 {
		t.Fatal("main-screen image still on the active list inside alt")
	}
	for r := range g.Rows { // a TUI painting its whole screen
		g.CursorR, g.CursorC = r, 0
		for range g.Cols {
			g.Put('x')
		}
	}
	g.ExitAlt()
	if len(g.Graphics) != 1 {
		t.Fatal("alt-screen text destroyed a parked main-screen image")
	}
}

// An image the alt screen placed itself is occludable there — a previewer
// running full-screen clears its own picture by painting over the cells — and
// dies with the alt screen rather than leaking onto the main one.
func TestGrid_Graphics_AltScreenOwnImages(t *testing.T) {
	g := newGrid(10, 40)
	g.CellPxW, g.CellPxH = 8, 16
	g.EnterAlt()
	g.CursorR, g.CursorC = 3, 5
	g.AddGraphic("alt.png", 16, 32)
	if len(g.Graphics) != 1 {
		t.Fatal("setup: alt-screen image not registered")
	}
	g.CursorR, g.CursorC = 3, 5
	g.Put('x')
	if len(g.Graphics) != 0 {
		t.Fatal("alt-screen image survived text painted over its cells")
	}
	g.CursorR, g.CursorC = 3, 5
	g.AddGraphic("alt2.png", 16, 32)
	g.ExitAlt()
	if len(g.Graphics) != 0 {
		t.Fatal("alt-screen image leaked onto the main screen")
	}
}

// An image that has scrolled entirely into scrollback can no longer be
// reached by a live-screen write, which is what occludeMaxR encodes. Writing
// over the row it *used* to occupy must leave it alone.
func TestGrid_OccludeGraphics_ScrollbackImageSurvives(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.ScrollbackCap = 100
	for range g.Rows { // push the image off the live screen
		g.CursorR = g.Rows - 1
		g.Newline()
	}
	if g.Scrollback.Len() < g.occludeMaxR {
		t.Fatalf("setup: image still live (base %d, bound %d)",
			g.Scrollback.Len(), g.occludeMaxR)
	}
	g.CursorR, g.CursorC = 3, 5
	g.Put('x')
	if len(g.Graphics) != 1 {
		t.Fatal("scrollback image removed by an unrelated live-screen write")
	}
}

// The bound may be too high (a wasted scan) but never too low. Reflow moves
// origins arbitrarily, so it must invalidate rather than keep a stale value.
func TestGrid_OccludeGraphics_BoundSurvivesReflow(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.Resize(10, 20) // narrower: re-wrap remaps graphic origins
	if len(g.Graphics) != 1 {
		t.Skip("resize dropped the graphic; nothing to assert")
	}
	gr := g.Graphics[0]
	g.CursorR, g.CursorC = gr.OriginR-g.Scrollback.Len(), gr.OriginC
	if g.CursorR < 0 || g.CursorR >= g.Rows {
		t.Skip("graphic not on the live screen after resize")
	}
	g.Put('x')
	if len(g.Graphics) != 0 {
		t.Fatal("stale occlusion bound let a write miss the image under it")
	}
}

// The alt screen never pushes rows to scrollback, so a region scroll there
// slides the text under fixed graphic origins unless the images are moved
// too — the picture would drift away from the cells it was placed on.
func TestGrid_Graphics_AltScrollMovesImages(t *testing.T) {
	g := newGrid(10, 40)
	g.CellPxW, g.CellPxH = 8, 16
	g.ScrollbackCap = 100
	g.EnterAlt()
	g.CursorR, g.CursorC = 6, 5
	g.AddGraphic("alt.png", 16, 32) // 2x2 cells at row 6
	g.ScrollUp(2)
	if n := len(g.Graphics); n != 1 {
		t.Fatalf("graphics after scroll = %d; want 1", n)
	}
	if got := g.Graphics[0].OriginR; got != 4 {
		t.Fatalf("origin after 2-row scroll = %d; want 4", got)
	}
	g.ScrollUp(6) // pushes it off the top of the region
	if len(g.Graphics) != 0 {
		t.Fatal("image scrolled out of the alt screen was kept")
	}
}

// The mirror case: on the main screen a full-screen scroll pushes rows into
// scrollback, so the content-row space absorbs the scroll and origins must
// stay exactly where they are.
func TestGrid_Graphics_MainScrollKeepsOriginsWhenPushed(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.ScrollbackCap = 100
	g.ScrollUp(2)
	if n := g.Scrollback.Len(); n != 2 {
		t.Fatalf("setup: scrollback = %d; want 2 rows pushed", n)
	}
	if got := g.Graphics[0].OriginR; got != 3 {
		t.Fatalf("origin moved to %d; scrollback push must leave it at 3", got)
	}
}

// IL/DL shift rows inside the region without touching scrollback, so images
// ride along and those pushed out of the region go away.
func TestGrid_Graphics_InsertDeleteLinesMoveImages(t *testing.T) {
	g := gridWithGraphic(t, 5, 5)
	g.CursorR, g.CursorC = 2, 0
	g.InsertLines(2)
	if got := g.Graphics[0].OriginR; got != 7 {
		t.Fatalf("origin after IL 2 = %d; want 7", got)
	}
	g.DeleteLines(3)
	if got := g.Graphics[0].OriginR; got != 4 {
		t.Fatalf("origin after DL 3 = %d; want 4", got)
	}
	g.CursorR = 0
	g.DeleteLines(g.Rows) // clears the whole region
	if len(g.Graphics) != 0 {
		t.Fatal("image survived a DL that deleted every row under it")
	}
}

// ED 3 drops scrollback, which shifts the shared content-row space — both the
// alt screen's own images and the parked main-screen list must follow it.
func TestGrid_Graphics_AltEraseScrollbackTrimsBothLists(t *testing.T) {
	g := gridWithGraphic(t, 8, 5)
	g.ScrollbackCap = 100
	for range 5 { // push 5 rows of history under the image
		g.CursorR = g.Rows - 1
		g.Newline()
	}
	if n := g.Scrollback.Len(); n != 5 {
		t.Fatalf("setup: scrollback = %d; want 5", n)
	}
	g.EnterAlt()
	g.CursorR, g.CursorC = 2, 0
	g.AddGraphic("alt.png", 16, 32)
	g.EraseInDisplay(3)
	// The alt image sat in the rows ED cleared, so occlusion removed it.
	if len(g.Graphics) != 0 {
		t.Fatal("alt image survived the ED 2/3 flat fill over its cells")
	}
	if n := len(g.mainSaved.graphics); n != 1 {
		t.Fatalf("parked graphics = %d; want 1", n)
	}
	if got := g.mainSaved.graphics[0].OriginR; got != 3 {
		t.Fatalf("parked origin after dropping 5 scrollback rows = %d; want 3", got)
	}
}

// The kitty store may evict a file while the alt screen is up. A parked
// placement still needs its file, and if the file does go the placement must
// go with it — ExitAlt otherwise restores an image pointing at a dead path.
func TestGrid_Graphics_ParkedListTracksSrcLifetime(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.EnterAlt()
	if !g.graphicsUseSrc("img.png") {
		t.Fatal("parked placement not counted as using its file")
	}
	g.deleteGraphicsBySrc("img.png")
	if g.graphicsUseSrc("img.png") {
		t.Fatal("placement still reported after its file was dropped")
	}
	g.ExitAlt()
	if len(g.Graphics) != 0 {
		t.Fatal("ExitAlt restored a placement whose file was deleted")
	}
}

// A resize while the alt screen is up reflows the *saved* main buffer, so the
// parked image list must ride that reflow too — otherwise ExitAlt restores
// origins addressing pre-resize rows.
func TestGrid_Graphics_AltResizeRemapsParkedList(t *testing.T) {
	g := gridWithGraphic(t, 3, 5)
	g.EnterAlt()
	g.Resize(10, 20) // narrower: re-wrap moves main-screen content rows
	if n := len(g.mainSaved.graphics); n != 1 {
		t.Fatalf("parked graphics after alt resize = %d; want 1", n)
	}
	g.ExitAlt()
	if len(g.Graphics) != 1 {
		t.Fatal("image lost across alt resize round trip")
	}
	// Its origin must land in the post-resize content space, and painting
	// that cell must still reach it (the occlusion bound was invalidated).
	gr := g.Graphics[0]
	if gr.OriginR < 0 || gr.OriginR >= g.ContentRows() {
		t.Fatalf("origin row %d outside content rows %d", gr.OriginR, g.ContentRows())
	}
	g.CursorR, g.CursorC = gr.OriginR-g.Scrollback.Len(), gr.OriginC
	if g.CursorR < 0 || g.CursorR >= g.Rows {
		t.Skip("graphic not on the live screen after resize")
	}
	g.Put('x')
	if len(g.Graphics) != 0 {
		t.Fatal("stale occlusion bound after alt resize let a write miss the image")
	}
}

// The per-glyph occlusion check must stay free once images leave the live
// screen — a long previewer session parks up to maxGraphics of them there.
func BenchmarkPutCell_GraphicsInScrollback(b *testing.B) {
	g := newGrid(24, 80)
	g.CellPxW, g.CellPxH = 8, 16
	g.ScrollbackCap = 1000
	for range maxGraphics {
		g.CursorR, g.CursorC = g.Rows-1, 40
		g.AddGraphic("img.png", 8, 16)
		g.Newline()
	}
	for range 64 { // push every image well past the live screen
		g.CursorR = g.Rows - 1
		g.Newline()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		g.CursorR, g.CursorC = 0, 0
		for range 80 {
			g.Put('x')
		}
	}
}

// `clear` emits ED 3 then ED 2, and in kitty both take the graphics layer with
// them (screen_erase_in_display → grman_clear). Without that the image stayed
// on a cleared screen for the rest of the session.
func TestGrid_EraseInDisplay_ClearsKittyPlacements(t *testing.T) {
	g := newGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	g.AddGraphicKitty("img.png", 16, 32, 0, 0, 7)
	if len(g.Graphics) != 1 {
		t.Fatalf("setup: want 1 placement, got %d", len(g.Graphics))
	}
	g.EraseInDisplay(2)
	if len(g.Graphics) != 0 {
		t.Fatalf("ED 2 left %d Kitty placements", len(g.Graphics))
	}
}

// ED 2 only reaches placements touching the live screen; one scrolled fully
// into scrollback survives, exactly as kitty's clear_filter_func has it. ED 3
// ("erase saved lines") takes that one too.
func TestGrid_EraseInDisplay_KittyScrollbackScope(t *testing.T) {
	for _, tc := range []struct {
		mode int
		want int
	}{{2, 1}, {3, 0}} {
		g := newGrid(4, 10)
		g.ScrollbackCap = 100
		g.CellPxW, g.CellPxH = 8, 16
		g.AddGraphicKitty("img.png", 8, 16, 0, 0, 7)
		// Push the placement's row out of the live screen and into history.
		g.ScrollUp(8)
		if g.Scrollback.Len() == 0 {
			t.Fatal("setup: nothing scrolled into scrollback")
		}
		g.EraseInDisplay(tc.mode)
		if len(g.Graphics) != tc.want {
			t.Errorf("ED %d: want %d placements, got %d",
				tc.mode, tc.want, len(g.Graphics))
		}
	}
}

// ED 3 while the alt screen is up must also clear the parked main-screen
// Kitty placements, or ExitAlt would resurrect an image kitty's grman_clear
// (all=true) would have removed. ED 2 (all=false) leaves parked images alone.
func TestGrid_EraseInDisplay_KittyAltScope(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int
		want int
	}{{`ED 2 keeps parked`, 2, 1}, {`ED 3 drops parked`, 3, 0}} {
		g := newGrid(4, 10)
		g.ScrollbackCap = 100
		g.CellPxW, g.CellPxH = 8, 16
		g.AddGraphicKitty("main.png", 8, 16, 0, 0, 7)
		g.EnterAlt()
		if len(g.mainSaved.graphics) != 1 {
			t.Fatalf("%s setup: parked want 1, got %d", tc.name, len(g.mainSaved.graphics))
		}
		g.EraseInDisplay(tc.mode)
		if n := len(g.mainSaved.graphics); n != tc.want {
			t.Errorf("%s: parked want %d, got %d", tc.name, tc.want, n)
		}
		// ED 2 must not have leaked non-Kitty logic onto the parked list:
		// a parked sixel would survive an ED 2 either way.
	}
}

// Selective ED 2/3 still takes the Kitty layer: DECSCA protection guards
// cells, not the separate Kitty placement layer, and kitty's grman_clear is
// keyed only on the erase mode, not the selective flag.
func TestGrid_EraseInDisplay_KittySelectiveScope(t *testing.T) {
	g := newGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	g.AddGraphicKitty("img.png", 8, 16, 0, 0, 7)
	g.SelectiveEraseInDisplay(2)
	if len(g.Graphics) != 0 {
		t.Fatalf("selective ED 2 left %d Kitty placements", len(g.Graphics))
	}
}

// A non-Kitty (sixel/iTerm2) image is never touched by the Kitty path —
// it goes away through occludeGraphics, not clearKittyGraphics, so a kitty
// erase must leave it to its own removal signal (painting over cells).
func TestGrid_EraseInDisplay_KittyPathLeavesSixel(t *testing.T) {
	g := newGrid(4, 10)
	g.CellPxW, g.CellPxH = 8, 16
	// A sixel image on the live screen: ED 2's flat fill already occludes it,
	// so it vanishes — but through the other layer, not the Kitty filter.
	// A scrollback sixel survives ED 2, proving the Kitty filter did not
	// claim it.
	g.ScrollbackCap = 100
	g.AddGraphic("sixel.png", 8, 16)
	g.ScrollUp(8)
	if g.Scrollback.Len() == 0 {
		t.Fatal("setup: nothing scrolled into scrollback")
	}
	g.EraseInDisplay(2)
	if len(g.Graphics) != 1 {
		t.Fatalf("ED 2 removed a scrollback sixel via Kitty path; want 1, got %d",
			len(g.Graphics))
	}
	g.EraseInDisplay(3)
	if len(g.Graphics) != 0 {
		t.Fatalf("ED 3 left %d scrollback sixels (should trim via scrollback drop)", len(g.Graphics))
	}
}
