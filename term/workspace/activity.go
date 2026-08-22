package workspace

import (
	"github.com/go-gui-org/go-term/term"
)

// tab activity indicators. A background tab collects the events its panes
// asserted — the bell rang, a command finished, a command failed — and the
// tab bar renders whichever is most interesting.
//
// Every state here comes from something the child said explicitly (a BEL, an
// OSC 133 D mark). Screen output deliberately does not count: an application
// that repaints on a timer — a spinner, a status-line clock, an animated
// prompt — changes cells forever whether or not anything happened, so "the
// screen changed" cannot tell a finished build from an idle TUI. An indicator
// that is always lit is an indicator nobody reads.

// tabIndicator is what the tab bar draws to the left of a tab's title.
type tabIndicator int

const (
	// indicatorNone is the active tab, or a background tab that has reported
	// nothing since it went to the background.
	indicatorNone tabIndicator = iota
	// indicatorCommandDone: a command finished successfully in this tab.
	indicatorCommandDone
	// indicatorCommandFailed: a command finished with a non-zero exit.
	// Outranks a success — a tab holding both has one thing worth reading.
	indicatorCommandFailed
	// indicatorBell: the bell rang. Outranks the rest — it is the signal the
	// child asked for by name rather than one derived from its exit status.
	indicatorBell
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
func (t *tab) indicator() tabIndicator {
	switch {
	case t.bell:
		return indicatorBell
	case t.cmdFailed:
		return indicatorCommandFailed
	case t.cmdDone:
		return indicatorCommandDone
	default:
		return indicatorNone
	}
}

// clearActivity drops a tab's accumulated indicator state. Called when the
// tab becomes active — the user is now looking at whatever it was reporting.
func (t *tab) clearActivity() {
	t.bell = false
	t.cmdDone = false
	t.cmdFailed = false
}
