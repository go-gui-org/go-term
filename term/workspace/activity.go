package workspace

import (
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// Tab activity indicators. A background tab collects three states from the
// output of its panes — something happened, the bell rang, it has gone quiet
// again — and the tab bar renders whichever is most interesting.
//
// Only the first two are reported by Term (Cfg.OnActivity). Silence is
// derived from how long ago the last report was, because "nothing happened"
// is not an event a terminal can send.

// silenceAfter is how long a background tab must go without output, after
// having produced some, before it reads as quiet. Sized for the case the
// indicator is for — a long build or test run that has finished writing —
// rather than for the gaps within one.
const silenceAfter = 10 * time.Second

// tabIndicator is what the tab bar draws to the left of a tab's title.
type tabIndicator int

const (
	// indicatorNone is the active tab, or a background tab that has produced
	// nothing since it went to the background.
	indicatorNone tabIndicator = iota
	// indicatorActivity: output arrived recently.
	indicatorActivity
	// indicatorSilence: output arrived, then stopped for silenceAfter.
	indicatorSilence
	// indicatorBell: the bell rang. Outranks the other two — it is the only
	// one the child asked for explicitly.
	indicatorBell
)

// glyph is the marker painted in the tab bar. Deliberately plain text rather
// than emoji: these sit in the tab title's own font, and a color emoji among
// them renders at a different size on every platform.
func (i tabIndicator) glyph() string {
	switch i {
	case indicatorActivity:
		return "●"
	case indicatorSilence:
		return "○"
	case indicatorBell:
		return "!"
	default:
		return ""
	}
}

// onPaneActivity records output or a bell against the tab that owns the pane.
// Runs on the main thread, dispatched there by Term.
func (ws *Workspace) onPaneActivity(leafID string, kind term.ActivityKind) {
	for i, tab := range ws.tabs {
		if _, ok := tab.terms[leafID]; !ok {
			continue
		}
		// The active tab is on screen; its output is its own indicator.
		if i == ws.activeTab {
			return
		}
		now := ws.now()
		was := tab.indicatorAt(now)
		tab.lastActivity = now
		if kind == term.ActivityBell {
			tab.bell = true
		}
		// Only repaint when the marker actually changes. Output arrives in a
		// steady stream from a busy pane and the indicator is the same on
		// every one of those reads; refreshing per read would rebuild the
		// whole view tree for a tab bar that already looks right.
		if tab.indicatorAt(now) != was {
			ws.refresh()
		}
		ws.armSilenceTimer()
		return
	}
}

// tabGlyph is the marker the tab bar paints for a tab, or "" for none. The
// active tab never carries one — activateTab clears the state of whichever tab
// the user switches to, and the check short-circuits ahead of resolving an
// indicator that would be discarded.
func (ws *Workspace) tabGlyph(tab *Tab, isActive bool) string {
	if isActive {
		return ""
	}
	return tab.indicatorAt(ws.now()).glyph()
}

// indicatorAt resolves a tab's marker as of now. The instant is passed in
// rather than read here so the tab bar and the activity handler cannot
// disagree about the current time, and so a test can move it. Caller is on
// the main thread.
func (t *Tab) indicatorAt(now time.Time) tabIndicator {
	switch {
	case t.bell:
		return indicatorBell
	case t.lastActivity.IsZero():
		return indicatorNone
	case !now.Before(t.lastActivity.Add(silenceAfter)):
		return indicatorSilence
	default:
		return indicatorActivity
	}
}

// clearActivity drops a tab's accumulated indicator state. Called when the
// tab becomes active — the user is now looking at whatever it was reporting.
func (t *Tab) clearActivity() {
	t.lastActivity = time.Time{}
	t.bell = false
}

// armSilenceTimer schedules the repaint that turns an activity marker into a
// silence marker. Nothing else would trigger it: the transition happens
// because output *stopped*, so there is no frame coming to notice it.
//
// One timer serves every tab. It fires at the earliest moment any tab could
// go quiet and re-arms from there, so the cost is one timer per burst of
// output rather than one per tab.
func (ws *Workspace) armSilenceTimer() {
	if ws.silenceTimer != nil {
		ws.silenceTimer.Stop()
		ws.silenceTimer = nil
	}
	now := ws.now()
	next, ok := ws.nextSilenceDeadline()
	if !ok {
		return
	}
	// Measured against the workspace clock, not time.Now, so a test clock and
	// the deadline it produced cannot disagree about how far away it is.
	ws.silenceTimer = time.AfterFunc(next.Sub(now), func() {
		ws.w.QueueCommand(func(*gui.Window) {
			ws.onSilenceTimeout()
		})
	})
}

// nextSilenceDeadline is the soonest instant at which some background tab
// crosses into silence, if any is pending.
//
// A tab that is *already* silent is not pending and must be skipped: its
// deadline is in the past, and arming a timer on it would fire immediately,
// re-arm on the same stale deadline, and spin the main thread repainting the
// tab bar forever.
func (ws *Workspace) nextSilenceDeadline() (time.Time, bool) {
	now := ws.now()
	var next time.Time
	for i, tab := range ws.tabs {
		if i == ws.activeTab || tab.lastActivity.IsZero() || tab.bell {
			continue
		}
		d := tab.lastActivity.Add(silenceAfter)
		if !d.After(now) {
			continue
		}
		if next.IsZero() || d.Before(next) {
			next = d
		}
	}
	return next, !next.IsZero()
}

// onSilenceTimeout repaints for the activity → silence transition and re-arms
// for whichever tab is next in line. Runs on the main thread.
func (ws *Workspace) onSilenceTimeout() {
	ws.silenceTimer = nil
	ws.refresh()
	ws.armSilenceTimer()
}

// now reads the workspace clock, defaulting to time.Now. The seam lets a test
// drive the activity → silence transition without waiting silenceAfter.
func (ws *Workspace) now() time.Time {
	if ws.clock != nil {
		return ws.clock()
	}
	return time.Now()
}
