package term

import (
	"time"

	"github.com/mike-ward/go-gui/gui"
)

// linesFromScroll converts a wheel/trackpad pixel delta into a row
// count using cellH. Returns 0 for unusable inputs (non-finite cellH,
// non-real scrollY, or no movement). Sub-cell deltas round up to a
// single line in their direction so trackpad nudges aren't lost.
func linesFromScroll(scrollY, cellH float32) int {
	if !finite(cellH) {
		return 0
	}
	if !realNumber(scrollY) {
		return 0
	}
	lines := int(scrollY / cellH)
	if lines != 0 {
		return lines
	}
	switch {
	case scrollY > 0:
		return 1
	case scrollY < 0:
		return -1
	}
	return 0
}

// showScrollbar arms the auto-hide timer for the scrollbar thumb. Call on
// the main thread whenever the viewport scrolls. Uses a single debounced
// timer so rapid scroll events don't accumulate goroutines.
func (t *Term) showScrollbar() {
	t.scrollbarUntil = time.Now().Add(scrollbarDuration)
	if t.scrollbarTimer == nil {
		t.scrollbarTimer = time.AfterFunc(scrollbarDuration+time.Millisecond, func() {
			if !t.closed.Load() {
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
			}
		})
	} else {
		t.scrollbarTimer.Reset(scrollbarDuration + time.Millisecond)
	}
}

// snapToLive clears any scrollback view-offset so subsequent input is
// rendered at the live grid. No-op when already at the bottom.
func (t *Term) snapToLive() {
	t.grid.Mu.Lock()
	if t.grid.ViewOffset != 0 || t.grid.ViewSubPx != 0 {
		t.grid.ResetView()
	}
	t.grid.Mu.Unlock()
}

// scrollByPage moves the viewport one page (rows-1) in `dir` direction.
func (t *Term) scrollByPage(dir int, w *gui.Window) {
	t.grid.Mu.Lock()
	step := t.grid.Rows - 1
	if step < 1 {
		step = 1
	}
	t.grid.ScrollView(dir * step)
	t.grid.Mu.Unlock()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToTop pins the viewport at the oldest scrollback row.
func (t *Term) scrollToTop(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ScrollViewTop()
	t.grid.Mu.Unlock()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToBottom snaps the viewport back to the live grid.
func (t *Term) scrollToBottom(w *gui.Window) {
	t.grid.Mu.Lock()
	t.grid.ResetView()
	t.grid.Mu.Unlock()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// jumpToMark scrolls the viewport to the previous (backward=true) or next
// (backward=false) MarkPromptStart mark. No-op when no marks exist or no
// mark is found in that direction. Suppressed while the alt screen is active.
func (t *Term) jumpToMark(backward bool, w *gui.Window) {
	t.grid.Mu.Lock()
	if t.grid.AltActive {
		t.grid.Mu.Unlock()
		return
	}
	sb := t.grid.Scrollback.Len()
	off := clamp(t.grid.ViewOffset, 0, sb)
	viewTop := sb - off
	var row int
	var ok bool
	if backward {
		row, ok = t.grid.PrevMark(viewTop, MarkPromptStart)
	} else {
		row, ok = t.grid.NextMark(viewTop, MarkPromptStart)
	}
	if ok {
		if row >= sb {
			t.grid.ViewOffset = 0
		} else {
			t.grid.ViewOffset = sb - row
		}
		t.grid.ViewSubPx = 0
	}
	t.grid.Mu.Unlock()
	if ok {
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
	}
}

// searchJump finds the next (forward=true) or previous (forward=false) match
// for the current search query and scrolls the viewport to show it.
func (t *Term) searchJump(forward bool, w *gui.Window) {
	if t.searchQuery == "" {
		return
	}
	g := t.grid
	g.Mu.Lock()
	sb := g.Scrollback.Len()
	var start ContentPos
	if len(t.searchMatches) > 0 && t.searchIdx < len(t.searchMatches) {
		start = t.searchMatches[t.searchIdx].ContentPos
	} else {
		start = ContentPos{Row: sb - clamp(g.ViewOffset, 0, sb)}
	}
	var (
		pos ContentPos
		ok  bool
	)
	if t.searchRegex && t.searchRE != nil {
		pos, _, ok = g.FindRegex(t.searchRE, start, forward)
	} else if !t.searchRegex {
		pos, ok = g.Find(t.searchQuery, start, forward)
	}
	if ok {
		liveRow := pos.Row - sb
		if liveRow >= 0 {
			g.ViewOffset = 0
		} else {
			g.ViewOffset = clamp(sb-pos.Row, 0, sb)
		}
		g.ViewSubPx = 0
	}
	g.Mu.Unlock()
	if ok {
		w.UpdateWindow()
	}
}
