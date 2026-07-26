package term

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strconv"
	"strings"
	"testing"
)

// make1x1PNG returns a 1×1 PNG image encoded as bytes.
func make1x1PNG(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, c)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// make1x1JPEG returns a 1×1 JPEG image encoded as bytes.
func make1x1JPEG(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, c)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func makePNGHeader(width, height uint32) []byte {
	var buf bytes.Buffer
	buf.Write([]byte("\x89PNG\r\n\x1a\n"))

	var ihdr bytes.Buffer
	_ = binary.Write(&ihdr, binary.BigEndian, width)
	_ = binary.Write(&ihdr, binary.BigEndian, height)
	ihdr.Write([]byte{8, 6, 0, 0, 0})

	chunk := ihdr.Bytes()
	_ = binary.Write(&buf, binary.BigEndian, uint32(len(chunk)))
	buf.WriteString("IHDR")
	buf.Write(chunk)
	crc := crc32.NewIEEE()
	_, _ = crc.Write([]byte("IHDR"))
	_, _ = crc.Write(chunk)
	_ = binary.Write(&buf, binary.BigEndian, crc.Sum32())
	return buf.Bytes()
}

// feedOSC1337 constructs and feeds an OSC 1337 sequence via BEL terminator.
func feedOSC1337(t *testing.T, g *grid, p *parser, args string, imgData []byte) {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString(imgData)
	seq := "\x1b]1337;File=" + args + ":" + b64 + "\x07"
	feed(t, g, p, []byte(seq))
}

// --- decodeImageBytes tests ---

func TestDecodeImageBytes_PNG(t *testing.T) {
	want := color.NRGBA{0xFF, 0x80, 0x00, 0xFF}
	raw := make1x1PNG(want)
	img := decodeImageBytes(raw)
	if img == nil {
		t.Fatal("got nil")
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("bounds %v; want 1x1", b)
	}
	got := img.NRGBAAt(0, 0)
	if got != want {
		t.Errorf("pixel = %v; want %v", got, want)
	}
}

func TestDecodeImageBytes_JPEG(t *testing.T) {
	raw := make1x1JPEG(color.RGBA{200, 100, 50, 255})
	img := decodeImageBytes(raw)
	if img == nil {
		t.Fatal("got nil for valid JPEG")
	}
	if b := img.Bounds(); b.Dx() != 1 || b.Dy() != 1 {
		t.Fatalf("bounds %v; want 1x1", b)
	}
}

func TestDecodeImageBytes_Invalid(t *testing.T) {
	if img := decodeImageBytes([]byte("not an image")); img != nil {
		t.Fatal("expected nil for garbage input")
	}
	if img := decodeImageBytes(nil); img != nil {
		t.Fatal("expected nil for nil input")
	}
	if img := decodeImageBytes([]byte{}); img != nil {
		t.Fatal("expected nil for empty input")
	}
}

// --- OSC 1337 dispatch tests ---

func feedOSC1337Grid(t *testing.T, rows, cols int, args string, imgData []byte) *grid {
	t.Helper()
	g, p := newParserGrid(rows, cols)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	feedOSC1337(t, g, p, args, imgData)
	return g
}

func TestOSC1337_BasicPNG(t *testing.T) {
	raw := make1x1PNG(color.NRGBA{255, 0, 0, 255})
	g := feedOSC1337Grid(t, 10, 80, "inline=1", raw)
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1", len(g.Graphics))
	}
	gr := g.Graphics[0]
	if gr.Src == "" {
		t.Error("Src empty")
	}
	if gr.WidthPx != 1 || gr.HeightPx != 1 {
		t.Errorf("pixel dims = %dx%d; want 1x1", gr.WidthPx, gr.HeightPx)
	}
	// Cursor should have advanced past the image (at least 1 row).
	if g.CursorR == 0 {
		t.Error("cursor row didn't advance")
	}
}

func TestOSC1337_InlineZero_Drops(t *testing.T) {
	raw := make1x1PNG(color.NRGBA{0, 255, 0, 255})
	g := feedOSC1337Grid(t, 10, 80, "inline=0", raw)
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics for inline=0, got %d", len(g.Graphics))
	}
}

func TestOSC1337_MissingInline_Drops(t *testing.T) {
	raw := make1x1PNG(color.NRGBA{0, 0, 255, 255})
	// No inline key at all — spec says default is 0 (don't display).
	g := feedOSC1337Grid(t, 10, 80, "width=auto", raw)
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics, got %d", len(g.Graphics))
	}
}

