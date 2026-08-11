package term

import (
	"io"
	"log"
	"testing"
	"time"
)

// enableLatency turns instrumentation on for one test with a batch large
// enough that nothing flushes mid-test, and silences the flush logger.
func enableLatency(t *testing.T, batch int) {
	t.Helper()
	oldEnabled, oldBatch := latencyEnabled, latencyBatch
	latencyEnabled, latencyBatch = true, batch
	log.SetOutput(io.Discard)
	t.Cleanup(func() {
		latencyEnabled, latencyBatch = oldEnabled, oldBatch
		log.SetOutput(defaultLogOutput)
	})
}

// defaultLogOutput is stderr, which is what the log package starts with;
// captured here because log offers no getter.
var defaultLogOutput = log.Writer()

func TestParseLatencyEnv(t *testing.T) {
	tests := []struct {
		in    string
		on    bool
		batch int
	}{
		{"", false, 25},
		{"0", false, 25},
		{"off", false, 25},
		{"false", false, 25},
		{"1", true, 25},
		{"true", true, 25},
		{" ON ", true, 25},
		{"100", true, 100},
		{"5000", true, 1000}, // clamped
		{"garbage", true, 25},
		{"-3", true, 25},
	}
	for _, tc := range tests {
		on, batch := parseLatencyEnv(tc.in)
		if on != tc.on || batch != tc.batch {
			t.Errorf("parseLatencyEnv(%q) = %v,%d; want %v,%d", tc.in, on, batch, tc.on, tc.batch)
		}
	}
}

// A complete keystroke → echo → frame cycle produces exactly one sample whose
// spans decompose the total.
func TestLatencyTracker_FullCycle(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	drawStart := time.Now()
	l.markKey()
	l.noteChunk()
	l.markEcho()
	l.markWake()
	l.sample(drawStart, nil)

	if len(l.samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(l.samples))
	}
	s := l.samples[0]
	if s.echo < 0 || s.wake < 0 || s.paint < 0 || s.total < 0 || s.draw < 0 {
		t.Fatalf("negative span in %+v", s)
	}
	if s.echo+s.wake+s.paint != s.total {
		t.Errorf("echo+wake+paint = %v, want total %v", s.echo+s.wake+s.paint, s.total)
	}
	if s.chunks != 1 {
		t.Errorf("chunks = %d, want 1", s.chunks)
	}
	if l.nowake != 0 {
		t.Errorf("nowake = %d, want 0", l.nowake)
	}
	if l.keyAt.Load() != 0 || l.echoAt.Load() != 0 {
		t.Error("slot not cleared after sample")
	}
}

// Disabled instrumentation records nothing, which is what keeps the hot path
// free in the default build.
func TestLatencyTracker_Disabled(t *testing.T) {
	var l latencyTracker // latencyEnabled left at its env-derived default (off)
	if latencyEnabled {
		t.Skip("GOTERM_LATENCY set in the environment")
	}
	l.markKey()
	l.markEcho()
	l.sample(time.Now(), nil)
	if l.keyAt.Load() != 0 || len(l.samples) != 0 {
		t.Error("instrumentation active while disabled")
	}
}

// A second keystroke arriving before the first is drawn must not restart the
// measurement: the oldest outstanding key is the one the user is waiting on.
func TestLatencyTracker_SingleSlot(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.markKey()
	first := l.keyAt.Load()
	time.Sleep(time.Millisecond)
	l.markKey()
	if got := l.keyAt.Load(); got != first {
		t.Errorf("second markKey overwrote the slot: %d != %d", got, first)
	}
}

// Frames that arrive before the echo (cursor blink repaints) must leave the
// measurement open rather than consuming it.
func TestLatencyTracker_BlinkFrameDoesNotConsume(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.markKey()
	l.sample(time.Now(), nil) // an unrelated repaint
	if l.keyAt.Load() == 0 {
		t.Fatal("echo-less frame consumed the measurement")
	}
	if len(l.samples) != 0 {
		t.Fatalf("samples = %d, want 0", len(l.samples))
	}

	l.markEcho()
	l.sample(time.Now(), nil)
	if len(l.samples) != 1 {
		t.Fatalf("samples = %d after echo, want 1", len(l.samples))
	}
}

// A keystroke the child never echoes is retired by the staleness cutoff, not
// left to mis-attribute the next echo.
func TestLatencyTracker_Stale(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.keyAt.Store(time.Now().Add(-2 * latencyStale).UnixNano())
	l.sample(time.Now(), nil)
	if l.stale != 1 {
		t.Errorf("stale = %d, want 1", l.stale)
	}
	if l.keyAt.Load() != 0 {
		t.Error("stale keystroke not cleared")
	}
	if len(l.samples) != 0 {
		t.Errorf("samples = %d, want 0", len(l.samples))
	}
}

