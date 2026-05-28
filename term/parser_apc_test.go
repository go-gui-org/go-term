package term

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
)

// apcHelper wraps a fresh parser+grid for APC/KGP tests.
type apcHelper struct {
	g       *Grid
	p       *Parser
	replies [][]byte
	dir     string
}

func newAPCHelper(t *testing.T) *apcHelper {
	t.Helper()
	g := NewGrid(24, 80)
	g.CellPxW, g.CellPxH = 8, 16
	p := NewParser(g)
	p.SetGraphicsDir(t.TempDir())
	h := &apcHelper{g: g, p: p, dir: p.graphicsDir}
	p.SetReplyHandler(func(b []byte) {
		cp := make([]byte, len(b))
		copy(cp, b)
		h.replies = append(h.replies, cp)
	})
	return h
}

func (h *apcHelper) feed(s string) {
	h.p.Feed([]byte(s))
}

// feedAPC wraps payload in ESC _ G … ESC \.
func (h *apcHelper) feedAPC(payload string) {
	h.feed("\x1b_G" + payload + "\x1b\\")
}

// makePNG creates a minimal valid 1×1 PNG and returns its bytes + base64.
func makePNG(t *testing.T) ([]byte, string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0xFF, G: 0, B: 0, A: 0xFF})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	raw := buf.Bytes()
	return raw, base64.StdEncoding.EncodeToString(raw)
}

// --- State machine tests ---

func TestParser_APC_StateTransition(t *testing.T) {
	h := newAPCHelper(t)
	// ESC _ must enter stAPC; ESC \ must trigger dispatch and return to stGround.
	// Non-G payload is dropped silently (no reply, no graphics).
	h.feedAPC("a=q,q=2;")
	// q=2 suppresses reply — just verify no panic and state is clean.
	if h.p.state != stGround {
		t.Fatalf("state = %d; want stGround after ESC \\", h.p.state)
	}
}

func TestParser_APC_BareESCAborts(t *testing.T) {
	h := newAPCHelper(t)
	// ESC _ G ... ESC [something other than \] → abort APC, restart as ESC.
	// The 'P' after the bare ESC starts a DCS; the whole sequence should not crash.
	h.feed("\x1b_Gfoo\x1bP\x1b\\")
	if h.p.state != stGround {
		t.Fatalf("state = %d after abort; want stGround", h.p.state)
	}
}

func TestParser_APC_NonKittyIgnored(t *testing.T) {
	h := newAPCHelper(t)
	// APC payload not starting with 'G' must be silently dropped.
	// feedAPC adds 'G', so use raw feed here to send a non-G APC.
	h.feed("\x1b_Xfoo=bar;\x1b\\")
	if len(h.replies) != 0 {
		t.Fatalf("got %d replies; want 0 for non-G APC", len(h.replies))
	}
	if len(h.g.Graphics) != 0 {
		t.Fatalf("got %d graphics; want 0", len(h.g.Graphics))
	}
}

// --- Query ---

func TestParser_APC_KittyQuery(t *testing.T) {
	h := newAPCHelper(t)
	h.feedAPC("a=q,i=42;")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1", len(h.replies))
	}
	got := string(h.replies[0])
	if !strings.Contains(got, "i=42") {
		t.Errorf("reply %q missing i=42", got)
	}
	if !strings.Contains(got, "OK") {
		t.Errorf("reply %q missing OK", got)
	}
}

func TestParser_APC_KittyQuietMode(t *testing.T) {
	h := newAPCHelper(t)
	// q=2 suppresses all replies.
	h.feedAPC("a=q,i=1,q=2;")
	if len(h.replies) != 0 {
		t.Fatalf("got %d replies with q=2; want 0", len(h.replies))
	}
	// q=1 suppresses OK but not errors.
	h.feedAPC("a=q,i=2,q=1;")
	if len(h.replies) != 0 {
		// query action always succeeds → OK → suppressed by q=1
		t.Fatalf("got %d replies with q=1 for OK; want 0", len(h.replies))
	}
}

// --- Transmit + display (a=T) ---

func TestParser_APC_KittyTransmitAndDisplay(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=T,f=100,q=1;" + b64)
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics; want 1", len(h.g.Graphics))
	}
}

// --- Transmit only (a=t) → stored in kittyStore, not displayed ---

