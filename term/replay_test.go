package term

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-gui-org/go-term/internal/recfmt"
)

// testFrame describes one frame for buildRec. Deltas are given explicitly so
// timing assertions do not depend on how fast the test machine runs.
type testFrame struct {
	data       []byte
	delta      time.Duration
	rows, cols int
	kind       byte
}

// buildRec writes a recording with exact frame deltas and returns its path.
func buildRec(t *testing.T, frames ...testFrame) string {
	t.Helper()
	var b []byte
	b = append(b, `{"goterm":1,"width":80,"height":24}`...)
	b = append(b, '\n')
	for _, f := range frames {
		b = append(b, f.kind)
		b = binary.AppendUvarint(b, uint64(f.delta.Microseconds()))
		if f.kind == recfmt.KindResize {
			b = binary.AppendUvarint(b, uint64(f.rows))
			b = binary.AppendUvarint(b, uint64(f.cols))
			continue
		}
		b = binary.AppendUvarint(b, uint64(len(f.data)))
		b = append(b, f.data...)
	}
	path := filepath.Join(t.TempDir(), "r.gtr")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// openTestReplay opens a replay with the real sleep replaced by a recorder,
// so playback runs instantly and the requested delays can be asserted.
func openTestReplay(t *testing.T, rc ReplayCfg) (*replayPTY, *[]time.Duration) {
	t.Helper()
	p, err := openReplay(rc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	// Guarded: some tests read from a second goroutine while the main one
	// drives the playback controls.
	var mu sync.Mutex
	var slept []time.Duration
	p.sleep = func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
	}
	return p, &slept
}

// Replay must deliver output frames in order and skip input and resize
// frames — they are context for a human, not bytes the emulator consumed.
func TestReplaySkipsNonOutputFrames(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("one")},
		testFrame{kind: recfmt.KindInput, data: []byte("typed")},
		testFrame{kind: recfmt.KindResize, rows: 40, cols: 120},
		testFrame{kind: recfmt.KindOutput, data: []byte("two")},
	)
	p, _ := openTestReplay(t, ReplayCfg{Path: path})

	got := make([]string, 0, 2)
	buf := make([]byte, 64)
	for range 2 {
		n, err := p.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, string(buf[:n]))
	}
	if got[0] != "one" || got[1] != "two" {
		t.Errorf("reads = %q, want [one two]", got)
	}
}

// A frame larger than the caller's buffer must come back in pieces. This is
// the real case: readLoop reads 4 KiB at a time and a graphics blob is far
// bigger.
func TestReplaySplitsLargeFrame(t *testing.T) {
	payload := bytes.Repeat([]byte("ab"), 100) // 200 bytes
	path := buildRec(t, testFrame{kind: recfmt.KindOutput, data: payload})
	p, _ := openTestReplay(t, ReplayCfg{Path: path})

	got := make([]byte, 0, len(payload))
	buf := make([]byte, 64)
	for range 4 {
		n, err := p.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("reassembled %d bytes, want %d", len(got), len(payload))
	}
}

// Speed divides the recorded gap; IdleLimit caps it. Both apply to the gap
// before a frame, and the cap is applied before the speed division.
func TestReplaySpeedAndIdleLimit(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("a"), delta: 100 * time.Millisecond},
		testFrame{kind: recfmt.KindOutput, data: []byte("b"), delta: 10 * time.Second},
	)
	p, slept := openTestReplay(t, ReplayCfg{
		Path:      path,
		Speed:     2,
		IdleLimit: 2 * time.Second,
	})

	buf := make([]byte, 16)
	for range 2 {
		if _, err := p.Read(buf); err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	want := []time.Duration{50 * time.Millisecond, time.Second}
	if len(*slept) != 2 || (*slept)[0] != want[0] || (*slept)[1] != want[1] {
		t.Errorf("slept = %v, want %v", *slept, want)
	}
}

// Space pauses playback; a step releases exactly one frame and stays paused;
// space again resumes.
func TestReplayPauseStepResume(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("a")},
		testFrame{kind: recfmt.KindOutput, data: []byte("b")},
		testFrame{kind: recfmt.KindOutput, data: []byte("c")},
	)
	p, _ := openTestReplay(t, ReplayCfg{Path: path, Controls: true})

	buf := make([]byte, 16)
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := p.Write([]byte(" ")); err != nil { // pause
		t.Fatal(err)
	}

	// A paused read must block. Prove it by racing it against a step: the
	// read may only complete once the step is granted.
	done := make(chan string, 1)
	go func() {
		n, err := p.Read(buf)
		if err != nil {
			done <- "err: " + err.Error()
			return
		}
		done <- string(buf[:n])
	}()
	select {
	case got := <-done:
		t.Fatalf("paused read returned %q; want it to block", got)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := p.Write([]byte(".")); err != nil { // step one frame
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got != "b" {
			t.Errorf("stepped read = %q, want b", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("step did not release a frame")
	}

	// Still paused after the step, so resume to get the last frame.
	if _, err := p.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "c" {
		t.Errorf("resumed read = %q, %v; want c", buf[:n], err)
	}
}

// The right-arrow sequence must step, like '.', without its ESC and '[' also
// being read as separate commands.
func TestReplayRightArrowSteps(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("a")},
		testFrame{kind: recfmt.KindOutput, data: []byte("b")},
	)
	p, _ := openTestReplay(t, ReplayCfg{Path: path, Controls: true})
	buf := make([]byte, 16)
	if _, err := p.Read(buf); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("\x1b[C")); err != nil {
		t.Fatal(err)
	}
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "b" {
		t.Errorf("read after Right = %q, %v; want b", buf[:n], err)
	}
	if !p.isPaused() {
		t.Error("Right should leave playback paused")
	}
}

