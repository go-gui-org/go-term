package term

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gui "github.com/go-gui-org/go-gui/gui"
)

// recordPty is a ptyIO stub that records Resize calls and blocks Read
// until closed. Used to test resizeLoop in isolation (readLoop not run).
type recordPty struct {
	mu    sync.Mutex
	calls [][2]int
}

func (p *recordPty) Read(b []byte) (int, error)  { return 0, io.EOF }
func (p *recordPty) Write(b []byte) (int, error) { return len(b), nil }
func (p *recordPty) Close() error                { return nil }
func (p *recordPty) PID() int                    { return 0 }
func (p *recordPty) Resize(rows, cols int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, [2]int{rows, cols})
	return nil
}

func (p *recordPty) snapshot() [][2]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][2]int, len(p.calls))
	copy(out, p.calls)
	return out
}

// newResizeLoopTerm builds a bare Term running only resizeLoop against a
// recordPty. Returns the Term, the pty stub, and a teardown.
func newResizeLoopTerm() (*Term, *recordPty, func()) {
	p := &recordPty{}
	tm := &Term{pty: p, blinkDone: make(chan struct{})}
	tm.ptyResizeKick = make(chan struct{}, 1)
	tm.loopWg.Add(1)
	go tm.resizeLoop()
	teardown := func() {
		close(tm.blinkDone)
		tm.loopWg.Wait()
	}
	return tm, p, teardown
}

// latchResize simulates onDraw's doResize path: store dims, set pending,
// kick the loop.
func latchResize(tm *Term, rows, cols int) {
	tm.ptyResizeRows.Store(int32(rows))
	tm.ptyResizeCols.Store(int32(cols))
	tm.ptyResizePending.Store(true)
	select {
	case tm.ptyResizeKick <- struct{}{}:
	default:
	}
}

