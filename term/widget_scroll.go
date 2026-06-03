package term

import (
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// scheduleViewUpdate signals that the viewport has changed: shows the
// scrollbar thumb, bumps the tessellation-cache version, and triggers an
// immediate window repaint. Call this on the main thread after any
// operation that changes the visible cell range or selection.
func (t *Term) scheduleViewUpdate(w *gui.Window) {
	t.showScrollbar()
	t.bumpVersion()
	if w != nil {
		w.UpdateWindow()
	}
}

// showScrollbar arms the auto-hide timer for the scrollbar thumb. Call on
// the main thread whenever the viewport scrolls. Uses a single debounced
// timer so rapid scroll events don't accumulate goroutines.
func (t *Term) showScrollbar() {
	t.scrollbar.until = time.Now().Add(scrollbarDuration)
	t.scheduleDelayedUpdate(scrollbarDuration+time.Millisecond, &t.scrollbar.timer)
}

// snapToLive clears any scrollback view-offset and selection so subsequent
// input is rendered at the live grid. No-op when already at the bottom.
func (t *Term) snapToLive() {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	t.grid.ClearSelection()
	if t.grid.ViewOffset != 0 || t.grid.ViewSubPx != 0 {
		t.grid.ResetView()
	}
}

// scrollByPage moves the viewport one page (rows-1) in `dir` direction.
func (t *Term) scrollByPage(dir int, w *gui.Window) {
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		step := max(t.grid.Rows-1, 1)
		t.grid.ScrollView(dir * step)
	}()
	t.scheduleViewUpdate(w)
}

// scrollToTop pins the viewport at the oldest scrollback row.
func (t *Term) scrollToTop(w *gui.Window) {
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		t.grid.ScrollViewTop()
	}()
	t.scheduleViewUpdate(w)
}

// scrollToBottom snaps the viewport back to the live grid.
func (t *Term) scrollToBottom(w *gui.Window) {
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		t.grid.ResetView()
	}()
	t.scheduleViewUpdate(w)
}

// jumpToMark scrolls the viewport to the previous (backward=true) or next
// (backward=false) markPromptStart mark. No-op when no marks exist or no
// mark is found in that direction. Suppressed while the alt screen is active.
func (t *Term) jumpToMark(backward bool, w *gui.Window) {
	var found bool
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		if t.grid.AltActive {
			return
		}
		sb := t.grid.Scrollback.Len()
		off := clamp(t.grid.ViewOffset, 0, sb)
		viewTop := sb - off
		var row int
		if backward {
			row, found = t.grid.PrevMark(viewTop, markPromptStart)
		} else {
			row, found = t.grid.NextMark(viewTop, markPromptStart)
		}
		if found {
			if row >= sb {
				t.grid.ViewOffset = 0
			} else {
				t.grid.ViewOffset = sb - row
			}
			t.grid.ViewSubPx = 0
		}
	}()
	if found {
		t.scheduleViewUpdate(w)
	}
}

// searchJump finds the next (forward=true) or previous (forward=false) match
// for the current search query and scrolls the viewport to show it.
func (t *Term) searchJump(forward bool, w *gui.Window) {
	if t.search.query == "" {
		return
	}
	ok := func() bool {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		sb := g.Scrollback.Len()
		var start contentPos
		if len(t.search.matches) > 0 && t.search.idx < len(t.search.matches) {
			start = t.search.matches[t.search.idx].contentPos
		} else {
			start = contentPos{Row: sb - clamp(g.ViewOffset, 0, sb)}
		}
		var (
			pos contentPos
			ok  bool
		)
		if t.search.regex && t.search.re != nil {
			pos, _, ok = g.FindRegex(t.search.re, start, forward)
		} else if !t.search.regex {
			pos, ok = g.Find(t.search.query, start, forward)
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
		return ok
	}()
	if ok {
		t.scheduleViewUpdate(w)
	}
}
