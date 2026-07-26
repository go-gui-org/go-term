package recfmt

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readAllFrames decodes every frame in a recording, copying payloads out of
// the reader's shared buffer so the caller can compare them afterwards.
func readAllFrames(t *testing.T, path string) (Header, []Frame, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rr, err := NewReader(f)
	if err != nil {
		return Header{}, nil, err
	}
	var frames []Frame
	for {
		fr, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return rr.Header(), frames, nil
		}
		if err != nil {
			return rr.Header(), frames, err
		}
		fr.Data = bytes.Clone(fr.Data)
		frames = append(frames, fr)
	}
}

// A recording must round-trip every frame kind, and payload bytes must come
// back exactly — including malformed UTF-8 and NULs, which are precisely the
// byte sequences a rendering-bug report needs to carry.
func TestRecorderRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	binary := []byte{0x00, 0xff, 0xfe, 0x1b, '[', '3', '1', 'm', 0xc3, 0x28}

	r, err := NewRecorder(path, 24, 80, map[string]string{"TERM": "xterm-256color"}, true)
	if err != nil {
		t.Fatal(err)
	}
	r.Output([]byte("hello"))
	r.Input([]byte("ls\r"))
	r.Resize(30, 100)
	r.Output(binary)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	hdr, frames, err := readAllFrames(t, path)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hdr.Goterm != FormatVersion || hdr.Width != 80 || hdr.Height != 24 {
		t.Errorf("header = %+v, want goterm=%d width=80 height=24", hdr, FormatVersion)
	}
	if hdr.Env["TERM"] != "xterm-256color" {
		t.Errorf("header env = %v", hdr.Env)
	}
	if len(frames) != 4 {
		t.Fatalf("frames = %d, want 4", len(frames))
	}
	if frames[0].Kind != KindOutput || string(frames[0].Data) != "hello" {
		t.Errorf("frame 0 = %c %q", frames[0].Kind, frames[0].Data)
	}
	if frames[1].Kind != KindInput || string(frames[1].Data) != "ls\r" {
		t.Errorf("frame 1 = %c %q", frames[1].Kind, frames[1].Data)
	}
	if frames[2].Kind != KindResize || frames[2].Rows != 30 || frames[2].Cols != 100 {
		t.Errorf("frame 2 = %c %dx%d", frames[2].Kind, frames[2].Rows, frames[2].Cols)
	}
	if !bytes.Equal(frames[3].Data, binary) {
		t.Errorf("frame 3 = % x, want % x", frames[3].Data, binary)
	}
}

// Input frames must be dropped unless the recording explicitly asked for
// them: keystroke capture is opt-in.
func TestRecorderInputOptIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	r, err := NewRecorder(path, 24, 80, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	r.Input([]byte("secret"))
	r.Output([]byte("x"))
	_ = r.Close()

	_, frames, err := readAllFrames(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || frames[0].Kind != KindOutput {
		t.Fatalf("frames = %+v, want one output frame", frames)
	}
}

// Inter-frame deltas must reflect the wall-clock gap between writes.
func TestRecorderTiming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	r, err := NewRecorder(path, 24, 80, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	r.Output([]byte("a"))
	time.Sleep(30 * time.Millisecond)
	r.Output([]byte("b"))
	_ = r.Close()

	_, frames, err := readAllFrames(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	// Generous lower bound only: CI timers are coarse, and an upper bound
	// would make this flaky on a loaded machine.
	if frames[1].Delta < 20*time.Millisecond {
		t.Errorf("delta = %v, want >= 20ms", frames[1].Delta)
	}
}

// Steady-state recording must not allocate: the reader goroutine writes a
// frame for every pty read, and this sits directly in that path.
func TestRecorderFrameAllocations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	r, err := NewRecorder(path, 24, 80, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	chunk := bytes.Repeat([]byte("x"), 4096)
	r.Output(chunk) // grow the frame buffer before measuring
	if n := testing.AllocsPerRun(50, func() { r.Output(chunk) }); n != 0 {
		t.Errorf("allocs per frame = %v, want 0", n)
	}
}

// A truncated recording — the shape a crash leaves behind — must yield the
// frames that did survive, flagged as an unexpected EOF rather than a clean
// end of stream.
func TestRecReaderTruncatedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	r, err := NewRecorder(path, 24, 80, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	r.Output([]byte("first"))
	r.Output([]byte("second"))
	_ = r.Close()

	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cut := filepath.Join(t.TempDir(), "cut.gtr")
	if err := os.WriteFile(cut, full[:len(full)-3], 0o600); err != nil {
		t.Fatal(err)
	}

	_, frames, err := readAllFrames(t, cut)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want ErrUnexpectedEOF", err)
	}
	if len(frames) != 1 || string(frames[0].Data) != "first" {
		t.Errorf("frames = %+v, want the one complete frame", frames)
	}
}

// Header sniffing must reject files that are not go-term recordings —
// notably asciicast, whose header is superficially similar.
func TestRecReaderRejectsForeignHeaders(t *testing.T) {
	cases := map[string]string{
		"asciicast":     "{\"version\":2,\"width\":80,\"height\":24}\n",
		"wrong version": "{\"goterm\":99}\n",
		"not json":      "hello\n",
		"no newline":    "{\"goterm\":1}",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewReader(strings.NewReader(body)); err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

// An over-long header line must be rejected by the bound, not absorbed into
// memory: a recording is untrusted the moment it arrives in a bug report.
func TestRecReaderHeaderTooLong(t *testing.T) {
	body := "{\"goterm\":1,\"pad\":\"" + strings.Repeat("a", MaxHeader) + "\"}\n"
	_, err := NewReader(strings.NewReader(body))
	if !errors.Is(err, ErrHeaderTooLong) {
		t.Errorf("err = %v, want ErrHeaderTooLong", err)
	}
}

// An unknown frame kind must fail rather than resynchronise on garbage.
func TestRecReaderUnknownFrameKind(t *testing.T) {
	body := "{\"goterm\":1}\n\x99\x00\x00"
	rr, err := NewReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rr.Next(); err == nil {
		t.Error("want error for unknown frame kind")
	}
}

// The raw mode behind GOTERM_CAPTURE writes payload bytes only — no header,
// no framing — so existing capture files stay byte-identical.
func TestRawRecorderWritesBareBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRawRecorder(f)
	r.Output([]byte("abc"))
	r.Input([]byte("ignored"))
	r.Resize(10, 10)
	r.Output([]byte("def"))
	_ = r.Close()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdef" {
		t.Errorf("raw capture = %q, want %q", got, "abcdef")
	}
}

// Every recorder method must tolerate a nil receiver: the hot-path hooks
// call them unconditionally.
func TestRecorderNilSafe(t *testing.T) {
	var r *Recorder
	r.Output([]byte("x"))
	r.Input([]byte("x"))
	r.Resize(1, 1)
	if r.Active() || r.Elapsed() != 0 || r.Close() != nil {
		t.Error("nil recorder should be inert")
	}
}
