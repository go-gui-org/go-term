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

// activityWorkspace builds a two-tab workspace with a frozen clock. tab 0 is
// active, so tab 1 is the one that accumulates indicator state.
func activityWorkspace(t *testing.T, now *time.Time) *Workspace {
	t.Helper()
	ws := newTestWorkspace(t)
	ws.clock = func() time.Time { return *now }
	ws.tabs = []*tab{
		{id: "tab-0", terms: map[string]*term.Term{"pane-0": nil}},
		{id: "tab-1", terms: map[string]*term.Term{"pane-1": nil}},
	}
	ws.activeTab = 0
	return ws
}

func TestTabIndicator_BackgroundOutputThenSilence(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	tab := ws.tabs[1]

	if got := tab.indicatorAt(now); got != indicatorNone {
		t.Errorf("fresh tab indicator = %v; want none", got)
	}

	ws.onPaneActivity("pane-1", term.ActivityOutput)
	if got := tab.indicatorAt(now); got != indicatorActivity {
		t.Errorf("after output indicator = %v; want activity", got)
	}

	// Just short of the threshold is still activity.
	if got := tab.indicatorAt(now.Add(silenceAfter - time.Millisecond)); got != indicatorActivity {
		t.Errorf("before threshold indicator = %v; want activity", got)
	}
	if got := tab.indicatorAt(now.Add(silenceAfter)); got != indicatorSilence {
		t.Errorf("at threshold indicator = %v; want silence", got)
	}
}

// A bell outranks both other states and does not decay into silence: it is a
// deliberate signal from the child, not a byproduct of output.
func TestTabIndicator_BellOutranksAndPersists(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	tab := ws.tabs[1]

	ws.onPaneActivity("pane-1", term.ActivityBell)
	if got := tab.indicatorAt(now); got != indicatorBell {
		t.Errorf("indicator = %v; want bell", got)
	}
	if got := tab.indicatorAt(now.Add(time.Hour)); got != indicatorBell {
		t.Errorf("indicator after an hour = %v; want bell", got)
	}
	// Later plain output must not demote it.
	ws.onPaneActivity("pane-1", term.ActivityOutput)
	if got := tab.indicatorAt(now); got != indicatorBell {
		t.Errorf("indicator after later output = %v; want bell", got)
	}
}

// The active tab is on screen, so its own output is the indicator.
func TestTabIndicator_ActiveTabAccumulatesNothing(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)

	ws.onPaneActivity("pane-0", term.ActivityBell)
	if got := ws.tabs[0].indicatorAt(now); got != indicatorNone {
		t.Errorf("active tab indicator = %v; want none", got)
	}
	if !ws.tabs[0].lastActivity.IsZero() || ws.tabs[0].bell {
		t.Error("active tab recorded activity state")
	}
}

func TestTabIndicator_ClearedOnActivation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	tab := ws.tabs[1]

	ws.onPaneActivity("pane-1", term.ActivityBell)
	if got := tab.indicatorAt(now); got != indicatorBell {
		t.Fatalf("setup: indicator = %v; want bell", got)
	}

	tab.clearActivity()
	if got := tab.indicatorAt(now); got != indicatorNone {
		t.Errorf("indicator after clear = %v; want none", got)
	}
}

// The silence timer must target the earliest tab that can go quiet, and stop
// existing once nothing is pending.
func TestSilenceDeadline_EarliestPendingTab(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	ws.tabs = append(ws.tabs, &tab{id: "tab-2", terms: map[string]*term.Term{"pane-2": nil}})

	ws.tabs[1].lastActivity = now.Add(-3 * time.Second)
	ws.tabs[2].lastActivity = now.Add(-1 * time.Second)

	got, ok := ws.nextSilenceDeadline()
	if !ok {
		t.Fatal("no deadline; want the earlier of two pending tabs")
	}
	if want := now.Add(-3 * time.Second).Add(silenceAfter); !got.Equal(want) {
		t.Errorf("deadline = %v; want %v", got, want)
	}

	// A tab already showing a bell is not waiting on silence.
	ws.tabs[1].bell = true
	got, ok = ws.nextSilenceDeadline()
	if !ok {
		t.Fatal("no deadline; tab-2 is still pending")
	}
	if want := now.Add(-1 * time.Second).Add(silenceAfter); !got.Equal(want) {
		t.Errorf("deadline = %v; want %v", got, want)
	}

	// Nothing pending at all.
	ws.tabs[1].clearActivity()
	ws.tabs[2].clearActivity()
	if _, ok := ws.nextSilenceDeadline(); ok {
		t.Error("deadline reported with no pending tabs")
	}
}

// A tab that has *already* gone silent is not waiting on anything. Reporting
// its stale deadline would arm a timer that fires immediately, re-arms on the
// same deadline, and spins the main thread repainting the tab bar forever.
func TestSilenceDeadline_SkipsAlreadySilentTab(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	ws.tabs[1].lastActivity = now.Add(-2 * silenceAfter)

	if got, ok := ws.nextSilenceDeadline(); ok {
		t.Errorf("deadline %v reported for an already-silent tab; want none", got)
	}
	// Sanity: the tab really is silent, so there is nothing left to repaint for.
	if got := ws.tabs[1].indicatorAt(now); got != indicatorSilence {
		t.Fatalf("setup: indicator = %v; want silence", got)
	}

	// Fresh output re-arms it — the deadline comes back once it is in the future.
	ws.tabs[1].lastActivity = now
	if _, ok := ws.nextSilenceDeadline(); !ok {
		t.Error("no deadline after fresh output; want one")
	}
}

// The pending repaint must not outlive the workspace: its callback queues work
// on a window the workspace no longer handles events for.
func TestSilenceTimer_StoppedOnClose(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ws := activityWorkspace(t, &now)
	ws.tabs[1].lastActivity = now
	ws.armSilenceTimer()
	if ws.silenceTimer == nil {
		t.Fatal("setup: no timer armed for a tab pending silence")
	}
	// The fake tabs hold nil terminals, which teardown would dereference; the
	// timer, not pane teardown, is what this test is about.
	for _, tab := range ws.tabs {
		tab.terms = nil
	}
	if err := ws.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if ws.silenceTimer != nil {
		t.Error("silence timer still armed after Close")
	}
}

func TestTabIndicator_Glyphs(t *testing.T) {
	tests := []struct {
		in   tabIndicator
		want string
	}{
		{indicatorNone, ""},
		{indicatorActivity, "●"},
		{indicatorSilence, "○"},
		{indicatorBell, "!"},
	}
	for _, tc := range tests {
		if got := tc.in.glyph(); got != tc.want {
			t.Errorf("indicator %v glyph = %q; want %q", tc.in, got, tc.want)
		}
	}
}
