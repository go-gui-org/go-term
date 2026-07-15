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

// scrollbarClick handles a left button-down over the scrollbar. Returns
// false when the pointer is outside the (visible) thumb's hit region so the
// caller falls through to selection / mouse-report handling. Clicking the
// thumb begins a drag; clicking elsewhere in the track jumps the viewport so
// the thumb centers on the pointer, then also begins a drag. Main-thread only.
func (t *Term) scrollbarClick(e *gui.Event, w *gui.Window) bool {
	if !t.scrollbar.active || !finite(t.cellH) {
		return false
	}
	if !realNumber(e.MouseX) || !realNumber(e.MouseY) {
		return false
	}
	if e.MouseX < t.scrollbar.hitX0 {
		return false
	}

	// Grabbing the thumb takes over the viewport; cancel any coasting
	// momentum so it doesn't fight the drag.
	t.cancelMomentum()

	sb, rows, viewOffsetVal := func() (int, int, float32) {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		return t.grid.Scrollback.Len(), t.grid.Rows,
			float32(t.grid.ViewOffset) + t.grid.ViewSubPx/t.cellH
	}()
	viewH := t.scrollbar.viewH
	thumbY, thumbH := scrollbarGeometry(sb, rows, viewOffsetVal, viewH)

	if e.MouseY >= thumbY && e.MouseY < thumbY+thumbH {
		// On the thumb: start a drag, remembering the grab offset.
		t.scrollbar.grabDy = e.MouseY - thumbY
	} else {
		// In the track above/below the thumb: jump so the thumb centers on
		// the pointer, then drag from its center.
		t.scrollbar.grabDy = thumbH / 2
		t.jumpScrollbarTo(e.MouseY - t.scrollbar.grabDy)
	}
	t.scrollbar.dragging = true
	t.lockMouse(w)
	t.scheduleViewUpdate(w)
	return true
}

// scrollbarDrag repositions the viewport during a thumb drag so the thumb top
// tracks the pointer (minus the grab offset). Main-thread only.
func (t *Term) scrollbarDrag(e *gui.Event, w *gui.Window) {
	if !finite(t.cellH) || !realNumber(e.MouseY) {
		return
	}
	t.jumpScrollbarTo(e.MouseY - t.scrollbar.grabDy)
	t.scheduleViewUpdate(w)
}

// jumpScrollbarTo positions the viewport so the thumb top lands at pixel
// thumbTopY. Caller ensures cellH is finite. Takes grid.Mu.
func (t *Term) jumpScrollbarTo(thumbTopY float32) {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	off := scrollbarOffsetForY(t.grid.Scrollback.Len(), t.grid.Rows, thumbTopY, t.scrollbar.viewH)
	t.grid.ClearSelection()
	t.grid.SetViewFractional(off, t.cellH)
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
