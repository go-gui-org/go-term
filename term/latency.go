package term

import (
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// Keystroke-to-frame latency instrumentation, gated on GOTERM_LATENCY.
//
// The path measured is:
//
//	key event → writeRaw → pty → child echo → readLoop → applyChunk
//	  → queueCommand → FrameFn → onDraw → (present, not visible here)
//
// split into three spans:
//
//   - key→echo:  what the child cost. A shell round-trip, not go-term's
//     latency — but it must be subtracted before blaming the widget.
//   - echo→paint: what go-term cost. Scheduling the redraw, waiting for the
//     next frame, and running the draw passes.
//   - total:     key event to end of onDraw.
//
// Everything after onDraw returns — GPU submit, compositor, vsync, panel — is
// invisible from inside the process and is typically another 8–17 ms on a
// 60 Hz display. Treat these numbers as a lower bound, and use an external
// method (high-speed camera) for the end-to-end figure.

// latencyStale bounds how long an unmatched keystroke stays outstanding. Keys
// that produce no echo at all (pager navigation, a key the child swallows)
// would otherwise sit in the slot forever and mis-attribute the next echo.
const latencyStale = 250 * time.Millisecond

// latencyEnabled and latencyBatch are resolved once from the environment.
// GOTERM_LATENCY=1 turns instrumentation on with the default batch; a number
// greater than 1 sets how many samples are aggregated per log line.
var latencyEnabled, latencyBatch = parseLatencyEnv(os.Getenv("GOTERM_LATENCY"))

// parseLatencyEnv interprets the GOTERM_LATENCY value. Unset, empty, "0",
// "false" and "off" disable; "1"/"true"/"on" enable with a 25-sample batch;
// any integer >1 enables and sets the batch size (clamped to 1000, past which
// a "summary" stops summarizing anything you would notice while typing).
func parseLatencyEnv(s string) (bool, int) {
	const defaultBatch = 25
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "0", "false", "off", "no":
		return false, defaultBatch
	case "1", "true", "on", "yes":
		return true, defaultBatch
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		// Unparseable but non-empty reads as "the user meant to turn this on".
		return true, defaultBatch
	}
	return true, min(n, 1000)
}

// latencySample is one keystroke's journey, in the three spans above plus the
// wall time onDraw itself took to run.
type latencySample struct {
	echo  time.Duration // key event → echoed bytes parsed
	wake  time.Duration // echo parsed → main thread picked the repaint up
	paint time.Duration // main thread picked it up → onDraw returned
	total time.Duration // key event → onDraw returned
	draw  time.Duration // onDraw body alone

	// chunks counts the PTY reads that landed while this keystroke was
	// outstanding. One means the child answered in a single read, as a shell
	// echoing a character does. Many means a full-screen frame arriving in
	// 4096-byte pieces, each of which takes grid.Mu — which is the difference
	// between "the main thread was slow to wake" and "the main thread woke on
	// time and then queued behind the reader".
	chunks int

	// go-gui's own pipeline split for the *previous* frame, zero unless the
	// embedder set WindowCfg.Timings. Previous, not current: the window records
	// its timings after buildRenderers, and onDraw runs inside that call.
	// Across a batch of 25 consecutive terminal-driven frames the shift does not
	// change what the percentiles say.
	viewGen, layout, renderBuild time.Duration
}

// latencyTracker holds the single outstanding keystroke plus the current
// aggregation batch. Deliberately single-slot: fast typing overlaps, and the
// oldest outstanding key is the one whose delay a human perceives. A second
// keystroke arriving before the first is drawn simply does not open a new
// measurement.
//
// keyAt/echoAt are nanosecond wall clocks written from two goroutines (main
// thread and the pty reader), so they are atomic. Everything below them is
// touched only by sample(), which runs on the main thread inside onDraw.
type latencyTracker struct {
	keyAt  atomic.Int64 // time.Now().UnixNano() of the oldest un-drawn keystroke
	echoAt atomic.Int64 // ... when its echo was parsed; 0 until then
	wakeAt atomic.Int64 // ... and when the main thread ran the queued repaint
	chunks atomic.Int64 // PTY reads seen while it was outstanding

	samples []latencySample // current batch, reused across flushes
	scratch []time.Duration // percentile workspace, reused across flushes
	stale   int             // keystrokes discarded with no echo inside latencyStale
	skewed  int             // samples discarded because the clocks disagreed (see sample)
	nowake  int             // samples whose repaint reached the frame without a queued wake
}