// '+' and '-' scale playback speed and clamp at the bounds.
func TestReplaySpeedControls(t *testing.T) {
	path := buildRec(t, testFrame{kind: recfmt.KindOutput, data: []byte("a")})
	p, _ := openTestReplay(t, ReplayCfg{Path: path, Controls: true})

	if _, err := p.Write([]byte("++")); err != nil {
		t.Fatal(err)
	}
	if got := p.speedVal(); got != 4 {
		t.Errorf("speed after ++ = %v, want 4", got)
	}
	if _, err := p.Write([]byte("---")); err != nil {
		t.Fatal(err)
	}
	if got := p.speedVal(); got != 0.5 {
		t.Errorf("speed after --- = %v, want 0.5", got)
	}
	if _, err := p.Write([]byte("--------------")); err != nil {
		t.Fatal(err)
	}
	if got := p.speedVal(); got != minReplaySpeed {
		t.Errorf("speed = %v, want clamp at %v", got, minReplaySpeed)
	}
}

// '0' rewinds to the first frame.
func TestReplayRestartControl(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("a")},
		testFrame{kind: recfmt.KindOutput, data: []byte("b")},
	)
	p, _ := openTestReplay(t, ReplayCfg{Path: path, Controls: true})

	buf := make([]byte, 16)
	for range 2 {
		if _, err := p.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Write([]byte("0")); err != nil {
		t.Fatal(err)
	}
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "a" {
		t.Errorf("read after restart = %q, %v; want a", buf[:n], err)
	}
}

// With Controls off, keystrokes are swallowed: they must not pause playback
// and must report as fully written, so the widget never sees a short write.
func TestReplayControlsDisabled(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("a")},
		testFrame{kind: recfmt.KindOutput, data: []byte("b")},
	)
	p, _ := openTestReplay(t, ReplayCfg{Path: path})

	in := []byte(" .0+")
	n, err := p.Write(in)
	if n != len(in) || err != nil {
		t.Errorf("Write = %d, %v; want %d, nil", n, err, len(in))
	}
	buf := make([]byte, 16)
	if _, err := p.Read(buf); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(buf); err != nil {
		t.Fatalf("playback should not have paused: %v", err)
	}
}

// At end of stream playback holds rather than reporting EOF, so the final
// frame stays on screen and the pane is not torn down. Close releases it.
func TestReplayParksAtEndUntilClose(t *testing.T) {
	path := buildRec(t, testFrame{kind: recfmt.KindOutput, data: []byte("a")})
	p, _ := openTestReplay(t, ReplayCfg{Path: path})

	buf := make([]byte, 16)
	if _, err := p.Read(buf); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := p.Read(buf)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("read at end returned %v; want it to park", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Errorf("read after Close = %v, want EOF", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release the parked read")
	}
}

// Loop restarts at end of stream instead of parking.
func TestReplayLoop(t *testing.T) {
	path := buildRec(t, testFrame{kind: recfmt.KindOutput, data: []byte("a")})
	p, _ := openTestReplay(t, ReplayCfg{Path: path, Loop: true})

	buf := make([]byte, 16)
	for i := range 3 {
		n, err := p.Read(buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(buf[:n]) != "a" {
			t.Fatalf("read %d = %q, want a", i, buf[:n])
		}
	}
}

// A truncated recording plays what survived and then holds, rather than
// failing to open or tearing the pane down mid-frame.
func TestReplayTruncatedRecording(t *testing.T) {
	path := buildRec(t,
		testFrame{kind: recfmt.KindOutput, data: []byte("good")},
		testFrame{kind: recfmt.KindOutput, data: []byte("cut short")},
	)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, full[:len(full)-4], 0o600); err != nil {
		t.Fatal(err)
	}

	p, _ := openTestReplay(t, ReplayCfg{Path: path})
	buf := make([]byte, 16)
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "good" {
		t.Errorf("read = %q, %v; want good", buf[:n], err)
	}
}

// A missing or malformed recording must fail at open, not halfway through
// building a Term.
func TestOpenReplayErrors(t *testing.T) {
	if _, err := openReplay(ReplayCfg{}); err == nil {
		t.Error("empty path: want error")
	}
	if _, err := openReplay(ReplayCfg{Path: filepath.Join(t.TempDir(), "nope.gtr")}); err == nil {
		t.Error("missing file: want error")
	}
	bad := filepath.Join(t.TempDir(), "bad.gtr")
	if err := os.WriteFile(bad, []byte("not a recording\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openReplay(ReplayCfg{Path: bad}); err == nil {
		t.Error("bad header: want error")
	}
}
