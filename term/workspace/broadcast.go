package workspace

// Broadcast mode: user input from the focused pane is mirrored to every other
// live pane in the same tab, the standard multiplexer feature for driving
// several hosts in lockstep (tmux `setw synchronize-panes`, iTerm2 "Broadcast
// Input").
//
// Scope is deliberately one tab. Cross-tab broadcast is a footgun: the panes
// you cannot see are exactly the ones you would regret typing into.
//
// The fan-out lives here rather than in term/ because only the workspace knows
// what a sibling pane is. term/ contributes just the tap (Cfg.OnInput) and the
// per-pane input encoder (Term.SendInput) — see term/widget_cfg.go.

import (
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// broadcastTint is the amber used for both the split dividers and the status
// pill while broadcast is on. Deliberately not a theme color: the point is
// that this state looks like nothing else the workspace normally draws.
var broadcastTint = gui.RGB(224, 150, 20)

// broadcastPillPad is the padding inside the status pill and its inset from
// the bottom edge of the window.
const broadcastPillPad = 8

// dividerColor returns the color for a tab's split dividers: the theme border
// normally, the broadcast tint while the tab is mirroring input.
//
// Recoloring the existing 1px dividers rather than adding a border around each
// pane is what keeps toggling free: an inset border would shrink every pane's
// draw box, which can cross a cell boundary and reflow every shell in the tab
// through a SIGWINCH.
func dividerColor(tab *tab) gui.Color {
	if tab != nil && tab.broadcast {
		return broadcastTint
	}
	return gui.CurrentTheme().ColorBorder
}

// broadcastPill is the floating status badge shown while the active tab is
// broadcasting. Anchored bottom-center: the panes draw their own "● REC" pill
// in the top-right corner (term/widget_draw_overlay.go), and the two must
// never land on top of each other.
//
// Purely a readout — no OnClick, so it cannot swallow a click meant for the
// pane underneath, and it sits below the help and theme overlays in z-order.
func (ws *Workspace) broadcastPill() gui.View {
	style := gui.CurrentTheme().M5
	style.Color = gui.RGB(20, 16, 4) // dark text on the amber fill

	pill := tight(gui.FitFit)
	pill.Float = true
	pill.FloatAnchor = gui.FloatBottomCenter
	pill.FloatTieOff = gui.FloatBottomCenter
	pill.FloatOffsetY = -broadcastPillPad
	pill.FloatZIndex = 998
	pill.Color = broadcastTint
	pill.Radius = gui.SomeF(10)
	pill.Padding = gui.NewPadding(3, broadcastPillPad+4, 3, broadcastPillPad+4)
	pill.Content = []gui.View{
		gui.Text(gui.TextCfg{Text: "⌁ BROADCAST", TextStyle: style}),
	}
	return gui.Column(pill)
}

// toggleBroadcast turns input broadcast on or off for the active tab.
// Bound to Cmd+Shift+I by default.
//
// Deliberately unconditional — it toggles on a single-pane tab and survives a
// tab dropping back to one pane, matching tmux `synchronize-panes`. Refusing
// the toggle, or auto-clearing it on pane close, would trade one visible state
// for a rule the user has to remember; the pill is the safety mechanism here,
// and it is on screen the whole time the mode is armed.
func (ws *Workspace) toggleBroadcast() {
	tab := ws.activeTabPtr()
	if tab == nil {
		return
	}
	tab.broadcast = !tab.broadcast
	ws.refresh()
}

// broadcasting reports whether the active tab is mirroring input. Drives the
// divider tint and the status pill.
func (ws *Workspace) broadcasting() bool {
	tab := ws.activeTabPtr()
	return tab != nil && tab.broadcast
}

// activeTabPtr returns the active tab, or nil when the workspace is empty or
// the index is out of range (both happen transiently while tabs are closing).
func (ws *Workspace) activeTabPtr() *tab {
	if ws.activeTab < 0 || ws.activeTab >= len(ws.tabs) {
		return nil
	}
	return ws.tabs[ws.activeTab]
}

// onPaneInput is the term.Cfg.OnInput tap for every pane. It runs on the main
// thread, synchronously inside the source pane's key or paste handler.
//
// Only the *active* tab broadcasts. A background tab cannot receive keystrokes
// anyway, so the check is a guard against a stray callback rather than a
// policy, but it keeps the invariant explicit: what you cannot see does not
// type.
func (ws *Workspace) onPaneInput(srcID string, p []byte, kind term.InputKind) {
	tab := ws.broadcastSource(srcID)
	if tab == nil {
		return
	}
	for id, tm := range tab.terms {
		if !broadcastTarget(srcID, id, tm) {
			continue
		}
		// SendInput owns the per-kind rules — keys verbatim, pastes
		// re-wrapped for the receiver's own ?2004 state — because that is
		// knowledge about terminal encoding, not about panes.
		tm.SendInput(p, kind)
	}
}

// broadcastSource returns the tab whose input should be mirrored, or nil when
// this callback must be ignored: no active tab, broadcast off, or the source
// pane belongs to some other tab.
func (ws *Workspace) broadcastSource(srcID string) *tab {
	tab := ws.activeTabPtr()
	if tab == nil || !tab.broadcast {
		return nil
	}
	if _, ok := tab.terms[srcID]; !ok {
		return nil
	}
	return tab
}

// broadcastTarget reports whether pane id should receive a copy of input that
// originated in srcID. The source pane already wrote for itself, and a dead
// shell would only collect EIO.
func broadcastTarget(srcID, id string, tm *term.Term) bool {
	return id != srcID && tm != nil && tm.Alive()
}
