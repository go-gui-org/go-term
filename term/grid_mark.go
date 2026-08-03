package term

// markKind classifies an OSC 133 semantic shell-integration mark.
type markKind uint8

// mark records a command-boundary position in content coordinates.
// Row is a content row index (scrollback + live), stable across ViewOffset
// changes. Adjusted when scrollback is trimmed or the grid is resized.
type mark struct {
	Row  int
	Kind markKind
	// Exit is the command's exit status, valid only when Kind is
	// markCommandEnd. markExitUnknown means the shell reported no status.
	// The field is free: Row+Kind already pad to 16 bytes on 64-bit, and
	// an int16 at offset 10 keeps the struct that size.
	Exit int16
	// Col is the cursor column the mark arrived at. Only meaningful for
	// markCommandStart, where it is where the prompt ended and the typed
	// command begins — commandText reads the row from here so the
	// notification body is the command rather than the prompt plus the
	// command. Also free: it sits in the padding Exit left behind, so the
	// struct is still 16 bytes.
	//
	// MaxGridDim (1024) bounds it, so int16 cannot overflow. Reflow does not
	// adjust it (remapMarks rewrites rows only), so a resize mid-command can
	// leave it pointing at the wrong column; the cost is a cosmetically wrong
	// notification body, never a wrong span.
	Col int16
}

// markExitUnknown marks a command end whose exit status was absent or
// unparsable. Deliberately distinct from 0 so a shell that omits the status
// never reads as success.
const markExitUnknown int16 = -1

// AddMark records an OSC 133 command boundary at the cursor's current
// content row. Caller holds Mu. Marks in the alt screen are not recorded
// (full-screen apps like vim/htop don't emit OSC 133).
func (g *grid) AddMark(kind markKind) { g.addMark(kind, markExitUnknown) }

// AddCommandEnd records an OSC 133 D boundary carrying the command's exit
// status. Caller holds Mu.
func (g *grid) AddCommandEnd(exit int16) { g.addMark(markCommandEnd, exit) }

func (g *grid) addMark(kind markKind, exit int16) {
	if g.AltActive {
		return
	}
	row := g.Scrollback.Len() + g.CursorR
	// settledCol collapses the deferred-wrap encoding (CursorC == Cols), which
	// a prompt ending exactly at the right margin would otherwise store as a
	// column one past the end of the row.
	g.Marks = append(g.Marks, mark{
		Row: row, Kind: kind, Exit: exit, Col: int16(g.settledCol()),
	})
	if len(g.Marks) > maxMarks {
		g.Marks = g.Marks[len(g.Marks)-maxMarks:]
	}
	g.marksVer++
}

// PrevMark returns the content row of the last mark of kind strictly
// before row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *grid) PrevMark(row int, kind markKind) (int, bool) {
	for i := len(g.Marks) - 1; i >= 0; i-- {
		if g.Marks[i].Kind == kind && g.Marks[i].Row < row {
			return g.Marks[i].Row, true
		}
	}
	return 0, false
}

// NextMark returns the content row of the first mark of kind strictly
// after row, and true. Returns 0, false when no such mark exists.
// Caller holds Mu.
func (g *grid) NextMark(row int, kind markKind) (int, bool) {
	for _, m := range g.Marks {
		if m.Kind == kind && m.Row > row {
			return m.Row, true
		}
	}
	return 0, false
}

// trimMarks removes extra rows from the front of all mark row indices and
// drops marks that fall below 0. Called after scrollback is trimmed.
// Caller holds Mu.
func (g *grid) trimMarks(extra int) {
	// Called on every scrollback eviction, so bail before bumping marksVer
	// when there is nothing to trim — otherwise ordinary scrolling output
	// invalidates the failure-row cache on every frame for no reason.
	if len(g.Marks) == 0 {
		return
	}
	j := 0
	for _, m := range g.Marks {
		m.Row -= extra
		if m.Row >= 0 {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
	g.marksVer++
}

// shiftMarks applies delta to all mark row indices, dropping marks that
// fall outside [0, total). Only correct when no re-wrap happened, i.e. rows
// kept their identity — a logical line that re-wraps to a different physical
// row count invalidates a flat delta. The reflow path uses remapMarks; this
// remains for the alt-screen path where no reflow runs. Caller holds Mu.
func (g *grid) shiftMarks(delta, total int) {
	if len(g.Marks) == 0 {
		return
	}
	j := 0
	for _, m := range g.Marks {
		m.Row += delta
		if m.Row >= 0 && m.Row < total {
			g.Marks[j] = m
			j++
		}
	}
	g.Marks = g.Marks[:j]
	g.marksVer++
}

// remapMarks rewrites mark rows from the mapping logicalReflow produced:
// rows[i] is the new content row of Marks[i], -1 meaning the re-wrap
// discarded it. A short slice defensively drops the unmapped tail rather
// than leaving stale rows behind. Caller holds Mu.
func (g *grid) remapMarks(rows []int) {
	if len(g.Marks) == 0 {
		return
	}
	j := 0
	for i, m := range g.Marks {
		if i >= len(rows) || rows[i] < 0 {
			continue
		}
		m.Row = rows[i]
		g.Marks[j] = m
		j++
	}
	g.Marks = g.Marks[:j]
	g.marksVer++
}
