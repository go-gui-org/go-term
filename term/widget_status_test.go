package term

import (
	"strconv"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// deferredScheduler holds QueueCommand callbacks until run is called, the way
// the real main thread runs them some time after the reader queued them. It is
// what makes coalescing observable.
type deferredScheduler struct{ queue *[]func(*gui.Window) }

func (s deferredScheduler) QueueCommand(fn func(*gui.Window)) { *s.queue = append(*s.queue, fn) }

func (s deferredScheduler) run() {
	q := *s.queue
	*s.queue = nil
	for _, fn := range q {
		fn(&gui.Window{})
	}
}

// statusTestTerm builds a Term whose OnProgramStatus calls are counted.
func statusTestTerm(t *testing.T) (*Term, deferredScheduler, *int) {
	t.Helper()
	calls := 0
	sched := deferredScheduler{queue: new([]func(*gui.Window))}
	g := newGrid(4, 80)
	tm := &Term{
		grid:   g,
		parser: newParser(g),
		cmd:    sched,
		cfg:    Cfg{OnProgramStatus: func() { calls++ }},
	}
	tm.bellMode.Store(int32(BellNone))
	return tm, sched, &calls
}

func TestProgramStatus_SnapshotSortedWithInheritedApp(t *testing.T) {
	tm, _, _ := statusTestTerm(t)
	tm.applyChunk(osc7501("state=working:id=eu-west/push:progress=40"), true)
	tm.applyChunk(osc7501("state=blocked:kind=permission:id=eu-west:msg="+b64("Approve?")), true)
	tm.applyChunk(osc7501("state=working:app=deploy:title="+b64("Deploy")), true)
	tm.applyChunk(osc7501("state=done:id=other:app=own"), true)

	got := tm.ProgramStatus()
	want := []ProgramStatus{
		{ID: "", State: ProgramWorking, Progress: -1, App: "deploy", Title: "Deploy"},
		{ID: "eu-west", State: ProgramBlocked, Kind: "permission", Progress: -1,
			App: "deploy", Msg: "Approve?"},
		// Two levels down: inherits through a parent that itself inherited.
		{ID: "eu-west/push", State: ProgramWorking, Progress: 40, App: "deploy"},
		{ID: "other", State: ProgramDone, Progress: -1, App: "own"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A parent need not exist: inheritance skips the missing level.
func TestProgramStatus_InheritsAcrossMissingParent(t *testing.T) {
	tm, _, _ := statusTestTerm(t)
	tm.applyChunk(osc7501("state=idle:app=root"), true)
	tm.applyChunk(osc7501("state=working:id=a/b/c"), true)
	got := tm.ProgramStatus()
	if len(got) != 2 || got[1].App != "root" {
		t.Fatalf("got %+v, want a/b/c to inherit app=root", got)
	}
}

func TestProgramStatus_EmptyIsNil(t *testing.T) {
	tm, _, _ := statusTestTerm(t)
	if got := tm.ProgramStatus(); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

// A burst of reports yields one call, made after the last of them.
func TestOnProgramStatus_Coalesced(t *testing.T) {
	tm, sched, calls := statusTestTerm(t)
	for i := range 50 {
		tm.applyChunk(osc7501("state=working:progress="+strconv.Itoa(i)), true)
	}
	sched.run()
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}
	// A change after the queued call ran queues a fresh one.
	tm.applyChunk(osc7501("state=done"), true)
	sched.run()
	if *calls != 2 {
		t.Fatalf("calls = %d, want 2", *calls)
	}
}

// Output that changes no record must not call the hook.
func TestOnProgramStatus_SilentWithoutChange(t *testing.T) {
	tm, sched, calls := statusTestTerm(t)
	tm.applyChunk([]byte("hello\x1b]133;A\x07"), true)
	tm.applyChunk(osc7501("state=bogus"), true)
	sched.run()
	if *calls != 0 {
		t.Fatalf("calls = %d, want 0", *calls)
	}
}

// The child exiting drops working and blocked records and tells the embedder.
func TestOnProgramStatus_ExitDropsTransient(t *testing.T) {
	tm, sched, calls := statusTestTerm(t)
	tm.applyChunk(osc7501("state=working"), true)
	tm.applyChunk(osc7501("state=error:id=job"), true)
	sched.run()
	*calls = 0

	tm.dropTransientStatusOnExit()
	sched.run()
	if *calls != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}
	got := tm.ProgramStatus()
	if len(got) != 1 || got[0].ID != "job" {
		t.Fatalf("after exit: %+v, want only the error record", got)
	}

	// Nothing transient left: a second exit pass is silent.
	tm.dropTransientStatusOnExit()
	sched.run()
	if *calls != 1 {
		t.Fatalf("calls = %d after no-op exit, want 1", *calls)
	}
}

func TestOnProgramStatus_NilHookIsSafe(t *testing.T) {
	g := newGrid(4, 80)
	tm := &Term{grid: g, parser: newParser(g), cmd: syncScheduler{}}
	tm.bellMode.Store(int32(BellNone))
	tm.applyChunk(osc7501("state=working"), true)
	tm.dropTransientStatusOnExit()
}
