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

func TestOSC1337_ExtraParams_Ignored(t *testing.T) {
	// Extra params (name, size, width, height) should not break dispatch.
	raw := make1x1PNG(color.NRGBA{128, 128, 128, 255})
	b64name := base64.StdEncoding.EncodeToString([]byte("test.png"))
	args := "name=" + b64name + ";size=100;width=auto;height=auto;inline=1"
	g := feedOSC1337Grid(t, 10, 80, args, raw)
	if len(g.Graphics) != 1 {
		t.Fatalf("Graphics len = %d; want 1 with extra params", len(g.Graphics))
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
