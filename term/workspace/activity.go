package workspace

import (
	"github.com/go-gui-org/go-term/term"
)

// tab activity indicators. A background tab collects the events its panes
// asserted — the bell rang, a command finished, a command failed — and the
// state its programs report over OSC 7501 (working, blocked, done, failed),
// and the tab bar renders whichever is most interesting.
//
// Every state here comes from something the child said explicitly (a BEL, an
// OSC 133 D mark, an OSC 7501 report). Screen output deliberately does not
// count: an application that repaints on a timer — a spinner, a status-line
// clock, an animated prompt — changes cells forever whether or not anything
// happened, so "the screen changed" cannot tell a finished build from an idle
// TUI. An indicator that is always lit is an indicator nobody reads.

// tabIndicator is what the tab bar draws to the left of a tab's title.
type tabIndicator int

const (
	// indicatorNone is the active tab, or a background tab that has reported
	// nothing since it went to the background.
	indicatorNone tabIndicator = iota
	// indicatorCommandDone: a command finished successfully in this tab, or
	// a program reported OSC 7501 done.
	indicatorCommandDone
	// indicatorCommandFailed: a command finished with a non-zero exit, or a
	// program reported OSC 7501 error. Outranks a success — a tab holding
	// both has one thing worth reading.
	indicatorCommandFailed
	// indicatorBell: the bell rang.
	indicatorBell
	// indicatorWorking: a program reports OSC 7501 working. The weakest
	// marker: it says the tab is busy, which is worth less than any result.
	indicatorWorking
	// indicatorBlocked: a program reports OSC 7501 blocked — it cannot go on
	// until the user acts. Outranks everything: every other marker is news,
	// this one is a program waiting.
	indicatorBlocked
)

// glyph is the marker painted in the tab bar. Deliberately plain text rather
// than emoji: these sit in the tab title's own font, and a color emoji among
// them renders at a different size on every platform.
func (i tabIndicator) glyph() string {
	switch i {
	case indicatorCommandDone:
		return "✓"
	case indicatorCommandFailed:
		return "✗"
	case indicatorBell:
		return "!"
	case indicatorWorking:
		return "…"
	case indicatorBlocked:
		return "?"
	default:
		return ""
	}
}

// onPaneActivity records an event against the tab that owns the pane.
// Runs on the main thread, dispatched there by Term.
func (ws *Workspace) onPaneActivity(leafID string, kind term.ActivityKind) {
	for i, tab := range ws.tabs {
		if _, ok := tab.terms[leafID]; !ok {
			continue
		}
		// The active tab is on screen; the user saw whatever happened.
		if i == ws.activeTab {
			return
		}
		was := tab.indicator()
		tab.noteActivity(kind)
		// Only repaint when the marker actually changes. A tab that already
		// shows a bell learns nothing from a second one, and rebuilding the
		// whole view tree for a tab bar that already looks right is waste.
		if tab.indicator() != was {
			ws.refresh()
		}
		return
	}
}

// onPaneProgramStatus folds a pane's new OSC 7501 record set into its tab.
// Runs on the main thread, dispatched there by Term.
func (ws *Workspace) onPaneProgramStatus(leafID string) {
	for i, tab := range ws.tabs {
		tm, ok := tab.terms[leafID]
		if !ok {
			continue
		}
		was := tab.indicator()
		tab.noteProgramStatus(leafID, tm.ProgramStatus(), i != ws.activeTab)
		// Same rule as onPaneActivity: the active tab draws no marker, and a
		// background one repaints only when its marker changes. Progress
		// updates arrive many times a second and almost never change it.
		if i != ws.activeTab && tab.indicator() != was {
			ws.refresh()
		}
		return
	}
}

// noteProgramStatus records a pane's current OSC 7501 states.
//
// Working and blocked are live: the indicator reads them from the stored
// states, so a record that is cleared, finishes, or dies with its process
// takes its marker with it. Done and error latch instead, like a command end,
// so the marker stays until the user looks at the tab — but only on the
// transition into done or error. A done record outlives the moment it was
// reported (the protocol keeps it until the terminal decides to stop showing
// it), so latching on its mere presence would re-light a tab the user already
// read the next time any other record in that pane changed.
func (t *tab) noteProgramStatus(leafID string, recs []term.ProgramStatus, background bool) {
	prev := t.status[leafID]
	var next map[string]term.ProgramState
	if len(recs) > 0 {
		next = make(map[string]term.ProgramState, len(recs))
	}
	for _, r := range recs {
		next[r.ID] = r.State
		if old, ok := prev[r.ID]; background && (!ok || old != r.State) {
			switch r.State {
			case term.ProgramDone:
				t.statusDone = true
			case term.ProgramError:
				t.statusError = true
			}
		}
	}
	if next == nil {
		delete(t.status, leafID)
		return
	}
	if t.status == nil {
		t.status = make(map[string]map[string]term.ProgramState)
	}
	t.status[leafID] = next
}

// liveStatus reports whether any pane in the tab currently has a blocked or a
// working record.
func (t *tab) liveStatus() (blocked, working bool) {
	for _, recs := range t.status {
		for _, st := range recs {
			switch st {
			case term.ProgramBlocked:
				blocked = true
			case term.ProgramWorking:
				working = true
			}
		}
	}
	return blocked, working
}

// tabGlyph is the marker the tab bar paints for a tab, or "" for none. The
// active tab never carries one — activateTab clears the state of whichever tab
// the user switches to, and the check short-circuits ahead of resolving an
// indicator that would be discarded.
func (ws *Workspace) tabGlyph(tab *tab, isActive bool) string {
	if isActive {
		return ""
	}
	return tab.indicator().glyph()
}

// noteActivity folds one report into the tab's accumulated state. Each kind
// latches independently so a later success cannot erase an earlier failure;
// the priority ordering in indicator decides what actually gets drawn.
func (t *tab) noteActivity(kind term.ActivityKind) {
	switch kind {
	case term.ActivityBell:
		t.bell = true
	case term.ActivityCommandFailed:
		t.cmdFailed = true
	case term.ActivityCommandDone:
		t.cmdDone = true
	}
}

// indicator resolves a tab's marker from the events it has latched. Caller is
// on the main thread.
//
// Order: blocked, then a program's own error report, then the bell, then a
// failed command, then done, then working. A program's error outranks the bell
// because it names what failed; a bare non-zero exit stays below the bell, as
// it was before OSC 7501.
func (t *tab) indicator() tabIndicator {
	blocked, working := t.liveStatus()
	switch {
	case blocked:
		return indicatorBlocked
	case t.statusError:
		return indicatorCommandFailed
	case t.bell:
		return indicatorBell
	case t.cmdFailed:
		return indicatorCommandFailed
	case t.cmdDone || t.statusDone:
		return indicatorCommandDone
	case working:
		return indicatorWorking
	default:
		return indicatorNone
	}
}

// clearActivity drops a tab's accumulated indicator state. Called when the
// tab becomes active — the user is now looking at whatever it was reporting.
// The live OSC 7501 states stay: they describe what is true now, not what
// happened while the tab was hidden.
func (t *tab) clearActivity() {
	t.bell = false
	t.cmdDone = false
	t.cmdFailed = false
	t.statusDone = false
	t.statusError = false
}