// waitCalls polls until the recorded Resize calls equal want or the
// deadline expires.
func waitCalls(t *testing.T, p *recordPty, want [][2]int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := p.snapshot()
		if len(got) == len(want) {
			ok := true
			for i := range got {
				if got[i] != want[i] {
					ok = false
					break
				}
			}
			if ok {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("resize calls = %v, want %v", p.snapshot(), want)
}

// A latched size different from the last applied one must reach the pty
// even when the child produces no output — the resize must not wait for
// the reader goroutine's next Read to return (the pre-resizeLoop design
// left an idle full-screen app stale until the next keystroke).
func TestResizeLoop_AppliesWithoutPtyOutput(t *testing.T) {
	tm, p, teardown := newResizeLoopTerm()
	defer teardown()

	latchResize(tm, 30, 100)
	waitCalls(t, p, [][2]int{{30, 100}})
}

// A latched size equal to the last applied one means the grid resized
// away and back while no intermediate size reached the kernel (A→B→A
// race). A same-size TIOCSWINSZ delivers no SIGWINCH, so the loop must
// bounce through a one-row-off size to force the child to repaint the
// content the intermediate clip destroyed.
func TestResizeLoop_SameSizeHealBumpsRows(t *testing.T) {
	tm, p, teardown := newResizeLoopTerm()
	defer teardown()

	// resizeLoop seeds lastApplied from initRows/initCols; latching the
	// same size is the coalesced A→B→A case.
	latchResize(tm, initRows, initCols)
	waitCalls(t, p, [][2]int{{initRows - 1, initCols}, {initRows, initCols}})
}

// At the 1-row floor the heal must bump up, not to an invalid 0 rows.
func TestResizeLoop_SameSizeHealAtOneRowBumpsUp(t *testing.T) {
	tm, p, teardown := newResizeLoopTerm()
	defer teardown()

	latchResize(tm, 1, 80)
	waitCalls(t, p, [][2]int{{1, 80}})

	latchResize(tm, 1, 80)
	waitCalls(t, p, [][2]int{{1, 80}, {2, 80}, {1, 80}})
}

// scriptPty is a ptyIO stub whose Read returns queued chunks then EOF.
type scriptPty struct {
	recordPty
	chunks [][]byte
}

func (p *scriptPty) Read(b []byte) (int, error) {
	if len(p.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(b, p.chunks[0])
	p.chunks = p.chunks[1:]
	return n, nil
}

// The GOTERM_CAPTURE tee must record the exact bytes the pty delivered,
// including escape sequences, before any parsing.
func TestReadLoop_CaptureTeeRecordsRawBytes(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cap-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	input := []byte("hello \x1b[31mred\x1b[0m")
	p := &scriptPty{chunks: [][]byte{input[:6], input[6:]}}
	g := newGrid(4, 20)
	tm := &Term{
		grid:     g,
		parser:   newParser(g),
		cmd:      &gui.Window{},
		pty:      p,
		pw:       p,
		capture:  f,
		readDone: make(chan struct{}),
	}
	go tm.readLoop()
	select {
	case <-tm.readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit")
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(input) {
		t.Errorf("capture = %q, want %q", got, input)
	}
}

// openCapture derives one file per Term from the GOTERM_CAPTURE prefix
// and returns nil when the variable is unset.
func TestOpenCapture(t *testing.T) {
	t.Setenv("GOTERM_CAPTURE", "")
	if f := openCapture(1); f != nil {
		_ = f.Close()
		t.Error("openCapture with empty env: want nil")
	}

	prefix := filepath.Join(t.TempDir(), "cap")
	t.Setenv("GOTERM_CAPTURE", prefix)
	f := openCapture(7)
	if f == nil {
		t.Fatal("openCapture: got nil")
	}
	defer func() { _ = f.Close() }()
	if f.Name() != prefix+"-7.bin" {
		t.Errorf("capture path = %q, want %q", f.Name(), prefix+"-7.bin")
	}
	if _, err := os.Stat(prefix + "-7.bin"); err != nil {
		t.Errorf("capture file: %v", err)
	}
}

// errPty is a recordPty whose Resize returns a fixed error, exercising
// resizeLoop's error-logging branch without a panic.
type errPty struct{ recordPty }

var errResize = errors.New("resize failed")

func (p *errPty) Resize(rows, cols int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, [2]int{rows, cols})
	return errResize
}

func TestResizeLoop_ResizeError(t *testing.T) {
	p := &errPty{}
	tm := &Term{pty: p, blinkDone: make(chan struct{})}
	tm.ptyResizeKick = make(chan struct{}, 1)
	tm.loopWg.Add(1)
	go tm.resizeLoop()
	defer func() {
		close(tm.blinkDone)
		tm.loopWg.Wait()
	}()

	latchResize(tm, 30, 100)
	waitCalls(t, &p.recordPty, [][2]int{{30, 100}})
	// Resize returned an error; resizeLoop should not panic and
	// lastRows/lastCols should still be updated so the next resize
	// is applied correctly.
	latchResize(tm, 35, 100)
	waitCalls(t, &p.recordPty, [][2]int{{30, 100}, {35, 100}})
}

// A capture write failure (disk full, closed fd) must disable further
// writes without crashing readLoop and without dropping pty throughput.
func TestReadLoop_CaptureWriteFails(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "cap-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // ready for write; Write on a closed fd returns an error
	input := []byte("hello world")
	p := &scriptPty{chunks: [][]byte{input}}
	g := newGrid(1, 20)
	tm := &Term{
		grid:     g,
		parser:   newParser(g),
		cmd:      &gui.Window{},
		pty:      p,
		pw:       p,
		capture:  f,
		readDone: make(chan struct{}),
	}
	go tm.readLoop()
	select {
	case <-tm.readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit")
	}
	// readLoop must have nilled capture after the write error so
	// subsequent iterations skip the tee.
	if tm.capture != nil {
		t.Error("capture was not nilled after write failure")
	}
	// The pty data must still have been fed to the parser — the grid
	// should contain the input bytes.
	tm.grid.Mu.Lock()
	c0 := tm.grid.Cells[0]
	tm.grid.Mu.Unlock()
	if c0.Ch != 'h' {
		t.Errorf("first cell = %c, want 'h'", c0.Ch)
	}
}
