//go:build !windows

package term

import (
	"bytes"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	gui "github.com/go-gui-org/go-gui/gui"
)

// newReplyTestTerm builds a minimal Term wired to a real pty, with the reader
// and reply-writer goroutines running — the production reply path minus the
// GUI. Returns the Term, the application-side (slave) end, and a teardown.
func newReplyTestTerm(tb testing.TB) (*Term, *os.File, func()) {
	tb.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		tb.Skipf("pty.Open: %v", err)
	}
	if err := setRawMode(int(slave.Fd())); err != nil {
		tb.Skipf("raw mode: %v", err)
	}
	dev := &ptyDev{file: master}
	g := newGrid(24, 80)
	tm := &Term{
		grid:      g,
		parser:    newParser(g),
		cmd:       &gui.Window{},
		pty:       dev,
		pw:        dev,
		blinkDone: make(chan struct{}),
		readDone:  make(chan struct{}),
	}
	tm.parser.SetReplyHandler(tm.onParserReply)
	tm.replyCond = sync.NewCond(&tm.replyMu)
	tm.loopWg.Add(1)
	go tm.writeLoop()
	go tm.readLoop()

	teardown := func() {
		_ = slave.Close()
		_ = master.Close() // unblocks readLoop's Read so it exits
		select {
		case <-tm.readDone:
		case <-time.After(time.Second):
		}
		tm.replyMu.Lock()
		tm.replyDone = true
		tm.replyMu.Unlock()
		tm.replyCond.Signal()
		tm.loopWg.Wait()
	}
	return tm, slave, teardown
}

// TestReplyWriter_NoDeadlockOnLargeQueryBatch reproduces the ucs-detect hang:
// an application writes a large batch of queries before reading any replies.
// If replies were written on the reader goroutine, that write would block on a
// full slave-input buffer while the application's write blocked on a full
// master buffer — a deadlock. The dedicated writer goroutine keeps the reader
// draining the master, so the batch completes. A timeout guards the deadlock.
func TestReplyWriter_NoDeadlockOnLargeQueryBatch(t *testing.T) {
	_, app, teardown := newReplyTestTerm(t)
	defer teardown()

	const n = 4000 // ~44 KB of queries, ~88 KB of replies — overflows pty buffers
	// XTGETTCAP query for "Co" (hex 436f): DCS + q 436f ST. Each yields a DCS
	// reply terminated by ST (ESC \).
	query := []byte("\x1bP+q436f\x1b\\")

	done := make(chan int, 1)
	go func() {
		batch := make([]byte, 0, len(query)*n)
		for i := 0; i < n; i++ {
			batch = append(batch, query...)
		}
		if _, err := app.Write(batch); err != nil {
			done <- -1
			return
		}
		buf := make([]byte, 4096)
		count := 0
		for count < n {
			m, err := app.Read(buf)
			if m > 0 {
				count += bytes.Count(buf[:m], []byte("\x1b\\"))
			}
			if err != nil {
				break
			}
		}
		done <- count
	}()

	select {
	case got := <-done:
		if got < n {
			t.Fatalf("got %d replies, want %d", got, n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: timed out on large XTGETTCAP query batch")
	}
}

// BenchmarkCPRRoundTrip measures the wall-clock cost of a single
// Cursor-Position-Report round-trip through go-term's reply path: the
// application writes ESC[6n, readLoop reads it, the parser emits the CPR, and
// the writer goroutine writes the reply back — all off the GUI render loop.
// This is the exact path ucs-detect exercises thousands of times. Before the
// fix, replies drained on the main thread inside the vsync-throttled frame
// loop, capping each round-trip at one display refresh (~16.7 ms at 60 Hz).
//
// Multiply ns/op by the ~6.3k codepoints ucs-detect probes to project total
// runtime: e.g. 8 µs/op → ~0.05 s, versus 16.7 ms/op → ~105 s.
func BenchmarkCPRRoundTrip(b *testing.B) {
	_, app, teardown := newReplyTestTerm(b)
	defer teardown()

	buf := make([]byte, 64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := app.Write([]byte("\x1b[6n")); err != nil {
			b.Fatalf("write: %v", err)
		}
		for {
			n, err := app.Read(buf)
			if err != nil {
				b.Fatalf("read: %v", err)
			}
			if bytes.IndexByte(buf[:n], 'R') >= 0 {
				break
			}
		}
	}
	b.StopTimer()
}
