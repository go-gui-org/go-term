package term

import (
	"github.com/go-gui-org/go-gui/gui"
)

// Live setters for settings that Cfg otherwise fixes at construction.
//
// Cfg is read once, in New. A config-file reload (or a settings UI) needs to
// change these on a terminal that is already running, which is what these
// provide. Each mirrors the Cfg field of the same name, including its
// zero/negative conventions, so the two paths can't drift.
//
// All are main-thread only unless noted: they write t.cfg, which onDraw and
// the keyboard handlers read without a lock. The one exception is the bell
// mode, which the reader goroutine consults and so lives in an atomic.

// SetTextStyle replaces the terminal's base text style — family, size,
// typeface — and forces a remeasure. Mirrors Cfg.TextStyle: the zero value
// means "fall back to gui.CurrentTheme().M5".
//
// This also clears any runtime zoom, exactly as ResetFontSize does. t.fontSize
// is an *absolute* size derived from the previous base and wins over
// cfg.TextStyle.Size in style(), so a pane zoomed to 14 pt would otherwise
// ignore a new configured size of 12 pt until the user pressed Cmd+0.
func (t *Term) SetTextStyle(ts gui.TextStyle) {
	// Normalize a non-finite size to "unset" before storing it. Otherwise the
	// equality check below can never hold (NaN != NaN), so every call would
	// force a remeasure and silently discard the user's zoom.
	if !realNumber(ts.Size) {
		ts.Size = 0
	}
	if t.cfg.TextStyle == ts && t.fontSize == 0 {
		return
	}
	t.cfg.TextStyle = ts
	t.fontSize = 0
	t.invalidateFontMetrics()
}

// SetScrollbackRows changes the scrollback cap on a live terminal. The sign
// convention matches Cfg.ScrollbackRows: zero restores the built-in default,
// positive sets that many rows (clamped to [0, MaxScrollbackCap]), negative
// disables scrollback entirely.
//
// Shrinking trims the stored history immediately — keeping the newest rows and
// discarding the oldest — rather than waiting for eviction to catch up, so
// lowering the cap actually returns the memory. Safe to call from the main
// thread at any time; takes grid.Mu.
func (t *Term) SetScrollbackRows(n int) {
	var capRows int
	switch {
	case n == 0:
		capRows = defaultScrollbackRows
	case n > 0:
		capRows = clampScrollback(n)
	default:
		capRows = 0 // negative: scrollback off
	}
	if !t.resizeScrollback(capRows) {
		return
	}
	// Outside the lock: grid.Mu must never be held across a go-gui call.
	// Trimming can clamp ViewOffset, which moves the visible viewport, so the
	// frame has to be requested here rather than left to the next event.
	t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
}

// resizeScrollback applies a new scrollback capacity under grid.Mu, reporting
// whether anything actually changed. Split out of SetScrollbackRows so the
// repaint request can happen after the lock is released.
func (t *Term) resizeScrollback(capRows int) bool {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	if t.grid.ScrollbackCap == capRows {
		return false
	}
	t.grid.ScrollbackCap = capRows
	// EnsureGeom keeps the newest min(size, cap) rows on a capacity-only
	// change and drops the backing entirely at cap 0. Pass the ring's own
	// column count: a differing value would reset the content, and the ring
	// is repopulated at the live column count by scrollUpRegion anyway.
	t.grid.Scrollback.EnsureGeom(capRows, t.grid.Scrollback.cols)
	// The viewport may have been scrolled into rows that no longer exist.
	if n := t.grid.Scrollback.Len(); t.grid.ViewOffset > n {
		t.grid.ViewOffset = n
	}
	t.grid.markAllDirty()
	t.bumpVersion()
	return true
}

// SetBellMode changes how BEL is signalled on a live terminal. Safe to call
// from the main thread at any time; the reader goroutine reads it atomically.
func (t *Term) SetBellMode(m BellMode) { t.bellMode.Store(int32(m)) }

// SetScrollbarWidth changes the scrollbar thumb width in pixels. Mirrors
// Cfg.ScrollbarWidth: zero restores the built-in default, negative hides the
// scrollbar. A non-finite value is ignored. Main-thread only.
func (t *Term) SetScrollbarWidth(px float32) {
	if !realNumber(px) || t.cfg.ScrollbarWidth == px {
		return
	}
	t.cfg.ScrollbarWidth = px
	t.bumpVersion()
	t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
}