func TestParser_APC_KittyTransmitPNG_Store(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=t,f=100,i=7,q=1;" + b64)
	if len(h.g.Graphics) != 0 {
		t.Fatalf("action=t should not display; got %d graphics", len(h.g.Graphics))
	}
	if h.p.kittyStore == nil || h.p.kittyStore[7].path == "" {
		t.Fatal("image not stored in kittyStore[7]")
	}
}

// --- Place (a=p) ---

func TestParser_APC_KittyPlace(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	// Transmit with id=3.
	h.feedAPC("a=t,f=100,i=3,q=1;" + b64)
	if h.p.kittyStore == nil || h.p.kittyStore[3].path == "" {
		t.Fatal("transmit failed; kittyStore[3] empty")
	}
	// Place: should add a graphic.
	h.feedAPC("a=p,i=3,q=1;")
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics after place; want 1", len(h.g.Graphics))
	}
}

// --- Chunked transmission ---

func TestParser_APC_KittyChunked(t *testing.T) {
	h := newAPCHelper(t)
	raw, _ := makePNG(t)
	// Split base64 into two halves.
	full := base64.StdEncoding.EncodeToString(raw)
	mid := len(full) / 2
	part1 := full[:mid]
	part2 := full[mid:]

	// First chunk: m=1 (more).
	h.feedAPC("a=T,f=100,i=5,m=1,q=1;" + part1)
	if len(h.g.Graphics) != 0 {
		t.Fatalf("should not display mid-chunk; got %d graphics", len(h.g.Graphics))
	}
	// Last chunk: m=0 (default / omitted).
	h.feedAPC("a=T,f=100,i=5,m=0,q=1;" + part2)
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics after final chunk; want 1", len(h.g.Graphics))
	}
}

// --- Delete (a=d) ---

func TestParser_APC_KittyDelete(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=t,f=100,i=9,q=1;" + b64)
	if h.p.kittyStore[9].path == "" {
		t.Fatal("transmit failed")
	}
	h.feedAPC("a=d,i=9,q=2;")
	if h.p.kittyStore != nil && h.p.kittyStore[9].path != "" {
		t.Fatal("image not deleted from kittyStore")
	}
}

func TestParser_APC_KittyDeleteAll(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=t,f=100,i=10,q=1;" + b64)
	h.feedAPC("a=t,f=100,i=11,q=1;" + b64)
	h.feedAPC("a=d,d=a,q=2;")
	if len(h.p.kittyStore) != 0 {
		t.Fatalf("kittyStore still has %d entries after d=a", len(h.p.kittyStore))
	}
}

// --- Raw RGBA / RGB ---

func TestParser_APC_KittyRGBA(t *testing.T) {
	// 2×2 RGBA image: 4 pixels × 4 bytes = 16 bytes.
	raw := make([]byte, 16)
	for i := range 4 {
		raw[i*4+0] = 0xFF // R
		raw[i*4+3] = 0xFF // A
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	h := newAPCHelper(t)
	h.feedAPC("a=T,f=32,s=2,v=2,q=1;" + b64)
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics for RGBA; want 1", len(h.g.Graphics))
	}
}

