package term

import (
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// activityTestTerm builds a Term whose OnActivity calls land on a channel.
// applyChunk dispatches the hook through queueCommand, so the scheduler must
// run callbacks inline for the test to see them.
func activityTestTerm(t *testing.T) (*Term, chan ActivityKind) {
	t.Helper()
	got := make(chan ActivityKind, 8)
	g := newGrid(4, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    syncScheduler{},
		cfg: Cfg{OnActivity: func(kind ActivityKind) {
			got <- kind
		}},
	}
	tm.bellMode.Store(int32(BellNone)) // no beep/flash side effects in tests
	return tm, got
}

func TestOnActivity_Output(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("hello"), true)
	assertActivity(t, got, ActivityOutput)
}

func TestOnActivity_Bell(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\a"), true)
	assertActivity(t, got, ActivityBell)
}

// A read that both wrote text and rang the bell reports the bell: it is the
// state a pane manager must surface, and reporting output instead would hide
// it behind the more common signal.
func TestOnActivity_BellOutranksOutput(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("text\a"), true)
	assertActivity(t, got, ActivityBell)
}

// Bytes that change nothing on screen must not read as activity, or an idle
// application polling the terminal would keep a background tab lit forever.
func TestOnActivity_NoScreenChangeIsSilent(t *testing.T) {
	tm, got := activityTestTerm(t)
	// A DSR query: the parser answers it, but no cell changes and no row is
	// marked dirty.
	tm.applyChunk([]byte("\x1b[5n"), true)
	select {
	case kind := <-got:
		t.Fatalf("unexpected activity %v for a no-op sequence", kind)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestOnActivity_NilHookIsSafe(t *testing.T) {
	g := newGrid(4, 80)
	tm := &Term{grid: g, parser: newParser(g), cmd: syncScheduler{}}
	tm.bellMode.Store(int32(BellNone))
	tm.applyChunk([]byte("hello\a"), true) // must not panic
}

// deferScheduler buffers QueueCommand callbacks instead of running them, so a
// test can see how many dispatches a burst of reads actually produced.
type deferScheduler struct{ queued []func(*gui.Window) }

func (s *deferScheduler) QueueCommand(fn func(*gui.Window)) { s.queued = append(s.queued, fn) }

// run executes and drains the queued callbacks, returning how many there were.
func (s *deferScheduler) run() int {
	n := len(s.queued)
	for _, fn := range s.queued {
		fn(&gui.Window{})
	}
	s.queued = nil
	return n
}

// deferredActivityTerm is activityTestTerm with a scheduler that defers, which
// is what a real window does between frames.
func deferredActivityTerm(t *testing.T) (*Term, *deferScheduler, chan ActivityKind) {
	t.Helper()
	tm, got := activityTestTerm(t)
	sched := &deferScheduler{}
	tm.cmd = sched
	return tm, sched, got
}

// A child spewing output produces a read every few hundred microseconds. One
// queued closure per read would flood the command queue with calls whose
// answer is identical, so reads fold into a single outstanding dispatch.
func TestOnActivity_CoalescesBurst(t *testing.T) {
	tm, sched, got := deferredActivityTerm(t)
	for range 50 {
		tm.applyChunk([]byte("spam\r\n"), true)
	}
	// The redraw dispatch coalesces separately and rides the same queue, so
	// count activity by what the hook received, not by queue length.
	sched.run()
	assertActivity(t, got, ActivityOutput)
	if len(got) != 0 {
		t.Errorf("%d extra activity calls; want the burst folded into one", len(got))
	}

	// Once the dispatch has run, the next read must queue a fresh one rather
	// than being swallowed by a stale pending flag.
	tm.applyChunk([]byte("more\r\n"), true)
	sched.run()
	assertActivity(t, got, ActivityOutput)
}

// A bell that arrives while a plain-output dispatch is still pending must
// upgrade it. Dropping it would lose the one signal the child asked for.
func TestOnActivity_BellUpgradesPendingDispatch(t *testing.T) {
	tm, sched, got := deferredActivityTerm(t)
	tm.applyChunk([]byte("text"), true)
	tm.applyChunk([]byte("\a"), true)
	sched.run()
	assertActivity(t, got, ActivityBell)
	if len(got) != 0 {
		t.Errorf("%d extra activity calls; want one upgraded call", len(got))
	}
}

// Window focus feeds the long-running-command notification, so it must be
// tracked for every child — not only the rare one that enabled focus
// reporting, whose early return sits directly after this in HandleWindowEvent.
func TestHandleWindowEvent_TracksFocusWithoutReporting(t *testing.T) {
	tm, _, _, _ := notifyTestTerm(t, time.Second)
	tm.winFocused.Store(true)

	tm.HandleWindowEvent(&gui.Event{Type: gui.EventUnfocused})
	if tm.winFocused.Load() {
		t.Error("winFocused still set after EventUnfocused")
	}
	tm.HandleWindowEvent(&gui.Event{Type: gui.EventFocused})
	if !tm.winFocused.Load() {
		t.Error("winFocused not set after EventFocused")
	}
}

func assertActivity(t *testing.T, got chan ActivityKind, want ActivityKind) {
	t.Helper()
	select {
	case kind := <-got:
		if kind != want {
			t.Errorf("activity kind = %v; want %v", kind, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no activity reported; want %v", want)
	}
}
