package term

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"testing"
)

// zlibB64 deflates raw the way a client sending o=z does, then base64s it.
func zlibB64(t *testing.T, raw []byte) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// ttt (internal/image/kitty.go) transmits deflated RGBA in chunks with a=t,
// then places it with a separate a=p after a CUP. Before o=z was honored the
// inflated-size check failed on the compressed bytes, the image was never
// stored, and the a=p got ENOENT: ttt detected falcon but showed nothing.
func TestParser_APC_KittyTTTSequence(t *testing.T) {
	h := newAPCHelper(t)
	const w, hgt = 16, 8
	raw := make([]byte, w*hgt*4)
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+3] = 0xFF, 0xFF
	}
	enc := zlibB64(t, raw)
	// Small chunks force the m=1 path, as ttt's 4096-byte chunks do for any
	// real image.
	const chunk = 8
	for lo := 0; lo < len(enc); lo += chunk {
		hi := min(lo+chunk, len(enc))
		m := 0
		if hi < len(enc) {
			m = 1
		}
		params := fmt.Sprintf("m=%d", m)
		if lo == 0 {
			params = fmt.Sprintf("a=t,q=2,f=32,o=z,t=d,s=%d,v=%d,i=1,m=%d", w, hgt, m)
		}
		h.feedAPC(params + ";" + enc[lo:hi])
	}
	if h.p.kittyStore[1].path == "" {
		t.Fatal("o=z transmission not stored")
	}
	h.feed("\x1b[3;5H")
	h.feedAPC("a=p,q=2,i=1,p=1,C=1,x=0,y=0,w=16,h=8,c=4,r=2,z=0;")
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics after a=p; want 1", len(h.g.Graphics))
	}
	if h.g.CursorR != 2 || h.g.CursorC != 4 {
		t.Errorf("cursor = %d,%d; want 2,4 (C=1 keeps it)", h.g.CursorR, h.g.CursorC)
	}
	if len(h.replies) != 0 {
		t.Errorf("q=2 still replied: %q", h.replies)
	}
}

// o=z on a PNG (f=100) is legal, if pointless; it must inflate too.
func TestParser_APC_KittyZlibPNG(t *testing.T) {
	h := newAPCHelper(t)
	png, _ := makePNG(t)
	h.feedAPC("a=T,f=100,o=z,q=1;" + zlibB64(t, png))
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(h.g.Graphics))
	}
}

// A small deflated payload can expand to gigabytes. A raw image's inflated
// size is known from s=/v=, so anything past it is refused, not buffered.
func TestParser_APC_KittyZlibBombRefused(t *testing.T) {
	h := newAPCHelper(t)
	h.feedAPC("a=T,f=32,o=z,s=2,v=2,i=9,q=0;" + zlibB64(t, make([]byte, 1<<20)))
	if len(h.g.Graphics) != 0 {
		t.Fatalf("got %d graphics; want 0 (oversize inflate)", len(h.g.Graphics))
	}
	if len(h.replies) != 1 || !bytes.Contains(h.replies[0], []byte("EINVAL")) {
		t.Fatalf("replies = %q; want one EINVAL", h.replies)
	}
}

// Corrupt deflate data is an error reply, not a panic or a half image.
func TestParser_APC_KittyZlibCorruptRefused(t *testing.T) {
	h := newAPCHelper(t)
	bad := base64.StdEncoding.EncodeToString([]byte("not zlib at all"))
	h.feedAPC("a=T,f=32,o=z,s=1,v=1,i=9,q=0;" + bad)
	if len(h.g.Graphics) != 0 {
		t.Fatalf("got %d graphics; want 0", len(h.g.Graphics))
	}
	if len(h.replies) != 1 || !bytes.Contains(h.replies[0], []byte("EINVAL")) {
		t.Fatalf("replies = %q; want one EINVAL", h.replies)
	}
}
