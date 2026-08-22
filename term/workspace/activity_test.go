package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/term"
)

// ---------------------------------------------------------------------------
// notify-after parsing
// ---------------------------------------------------------------------------

func TestParseConfig_NotifyAfter(t *testing.T) {
	tests := []struct {
		val     string
		want    time.Duration
		wantErr bool
	}{
		{"30", 30 * time.Second, false},  // bare seconds
		{"30s", 30 * time.Second, false}, // Go duration
		{"2m", 2 * time.Minute, false},   // ditto
		{"1500ms", 1500 * time.Millisecond, false},
		{"0", 0, false},   // explicit off
		{"-5", 0, true},   // negative is meaningless
		{"90m", 0, true},  // past maxNotifyAfter
		{"soon", 0, true}, // not a duration
		{"", 0, true},     // empty value
		// A bare second count large enough that scaling it to a Duration
		// overflows int64 must be rejected on its magnitude, not wrapped into
		// a small or negative threshold that passes the range check.
		{"9223372036854775", 0, true},
	}
	for _, tc := range tests {
		in := "[general]\nnotify-after = " + tc.val + "\n"
		cfg, errs := parseConfig(strings.NewReader(in))
		if tc.wantErr {
			if len(errs) == 0 {
				t.Errorf("notify-after = %q: expected an error, got none", tc.val)
			}
			if cfg.hasNotifyAfter {
				t.Errorf("notify-after = %q: rejected value must not be recorded", tc.val)
			}
			continue
		}
		if len(errs) != 0 {
			t.Errorf("notify-after = %q: unexpected errors %v", tc.val, errs)
			continue
		}
		if !cfg.hasNotifyAfter {
			t.Errorf("notify-after = %q: hasNotifyAfter not set", tc.val)
		}
		if cfg.notifyAfter != tc.want {
			t.Errorf("notify-after = %q: got %v; want %v", tc.val, cfg.notifyAfter, tc.want)
		}
	}
}

// A key deleted from the file must revert to the embedder's default rather
// than leaving the last-loaded value applied — the reload contract every other
// [general] key follows.
func TestApplySettings_NotifyAfterRevertsWhenAbsent(t *testing.T) {
	base := Cfg{}
	with, _ := parseConfig(strings.NewReader("[general]\nnotify-after = 45\n"))
	if got := applySettings(base, with, nil).opts.notifyAfter; got != 45*time.Second {
		t.Fatalf("notifyAfter = %v; want 45s", got)
	}
	without, _ := parseConfig(strings.NewReader("[general]\n"))
	if got := applySettings(base, without, nil).opts.notifyAfter; got != 0 {
		t.Errorf("notifyAfter = %v after the key was removed; want 0", got)
	}
}

// The resolved setting has to reach a pane's term.Cfg, or the config key is
// parsed and then dropped on the floor.
func TestTermCfg_CarriesNotifyAfter(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.cfg.opts.notifyAfter = 90 * time.Second
	cfg := (&tab{}).termCfg(&gui.Window{}, ws.cfg, "pane", "", ws.hooks())
	if cfg.NotifyAfter != 90*time.Second {
		t.Errorf("term.Cfg.NotifyAfter = %v; want 90s", cfg.NotifyAfter)
	}
}

// ---------------------------------------------------------------------------
// tab indicators
// ---------------------------------------------------------------------------

// activityWorkspace builds a two-tab workspace. tab 0 is active, so tab 1 is
// the one that accumulates indicator state.
func activityWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := newTestWorkspace(t)
	ws.tabs = []*tab{
		{id: "tab-0", terms: map[string]*term.Term{"pane-0": nil}},
		{id: "tab-1", terms: map[string]*term.Term{"pane-1": nil}},
	}
	ws.activeTab = 0
	return ws
}

func TestTabIndicator_CommandEnds(t *testing.T) {
	ws := activityWorkspace(t)
	tab := ws.tabs[1]

	if got := tab.indicator(); got != indicatorNone {
		t.Errorf("fresh tab indicator = %v; want none", got)
	}

	ws.onPaneActivity("pane-1", term.ActivityCommandDone)
	if got := tab.indicator(); got != indicatorCommandDone {
		t.Errorf("after a successful command indicator = %v; want done", got)
	}

	// A failure among several commands is the one worth surfacing, whichever
	// order they finished in.
	ws.onPaneActivity("pane-1", term.ActivityCommandFailed)
	if got := tab.indicator(); got != indicatorCommandFailed {
		t.Errorf("after a failure indicator = %v; want failed", got)
	}
	ws.onPaneActivity("pane-1", term.ActivityCommandDone)
	if got := tab.indicator(); got != indicatorCommandFailed {
		t.Errorf("a later success demoted the failure to %v", got)
	}
}

