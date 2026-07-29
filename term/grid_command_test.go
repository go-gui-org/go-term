package term

import "testing"

// markStream builds a grid whose mark list is exactly the given marks, with
// enough content rows for the trailing open span to have somewhere to end.
func markStream(rows int, marks ...mark) *grid {
	g := newGrid(rows, 20)
	g.Marks = append(g.Marks, marks...)
	g.marksVer++
	return g
}

func end(row int, exit int16) mark {
	return mark{Row: row, Kind: markCommandEnd, Exit: exit}
}

func spansOf(g *grid) []commandSpan {
	var out []commandSpan
	g.forEachCommand(func(s commandSpan) bool {
		out = append(out, s)
		return true
	})
	return out
}

func TestForEachCommand_FullCycle(t *testing.T) {
	g := markStream(20,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 0, Kind: markCommandStart},
		mark{Row: 1, Kind: markOutputStart},
		end(5, 0),
	)
	spans := spansOf(g)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1: %+v", len(spans), spans)
	}
	s := spans[0]
	if s.Prompt != 0 || s.Cmd != 0 || s.OutStart != 1 || s.OutEnd != 4 {
		t.Errorf("span = %+v, want Prompt 0 Cmd 0 OutStart 1 OutEnd 4", s)
	}
	if !s.Ended || s.Exit != 0 || s.failed() {
		t.Errorf("span should be a clean success: %+v", s)
	}
	if !s.hasOutput() {
		t.Error("span should have output")
	}
}

func TestForEachCommand_MissingOutputStart(t *testing.T) {
	// Shell emitted A/B/D but never C: output starts after the command line.
	g := markStream(20,
		mark{Row: 2, Kind: markPromptStart},
		mark{Row: 2, Kind: markCommandStart},
		end(6, 1),
	)
	spans := spansOf(g)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	s := spans[0]
	if s.OutStart != 3 || s.OutEnd != 5 {
		t.Errorf("span = %+v, want OutStart 3 OutEnd 5", s)
	}
	if !s.failed() {
		t.Errorf("exit 1 should read as failed: %+v", s)
	}
}

func TestForEachCommand_EndWithNoOutputRows(t *testing.T) {
	// D arrives immediately after B: OutStart would exceed OutEnd, which
	// must degrade to "no output" rather than an inverted range.
	g := markStream(20,
		mark{Row: 4, Kind: markPromptStart},
		mark{Row: 4, Kind: markCommandStart},
		end(5, 0),
	)
	s := spansOf(g)[0]
	if s.hasOutput() {
		t.Errorf("span should have no output: %+v", s)
	}
	if s.OutStart <= s.OutEnd {
		t.Errorf("expected inverted/empty range, got %+v", s)
	}
}

func TestForEachCommand_MissingPromptStart(t *testing.T) {
	// Integration script skipped A entirely; B opens the span and doubles
	// as the prompt row.
	g := markStream(20,
		mark{Row: 3, Kind: markCommandStart},
		mark{Row: 4, Kind: markOutputStart},
		end(8, 2),
	)
	s := spansOf(g)[0]
	if s.Prompt != 3 || s.Cmd != 3 || s.OutStart != 4 {
		t.Errorf("span = %+v, want Prompt 3 Cmd 3 OutStart 4", s)
	}
}

func TestForEachCommand_InterruptedCommandClosedByNextPrompt(t *testing.T) {
	// Ctrl+C: no D ever arrives, and the next A must close the span.
	g := markStream(20,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		mark{Row: 7, Kind: markPromptStart},
		end(9, 0),
	)
	spans := spansOf(g)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	if spans[0].Ended {
		t.Errorf("interrupted span should not be marked ended: %+v", spans[0])
	}
	if spans[0].OutEnd != 6 {
		t.Errorf("interrupted span OutEnd = %d, want 6", spans[0].OutEnd)
	}
}

// A still-running command's output ends at the last row it has written, not
// at the bottom of the screen: the blank rows below a prompt are nobody's
// output, and handing them to the selection would highlight empty space.
func TestForEachCommand_TrailingOpenSpanStopsAtWrittenText(t *testing.T) {
	g := markStream(10,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
	)
	writeLine(g, "$ tail -f log")
	writeLine(g, "one")
	writeLine(g, "two") // last written row is 2; rows 3..9 stay blank

	s := spansOf(g)[0]
	if s.Ended {
		t.Error("running command should not be marked ended")
	}
	if s.OutEnd != 2 {
		t.Errorf("OutEnd = %d, want 2 (last written row)", s.OutEnd)
	}
}

// A fresh prompt with nothing under it has no output at all, so the
// output-selection fallback skips past it instead of grabbing blank rows.
func TestForEachCommand_FreshPromptHasNoOutput(t *testing.T) {
	g := markStream(10,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markCommandStart},
	)
	writeLine(g, "$ ")

	if s := spansOf(g)[0]; s.hasOutput() {
		t.Errorf("fresh prompt reports output rows %d..%d", s.OutStart, s.OutEnd)
	}
}

func TestForEachCommand_RegressingRowClosesSpan(t *testing.T) {
	// A prompt redraw moves the cursor back up; the earlier span can never
	// complete and must not swallow the new one.
	g := markStream(20,
		mark{Row: 5, Kind: markPromptStart},
		mark{Row: 6, Kind: markOutputStart},
		mark{Row: 2, Kind: markPromptStart},
		end(4, 1),
	)
	spans := spansOf(g)
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2: %+v", len(spans), spans)
	}
	if !spans[1].failed() || spans[1].Prompt != 2 {
		t.Errorf("second span = %+v, want failed at prompt 2", spans[1])
	}
}

