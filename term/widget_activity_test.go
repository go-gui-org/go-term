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
	tm.registerCommandHandler()        // OSC 133 D is half of what is reported
	return tm, got
}

func TestOnActivity_Bell(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\a"), true)
	assertActivity(t, got, ActivityBell)
}

// Plain output must never report activity. An application that repaints on a
// timer dirties cells forever, so an embedder marking panes from screen
// changes would light every pane permanently and mean nothing by it.
func TestOnActivity_OutputIsSilent(t *testing.T) {
	tm, got := activityTestTerm(t)
	for range 20 {
		tm.applyChunk([]byte("spinner\r"), true)
	}
	assertNoActivity(t, got)
}

func TestOnActivity_CommandDone(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\x1b]133;C\x07"), true)
	tm.applyChunk([]byte("\x1b]133;D;0\x07"), true)
	assertActivity(t, got, ActivityCommandDone)
}

func TestOnActivity_CommandFailed(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\x1b]133;C\x07"), true)
	tm.applyChunk([]byte("\x1b]133;D;1\x07"), true)
	assertActivity(t, got, ActivityCommandFailed)
}

// A shell that reports no exit status said nothing about success or failure,
// so the marker must not accuse it of failing.
func TestOnActivity_CommandUnknownExitIsNotFailure(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\x1b]133;C\x07"), true)
	tm.applyChunk([]byte("\x1b]133;D\x07"), true)
	assertActivity(t, got, ActivityCommandDone)
}

// D without a preceding C is the shell bracketing an empty prompt — the user
// pressed Enter on a blank line. Nothing ran, so there is nothing to report.
func TestOnActivity_CommandEndWithoutStartIsSilent(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\x1b]133;D;0\x07"), true)
	assertNoActivity(t, got)
}

func TestOnActivity_NilHookIsSafe(t *testing.T) {
	g := newGrid(4, 80)
	tm := &Term{grid: g, parser: newParser(g), cmd: syncScheduler{}}
	tm.bellMode.Store(int32(BellNone))
	tm.registerCommandHandler()
	tm.applyChunk([]byte("hello\a"), true) // must not panic
	tm.applyChunk([]byte("\x1b]133;C\x07\x1b]133;D;1\x07"), true)
}

// Each event gets its own call. The kinds are things a child asserts at human
// rates, so unlike the redraw they are not coalesced — and a bell folded into
// a command end (or the reverse) would drop a signal the embedder asked for.
func TestOnActivity_EventsAreNotCoalesced(t *testing.T) {
	tm, got := activityTestTerm(t)
	tm.applyChunk([]byte("\a"), true)
	tm.applyChunk([]byte("\x1b]133;C\x07"), true)
	tm.applyChunk([]byte("\x1b]133;D;1\x07"), true)
	assertActivity(t, got, ActivityBell)
	assertActivity(t, got, ActivityCommandFailed)
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

func assertNoActivity(t *testing.T, got chan ActivityKind) {
	t.Helper()
	select {
	case kind := <-got:
		t.Fatalf("unexpected activity %v", kind)
	case <-time.After(100 * time.Millisecond):
	}
}