func TestOSC1337_NoFilePrefix_Drops(t *testing.T) {
	g, p := newParserGrid(10, 80)
	p.SetGraphicsDir(t.TempDir())
	// Payload doesn't start with "File="
	seq := "\x1b]1337;inline=1:" + base64.StdEncoding.EncodeToString(make1x1PNG(color.NRGBA{})) + "\x07"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics, got %d", len(g.Graphics))
	}
}

func TestOSC1337_NoColon_Drops(t *testing.T) {
	g, p := newParserGrid(10, 80)
	p.SetGraphicsDir(t.TempDir())
	// No colon separator between args and data.
	seq := "\x1b]1337;File=inline=1" + "\x07"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics, got %d", len(g.Graphics))
	}
}

func TestOSC1337_InvalidBase64_Drops(t *testing.T) {
	g, p := newParserGrid(10, 80)
	p.SetGraphicsDir(t.TempDir())
	seq := "\x1b]1337;File=inline=1:!!!not-valid-base64!!!\x07"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics for bad base64, got %d", len(g.Graphics))
	}
}

func TestOSC1337_InvalidImageData_Drops(t *testing.T) {
	g, p := newParserGrid(10, 80)
	p.SetGraphicsDir(t.TempDir())
	// Valid base64 but the decoded bytes are not an image.
	b64 := base64.StdEncoding.EncodeToString([]byte("this is not an image"))
	seq := "\x1b]1337;File=inline=1:" + b64 + "\x07"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 0 {
		t.Fatalf("expected no graphics for non-image data, got %d", len(g.Graphics))
	}
}

func TestOSC1337_STTerminator(t *testing.T) {
	raw := make1x1PNG(color.NRGBA{255, 255, 0, 255})
	g, p := newParserGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	b64 := base64.StdEncoding.EncodeToString(raw)
	seq := "\x1b]1337;File=inline=1:" + b64 + "\x1b\\"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 1 {
		t.Fatalf("ST terminator: Graphics len = %d; want 1", len(g.Graphics))
	}
}

func TestOSC1337_ExtraParams_NaturalSize(t *testing.T) {
	// A full key list with both dimensions on auto renders at natural size.
	raw := make1x1PNG(color.NRGBA{128, 128, 128, 255})
	b64name := base64.StdEncoding.EncodeToString([]byte("test.png"))
	args := "name=" + b64name + ";size=100;width=auto;height=auto;inline=1"
	g := feedOSC1337Grid(t, 10, 80, args, raw)
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1 with extra params", len(g.Graphics))
	}
	if gr := g.Graphics[0]; gr.Cols != 1 || gr.Rows != 1 {
		t.Errorf("footprint = %dx%d cells; want 1x1 (auto)", gr.Cols, gr.Rows)
	}
}

func TestOSC1337_UnknownAndMalformedParams(t *testing.T) {
	// Unknown keys, a bare field with no '=', and an empty value must not
	// derail the sequence — real emitters produce all three.
	raw := make1x1PNG(color.NRGBA{10, 20, 30, 255})
	args := "unknownKey=zzz;justAFlag;name=;inline=1;trailing="
	g := feedOSC1337Grid(t, 10, 80, args, raw)
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1", len(g.Graphics))
	}
}

func TestOSC1337_RawStdEncodingFallback(t *testing.T) {
	// Some tools omit base64 padding. RawStdEncoding handles this.
	raw := make1x1PNG(color.NRGBA{0, 128, 255, 255})
	b64 := base64.RawStdEncoding.EncodeToString(raw) // no padding
	g, p := newParserGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	seq := "\x1b]1337;File=inline=1:" + b64 + "\x07"
	feed(t, g, p, []byte(seq))
	if len(g.Graphics) != 1 {
		t.Fatalf("raw-encoding fallback: Graphics len = %d; want 1", len(g.Graphics))
	}
}

func TestDecodeImageBytes_OversizedReturnsNil(t *testing.T) {
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	for _, tc := range []struct{ w, h int }{
		{maxSixelWidth + 1, 1},
		{1, maxSixelHeight + 1},
	} {
		img := image.NewNRGBA(image.Rect(0, 0, tc.w, tc.h))
		var buf bytes.Buffer
		if err := enc.Encode(&buf, img); err != nil {
			t.Fatalf("encode %dx%d: %v", tc.w, tc.h, err)
		}
		if got := decodeImageBytes(buf.Bytes()); got != nil {
			t.Errorf("%dx%d: got non-nil; want nil (oversized)", tc.w, tc.h)
		}
	}
}