func TestCommandAt(t *testing.T) {
	g := markStream(30,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		end(5, 0),
		mark{Row: 6, Kind: markPromptStart},
		mark{Row: 7, Kind: markOutputStart},
		end(12, 1),
	)
	cases := []struct {
		row        int
		wantPrompt int
		wantOK     bool
	}{
		{0, 0, true},   // on the prompt row
		{3, 0, true},   // inside the first output
		{6, 6, true},   // on the second prompt
		{11, 6, true},  // inside the second output
		{25, 0, false}, // past every span
	}
	for _, c := range cases {
		s, ok := g.commandAt(c.row)
		if ok != c.wantOK {
			t.Errorf("commandAt(%d) ok = %v, want %v", c.row, ok, c.wantOK)
			continue
		}
		if ok && s.Prompt != c.wantPrompt {
			t.Errorf("commandAt(%d) prompt = %d, want %d", c.row, s.Prompt, c.wantPrompt)
		}
	}
}

func TestOutputBefore(t *testing.T) {
	g := markStream(30,
		mark{Row: 0, Kind: markPromptStart},
		mark{Row: 1, Kind: markOutputStart},
		end(5, 0),
		mark{Row: 6, Kind: markPromptStart},
	)
	// The cursor sits on the fresh prompt at row 6; the interesting output
	// belongs to the command before it.
	s, ok := g.outputBefore(6)
	if !ok || s.Prompt != 0 {
		t.Fatalf("outputBefore(6) = %+v, %v; want prompt 0", s, ok)
	}
	// Searching from past the end must skip the trailing empty prompt span
	// rather than stopping on it — that is the live-view case.
	s, ok = g.outputBefore(g.ContentRows())
	if !ok || s.Prompt != 0 {
		t.Fatalf("outputBefore(end) = %+v, %v; want prompt 0", s, ok)
	}
	if _, ok := g.outputBefore(0); ok {
		t.Error("outputBefore(0) should find nothing")
	}
}

func TestLastFailedBefore_WrapsToNewest(t *testing.T) {
	g := markStream(40,
		mark{Row: 0, Kind: markPromptStart},
		end(3, 1), // failure A
		mark{Row: 4, Kind: markPromptStart},
		end(7, 0), // success
		mark{Row: 8, Kind: markPromptStart},
		end(11, 2), // failure B
	)
	if s, ok := g.lastFailedBefore(40); !ok || s.Prompt != 8 {
		t.Errorf("from bottom: got %+v, %v; want prompt 8", s, ok)
	}
	if s, ok := g.lastFailedBefore(8); !ok || s.Prompt != 0 {
		t.Errorf("above failure B: got %+v, %v; want prompt 0", s, ok)
	}
	// Nothing failed above row 0, so it wraps to the newest failure.
	if s, ok := g.lastFailedBefore(0); !ok || s.Prompt != 8 {
		t.Errorf("wrap: got %+v, %v; want prompt 8", s, ok)
	}
}

func TestLastFailedBefore_NoFailures(t *testing.T) {
	g := markStream(20,
		mark{Row: 0, Kind: markPromptStart},
		end(3, 0),
		mark{Row: 4, Kind: markPromptStart},
		end(7, markExitUnknown), // ended, but no status reported
	)
	if _, ok := g.lastFailedBefore(20); ok {
		t.Error("unknown exit status must not count as a failure")
	}
}

func TestFailureRows_CachedAndInvalidated(t *testing.T) {
	g := markStream(40,
		mark{Row: 0, Kind: markPromptStart},
		end(3, 1),
		mark{Row: 4, Kind: markPromptStart},
		end(7, 0),
	)
	first := g.failureRows()
	if len(first) != 1 || first[0] != 0 {
		t.Fatalf("failureRows = %v, want [0]", first)
	}
	// A repeat call must not rebuild: same backing array, same contents.
	second := g.failureRows()
	if &first[0] != &second[0] {
		t.Error("cached call reallocated the slice")
	}

	// A new failing command invalidates the cache.
	g.MoveCursor(9, 0)
	g.AddMark(markPromptStart)
	g.AddCommandEnd(3)
	third := g.failureRows()
	if len(third) != 2 || third[1] != 9 {
		t.Errorf("after new failure: %v, want [0 9]", third)
	}
}

func TestFailureRows_Capped(t *testing.T) {
	g := newGrid(10, 20)
	for i := range maxFailRows + 50 {
		g.Marks = append(g.Marks,
			mark{Row: i * 2, Kind: markPromptStart},
			end(i*2+1, 1))
	}
	g.marksVer++
	rows := g.failureRows()
	if len(rows) != maxFailRows {
		t.Fatalf("len = %d, want %d", len(rows), maxFailRows)
	}
	// The cap keeps the newest failures, so the last entry is the last one added.
	if want := (maxFailRows + 49) * 2; rows[len(rows)-1] != want {
		t.Errorf("newest row = %d, want %d", rows[len(rows)-1], want)
	}
}

func TestOSCExitStatus(t *testing.T) {
	cases := []struct {
		pt   string
		want int16
	}{
		{"D", markExitUnknown},
		{"D;", markExitUnknown},
		{"D;0", 0},
		{"D;1", 1},
		{"D;130", 130},
		{"D;notanum", markExitUnknown},
		{"D;1;aid=17", 1},
		{"D;0;err=0", 0},
		{"D;99999", 32767},
		{"D;-1", 32767},
	}
	for _, c := range cases {
		if got := oscExitStatus(c.pt); got != c.want {
			t.Errorf("oscExitStatus(%q) = %d, want %d", c.pt, got, c.want)
		}
	}
}
