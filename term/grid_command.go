package term

import "strings"

// Command regions derived from the OSC 133 mark stream. Nothing here is
// stored: a commandSpan is reconstructed on demand from grid.Marks, so the
// marks remain the single source of truth and reflow/trim keep working
// unchanged. The only cached artifact is failRows, keyed on marksVer.

// maxFailRows caps the cached failure list. The scrollbar can only show a
// few hundred distinguishable ticks, and the cap bounds the per-frame loop
// independently of maxMarks.
const maxFailRows = 512

// commandSpan is one OSC 133 command reconstructed from the mark stream.
// All rows are content rows; -1 means the shell never emitted that mark.
type commandSpan struct {
	Prompt   int // A, falling back to B then C
	Cmd      int // B
	OutStart int // C, or Cmd+1 when the shell never emitted C
	OutEnd   int // inclusive last output row
	Exit     int16
	Ended    bool // a D closed this span
}

// failed reports whether the command finished with a non-zero status. A
// command that ended without a reported status is not a failure — only an
// explicit non-zero exit counts, so shells that omit the status never paint
// the scrollback red.
func (s commandSpan) failed() bool {
	return s.Ended && s.Exit != markExitUnknown && s.Exit != 0
}

// hasOutput reports whether the span covers at least one output row.
func (s commandSpan) hasOutput() bool {
	return s.OutStart >= 0 && s.OutEnd >= s.OutStart
}

// start is the first row of the span's region: its prompt, or the first
// output row when the shell emitted neither A nor B. -1 when it has neither,
// which is a span the row queries below cannot place and must skip.
func (s commandSpan) start() int {
	if s.Prompt >= 0 {
		return s.Prompt
	}
	return s.OutStart
}

// forEachCommand walks the mark stream in order, assembling spans and
// calling fn for each, including the trailing still-running command.
// Returning false from fn stops the walk. Caller holds Mu.
func (g *grid) forEachCommand(fn func(commandSpan) bool) {
	g.walkCommands(true, fn)
}

// walkCommands is forEachCommand with control over the trailing open span.
// Resolving that span's end means scanning up from the bottom of the content
// for the last non-blank row, so callers that only look at *finished*
// commands pass false and skip the scan entirely. That matters because a
// mark stranded above a long run of blank rows makes the scan proportional
// to the scrollback depth, and the failure queries run once per frame.
//
// Real shells emit incomplete sequences constantly — a prompt redraw, an
// interrupted command, integration scripts that skip A or C — so the
// grouping rules below are all about degrading sensibly rather than
// assuming a clean A/B/C/D cycle. Caller holds Mu.
func (g *grid) walkCommands(trailing bool, fn func(commandSpan) bool) {
	var cur commandSpan
	open := false
	maxRow := 0 // highest row seen in the open span, for regression detection

	// reset starts a fresh span with every field cleared to "not emitted".
	reset := func(row int) {
		cur = commandSpan{Prompt: -1, Cmd: -1, OutStart: -1, OutEnd: -1, Exit: markExitUnknown}
		open = true
		maxRow = row
	}

	// emit closes the open span at outEnd and hands it to fn.
	emit := func(outEnd int) bool {
		if !open {
			return true
		}
		open = false
		// A span with no C falls back to the row after the command line;
		// when that lands past outEnd the span simply has no output, which
		// is the correct reading of a D that arrived with nothing between.
		if cur.OutStart < 0 {
			switch {
			case cur.Cmd >= 0:
				cur.OutStart = cur.Cmd + 1
			case cur.Prompt >= 0:
				cur.OutStart = cur.Prompt + 1
			}
		}
		cur.OutEnd = outEnd
		return fn(cur)
	}

	for _, m := range g.Marks {
		// A row that regresses below the open span belongs to a redrawn
		// prompt (cursor-up, ED); the old span can never be completed, so
		// close it before starting over.
		if open && m.Row < maxRow {
			if !emit(maxRow) {
				return
			}
		}
		if m.Row > maxRow {
			maxRow = m.Row
		}

		switch m.Kind {
		case markPromptStart:
			// An A with a span still open means the previous command was
			// interrupted; close it just above this prompt.
			if open {
				if !emit(m.Row - 1) {
					return
				}
			}
			reset(m.Row)
			cur.Prompt = m.Row

		case markCommandStart:
			if !open {
				reset(m.Row) // shell skipped A
			}
			if cur.Cmd < 0 { // ignore repeats from prompt redraws
				cur.Cmd = m.Row
			}
			if cur.Prompt < 0 {
				cur.Prompt = m.Row
			}

		case markOutputStart:
			if !open {
				reset(m.Row) // shell skipped A and B
			}
			if cur.OutStart < 0 {
				cur.OutStart = m.Row
			}
			if cur.Prompt < 0 {
				cur.Prompt = m.Row
			}

		case markCommandEnd:
			if !open {
				// A D with nothing open carries a status but no region.
				reset(m.Row)
				cur.Prompt = m.Row
			}
			cur.Exit = m.Exit
			cur.Ended = true
			if !emit(m.Row - 1) {
				return
			}
		}
	}

	// The trailing span is still open: its output runs to the last row it
	// has actually written. Taking the bottom of the content instead would
	// hand the blank rows below a fresh prompt to whatever is selecting the
	// span's output — the common case, since a prompt sits at the bottom of
	// the text but rarely at the bottom of the screen.
	if open && trailing {
		emit(g.lastWrittenRow(maxRow, g.ContentRows()-1))
	}
}

