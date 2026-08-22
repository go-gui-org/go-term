package term

import (
	"testing"
	"time"
)

// notifyTestTerm builds a Term wired for the command-notification path: a
// synchronous scheduler, a notifier that signals a channel, and a clock the
// test advances by hand so a "long" command costs no wall time.
func notifyTestTerm(t *testing.T, after time.Duration) (*Term, *grid, *parser, chan [2]string) {
	t.Helper()
	g := newGrid(4, 80)
	p := newParser(g)
	tm := &Term{grid: g, parser: p, cmd: syncScheduler{}}
	fired := make(chan [2]string, 4)
	tm.notif = notifierFunc(func(title, body string) {
		fired <- [2]string{title, body}
	})
	tm.winFocused.Store(false) // unattended unless a test says otherwise
	tm.notifyAfter.Store(int64(after))
	tm.registerCommandHandler()
	return tm, g, p, fired
}

// runCommand feeds one OSC 133 C … D cycle with elapsed simulated between
// them, and the given command line typed at the prompt.
func runCommand(t *testing.T, tm *Term, g *grid, p *parser, cmdLine string, elapsed time.Duration, exit string) {
	t.Helper()
	base := time.Unix(1700000000, 0)
	tm.clock = func() time.Time { return base }
	// B marks where the typed command begins, which is what commandText reads
	// from; the prompt ahead of it must not end up in the notification.
	feed(t, g, p, []byte("prompt$ \x1b]133;B\x07"+cmdLine))
	feed(t, g, p, []byte("\x1b]133;C\x07"))
	tm.clock = func() time.Time { return base.Add(elapsed) }
	feed(t, g, p, []byte("\x1b]133;D;"+exit+"\x07"))
}

func TestCommandNotify_FiresPastThreshold(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, 30*time.Second)
	runCommand(t, tm, g, p, "cargo build --release", 2*time.Minute+14*time.Second, "0")
	got := assertNotify(t, fired)
	if want := "Command finished (2m14s)"; got[0] != want {
		t.Errorf("title = %q; want %q", got[0], want)
	}
	if want := "cargo build --release"; got[1] != want {
		t.Errorf("body = %q; want %q", got[1], want)
	}
}

func TestCommandNotify_ReportsFailure(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	runCommand(t, tm, g, p, "make test", 90*time.Second, "1")
	got := assertNotify(t, fired)
	if want := "Command failed (exit 1, 1m30s)"; got[0] != want {
		t.Errorf("title = %q; want %q", got[0], want)
	}
}

// A shell that reports no status must not read as either success or failure —
// markExitUnknown means "ended", not "ended well".
func TestCommandNotify_UnknownExitIsNotAFailure(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	runCommand(t, tm, g, p, "sleep 90", 90*time.Second, "")
	got := assertNotify(t, fired)
	if want := "Command finished (1m30s)"; got[0] != want {
		t.Errorf("title = %q; want %q", got[0], want)
	}
}

func TestCommandNotify_SuppressedBelowThreshold(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, 30*time.Second)
	runCommand(t, tm, g, p, "ls", 2*time.Second, "0")
	assertNoNotify(t, fired)
}

// The whole point is to reach a user who is looking elsewhere. A focused
// terminal already showed them the command finishing.
func TestCommandNotify_SuppressedWhenFocused(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	tm.winFocused.Store(true)
	tm.focused.Store(true)
	runCommand(t, tm, g, p, "sleep 60", time.Minute, "0")
	assertNoNotify(t, fired)
}

// Attended means both halves: the window has focus *and* this pane is the one
// in front. A pane a tab switch put in the background is unattended even
// though its window never lost focus — the pane manager says so through
// SetFocused, never by faking a window event (that would write a bogus ?1004
// focus report to the child).
func TestCommandNotify_FiresForBackgroundPaneInFocusedWindow(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	tm.winFocused.Store(true)
	tm.SetFocused(false)
	runCommand(t, tm, g, p, "sleep 60", time.Minute, "0")
	got := assertNotify(t, fired)
	if want := "Command finished (1m0s)"; got[0] != want {
		t.Errorf("title = %q; want %q", got[0], want)
	}
}

// The window half of the gate stands on its own: a pane the user is looking
// at (focused=true) is still unattended when its window lost focus.
func TestCommandNotify_FiresWhenWindowUnfocused(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	tm.focused.Store(true)
	runCommand(t, tm, g, p, "sleep 60", time.Minute, "0")
	got := assertNotify(t, fired)
	if want := "Command finished (1m0s)"; got[0] != want {
		t.Errorf("title = %q; want %q", got[0], want)
	}
}

func TestCommandNotify_DisabledByDefault(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, 0)
	runCommand(t, tm, g, p, "sleep 300", 5*time.Minute, "0")
	assertNoNotify(t, fired)
}

// D with no preceding C is what a bare Enter at the prompt produces. Nothing
// ran, so there is nothing to report — and the elapsed time would be measured
// from whenever the previous command started.
func TestCommandNotify_DWithoutCIsIgnored(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, time.Second)
	base := time.Unix(1700000000, 0)
	tm.clock = func() time.Time { return base.Add(time.Hour) }
	feed(t, g, p, []byte("\x1b]133;D;0\x07"))
	assertNoNotify(t, fired)
}