func TestParser_APC_KittyRGB(t *testing.T) {
	// 2×2 RGB image: 4 pixels × 3 bytes = 12 bytes.
	raw := make([]byte, 12)
	for i := range 4 {
		raw[i*3+0] = 0xFF // R
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	h := newAPCHelper(t)
	h.feedAPC("a=T,f=24,s=2,v=2,q=1;" + b64)
	if len(h.g.Graphics) != 1 {
		t.Fatalf("got %d graphics for RGB; want 1", len(h.g.Graphics))
	}
}

// --- Error handling ---

func TestParser_APC_KittyBadBase64(t *testing.T) {
	h := newAPCHelper(t)
	// Invalid base64 → decode fails → image silently dropped.
	h.feedAPC("a=T,f=100,q=2;!!!notbase64!!!")
	if len(h.g.Graphics) != 0 {
		t.Fatalf("got %d graphics for bad base64; want 0", len(h.g.Graphics))
	}
}

func TestParser_APC_KittyRGBAMissingDimensions(t *testing.T) {
	raw := make([]byte, 16)
	b64 := base64.StdEncoding.EncodeToString(raw)
	h := newAPCHelper(t)
	// f=32 without s=/v= → w=0,h=0 → decode fails silently.
	h.feedAPC("a=T,f=32,q=2;" + b64)
	if len(h.g.Graphics) != 0 {
		t.Fatalf("got %d graphics with missing dims; want 0", len(h.g.Graphics))
	}
}

// --- kittyRawToNRGBA unit tests ---

func TestKittyRawToNRGBA_RGBA(t *testing.T) {
	raw := []byte{0xFF, 0x00, 0x00, 0xFF} // 1×1 opaque red RGBA
	img := kittyRawToNRGBA(raw, 1, 1, 4)
	if img == nil {
		t.Fatal("returned nil")
	}
	got := img.NRGBAAt(0, 0)
	want := color.NRGBA{R: 0xFF, G: 0, B: 0, A: 0xFF}
	if got != want {
		t.Errorf("pixel = %v; want %v", got, want)
	}
}

func TestKittyRawToNRGBA_RGB(t *testing.T) {
	raw := []byte{0x00, 0xFF, 0x00} // 1×1 green RGB
	img := kittyRawToNRGBA(raw, 1, 1, 3)
	if img == nil {
		t.Fatal("returned nil")
	}
	got := img.NRGBAAt(0, 0)
	want := color.NRGBA{R: 0, G: 0xFF, B: 0, A: 0xFF}
	if got != want {
		t.Errorf("pixel = %v; want %v", got, want)
	}
}

func TestKittyRawToNRGBA_TooShort(t *testing.T) {
	raw := []byte{0xFF, 0x00} // 2×2 RGBA needs 16 bytes
	if img := kittyRawToNRGBA(raw, 2, 2, 4); img != nil {
		t.Fatal("expected nil for too-short raw data")
	}
}

func TestKittyRawToNRGBA_InvalidDims(t *testing.T) {
	if img := kittyRawToNRGBA([]byte{0xFF}, 0, 1, 4); img != nil {
		t.Fatal("expected nil for w=0")
	}
}

// --- DoS guards ---

func TestParser_APC_KittyChunked_PendingChunkCap(t *testing.T) {
	h := newAPCHelper(t)
	// Fill maxKittyPendingChunks slots with open (m=1) streams.
	for i := 1; i <= maxKittyPendingChunks; i++ {
		h.feedAPC(fmt.Sprintf("a=T,f=100,i=%d,m=1;AAAA", i))
	}
	if len(h.p.kittyChunks) != maxKittyPendingChunks {
		t.Fatalf("kittyChunks = %d; want %d", len(h.p.kittyChunks), maxKittyPendingChunks)
	}
	// A new ID when the table is full must be silently dropped.
	h.feedAPC(fmt.Sprintf("a=T,f=100,i=%d,m=1;AAAA", maxKittyPendingChunks+1))
	if len(h.p.kittyChunks) > maxKittyPendingChunks {
		t.Fatalf("kittyChunks grew to %d; cap is %d", len(h.p.kittyChunks), maxKittyPendingChunks)
	}
}

func TestParser_APC_KittyStore_EvictsAtCapacity(t *testing.T) {
	h := newAPCHelper(t)
	// Pre-fill store to capacity with dummy (path-less) entries.
	h.p.kittyStore = make(map[uint32]kittyEntry, maxKittyStoreEntries)
	for i := uint32(1); i <= maxKittyStoreEntries; i++ {
		h.p.kittyStore[i] = kittyEntry{path: ""}
	}
	_, b64 := makePNG(t)
	h.feedAPC(fmt.Sprintf("a=t,f=100,i=%d,q=1;", maxKittyStoreEntries+1) + b64)
	// One old entry evicted, new entry added: total stays at capacity.
	if len(h.p.kittyStore) != maxKittyStoreEntries {
		t.Fatalf("store has %d entries after eviction; want %d", len(h.p.kittyStore), maxKittyStoreEntries)
	}
}

func TestParser_APC_KittyChunked_OversizeDropped(t *testing.T) {
	h := newAPCHelper(t)
	// Pre-fill accumulator to the per-image byte cap.
	h.p.kittyChunks = map[uint32][]byte{42: make([]byte, maxKittyImageBytes)}
	// Additional data must be silently dropped (would exceed cap).
	h.feedAPC("a=T,f=100,i=42,m=1;AAAA")
	if got := len(h.p.kittyChunks[42]); got != maxKittyImageBytes {
		t.Fatalf("accumulator grew to %d after cap; want %d", got, maxKittyImageBytes)
	}
}

// --- Additional action tests ---

func TestParser_APC_KittyDeleteAll_Uppercase(t *testing.T) {
	h := newAPCHelper(t)
	_, b64 := makePNG(t)
	h.feedAPC("a=t,f=100,i=10,q=1;" + b64)
	if h.p.kittyStore[10].path == "" {
		t.Fatal("transmit failed")
	}
	h.feedAPC("a=d,d=A,q=2;")
	if len(h.p.kittyStore) != 0 {
		t.Fatalf("kittyStore has %d entries after d=A; want 0", len(h.p.kittyStore))
	}
}

func TestParser_APC_KittyUnknownAction_ErrorReply(t *testing.T) {
	h := newAPCHelper(t)
	h.feedAPC("a=z,i=1;")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for unknown action", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
}

func TestParser_APC_KittyPlace_UnknownID_ErrorReply(t *testing.T) {
	h := newAPCHelper(t)
	h.feedAPC("a=p,i=99;")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for unknown id", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
}

// --- File/temp-file medium rejection ---

func TestParser_APC_KittyFileMediumRejected(t *testing.T) {
	h := newAPCHelper(t)
	// t=f requests pixel data from a host file path — not implemented.
	h.feedAPC("a=t,t=f,i=1;/some/path")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for t=f", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
	if len(h.p.kittyStore) != 0 {
		t.Errorf("kittyStore must stay empty after t=f rejection, got %d entries", len(h.p.kittyStore))
	}
}

func TestParser_APC_KittyTempFileMediumRejected(t *testing.T) {
	h := newAPCHelper(t)
	// t=t (temp file) is also rejected.
	h.feedAPC("a=t,t=t,i=2;/tmp/img")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for t=t", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
	if len(h.p.kittyStore) != 0 {
		t.Errorf("kittyStore must stay empty after t=t rejection, got %d entries", len(h.p.kittyStore))
	}
}

func TestParser_APC_KittyShmMediumRejected(t *testing.T) {
	h := newAPCHelper(t)
	// t=s (shared memory) is also a host resource — reject before accumulating.
	h.feedAPC("a=t,t=s,i=4;kitty-shm-XXXXX")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for t=s", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
	if len(h.p.kittyStore) != 0 {
		t.Errorf("kittyStore must stay empty after t=s rejection, got %d entries", len(h.p.kittyStore))
	}
}

func TestParser_APC_KittyTransmitAndDisplay_FileMediumRejected(t *testing.T) {
	h := newAPCHelper(t)
	// a=T (transmit+display) with t=f must also be rejected — no image placed.
	h.feedAPC("a=T,t=f,i=3;/path")
	if len(h.replies) != 1 {
		t.Fatalf("got %d replies; want 1 error reply for a=T,t=f", len(h.replies))
	}
	if !strings.Contains(string(h.replies[0]), "EINVAL") {
		t.Errorf("reply %q missing EINVAL", string(h.replies[0]))
	}
	if len(h.g.Graphics) != 0 {
		t.Errorf("Graphics must be empty after a=T,t=f rejection, got %d", len(h.g.Graphics))
	}
}

func TestParser_APC_KittyStore_EvictsFallback_AllVisible(t *testing.T) {
	h := newAPCHelper(t)
	tmpDir := t.TempDir()

	// Fill store to capacity; mark every entry as visible in Graphics
	// so the inUse check never fires — the fallback path must evict.
	h.p.kittyStore = make(map[uint32]kittyEntry, maxKittyStoreEntries)
	for i := uint32(1); i <= maxKittyStoreEntries; i++ {
		f, err := os.CreateTemp(tmpDir, "kgp-*.png")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		path := f.Name()
		_ = f.Close()
		h.p.kittyStore[i] = kittyEntry{path: path, w: 1, h: 1}
		h.p.g.Graphics = append(h.p.g.Graphics, Graphic{Src: path})
	}

	_, b64 := makePNG(t)
	h.feedAPC(fmt.Sprintf("a=t,f=100,i=%d,q=1;", maxKittyStoreEntries+1) + b64)

	if len(h.p.kittyStore) != maxKittyStoreEntries {
		t.Fatalf("store has %d entries after fallback eviction; want %d",
			len(h.p.kittyStore), maxKittyStoreEntries)
	}
	if len(h.p.g.Graphics) != maxKittyStoreEntries-1 {
		t.Errorf("Graphics should have one entry removed by fallback eviction: got %d; want %d",
			len(h.p.g.Graphics), maxKittyStoreEntries-1)
	}
}