// markKey stamps the start of a measurement. Called from writeRaw on the main
// thread for every keystroke and paste sent to the child. The CAS is what makes
// this single-slot: a key arriving while one is outstanding is ignored.
func (l *latencyTracker) markKey() {
	if !latencyEnabled {
		return
	}
	l.keyAt.CompareAndSwap(0, time.Now().UnixNano())
}

// markEcho stamps the arrival of output that changed the screen while a
// keystroke was outstanding. Called from applyChunk on the reader goroutine,
// only when the chunk actually dirtied something — output that paints nothing
// is not the echo we are waiting for.
func (l *latencyTracker) markEcho() {
	if !latencyEnabled || l.keyAt.Load() == 0 {
		return
	}
	l.echoAt.CompareAndSwap(0, time.Now().UnixNano())
}

// markWake stamps the moment the main thread ran the queued repaint command,
// which is the boundary between "waiting to be noticed" and "being drawn".
// Called from the queueCommand callback, before UpdateWindow.
//
// The command is coalesced — one callback can serve several PTY reads — so the
// first wake after an echo is the one that counts, exactly as with the echo
// itself.
func (l *latencyTracker) markWake() {
	if !latencyEnabled || l.echoAt.Load() == 0 {
		return
	}
	l.wakeAt.CompareAndSwap(0, time.Now().UnixNano())
}

// noteChunk counts one PTY read against the outstanding keystroke. Called from
// applyChunk on the reader goroutine for every read, dirty or not: a chunk that
// paints nothing still took grid.Mu and still delayed the frame.
func (l *latencyTracker) noteChunk() {
	if !latencyEnabled || l.keyAt.Load() == 0 {
		return
	}
	l.chunks.Add(1)
}

// sample closes out the outstanding measurement, if any, at the end of a frame.
// drawStart is when onDraw began; called on the main thread only.
//
// A frame with no echo yet does not consume the measurement — cursor blink
// alone repaints the window several times a second and would otherwise steal
// every sample before the child answered. Only the staleness cutoff clears an
// echo-less keystroke.
func (l *latencyTracker) sample(drawStart time.Time, w *gui.Window) {
	if !latencyEnabled {
		return
	}
	k := l.keyAt.Load()
	if k == 0 {
		return
	}
	now := time.Now()
	e := l.echoAt.Load()
	if e == 0 {
		if now.UnixNano()-k > int64(latencyStale) {
			l.stale++
			l.reset()
		}
		return
	}
	key, echo := time.Unix(0, k), time.Unix(0, e)
	wk, chunks := l.wakeAt.Load(), int(l.chunks.Load())
	l.reset()
	if echo.Before(key) {
		// The reader goroutine stamped an echo for a keystroke this method had
		// already retired (it clears echoAt first, then keyAt, so a write can
		// land in between). Attributing it to the *next* keystroke would report
		// a negative span, so drop it and count the loss.
		l.skewed++
		return
	}
	s := latencySample{
		echo:   echo.Sub(key),
		total:  now.Sub(key),
		draw:   now.Sub(drawStart),
		chunks: chunks,
	}
	// wake splits echo→paint into "waiting to be noticed" and "being drawn".
	// A wake stamp that is missing or predates the echo means this frame was not
	// the one the reader's command asked for — a blink repaint, a scroll, a
	// resize — so the whole span is attributed to paint rather than inventing a
	// wake time, and the count says how often that happened.
	if wake := time.Unix(0, wk); wk != 0 && !wake.Before(echo) && !wake.After(now) {
		s.wake = wake.Sub(echo)
		s.paint = now.Sub(wake)
	} else {
		s.paint = now.Sub(echo)
		l.nowake++
	}
	if w != nil {
		ft := w.Timings()
		s.viewGen, s.layout, s.renderBuild = ft.ViewGen, ft.LayoutArrange, ft.RenderBuild
	}
	l.samples = append(l.samples, s)
	if len(l.samples) >= latencyBatch {
		l.flush()
	}
}

// reset clears the outstanding measurement. Order matters: echoAt first, so a
// concurrent markEcho that already read a non-zero keyAt cannot resurrect the
// slot for the next keystroke — the worst it can do is land a stale stamp that
// sample() then rejects as skewed.
func (l *latencyTracker) reset() {
	l.wakeAt.Store(0)
	l.chunks.Store(0)
	l.echoAt.Store(0)
	l.keyAt.Store(0)
}

