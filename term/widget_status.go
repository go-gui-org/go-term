package term

import (
	"slices"
	"strings"

	"github.com/go-gui-org/go-gui/gui"
)

// ProgramState is the state of one OSC 7501 program status record.
type ProgramState int

const (
	// ProgramIdle: at rest, waiting for the user's next instruction.
	ProgramIdle ProgramState = iota
	// ProgramWorking: running. Dropped when the child exits or a new shell
	// prompt begins.
	ProgramWorking
	// ProgramDone: finished a piece of work the user has not looked at yet.
	// Survives the child's exit and the next prompt; the embedder decides when
	// to stop showing it.
	ProgramDone
	// ProgramBlocked: cannot continue until the user does something.
	// ProgramStatus.Kind says what. Dropped like ProgramWorking.
	ProgramBlocked
	// ProgramError: failed and stopped. Survives like ProgramDone.
	ProgramError
)

// ProgramStatus is one OSC 7501 record: what a program running in the pane
// says it is doing. A program that reports only its own state uses the root
// record (ID ""); one that runs several jobs reports each under an id such as
// "build/test", where "/" makes it a child of "build".
//
// Title and Msg are child-chosen text — untrusted. Control characters are
// refused and bidi overrides removed before they reach here, but the words
// themselves can say anything, so show them as plain text and never act on
// them. Anything shown outside the pane should say which pane it came from, so
// one program cannot pose as another.
type ProgramStatus struct {
	// ID is the record's "/"-separated path; "" is the root record.
	ID string
	// State is the record's state.
	State ProgramState
	// Kind says what a ProgramBlocked record waits for: "permission"
	// (approval to do something), "question" (an answer to type), "auth" (a
	// login or credential), or "" when not given. Always "" in other states.
	Kind string
	// Progress is a percentage, 0..100, for ProgramWorking and ProgramBlocked
	// records, or -1 when the program gave none (busy, amount unknown). Always
	// -1 in other states.
	Progress int
	// App is the program's stable machine name ("cargo", "terraform"). A
	// record that did not give one takes it from its nearest ancestor that did,
	// already resolved here.
	App string
	// Title is a short label for the record, "" when not given.
	Title string
	// Msg is one line saying what the record is doing, waiting for, or
	// finished, "" when not given.
	Msg string
}

// programState maps the grid's state onto the public enum. The two are kept
// separate so the grid's zero value stays invalid (see statusState).
func (s statusState) programState() ProgramState {
	switch s {
	case statusWorking:
		return ProgramWorking
	case statusDone:
		return ProgramDone
	case statusBlocked:
		return ProgramBlocked
	case statusError:
		return ProgramError
	default:
		return ProgramIdle
	}
}

// ProgramStatus returns the pane's current OSC 7501 records, sorted by ID so
// the root record (if any) comes first and children follow their parents. Nil
// when there are none. The slice is a copy; the caller owns it.
//
// Call it from Cfg.OnProgramStatus, or whenever the embedder redraws. Safe from
// any goroutine.
func (t *Term) ProgramStatus() []ProgramStatus {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	recs := t.grid.status
	if len(recs) == 0 {
		return nil
	}
	out := make([]ProgramStatus, len(recs))
	for i := range recs {
		r := &recs[i]
		out[i] = ProgramStatus{
			ID:       r.id,
			State:    r.state.programState(),
			Kind:     r.kind,
			Progress: int(r.progress),
			App:      r.app,
			Title:    r.title,
			Msg:      r.msg,
		}
	}
	slices.SortFunc(out, func(a, b ProgramStatus) int { return strings.Compare(a.ID, b.ID) })
	// App inheritance. Sorted by id, every ancestor of a record sorts before
	// it, so a single forward pass could almost do this — but a parent need
	// not exist, and the nearest ancestor that does may be any prefix, so look
	// each one up. At most maxStatusIDDepth lookups per record.
	for i := range out {
		if out[i].App != "" || out[i].ID == "" {
			continue
		}
		out[i].App = inheritedApp(out, out[i].ID)
	}
	return out
}

// inheritedApp walks id's ancestors from nearest to the root and returns the
// first non-empty App. The caller resolves records in id order, so an ancestor
// may already hold an App it inherited itself; that is the answer the walk
// would reach anyway.
func inheritedApp(recs []ProgramStatus, id string) string {
	for {
		parent := ""
		if i := strings.LastIndexByte(id, '/'); i >= 0 {
			parent = id[:i]
		}
		if j, ok := slices.BinarySearchFunc(recs, parent, func(r ProgramStatus, id string) int {
			return strings.Compare(r.ID, id)
		}); ok && recs[j].App != "" {
			return recs[j].App
		}
		if parent == "" {
			return ""
		}
		id = parent
	}
}

// reportProgramStatus tells the embedder the records changed. Coalesced: at
// most one call is queued at a time, and it reads nothing until it runs, so
// however many changes land before the main thread gets to it, the embedder
// sees one call and the latest set. Runs on the reader goroutine.
func (t *Term) reportProgramStatus() {
	fn := t.cfg.OnProgramStatus
	if fn == nil || t.statusPending.Swap(true) {
		return
	}
	t.queueCommand(func(*gui.Window) {
		// Cleared before the call, so a change that lands while fn runs
		// queues a fresh one instead of being folded into a read that has
		// already happened.
		t.statusPending.Store(false)
		fn()
	})
}

// dropTransientStatusOnExit applies the protocol's exit rule: when the child
// exits, its working and blocked records go. Runs on the reader goroutine as it
// shuts down.
func (t *Term) dropTransientStatusOnExit() {
	t.grid.Mu.Lock()
	t.grid.dropTransientStatus()
	ver := t.grid.StatusVersion
	t.grid.Mu.Unlock()
	t.syncStatusVersion(ver)
}

// syncStatusVersion reports a record change when ver, a grid.StatusVersion read
// under the lock, moved since the last report. Call it after releasing the lock:
// reportProgramStatus queues a command. Reader goroutine only.
func (t *Term) syncStatusVersion(ver uint64) {
	if ver != t.statusVersion {
		t.statusVersion = ver
		t.reportProgramStatus()
	}
}
