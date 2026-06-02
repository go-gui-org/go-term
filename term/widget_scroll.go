package term

import (
	"time"

	"github.com/mike-ward/go-gui/gui"
)

// showScrollbar arms the auto-hide timer for the scrollbar thumb. Call on
// the main thread whenever the viewport scrolls. Uses a single debounced
// timer so rapid scroll events don't accumulate goroutines.
func (t *Term) showScrollbar() {
	t.scrollbar.until = time.Now().Add(scrollbarDuration)
	if t.scrollbar.timer == nil {
		t.scrollbar.timer = time.AfterFunc(scrollbarDuration+time.Millisecond, func() {
			if !t.closed.Load() && t.cmd != nil {
				t.bumpVersion()
				t.cmd.QueueCommand(func(w *gui.Window) { w.UpdateWindow() })
			}
		})
	} else {
		t.scrollbar.timer.Reset(scrollbarDuration + time.Millisecond)
	}
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
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToTop pins the viewport at the oldest scrollback row.
func (t *Term) scrollToTop(w *gui.Window) {
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		t.grid.ScrollViewTop()
	}()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
}

// scrollToBottom snaps the viewport back to the live grid.
func (t *Term) scrollToBottom(w *gui.Window) {
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		t.grid.ResetView()
	}()
	t.showScrollbar()
	t.bumpVersion()
	w.UpdateWindow()
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
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
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
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
	}
}