func TestSetNotifyAfter_LiveAndClamped(t *testing.T) {
	tm, _, _, _ := notifyTestTerm(t, 0)
	tests := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, 0},
		{-5 * time.Second, 0},
		{time.Millisecond, minNotifyAfter}, // below the floor is raised, not off
		{45 * time.Second, 45 * time.Second},
	}
	for _, tc := range tests {
		tm.SetNotifyAfter(tc.in)
		if got := time.Duration(tm.notifyAfter.Load()); got != tc.want {
			t.Errorf("SetNotifyAfter(%v) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

// A live change must take effect without recreating the Term — this is the
// path a config reload uses.
func TestSetNotifyAfter_TakesEffectOnLiveTerm(t *testing.T) {
	tm, g, p, fired := notifyTestTerm(t, 0)
	runCommand(t, tm, g, p, "sleep 60", time.Minute, "0")
	assertNoNotify(t, fired)

	tm.SetNotifyAfter(30 * time.Second)
	runCommand(t, tm, g, p, "sleep 60", time.Minute, "0")
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("no notification after SetNotifyAfter enabled it")
	}
}

func TestCommandText_ExcludesPromptAndTrailingBlanks(t *testing.T) {
	g := newGrid(4, 80)
	p := newParser(g)
	feed(t, g, p, []byte("user@host ~/src $ \x1b]133;B\x07git commit -m 'fix'"))
	g.Mu.Lock()
	got := g.commandText()
	g.Mu.Unlock()
	if want := "git commit -m 'fix'"; got != want {
		t.Errorf("commandText() = %q; want %q", got, want)
	}
}

// Grapheme clusters live in the intern pool rather than in cell.Ch, so the
// scrape has to go through the pool the way SelectedText does.
func TestCommandText_Clusters(t *testing.T) {
	g := newGrid(4, 80)
	p := newParser(g)
	feed(t, g, p, []byte("$ \x1b]133;B\x07echo 👨‍👩‍👧‍👦 done"))
	g.Mu.Lock()
	got := g.commandText()
	g.Mu.Unlock()
	if want := "echo 👨‍👩‍👧‍👦 done"; got != want {
		t.Errorf("commandText() = %q; want %q", got, want)
	}
}

func TestCommandText_EmptyCommandLine(t *testing.T) {
	g := newGrid(4, 80)
	p := newParser(g)
	feed(t, g, p, []byte("$ \x1b]133;B\x07"))
	g.Mu.Lock()
	got := g.commandText()
	g.Mu.Unlock()
	if got != "" {
		t.Errorf("commandText() = %q; want empty", got)
	}
}

func TestCommandText_NoMarks(t *testing.T) {
	g := newGrid(4, 80)
	g.Mu.Lock()
	got := g.commandText()
	g.Mu.Unlock()
	if got != "" {
		t.Errorf("commandText() = %q; want empty", got)
	}
}

// The recorded column has to survive scrollback eviction along with the row,
// or the scrape reads from the wrong offset once history starts trimming.
func TestMarkCol_SurvivesTrim(t *testing.T) {
	g := newGrid(4, 80)
	p := newParser(g)
	g.ScrollbackCap = 8
	// Filler ahead of the command so the eviction has something older than
	// the mark to take: trimming the mark itself would leave nothing to
	// assert about, which is the trap the first version of this test fell in.
	for range 6 {
		feed(t, g, p, []byte("filler\r\n"))
	}
	feed(t, g, p, []byte("$ \x1b]133;B\x07make\r\n"))
	rowBefore := markRow(t, g, markCommandStart)
	for range 6 {
		feed(t, g, p, []byte("output\r\n"))
	}

	g.Mu.Lock()
	defer g.Mu.Unlock()
	var found bool
	for _, m := range g.Marks {
		if m.Kind != markCommandStart {
			continue
		}
		found = true
		if m.Col != 2 {
			t.Errorf("mark.Col = %d after trim; want 2", m.Col)
		}
		if m.Row >= rowBefore {
			t.Errorf("mark.Row = %d after trim; want < %d (trim should have shifted it)",
				m.Row, rowBefore)
		}
	}
	if !found {
		t.Fatal("command-start mark was evicted; test cannot assert on it")
	}
}

// markRow returns the row of the first mark of the given kind.
func markRow(t *testing.T, g *grid, kind markKind) int {
	t.Helper()
	g.Mu.Lock()
	defer g.Mu.Unlock()
	for _, m := range g.Marks {
		if m.Kind == kind {
			return m.Row
		}
	}
	t.Fatalf("no mark of kind %v", kind)
	return 0
}

// A prompt ending exactly at the right margin leaves the cursor in the
// deferred-wrap state (CursorC == Cols), which must not be stored as a column
// one past the end of the row.
func TestMarkCol_DeferredWrap(t *testing.T) {
	g := newGrid(4, 10)
	p := newParser(g)
	feed(t, g, p, []byte("0123456789\x1b]133;B\x07"))
	g.Mu.Lock()
	defer g.Mu.Unlock()
	for _, m := range g.Marks {
		if m.Kind == markCommandStart && int(m.Col) >= g.Cols {
			t.Errorf("mark.Col = %d; want < Cols (%d)", m.Col, g.Cols)
		}
	}
}

// assertNotify waits for one notification and returns it, so the caller
// asserts on the parts it cares about. The mirror of assertNoNotify below.
func assertNotify(t *testing.T, fired chan [2]string) [2]string {
	t.Helper()
	select {
	case got := <-fired:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("no notification fired")
		return [2]string{}
	}
}

func assertNoNotify(t *testing.T, fired chan [2]string) {
	t.Helper()
	select {
	case got := <-fired:
		t.Fatalf("unexpected notification: %q / %q", got[0], got[1])
	case <-time.After(100 * time.Millisecond):
	}
}
