package term

import "strings"

// Program status records — the storage half of OSC 7501 (the Program Status
// Protocol, https://www.superlogical.com/rex/docs/build/program-status). The
// parser validates a report and hands this file a finished record; nothing here
// sees raw bytes.
//
// Records live on the grid, not on a screen: the protocol says switching to the
// alternate screen does not touch them, RIS removes them all, and DECSTR leaves
// them alone. They are guarded by Grid.Mu like every other grid field.

// statusState is a record's state. Zero is not a valid state, so a zero
// statusRec can never be mistaken for an idle one.
type statusState uint8

const (
	statusIdle statusState = iota + 1
	statusWorking
	statusDone
	statusBlocked
	statusError
)

// transient reports whether a state is dropped when the child exits or a new
// shell prompt begins. The protocol requires that for working and blocked:
// both describe a process that is still running, and once the prompt is back
// it is not. done and error are results the user has not seen yet, so they
// stay. idle is allowed to go either way; it stays, because a record that
// vanishes at the prompt is one the embedder cannot show.
func (s statusState) transient() bool {
	return s == statusWorking || s == statusBlocked
}

// statusRec is one stored record. Strings are already decoded and checked.
// progress is 0..100, or -1 for "no percentage" (indeterminate, or a state
// that carries none).
type statusRec struct {
	id       string // "" is the root record
	state    statusState
	kind     string // "permission", "question", "auth", or ""
	progress int8
	app      string // as reported; inheritance is resolved on read
	title    string
	msg      string
	touched  uint64 // grid.statusSeq at the last update, for LRU eviction
}

// isStatusDescendant reports whether id is under parent in the "/" hierarchy.
// A record is not its own descendant; callers that clear a subtree test the
// equality separately.
func isStatusDescendant(id, parent string) bool {
	if parent == "" {
		return id != "" // every non-root record is below the root
	}
	return len(id) > len(parent) && id[len(parent)] == '/' && strings.HasPrefix(id, parent)
}

// setStatus stores rec, replacing any record with the same id completely: a
// key the report left out is gone from the record afterwards. When the store is
// full, the record updated least recently makes room. Called with g.Mu held.
func (g *grid) setStatus(rec statusRec) {
	g.statusSeq++
	rec.touched = g.statusSeq
	g.StatusVersion++
	for i := range g.status {
		if g.status[i].id == rec.id {
			g.status[i] = rec
			return
		}
	}
	if len(g.status) < maxStatusRecords {
		g.status = append(g.status, rec)
		return
	}
	// Full. A linear scan is fine: the cap is a few hundred and reports arrive
	// at the rate a program changes state.
	oldest := 0
	for i := range g.status {
		if g.status[i].touched < g.status[oldest].touched {
			oldest = i
		}
	}
	g.status[oldest] = rec
}

// clearStatus removes the record id and every record beneath it. The root id
// ("") therefore removes every record. Called with g.Mu held.
func (g *grid) clearStatus(id string) {
	g.removeStatus(func(r *statusRec) bool {
		return r.id == id || isStatusDescendant(r.id, id)
	})
}

// dropTransientStatus removes working and blocked records. Called on the two
// events the protocol ties their lifetime to: a new shell prompt (OSC 133 A)
// and the child exiting. Called with g.Mu held.
func (g *grid) dropTransientStatus() {
	g.removeStatus(func(r *statusRec) bool { return r.state.transient() })
}

// removeStatus deletes, in place, every record drop selects. StatusVersion
// moves only when a record was actually removed: a prompt with nothing to drop
// must not wake the embedder.
func (g *grid) removeStatus(drop func(*statusRec) bool) {
	kept := g.status[:0]
	for i := range g.status {
		if !drop(&g.status[i]) {
			kept = append(kept, g.status[i])
		}
	}
	if len(kept) == len(g.status) {
		return
	}
	// Zero the tail so the dropped strings can be collected.
	clear(g.status[len(kept):])
	g.status = kept
	g.StatusVersion++
}