// An echo stamped for an already-retired keystroke is dropped, never reported
// as a negative span against the keystroke that followed it.
func TestLatencyTracker_Skewed(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	now := time.Now()
	l.keyAt.Store(now.UnixNano())
	l.echoAt.Store(now.Add(-time.Second).UnixNano()) // echo predates the key
	l.sample(now, nil)

	if l.skewed != 1 {
		t.Errorf("skewed = %d, want 1", l.skewed)
	}
	if len(l.samples) != 0 {
		t.Errorf("samples = %d, want 0", len(l.samples))
	}
	if l.keyAt.Load() != 0 || l.echoAt.Load() != 0 {
		t.Error("slot not cleared")
	}
}

// A frame that is not the one the reader asked for (a blink repaint arriving
// between echo and wake) has no wake stamp: the whole span is attributed to
// paint rather than to a fabricated wake time.
func TestLatencyTracker_NoWake(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.markKey()
	l.markEcho()
	time.Sleep(time.Millisecond) // so the span is measurably non-zero
	l.sample(time.Now(), nil)    // drawn without the queued command having run

	if len(l.samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(l.samples))
	}
	if s := l.samples[0]; s.wake != 0 || s.paint == 0 {
		t.Errorf("wake = %v, paint = %v; want wake 0 and the span in paint", s.wake, s.paint)
	}
	if l.nowake != 1 {
		t.Errorf("nowake = %d, want 1", l.nowake)
	}
}

// A wake stamp left over from a retired keystroke must not produce a negative
// wake span for the next one.
func TestLatencyTracker_StaleWakeIgnored(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	now := time.Now()
	l.keyAt.Store(now.Add(-2 * time.Millisecond).UnixNano())
	l.echoAt.Store(now.Add(-time.Millisecond).UnixNano())
	l.wakeAt.Store(now.Add(-time.Hour).UnixNano()) // predates the echo
	l.sample(now, nil)

	if len(l.samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(l.samples))
	}
	if s := l.samples[0]; s.wake != 0 {
		t.Errorf("wake = %v, want 0 for a stamp older than the echo", s.wake)
	}
	if l.nowake != 1 {
		t.Errorf("nowake = %d, want 1", l.nowake)
	}
}

// Chunk counting is bounded by the outstanding keystroke: reads arriving with
// no key in flight (background output) must not inflate the next sample.
func TestLatencyTracker_ChunksScopedToKeystroke(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.noteChunk() // no keystroke outstanding — ignored
	l.noteChunk()
	if got := l.chunks.Load(); got != 0 {
		t.Fatalf("chunks = %d before any keystroke, want 0", got)
	}

	l.markKey()
	l.noteChunk()
	l.noteChunk()
	l.noteChunk()
	l.markEcho()
	l.markWake()
	l.sample(time.Now(), nil)

	if s := l.samples[0]; s.chunks != 3 {
		t.Errorf("chunks = %d, want 3", s.chunks)
	}
	if got := l.chunks.Load(); got != 0 {
		t.Errorf("chunks = %d after sample, want 0", got)
	}
}

// Reaching the batch size flushes and starts over, so a long session's memory
// use is bounded by the batch rather than by how long the user typed.
func TestLatencyTracker_FlushResetsBatch(t *testing.T) {
	enableLatency(t, 3)
	var l latencyTracker

	for range 3 {
		l.markKey()
		l.markEcho()
		l.sample(time.Now(), nil)
	}
	if len(l.samples) != 0 {
		t.Errorf("samples = %d after flush, want 0", len(l.samples))
	}
	if l.stale != 0 || l.skewed != 0 {
		t.Errorf("counters not reset: stale=%d skewed=%d", l.stale, l.skewed)
	}
}

// Stamps only land when their predecessor exists: markEcho without a key and
// markWake without an echo are no-ops (background output must not open a slot),
// and a second markEcho — another chunk of the same frame — must not move the
// stamp, since the first screen-changing chunk is what defines the echo time.
func TestLatencyTracker_StampGuards(t *testing.T) {
	enableLatency(t, 1000)
	var l latencyTracker

	l.markEcho() // no keystroke outstanding
	if l.echoAt.Load() != 0 {
		t.Fatal("markEcho with no key stamped an echo")
	}

	l.markKey()
	l.markWake() // echo not landed yet
	if l.wakeAt.Load() != 0 {
		t.Fatal("markWake with no echo stamped a wake")
	}

	first := time.Now().Add(-time.Millisecond).UnixNano()
	l.echoAt.Store(first)
	l.markEcho()
	if got := l.echoAt.Load(); got != first {
		t.Errorf("second markEcho moved the stamp: %d != %d", got, first)
	}
}

// pct must not index out of range for any batch size, including one sample.
func TestLatencyTracker_Percentiles(t *testing.T) {
	enableLatency(t, 1000)
	for _, n := range []int{1, 2, 3, 20, 25, 99} {
		var l latencyTracker
		for i := range n {
			l.samples = append(l.samples, latencySample{total: time.Duration(i) * time.Millisecond})
		}
		p50, p95, maxV := l.pct(func(s latencySample) time.Duration { return s.total })
		if p50 > p95 || p95 > maxV {
			t.Errorf("n=%d: unordered percentiles p50=%v p95=%v max=%v", n, p50, p95, maxV)
		}
		if want := time.Duration(n-1) * time.Millisecond; maxV != want {
			t.Errorf("n=%d: max = %v, want %v", n, maxV, want)
		}
	}
}