func TestDecodeImageBytes_OversizedHeaderReturnsNil(t *testing.T) {
	raw := makePNGHeader(maxSixelWidth+1, maxSixelHeight+1)
	if got := decodeImageBytes(raw); got != nil {
		t.Fatal("got non-nil for oversized PNG header")
	}
}

func TestOSC1337_PayloadExceedsNormalCap_StillRenders(t *testing.T) {
	// Build a PNG whose base64+prefix exceeds maxOSCBytes. NoCompression
	// guarantees a large enough output regardless of pixel content.
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x * 4), uint8(y * 4), 0xFF, 0xFF})
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(buf.Bytes())
	const oscPrefix = "1337;File=inline=1:"
	if len(oscPrefix)+len(b64) <= maxOSCBytes {
		t.Fatal("test setup: PNG too small; increase image dimensions")
	}

	g, p := newParserGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	feed(t, g, p, []byte("\x1b]"+oscPrefix+b64+"\x07"))
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1 (payload %d bytes > maxOSCBytes %d)", len(g.Graphics), len(b64), maxOSCBytes)
	}
}

func TestOSC1337_StateResetAfterDispatch_NormalOSCCapped(t *testing.T) {
	g, p := newParserGrid(1, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())

	feedOSC1337(t, g, p, "inline=1", make1x1PNG(color.NRGBA{255, 0, 0, 255}))

	// Subsequent normal OSC must use maxOSCBytes, not the enlarged 1337 cap.
	var got string
	p.SetTitleHandler(func(s string) { got = s })
	huge := make([]byte, 0, maxOSCBytes+200)
	huge = append(huge, "\x1b]0;"...)
	for range maxOSCBytes + 100 {
		huge = append(huge, 'A')
	}
	huge = append(huge, 0x07)
	feed(t, g, p, huge)

	if len(got) != maxOSCBytes-2 {
		t.Errorf("title len = %d; want %d (oscIsImage state leaked from 1337)", len(got), maxOSCBytes-2)
	}
}

// --- File= argument parsing ---

func TestParseOSC1337Args_Defaults(t *testing.T) {
	a := parseOSC1337Args("")
	if a.inline {
		t.Error("inline should default false")
	}
	if !a.preserveAR {
		t.Error("preserveAspectRatio should default true")
	}
	if a.size != -1 {
		t.Errorf("size = %d; want -1 (absent)", a.size)
	}
	if a.width.kind != dimAuto || a.height.kind != dimAuto {
		t.Errorf("dims = %v/%v; want auto", a.width, a.height)
	}
	if a.name != "" {
		t.Errorf("name = %q; want empty", a.name)
	}
}

func TestParseOSC1337Args_AllKeys(t *testing.T) {
	name := base64.StdEncoding.EncodeToString([]byte("report.pdf"))
	a := parseOSC1337Args("name=" + name +
		";size=1234;width=40;height=200px;preserveAspectRatio=0;inline=1")
	if a.name != "report.pdf" {
		t.Errorf("name = %q; want report.pdf", a.name)
	}
	if a.size != 1234 {
		t.Errorf("size = %d; want 1234", a.size)
	}
	if a.width != (dimSpec{val: 40, kind: dimCells}) {
		t.Errorf("width = %+v; want 40 cells", a.width)
	}
	if a.height != (dimSpec{val: 200, kind: dimPixels}) {
		t.Errorf("height = %+v; want 200px", a.height)
	}
	if a.preserveAR {
		t.Error("preserveAspectRatio=0 should clear preserveAR")
	}
	if !a.inline {
		t.Error("inline=1 should set inline")
	}
}

