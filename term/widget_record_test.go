package term

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	gui "github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/internal/recfmt"
)

// recTerm builds a bare Term whose pty replays the given chunks, records to
// path, then runs readLoop to completion. Returns the Term for inspection.
func recTerm(t *testing.T, path string, cfg Cfg, chunks ...[]byte) *Term {
	t.Helper()
	p := &scriptPty{chunks: chunks}
	g := newGrid(6, 40)
	tm := &Term{
		grid:     g,
		parser:   newParser(g),
		cmd:      &gui.Window{},
		cfg:      cfg,
		pty:      p,
		pw:       p,
		readDone: make(chan struct{}),
	}
	if err := tm.StartRecording(path); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	go tm.readLoop()
	select {
	case <-tm.readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit")
	}
	if err := tm.StopRecording(); err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	return tm
}

// The end-to-end contract: bytes recorded off a pty, concatenated back out
// of the recording, must drive a grid to exactly the state the live terminal
// reached. This is what makes a recording usable as a bug report and as a
// source for the EmulatorReplay fixture harness.
func TestRecordingReproducesGridState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	chunks := [][]byte{
		[]byte("\x1b[2J\x1b[H$ ls\r\n"),
		[]byte("\x1b[1;34mDocuments\x1b[0m  README.md\r\n"),
		[]byte("$ \xff\xfe binary\r\n"), // malformed UTF-8 must survive
	}
	live := recTerm(t, path, Cfg{}, chunks...)

	var recorded []byte
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rr, err := recfmt.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	for {
		fr, err := rr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if fr.Kind == recfmt.KindOutput {
			recorded = append(recorded, fr.Data...)
		}
	}

	var want []byte
	for _, c := range chunks {
		want = append(want, c...)
	}
	if !bytes.Equal(recorded, want) {
		t.Fatalf("recorded bytes differ from the pty stream\n got % x\nwant % x", recorded, want)
	}

	// Same bytes through the fixture harness must land on the same screen.
	fix := CaptureFixture("replay", live.grid.Rows, live.grid.Cols, recorded)
	live.grid.Mu.Lock()
	liveLines := gridLines(live.grid)
	live.grid.Mu.Unlock()
	for i := range liveLines {
		if fix.WantLines[i] != liveLines[i] {
			t.Errorf("row %d: replay %q, live %q", i, fix.WantLines[i], liveLines[i])
		}
	}
}

// A recording started mid-session must describe the grid it started on, so
// the file is self-explanatory without the surrounding context.
func TestRecordingHeaderCarriesGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	recTerm(t, path, Cfg{}, []byte("x"))

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	rr, err := recfmt.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if hdr := rr.Header(); hdr.Height != 6 || hdr.Width != 40 {
		t.Errorf("header = %d×%d, want 40×6 (cols×rows)", hdr.Width, hdr.Height)
	}
}

// Keystrokes reach the recording only when Cfg.RecordInput is set, and the
// pty still receives them either way.
func TestRecordingInputFrames(t *testing.T) {
	for _, want := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[want], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.gtr")
			p := &scriptPty{}
			g := newGrid(4, 20)
			tm := &Term{
				grid:     g,
				parser:   newParser(g),
				cmd:      &gui.Window{},
				cfg:      Cfg{RecordInput: want},
				pty:      p,
				pw:       p,
				readDone: make(chan struct{}),
			}
			if err := tm.StartRecording(path); err != nil {
				t.Fatal(err)
			}
			tm.writeBytes([]byte("hello\r"))
			if _, err := tm.Write([]byte("world\r")); err != nil {
				t.Fatal(err)
			}
			if err := tm.StopRecording(); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := bytes.Contains(raw, []byte("hello\r"))
			if got != want {
				t.Errorf("input recorded = %v, want %v", got, want)
			}
		})
	}
}

// Recording is a mode, not a stack: starting twice must fail rather than
// silently truncate the file the user believes is still filling.
func TestStartRecordingTwiceFails(t *testing.T) {
	dir := t.TempDir()
	tm := &Term{grid: newGrid(4, 8)}
	if err := tm.StartRecording(filepath.Join(dir, "a.gtr")); err != nil {
		t.Fatal(err)
	}
	if err := tm.StartRecording(filepath.Join(dir, "b.gtr")); !errors.Is(err, errAlreadyRecording) {
		t.Errorf("second StartRecording = %v, want errAlreadyRecording", err)
	}
	if !tm.Recording() {
		t.Error("Recording() = false during a recording")
	}
	if err := tm.StopRecording(); err != nil {
		t.Fatal(err)
	}
	if tm.Recording() {
		t.Error("Recording() = true after StopRecording")
	}
	// Stopping an idle Term is a no-op, not an error: the toggle command
	// must be safe to fire twice.
	if err := tm.StopRecording(); err != nil {
		t.Errorf("second StopRecording = %v, want nil", err)
	}
}