// maxBlankScanRows bounds the upward scan in lastWrittenRow. The scan stops
// at the first non-blank row, so it only ever runs long over a *blank*
// stretch — which a hostile stream can manufacture by parking a mark above
// thousands of empty rows and then making the walk repeat. Giving up past
// the cap costs nothing real: no command's output is thousands of blank rows.
const maxBlankScanRows = 2048

// lastWrittenRow returns the last row in [from, to] holding any non-blank
// cell, or from-1 when every row is blank. Rows before `from` are never
// examined: the caller already knows content exists there. Caller holds Mu.
func (g *grid) lastWrittenRow(from, to int) int {
	if from < 0 {
		from = 0
	}
	top := min(to, g.ContentRows()-1)
	// Floor the scan, but still answer "no output" rather than "everything
	// down to the floor is output" — the unscanned rows are unknown, and
	// claiming a span of blanks would hand them to the output selection.
	floor := max(from, top-maxBlankScanRows)
	for r := top; r >= floor; r-- {
		for c := range g.Cols {
			if !isDefaultBlank(g.ContentCellAt(r, c)) {
				return r
			}
		}
	}
	return from - 1
}

// commandText returns the command line of the most recently started command:
// the text of the markCommandStart row from the column that mark recorded (so
// the prompt is excluded) to the end of the row. Returns "" when no command
// has started.
//
// Only the first row is read. A command continued across a line continuation
// or wrapped past the right margin is therefore truncated, which is the right
// trade for the one caller — a notification body, where a single line is all
// that fits anyway. Caller holds Mu.
func (g *grid) commandText() string {
	for i := len(g.Marks) - 1; i >= 0; i-- {
		if g.Marks[i].Kind != markCommandStart {
			continue
		}
		return g.rowTextFrom(g.Marks[i].Row, int(g.Marks[i].Col))
	}
	return ""
}

