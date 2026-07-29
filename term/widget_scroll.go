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
			t.grid.scrollTopTo(row)
		}
	}()
	if found {
		t.scheduleViewUpdate(w)
	}
}

// jumpToFailure moves to the newest failed command above the reference row,
// wrapping to the newest failure overall when there is none above, so
// repeated presses walk back through failures instead of sticking at the
// oldest. In copy mode it moves the copy cursor (matching copyMarkJump);
// otherwise it scrolls the viewport. Suppressed while the alt screen is
// active, and a silent no-op when nothing has failed.
func (t *Term) jumpToFailure(w *gui.Window) {
	var found, copyMode bool
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		g := t.grid
		if g.AltActive {
			return
		}
		copyMode = t.copy.active
		span, ok := g.lastFailedBefore(t.markRefRow())
		if !ok || span.Prompt < 0 {
			return
		}
		found = true
		if copyMode {
			t.copy.cursor = contentPos{Row: span.Prompt}
			return
		}
		g.scrollTopTo(span.Prompt)
	}()
	if !found {
		return
	}
	if copyMode {
		t.revealCursor(w) // takes Mu itself
		return
	}
	t.scheduleViewUpdate(w)
}

// markRefRow is the content row that mark-relative navigation measures from:
// the copy cursor in copy mode, the viewport top when scrolled back, and the
// bottom of the content when sitting live — so a first press from the live
// view finds the newest match rather than nothing. Caller holds Mu.
func (t *Term) markRefRow() int {
	g := t.grid
	if t.copy.active {
		return t.copy.cursor.Row
	}
	sb := g.Scrollback.Len()
	if off := clamp(g.ViewOffset, 0, sb); off > 0 {
		return sb - off
	}
	return g.ContentRows()
}

// findMatch locates the next (forward=true) or previous (forward=false) match
// for the current search query, starting from the last jump target or, failing
// that, the viewport top. Returns the match position and whether one was found;
// it does not move anything, so both the search bar and copy mode can drive it.
// Takes Mu.
//
// The start position is derived inside the same critical section as the search
// itself: the reader goroutine can evict scrollback rows at any moment, and a
// start row computed under one lock and used under the next would search from
// a row index that has since shifted.
func (t *Term) findMatch(forward bool) (contentPos, bool) {
	if t.search.query == "" {
		return contentPos{}, false
	}
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
	return t.findMatchLocked(start, forward)
}

// findMatchFrom searches for the current query starting at the given content
// position. Copy-mode n/N use this so each press advances from the cursor
// instead of from stale search-bar match state, which is what happens when
// findMatch's t.search.idx is no longer updated after the bar closes. Takes Mu.
func (t *Term) findMatchFrom(start contentPos, forward bool) (contentPos, bool) {
	if t.search.query == "" {
		return contentPos{}, false
	}
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return t.findMatchLocked(start, forward)
}

// findMatchLocked is the shared search core: it picks the regex or literal
// matcher according to the search bar's mode. Caller holds Mu and has already
// checked that the query is non-empty.
func (t *Term) findMatchLocked(start contentPos, forward bool) (contentPos, bool) {
	g := t.grid
	if t.search.regex {
		if t.search.re == nil {
			return contentPos{}, false
		}
		pos, _, ok := g.FindRegex(t.search.re, start, forward)
		return pos, ok
	}
	pos, ok := g.Find(t.search.query, start, forward)
	return pos, ok
}

// searchJump finds the next (forward=true) or previous (forward=false) match
// for the current search query and scrolls the viewport to show it.
func (t *Term) searchJump(forward bool, w *gui.Window) {
	pos, ok := t.findMatch(forward)
	if !ok {
		return
	}
	func() {
		g := t.grid
		g.Mu.Lock()
		defer g.Mu.Unlock()
		sb := g.Scrollback.Len()
		if pos.Row-sb >= 0 {
			g.ViewOffset = 0
		} else {
			g.ViewOffset = clamp(sb-pos.Row, 0, sb)
		}
		g.ViewSubPx = 0
	}()
	t.scheduleViewUpdate(w)
}
