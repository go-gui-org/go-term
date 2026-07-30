package workspace

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// The toggle is unconditional, matching tmux `synchronize-panes`: arming it on
// a single-pane tab is allowed, because the pill makes the state visible and a
// pane-count rule would only be one more thing for the user to remember.
func TestToggleBroadcast_AllowedWithOnePane(t *testing.T) {
	ws := newLiveWorkspace(t)

	ws.ToggleBroadcast()

	if !ws.Broadcasting() {
		t.Error("broadcast refused on a single-pane tab")
	}
}

func TestToggleBroadcast_TogglesWithTwoPanes(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)

	ws.ToggleBroadcast()
	if !ws.Broadcasting() {
		t.Fatal("broadcast did not turn on with two live panes")
	}
	ws.ToggleBroadcast()
	if ws.Broadcasting() {
		t.Error("broadcast did not turn back off")
	}
}

// State is per-tab: a new tab must not inherit the mode from the tab that was
// active when it was created.
func TestToggleBroadcast_IsPerTab(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	first := activeTabOf(t, ws)

	ws.AddTab()

	if ws.Broadcasting() {
		t.Error("the new tab inherited broadcast")
	}
	if !first.broadcast {
		t.Error("the original tab lost its broadcast state")
	}
}

// The mode is sticky across a drop back to one pane: closing a pane must not
// silently disarm it, so re-splitting resumes broadcasting as the still-visible
// pill says it will.
func TestBroadcast_SurvivesPaneClose(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	tab := activeTabOf(t, ws)

	ws.ClosePane()

	if !tab.broadcast {
		t.Error("broadcast cleared itself on the drop to a single pane")
	}
}

// Same when the shell dies on its own rather than being closed.
func TestBroadcast_SurvivesShellExit(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	tab := activeTabOf(t, ws)

	ws.onPaneExit(leavesOf(tab.root)[0])

	if !tab.broadcast {
		t.Error("broadcast cleared itself on a shell exit")
	}
}

// --- fan-out targeting ---

// The gate: nothing is mirrored unless the active tab is broadcasting and the
// input actually came from one of its panes.
func TestBroadcastSource_Gating(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	tab := activeTabOf(t, ws)
	src := tab.focused

	if ws.broadcastSource(src) != nil {
		t.Error("mirrored while broadcast was off")
	}

	ws.ToggleBroadcast()

	if ws.broadcastSource(src) != tab {
		t.Error("did not mirror input from a pane of the broadcasting tab")
	}
	if ws.broadcastSource("no-such-leaf") != nil {
		t.Error("mirrored input from a pane this tab does not own")
	}
}

// A pane in a background tab cannot receive keystrokes, but a late callback
// must not be allowed to type into the tab the user is looking at.
func TestBroadcastSource_IgnoresBackgroundTab(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	background := activeTabOf(t, ws)
	backgroundPane := background.focused

	ws.AddTab()

	if got := ws.broadcastSource(backgroundPane); got != nil {
		t.Errorf("broadcastSource = %v for a background-tab pane, want nil", got)
	}
}

func TestBroadcastTarget(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	tab := activeTabOf(t, ws)
	leaves := leavesOf(tab.root)
	src, other := leaves[0], leaves[1]

	if broadcastTarget(src, src, tab.terms[src]) {
		t.Error("the source pane must not receive its own input twice")
	}
	if !broadcastTarget(src, other, tab.terms[other]) {
		t.Error("a live sibling pane should receive the input")
	}
	if broadcastTarget(src, other, nil) {
		t.Error("a missing Term must not be written to")
	}

	// A dead shell would only collect EIO.
	_ = tab.terms[other].Close()
	if broadcastTarget(src, other, tab.terms[other]) {
		t.Error("a dead pane should be skipped")
	}
}

// The fan-out loop itself: with broadcast armed on a two-pane tab, both input
// kinds must dispatch to the live sibling without panicking or killing it.
// Delivery is asserted at the term layer (Term.Write / Term.Paste); what is
// exercised here is the workspace-side dispatch that picks between them.
func TestOnPaneInput_DispatchesBothKinds(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	ws.ToggleBroadcast()
	tab := activeTabOf(t, ws)
	src := tab.focused

	ws.onPaneInput(src, []byte("x"), term.InputKey)
	ws.onPaneInput(src, []byte("hello"), term.InputPaste)

	for id, tm := range tab.terms {
		if !tm.Alive() {
			t.Errorf("pane %s died during broadcast fan-out", id)
		}
	}
}

// The gate is what stops a stray callback typing into panes: with broadcast
// off, or from a pane this workspace does not own, nothing may be dispatched.
func TestOnPaneInput_GatedWhenOff(t *testing.T) {
	ws := newLiveWorkspace(t)
	ws.SplitPane(false)
	tab := activeTabOf(t, ws)

	// Broadcast off.
	ws.onPaneInput(tab.focused, []byte("x"), term.InputKey)
	// Broadcast on, but the source pane belongs to no tab.
	ws.ToggleBroadcast()
	ws.onPaneInput("no-such-leaf", []byte("x"), term.InputPaste)

	for id, tm := range tab.terms {
		if !tm.Alive() {
			t.Errorf("pane %s died on a gated callback", id)
		}
	}
}

// --- visual state ---

// The tint has to be visibly not the theme border, or broadcast looks like an
// ordinary split.
func TestDividerColor(t *testing.T) {
	tab := &Tab{}
	if got := dividerColor(tab); got != gui.CurrentTheme().ColorBorder {
		t.Errorf("idle divider = %v, want the theme border", got)
	}
	tab.broadcast = true
	if got := dividerColor(tab); got != broadcastTint {
		t.Errorf("broadcasting divider = %v, want the broadcast tint", got)
	}
	if broadcastTint == gui.CurrentTheme().ColorBorder {
		t.Error("the broadcast tint is indistinguishable from an idle divider")
	}
}

// A nil tab reaches dividerColor while a tab is being torn down.
func TestDividerColor_NilTab(t *testing.T) {
	if got := dividerColor(nil); got != gui.CurrentTheme().ColorBorder {
		t.Errorf("nil tab = %v, want the theme border", got)
	}
}

// --- command wiring ---

func TestToggleBroadcast_HasCommandAndLabel(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.loadAndApplyConfig()

	for _, cmd := range ws.commands {
		if cmd.ID != "workspace.toggleBroadcast" {
			continue
		}
		// A blank Label would drop the row from the help overlay, which is
		// where a user discovers a mode this dangerous exists.
		if cmd.Label == "" {
			t.Error("toggleBroadcast has no Label; it would be hidden from the help overlay")
		}
		if !cmd.Shortcut.IsSet() {
			t.Error("toggleBroadcast has no shortcut")
		}
		if !cmd.Global {
			t.Error("toggleBroadcast must be Global to fire before the focused pane")
		}
		return
	}
	t.Fatal("workspace.toggleBroadcast is not in the command table")
}

// The tap must be wired on every pane a tab builds, or broadcast silently
// does nothing for panes created by that path.
func TestPaneHooks_OnInputWired(t *testing.T) {
	ws := newLiveWorkspace(t)
	cfg := (&Tab{}).termCfg(&gui.Window{}, ws.cfg, "pane", "", ws.hooks())
	if cfg.OnInput == nil {
		t.Fatal("termCfg left OnInput nil; broadcast would never see a keystroke")
	}
	// Unknown pane, broadcast off: must be a no-op rather than a panic.
	cfg.OnInput([]byte("x"), term.InputKey)
}
