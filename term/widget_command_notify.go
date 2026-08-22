package term

import (
	"fmt"
	"time"
)

// Long-running command notification. A shell that emits OSC 133 brackets each
// command with C (output starts) and D (command ended, with exit status);
// timing the gap between them is enough to tell "this took a while" without
// storing a timestamp on every mark. See scripts/shell-integration/ for the
// hooks that produce the marks.

// minNotifyAfter is the floor for Cfg.NotifyAfter. A threshold below this
// would fire on ordinary interactive commands whenever the user happened to
// be looking elsewhere, which trains them to ignore the notification.
const minNotifyAfter = time.Second

// now reads the Term's clock, defaulting to time.Now. The seam exists so a
// test can make a command appear to have taken minutes without sleeping.
func (t *Term) now() time.Time {
	if t.clock != nil {
		return t.clock()
	}
	return time.Now()
}

// registerCommandHandler wires the OSC 133 C/D marks to command timing.
// Extracted for the same reason as registerNotifyHandler: the tests install
// the identical handler rather than a copy that can drift.
func (t *Term) registerCommandHandler() {
	t.parser.SetCommandHandler(func(kind byte, exit int16) {
		switch kind {
		case 'C':
			t.cmdStart.Store(t.now().UnixNano())
		case 'D':
			start := t.cmdStart.Swap(0)
			if start == 0 {
				// D without a preceding C: the shell emits A/B/D around an
				// empty prompt (the user pressed Enter on a blank line), or
				// the integration script skips C. Nothing ran, so there is
				// nothing to report.
				return
			}
			t.reportCommandEnd(exit)
			t.maybeNotifyCommand(time.Duration(t.now().UnixNano()-start), exit)
		}
	})
}

// commandFailed reports whether an exit status is a real failure. Zero is
// success, and markExitUnknown is "the shell did not say" — which must not
// read as a failure, but must not read as success either.
func commandFailed(exit int16) bool {
	return exit != markExitUnknown && exit != 0
}

// reportCommandEnd tells the embedder a command finished so a pane manager can
// mark the tab it ran in. Deliberately separate from maybeNotifyCommand below:
// the desktop notification has a duration threshold and a focus check because
// it interrupts the whole desktop, while a tab marker interrupts nothing and
// wants every command end — a background tab is equally unread whether the
// command took an hour or a second.
//
// Called from the parser on the reader goroutine with grid.Mu held;
// reportActivity hops to the main thread rather than calling the hook here.
func (t *Term) reportCommandEnd(exit int16) {
	kind := ActivityCommandDone
	if commandFailed(exit) {
		kind = ActivityCommandFailed
	}
	t.reportActivity(kind)
}

// maybeNotifyCommand fires a desktop notification for a command that ran
// longer than the configured threshold while the window was unfocused.
// Called from the parser on the reader goroutine with grid.Mu held.
func (t *Term) maybeNotifyCommand(elapsed time.Duration, exit int16) {
	after := time.Duration(t.notifyAfter.Load())
	if after <= 0 || elapsed < after {
		return
	}
	// The point of the notification is to reach a user who is looking at
	// something else. Firing it for a window they are already watching is
	// pure noise, and duplicates what the pane itself just showed them.
	if t.winFocused.Load() {
		return
	}
	// Read the command line before releasing the reader goroutine: this runs
	// under grid.Mu, and by the time the goroutine below is scheduled the
	// next prompt may already have scrolled the row away.
	body := truncatePaste(t.grid.commandText(), notifyMax)
	t.notifyAsync(commandNotifyTitle(elapsed, exit), body)
}

// commandNotifyTitle renders the notification title. A failure is only named
// when the shell actually reported one; an absent status stays neutral.
func commandNotifyTitle(elapsed time.Duration, exit int16) string {
	d := elapsed.Round(time.Second)
	if commandFailed(exit) {
		return fmt.Sprintf("Command failed (exit %d, %s)", exit, d)
	}
	return fmt.Sprintf("Command finished (%s)", d)
}

// SetNotifyAfter changes the long-running-command notification threshold on a
// live terminal, mirroring Cfg.NotifyAfter: zero or negative disables it, and
// a positive value below minNotifyAfter is raised to it. Safe to call from
// any goroutine.
//
// The notification requires a shell that emits OSC 133 — see
// scripts/shell-integration/. Without those marks no command is ever timed
// and this setting has no effect.
func (t *Term) SetNotifyAfter(d time.Duration) {
	t.notifyAfter.Store(int64(clampNotifyAfter(d)))
}

// clampNotifyAfter applies the sign and floor conventions shared by
// Cfg.NotifyAfter and SetNotifyAfter.
func clampNotifyAfter(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return 0
	case d < minNotifyAfter:
		return minNotifyAfter
	default:
		return d
	}
}