// rowTextFrom extracts content row text from col to the end of the row,
// trimming trailing blanks. Mirrors the cell decoding in SelectedText —
// interned grapheme clusters expand, Kitty Unicode placeholders count as
// blank — without the selection geometry. Caller holds Mu.
func (g *grid) rowTextFrom(row, col int) string {
	if row < 0 || col < 0 || col >= g.Cols {
		return ""
	}
	// Find the last non-blank cell first so the builder is sized once and no
	// trailing run of spaces is emitted.
	end := col - 1
	for c := col; c < g.Cols; c++ {
		cell := g.ContentCellAt(row, c)
		if cell.Ch != ' ' && cell.Ch != 0 && !isPlaceholderCell(cell) {
			end = c
		}
	}
	if end < col {
		return ""
	}
	var b strings.Builder
	b.Grow(end - col + 1)
	for c := col; c <= end; c++ {
		cell := g.ContentCellAt(row, c)
		switch {
		case isPlaceholderCell(cell):
			b.WriteByte(' ')
		case cell.clusterID != 0 && int(cell.clusterID) < len(g.clusters):
			b.WriteString(g.clusters[cell.clusterID])
		case cell.Ch != 0:
			b.WriteRune(cell.Ch)
		}
	}
	return b.String()
}

// commandAt returns the span whose region contains row — from its prompt
// through the last row of its output. Caller holds Mu.
func (g *grid) commandAt(row int) (commandSpan, bool) {
	var found commandSpan
	ok := false
	g.forEachCommand(func(s commandSpan) bool {
		start := s.start()
		if start >= 0 && row >= start && row <= s.OutEnd {
			found, ok = s, true
			return false
		}
		// Spans are ordered, so once one starts past row there is no match.
		return start < 0 || start <= row
	})
	return found, ok
}

// outputBefore returns the last span that starts strictly before row and
// actually has output. Used when the reference row lands on a fresh prompt
// whose command has not run — the interesting output is the previous
// command's. Empty spans are skipped rather than ending the walk: the newest
// prompt nearly always has none, and stopping there would make the fallback
// find nothing precisely when it is needed. Caller holds Mu.
func (g *grid) outputBefore(row int) (commandSpan, bool) {
	var found commandSpan
	ok := false
	g.forEachCommand(func(s commandSpan) bool {
		start := s.start()
		if start >= 0 && start < row && s.hasOutput() {
			found, ok = s, true
		}
		return start < 0 || start < row
	})
	return found, ok
}

// lastFailedBefore returns the newest failed command starting strictly
// before row, wrapping to the newest failure overall when there is none —
// so repeated presses of the jump chord walk back through failures and then
// start again rather than sticking at the oldest. Caller holds Mu.
func (g *grid) lastFailedBefore(row int) (commandSpan, bool) {
	var found, newest commandSpan
	ok, any := false, false
	// A failure is always a *finished* command, so the trailing open span
	// can never match — skip resolving its end.
	g.walkCommands(false, func(s commandSpan) bool {
		if !s.failed() {
			return true
		}
		newest, any = s, true
		if s.Prompt >= 0 && s.Prompt < row {
			found, ok = s, true
		}
		return true
	})
	if ok {
		return found, true
	}
	return newest, any
}

// failureRows returns the prompt rows of failed commands, oldest first.
// The result is cached against marksVer and the backing array is reused, so
// the draw path costs a version comparison and allocates nothing on a
// repeat call. The returned slice aliases grid state — read it under Mu and
// do not retain it. Caller holds Mu.
func (g *grid) failureRows() []int {
	if g.failRowsValid && g.failRowsVer == g.marksVer {
		return g.failRows
	}
	g.failRows = g.failRows[:0]
	// Finished commands only (see lastFailedBefore) — this runs on the draw
	// path, so it must not depend on a scan of the content.
	g.walkCommands(false, func(s commandSpan) bool {
		if s.failed() && s.Prompt >= 0 {
			g.failRows = append(g.failRows, s.Prompt)
		}
		return true
	})
	if len(g.failRows) > maxFailRows {
		// Keep the newest: those are the ones a user is looking for.
		g.failRows = append(g.failRows[:0], g.failRows[len(g.failRows)-maxFailRows:]...)
	}
	g.failRowsVer = g.marksVer
	g.failRowsValid = true
	return g.failRows
}