// StartRecording must create the destination directory. The workspace
// toggle writes into a recordings/ folder that may not exist yet.
func TestStartRecordingCreatesDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "s.gtr")
	tm := &Term{grid: newGrid(4, 8)}
	if err := tm.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tm.StopRecording() }()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("recording file: %v", err)
	}
}

// Recording must not touch the widget's behavior: an unwritable destination
// fails the call, and the terminal keeps working.
func TestStartRecordingBadPathFails(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	// A path whose parent is an existing regular file cannot be created.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tm.StartRecording(filepath.Join(file, "s.gtr")); err == nil {
		t.Error("want error for unwritable path")
	}
	if tm.Recording() {
		t.Error("failed StartRecording must leave the Term not recording")
	}
}

// After Close, StartRecording must return an error rather than opening a file
// on a dead Term. The toggle command fires on the main thread; the Term being
// closed concurrently is a plausible race.
func TestStartRecording_ClosedTermReturnsError(t *testing.T) {
	tm := &Term{grid: newGrid(4, 8)}
	tm.closed.Store(true)
	if err := tm.StartRecording(filepath.Join(t.TempDir(), "c.gtr")); err == nil {
		t.Error("StartRecording on closed Term: want error, got nil")
	}
	if tm.Recording() {
		t.Error("Recording() = true after StartRecording on closed Term")
	}
}

// A resize recorded mid-session must appear as an 'r' frame, so a replay can
// report the geometry the session actually ran at.
func TestRecordingCapturesResize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.gtr")
	p := &recordPty{}
	tm := &Term{grid: newGrid(4, 8), pty: p, blinkDone: make(chan struct{})}
	tm.ptyResizeKick = make(chan struct{}, 1)
	if err := tm.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	tm.loopWg.Add(1)
	go tm.resizeLoop()
	latchResize(tm, 30, 100)
	waitCalls(t, p, [][2]int{{30, 100}})
	close(tm.blinkDone)
	tm.loopWg.Wait()
	if err := tm.StopRecording(); err != nil {
		t.Fatal(err)
	}

	_, frames, err := framesOf(t, path)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, fr := range frames {
		if fr.Kind == recfmt.KindResize && fr.Rows == 30 && fr.Cols == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("no 30×100 resize frame in %d frames", len(frames))
	}
}

// The full loop, with no stubs in the middle: a real shell writes to a real
// pty, the reader goroutine records it, and replaying that file through
// replayPTY into a second Term must reproduce the same screen.
func TestRecordThenReplayRealPTY(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	path := filepath.Join(t.TempDir(), "s.gtr")
	script := `printf 'line one\r\n'; printf '\033[1;31mred\033[0m\r\n'; printf 'done\r\n'`

	p, err := startPTY(6, 40, Cfg{Command: "/bin/sh", Args: []string{"-c", script}})
	if err != nil {
		t.Skipf("startPTY failed (no shell?): %v", err)
	}
	rec := newBareTerm(p, 6, 40)
	if err := rec.StartRecording(path); err != nil {
		t.Fatal(err)
	}
	go rec.readLoop()
	select {
	case <-rec.readDone:
	case <-time.After(10 * time.Second):
		t.Fatal("shell did not exit")
	}
	if err := rec.StopRecording(); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()

	rp, err := openReplay(ReplayCfg{Path: path, Speed: 1000})
	if err != nil {
		t.Fatalf("openReplay: %v", err)
	}
	defer func() { _ = rp.Close() }()
	play := newBareTerm(rp, 6, 40)
	go play.readLoop()
	// Playback parks at the end of the stream rather than reporting EOF, so
	// wait for the screen to match instead of for readLoop to exit.
	deadline := time.Now().Add(10 * time.Second)
	var recLines, playLines []string
	for time.Now().Before(deadline) {
		rec.grid.Mu.Lock()
		recLines = gridLines(rec.grid)
		rec.grid.Mu.Unlock()
		play.grid.Mu.Lock()
		playLines = gridLines(play.grid)
		play.grid.Mu.Unlock()
		if slices.Equal(recLines, playLines) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("replayed screen never matched the recorded one\n live: %q\nreplay: %q",
		recLines, playLines)
}

// newBareTerm builds a Term with just enough wiring for readLoop: no window,
// no auxiliary goroutines.
func newBareTerm(p ptyIO, rows, cols int) *Term {
	g := newGrid(rows, cols)
	return &Term{
		grid:     g,
		parser:   newParser(g),
		cmd:      &gui.Window{},
		pty:      p,
		pw:       p,
		readDone: make(chan struct{}),
	}
}

// framesOf decodes every frame of a recording for assertions.
func framesOf(t *testing.T, path string) (recfmt.Header, []recfmt.Frame, error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return recfmt.Header{}, nil, err
	}
	defer func() { _ = f.Close() }()
	rr, err := recfmt.NewReader(f)
	if err != nil {
		return recfmt.Header{}, nil, err
	}
	var frames []recfmt.Frame
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