// flush logs one aggregate line and starts a new batch. Percentiles rather
// than a mean: typing latency is a tail problem, and the frame you notice is
// the slow one, not the average one.
func (l *latencyTracker) flush() {
	n := len(l.samples)
	if n == 0 {
		return
	}
	// Per-span max, not just the total's: an outlier is only actionable once you
	// know which half it came from. A keystroke that starts a command (Enter)
	// measures key→echo as "how long until the command printed something", which
	// is seconds and says nothing about the widget — reporting one undivided max
	// makes that indistinguishable from a real stall in the paint path.
	echoP50, echoP95, echoMax := l.pct(func(s latencySample) time.Duration { return s.echo })
	wakeP50, wakeP95, wakeMax := l.pct(func(s latencySample) time.Duration { return s.wake })
	paintP50, paintP95, paintMax := l.pct(func(s latencySample) time.Duration { return s.paint })
	totP50, totP95, totMax := l.pct(func(s latencySample) time.Duration { return s.total })
	drawP50, drawP95, drawMax := l.pct(func(s latencySample) time.Duration { return s.draw })
	chunkP50, chunkMax := l.chunkStats()
	log.Printf("term: latency n=%d key→echo p50=%s p95=%s max=%s | "+
		"echo→wake p50=%s p95=%s max=%s | wake→paint p50=%s p95=%s max=%s | "+
		"total p50=%s p95=%s max=%s | onDraw p50=%s p95=%s max=%s | "+
		"chunks p50=%d max=%d | stale=%d skewed=%d nowake=%d",
		n, ms(echoP50), ms(echoP95), ms(echoMax),
		ms(wakeP50), ms(wakeP95), ms(wakeMax),
		ms(paintP50), ms(paintP95), ms(paintMax),
		ms(totP50), ms(totP95), ms(totMax),
		ms(drawP50), ms(drawP95), ms(drawMax),
		chunkP50, chunkMax, l.stale, l.skewed, l.nowake)

	// go-gui's split of the frame that repaint sat in. Logged separately, and
	// only when the embedder opted into WindowCfg.Timings — an all-zero line
	// would otherwise read as "these stages are free" rather than "not measured".
	vgP50, vgP95, _ := l.pct(func(s latencySample) time.Duration { return s.viewGen })
	loP50, loP95, _ := l.pct(func(s latencySample) time.Duration { return s.layout })
	rbP50, rbP95, _ := l.pct(func(s latencySample) time.Duration { return s.renderBuild })
	if vgP95+loP95+rbP95 > 0 {
		log.Printf("term: latency pipeline viewGen p50=%s p95=%s | layout p50=%s p95=%s | "+
			"renderBuild p50=%s p95=%s",
			ms(vgP50), ms(vgP95), ms(loP50), ms(loP95), ms(rbP50), ms(rbP95))
	}
	l.samples = l.samples[:0]
	l.stale, l.skewed, l.nowake = 0, 0, 0
}

// chunkStats returns the median and maximum PTY-read count across the batch.
// Reuses the same scratch buffer as pct by measuring chunks as a duration —
// they are only ever sorted and indexed, and one workspace is enough for a
// debug path that runs once per 25 keystrokes.
func (l *latencyTracker) chunkStats() (p50, maxV int) {
	c50, _, cmax := l.pct(func(s latencySample) time.Duration { return time.Duration(s.chunks) })
	return int(c50), int(cmax)
}

// pct returns the p50, p95 and max of one field across the current batch. The
// scratch slice is reused so a session-long instrumentation run does not
// allocate once per flush.
func (l *latencyTracker) pct(field func(latencySample) time.Duration) (p50, p95, maxV time.Duration) {
	l.scratch = l.scratch[:0]
	for _, s := range l.samples {
		l.scratch = append(l.scratch, field(s))
	}
	slices.Sort(l.scratch)
	n := len(l.scratch)
	return l.scratch[n*50/100], l.scratch[min(n*95/100, n-1)], l.scratch[n-1]
}

// ms formats a duration as milliseconds with two decimals — the resolution
// that matters here, where a whole frame is 16.7 ms.
func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', 2, 64) + "ms"
}
