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
	// indicatorNone: nothing to show. An active tab with no live OSC 7501
	// state, or a background tab that has reported nothing since it went to
	// the background.
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
		ws.applyProgramStatus(i, tab, leafID, tm.ProgramStatus())
		return
	}
}

// applyProgramStatus is onPaneProgramStatus after the pane lookup: it folds
// recs into the tab at index i and repaints if what the user sees changed.
// Split out so tests can drive it without a live Term.
func (ws *Workspace) applyProgramStatus(i int, tab *tab, leafID string, recs []term.ProgramStatus) {
	active := i == ws.activeTab
	// visible is the marker the user can see for this tab. A background tab's
	// marker covers latched news too; the active tab shows only its live state
	// (tab bar and window title).
	visible := tab.indicator
	if active {
		visible = tab.liveIndicator
	}
	was := visible()
	tab.noteProgramStatus(leafID, recs, !active)
	// Repaint only when the visible marker changes. Progress updates arrive
	// many times a second and almost never change it.
	if visible() != was {
		ws.refresh()
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

// liveIndicator is the marker for the tab's live OSC 7501 state only: blocked,
// working, or none. The latched events (bell, command ends, done, error) are
// left out: they report what happened while the tab was hidden, and the window
// title shows the tab the user is looking at.
func (t *tab) liveIndicator() tabIndicator {
	blocked, working := t.liveStatus()
	switch {
	case blocked:
		return indicatorBlocked
	case working:
		return indicatorWorking
	default:
		return indicatorNone
	}
}

// windowTitle is the OS window title for the tab: the focused pane's title,
// with the live-state marker in front of it when a program reports one.
//
// The marker is necessary because the active tab carries no tab-bar marker, and
// a window with one tab has no tab bar. Claude Code removes its own spinner from
// the title when the terminal answers the OSC 7501 query. Without this marker,
// nothing on screen would show that the program is working or waiting.
func (t *tab) windowTitle() string {
	title := t.focusedTitle()
	if g := t.liveIndicator().glyph(); g != "" {
		return g + " " + title
	}
	return title
}

// tabGlyph is the marker the tab bar paints for a tab, or "" for none. The
// active tab carries only its live state (working, blocked): the user is looking
// at it, so latched news (bell, command ends, done, error) means nothing there,
// and activateTab clears that state anyway. Live state still matters — it says
// the program is busy or waiting right now, the same marker the window title
// shows.
func (ws *Workspace) tabGlyph(tab *tab, isActive bool) string {
	if isActive {
		return tab.liveIndicator().glyph()
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