// Screen output is not reported by Term at all, so a repainting application
// in a background tab leaves the tab bar alone. This pins the contract from
// the workspace side: an ActivityKind it does not recognize marks nothing.
func TestTabIndicator_UnknownKindMarksNothing(t *testing.T) {
	ws := activityWorkspace(t)
	ws.onPaneActivity("pane-1", term.ActivityKind(99))
	if got := ws.tabs[1].indicator(); got != indicatorNone {
		t.Errorf("indicator = %v; want none", got)
	}
}

// A pane that is closing may still deliver one last report after the workspace
// dropped its Term — the lookup misses every tab and must be a no-op, not a
// panic.
func TestTabIndicator_StalePaneIDIsNoOp(t *testing.T) {
	ws := activityWorkspace(t)
	ws.onPaneActivity("gone-pane", term.ActivityBell)
	ws.onPaneActivity("gone-pane", term.ActivityCommandFailed)
	for _, tab := range ws.tabs {
		if got := tab.indicator(); got != indicatorNone {
			t.Errorf("stale report lit tab %s as %v; want none", tab.id, got)
		}
	}
}

// All panes of a tab report through the same latch — a tab with several panes
// shows the most interesting event across all of them, not just one.
func TestTabIndicator_MultiPaneTabSharesState(t *testing.T) {
	ws := newTestWorkspace(t)
	ws.tabs = []*tab{
		{id: "tab-0", terms: map[string]*term.Term{"pane-0": nil}},
		{id: "tab-1", terms: map[string]*term.Term{"pane-1": nil, "pane-2": nil}},
	}
	ws.activeTab = 0

	ws.onPaneActivity("pane-1", term.ActivityCommandDone)
	if got := ws.tabs[1].indicator(); got != indicatorCommandDone {
		t.Fatalf("indicator after pane-1 = %v; want done", got)
	}
	ws.onPaneActivity("pane-2", term.ActivityCommandFailed)
	if got := ws.tabs[1].indicator(); got != indicatorCommandFailed {
		t.Errorf("indicator after pane-2 = %v; want failed", got)
	}
	if ws.tabs[0].indicator() != indicatorNone {
		t.Error("events leaked to the active tab")
	}
}

// A bell outranks the command markers: it is the signal the child asked for
// by name rather than one derived from an exit status.
func TestTabIndicator_BellOutranksAndPersists(t *testing.T) {
	ws := activityWorkspace(t)
	tab := ws.tabs[1]

	ws.onPaneActivity("pane-1", term.ActivityBell)
	if got := tab.indicator(); got != indicatorBell {
		t.Errorf("indicator = %v; want bell", got)
	}
	// Later command ends must not demote it.
	ws.onPaneActivity("pane-1", term.ActivityCommandFailed)
	ws.onPaneActivity("pane-1", term.ActivityCommandDone)
	if got := tab.indicator(); got != indicatorBell {
		t.Errorf("indicator after later command ends = %v; want bell", got)
	}
}

// The active tab is on screen, so the user already saw whatever happened.
func TestTabIndicator_ActiveTabAccumulatesNothing(t *testing.T) {
	ws := activityWorkspace(t)

	ws.onPaneActivity("pane-0", term.ActivityBell)
	if got := ws.tabs[0].indicator(); got != indicatorNone {
		t.Errorf("active tab indicator = %v; want none", got)
	}
	if ws.tabs[0].bell || ws.tabs[0].cmdDone || ws.tabs[0].cmdFailed {
		t.Error("active tab recorded activity state")
	}
}

func TestTabIndicator_ClearedOnActivation(t *testing.T) {
	ws := activityWorkspace(t)
	tab := ws.tabs[1]

	ws.onPaneActivity("pane-1", term.ActivityBell)
	ws.onPaneActivity("pane-1", term.ActivityCommandFailed)
	if got := tab.indicator(); got != indicatorBell {
		t.Fatalf("setup: indicator = %v; want bell", got)
	}

	tab.clearActivity()
	if got := tab.indicator(); got != indicatorNone {
		t.Errorf("indicator after clear = %v; want none", got)
	}
}

// The glyph is what the user actually reads, so the mapping is pinned rather
// than left to whatever the render call happens to pass through.
func TestTabIndicator_Glyphs(t *testing.T) {
	tests := []struct {
		in   tabIndicator
		want string
	}{
		{indicatorNone, ""},
		{indicatorCommandDone, "✓"},
		{indicatorCommandFailed, "✗"},
		{indicatorBell, "!"},
	}
	for _, tc := range tests {
		if got := tc.in.glyph(); got != tc.want {
			t.Errorf("indicator %v glyph = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// tabGlyph is the render-path entry point: the active tab never carries a
// marker even when state somehow survived on it.
func TestTabGlyph_ActiveTabNeverMarked(t *testing.T) {
	ws := activityWorkspace(t)
	ws.tabs[0].bell = true
	if got := ws.tabGlyph(ws.tabs[0], true); got != "" {
		t.Errorf("active tab glyph = %q; want empty", got)
	}
	if got := ws.tabGlyph(ws.tabs[0], false); got != "!" {
		t.Errorf("background glyph = %q; want \"!\"", got)
	}
}