func TestParseDimSpec(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want dimSpec
	}{
		{"", dimSpec{}},
		{"auto", dimSpec{}},
		{"10", dimSpec{val: 10, kind: dimCells}},
		{"10px", dimSpec{val: 10, kind: dimPixels}},
		{"50%", dimSpec{val: 50, kind: dimPercent}},
		{"0", dimSpec{}},   // zero is meaningless — treat as auto
		{"-5", dimSpec{}},  // negative likewise
		{"abc", dimSpec{}}, // garbage
		{"px", dimSpec{}},  // suffix with no number
		{"%", dimSpec{}},   // ditto
		{"1e3", dimSpec{}}, // not an integer
	} {
		if got := parseDimSpec(tc.in); got != tc.want {
			t.Errorf("parseDimSpec(%q) = %+v; want %+v", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeDownloadName(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	for _, tc := range []struct{ in, want string }{
		{b64("report.pdf"), "report.pdf"},
		{b64("../../etc/passwd"), "passwd"},
		{b64("/abs/path/file.txt"), "file.txt"},
		{b64(`C:\Windows\evil.exe`), "evil.exe"},
		{b64("with\nnewline.txt"), "withnewline.txt"},
		{b64("with\x00nul.txt"), "withnul.txt"},
		{b64(".."), defaultDownloadName},
		{b64("."), defaultDownloadName},
		{b64(""), defaultDownloadName},
		{b64("   "), defaultDownloadName},
		{b64("dir/"), defaultDownloadName},
		{"not!valid!base64", defaultDownloadName},
	} {
		if got := sanitizeDownloadName(tc.in); got != tc.want {
			t.Errorf("sanitizeDownloadName(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
	// Over-long names are truncated to a filesystem-safe length.
	long := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 300)))
	if got := sanitizeDownloadName(long); len(got) != maxDownloadName {
		t.Errorf("long name len = %d; want %d", len(got), maxDownloadName)
	}
}

// --- download dispatch ---

// feedDownload feeds a non-inline File= transfer and returns what the
// download handler saw.
func feedDownload(t *testing.T, args string, payload []byte) (string, []byte, int) {
	t.Helper()
	g, p := newParserGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	var gotName string
	var gotData []byte
	calls := 0
	p.SetDownloadHandler(func(name string, data []byte) {
		gotName, gotData, calls = name, data, calls+1
	})
	feedOSC1337(t, g, p, args, payload)
	return gotName, gotData, calls
}

func TestOSC1337_Download_InlineZero(t *testing.T) {
	name := base64.StdEncoding.EncodeToString([]byte("notes.txt"))
	payload := []byte("hello world")
	gotName, gotData, calls := feedDownload(t, "name="+name+";size=11;inline=0", payload)
	if calls != 1 {
		t.Fatalf("handler calls = %d; want 1", calls)
	}
	if gotName != "notes.txt" {
		t.Errorf("name = %q; want notes.txt", gotName)
	}
	if string(gotData) != string(payload) {
		t.Errorf("data = %q; want %q", gotData, payload)
	}
}

func TestOSC1337_Download_MissingInlineKey(t *testing.T) {
	// No inline key at all is a transfer, not a render.
	_, _, calls := feedDownload(t, "size=5", []byte("hello"))
	if calls != 1 {
		t.Fatalf("handler calls = %d; want 1", calls)
	}
}

func TestOSC1337_Download_NoNameUsesDefault(t *testing.T) {
	gotName, _, calls := feedDownload(t, "inline=0", []byte("x"))
	if calls != 1 {
		t.Fatalf("handler calls = %d; want 1", calls)
	}
	if gotName != defaultDownloadName {
		t.Errorf("name = %q; want %q", gotName, defaultDownloadName)
	}
}

func TestOSC1337_Download_NotCalledForInlineImage(t *testing.T) {
	_, _, calls := feedDownload(t, "inline=1", make1x1PNG(color.NRGBA{1, 2, 3, 255}))
	if calls != 0 {
		t.Fatalf("handler calls = %d; want 0 for inline=1", calls)
	}
}

func TestOSC1337_Download_NoHandlerDrops(t *testing.T) {
	// The default (no SetDownloadHandler) must not render or panic.
	g := feedOSC1337Grid(t, 10, 80, "inline=0", []byte("hello"))
	if len(g.Graphics) != 0 {
		t.Fatalf("Graphics len = %d; want 0", len(g.Graphics))
	}
}

func TestOSC1337_Download_DeclaredSizeOverCapDrops(t *testing.T) {
	args := "inline=0;size=" + strconv.Itoa(maxOSC1337Bytes+1)
	_, _, calls := feedDownload(t, args, []byte("hello"))
	if calls != 0 {
		t.Fatalf("handler calls = %d; want 0 for over-cap declared size", calls)
	}
}

func TestOSC1337_TruncatedPayloadDrops(t *testing.T) {
	// A payload past the 1337 cap loses its tail. Dropping it is the point:
	// half a file must never reach the download handler.
	g, p := newParserGrid(10, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	calls := 0
	p.SetDownloadHandler(func(string, []byte) { calls++ })

	var b strings.Builder
	b.WriteString("\x1b]1337;File=inline=0:")
	// Base64 alphabet only, so a truncated tail still decodes cleanly and
	// the drop can only be attributed to the truncation flag.
	b.WriteString(strings.Repeat("A", maxOSC1337Bytes+16))
	b.WriteString("\x07")
	feed(t, g, p, []byte(b.String()))

	if calls != 0 {
		t.Fatalf("handler calls = %d; want 0 for truncated payload", calls)
	}
	if p.oscTrunc {
		t.Error("oscTrunc should be cleared by oscReset after dispatch")
	}
	// The oversized accumulator must not stay pinned for the parser's life.
	if cap(p.osc) > maxOSCBytes {
		t.Errorf("cap(p.osc) = %d; want <= %d after reset", cap(p.osc), maxOSCBytes)
	}
}

// --- inline sizing ---

// sizedGraphic feeds an inline image of imgW×imgH pixels with the given
// sizing args and returns its cell footprint.
func sizedGraphic(t *testing.T, imgW, imgH int, args string) (int, int) {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, imgW, imgH))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	// 8x16 cells, 80x24 grid: 1 cell = 8x16 px.
	g, p := newParserGrid(24, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p.SetGraphicsDir(t.TempDir())
	feedOSC1337(t, g, p, args+";inline=1", buf.Bytes())
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1", len(g.Graphics))
	}
	return g.Graphics[0].Cols, g.Graphics[0].Rows
}

func TestOSC1337_Sizing(t *testing.T) {
	for _, tc := range []struct {
		name               string
		imgW, imgH         int
		args               string
		wantCols, wantRows int
	}{
		// 80x32 px = 10x2 cells naturally.
		{"auto", 80, 32, "", 10, 2},
		// Width in cells; aspect preserved (10:2 → 20:4).
		{"width cells", 80, 32, "width=20", 20, 4},
		// Width in pixels: 160px = 20 cells, same as above.
		{"width px", 80, 32, "width=160px", 20, 4},
		// 50% of an 80-column grid = 40 cells wide, 8 rows tall.
		{"width percent", 80, 32, "width=50%", 40, 8},
		// Height alone drives width when aspect is preserved.
		{"height cells", 80, 32, "height=4", 20, 4},
		// Both axes, aspect preserved: fit inside the box (width binds).
		{"both fit", 80, 32, "width=20;height=40", 20, 4},
		// Aspect off: each axis is taken literally.
		{"both stretch", 80, 32, "width=20;height=40;preserveAspectRatio=0", 20, 40},
		// Aspect off with one axis given: the other keeps natural size.
		{"one stretch", 80, 32, "width=20;preserveAspectRatio=0", 20, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows := sizedGraphic(t, tc.imgW, tc.imgH, tc.args)
			if cols != tc.wantCols || rows != tc.wantRows {
				t.Errorf("footprint = %dx%d; want %dx%d",
					cols, rows, tc.wantCols, tc.wantRows)
			}
		})
	}
}

func TestOSC1337_Sizing_ClampedToGridWidth(t *testing.T) {
	// A request wider than the grid is truncated at the right margin, not
	// allowed to describe cells that don't exist.
	cols, _ := sizedGraphic(t, 80, 32, "width=200")
	if cols != 80 {
		t.Errorf("cols = %d; want 80 (grid width)", cols)
	}
}

func TestOSC1337_Sizing_NoCellMetricsFallsBack(t *testing.T) {
	// Before the first frame measures the font, cell pixels are zero and
	// explicit sizing can't be resolved — the natural 1x1 fallback applies.
	g, p := newParserGrid(10, 80)
	p.SetGraphicsDir(t.TempDir())
	feedOSC1337(t, g, p, "width=20;inline=1", make1x1PNG(color.NRGBA{9, 9, 9, 255}))
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1", len(g.Graphics))
	}
	if gr := g.Graphics[0]; gr.Cols != 1 || gr.Rows != 1 {
		t.Errorf("footprint = %dx%d; want 1x1", gr.Cols, gr.Rows)
	}
}
