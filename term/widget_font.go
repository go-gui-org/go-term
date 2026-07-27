package term

// style returns the resolved text style for this terminal. When fontSize
// is non-zero it overrides the configured Size.
//
// The result is clamped to [minFontSize, maxFontSize] — the same bounds the
// zoom path enforces. Doing it here rather than only in AdjustFontSize means a
// configured size (Cfg.TextStyle, SetTextStyle, a user config file) is bounded
// too, so no caller has to re-derive the limits.
import "github.com/go-gui-org/go-gui/gui"

func (t *Term) style() gui.TextStyle {
	ts := t.cfg.TextStyle
	if ts == (gui.TextStyle{}) {
		ts = gui.CurrentTheme().M5
	}
	if t.fontSize > 0 {
		ts.Size = t.fontSize
	}
	// realNumber, not "> 0": NaN fails every comparison, so a "Size > 0" gate
	// would skip the clamp and pass NaN straight through to text measurement.
	// A non-finite configured size falls back to the theme default instead.
	if !realNumber(ts.Size) {
		ts.Size = 0
	} else if ts.Size > 0 {
		ts.Size = clampFontSize(ts.Size)
	}
	return ts
}

// AdjustFontSize shifts the terminal font size by delta points and
// triggers a full remeasure + redraw. Clamps the result to [4, 72] pt.
// Main-thread only (called from onKeyDown, which writes cellW/runeCache
// without grid.Mu — onDraw is the only concurrent reader and also runs
// on the main thread).
func (t *Term) AdjustFontSize(delta float32) {
	if !realNumber(delta) {
		return
	}
	if t.fontSize == 0 {
		if s := t.style(); s.Size > 0 {
			t.fontSize = s.Size
		}
		if t.fontSize == 0 {
			return
		}
	}
	t.fontSize = clampFontSize(t.fontSize + delta)
	t.invalidateFontMetrics()
}

// SetFontSize sets the runtime zoom to an absolute size in points, clamped to
// [minFontSize, maxFontSize]. A value <= 0 clears the override (equivalent to
// ResetFontSize). Unlike seeding cfg.TextStyle.Size, this leaves the configured
// default intact, so a later ResetFontSize still returns to that default —
// which is why restore/split apply their inherited size through here rather
// than through Cfg. Main-thread only, same constraints as AdjustFontSize.
func (t *Term) SetFontSize(size float32) {
	if !realNumber(size) {
		return
	}
	if size <= 0 {
		t.ResetFontSize()
		return
	}
	size = clampFontSize(size)
	if t.fontSize == size {
		return
	}
	t.fontSize = size
	t.invalidateFontMetrics()
}

// ResetFontSize clears any runtime zoom, restoring the configured
// TextStyle.Size (or the theme default when no TextStyle was set). Main-thread
// only, same constraints as AdjustFontSize. A no-op when already unzoomed.
// After reset t.fontSize is 0; AdjustFontSize re-seeds it from style() on the
// next zoom, so subsequent zooming still works.
func (t *Term) ResetFontSize() {
	if t.fontSize == 0 {
		return // already at the configured default
	}
	t.fontSize = 0
	t.invalidateFontMetrics()
}

// FontSize returns the effective font size in points — the runtime zoom
// override when set, otherwise the configured TextStyle.Size. Main-thread
// only (reads style() without grid.Mu, like AdjustFontSize).
func (t *Term) FontSize() float32 { return t.style().Size }

// minFontSize/maxFontSize bound the runtime zoom. 4 pt stays legible at the
// smallest useful size; 72 pt is a generous presentation ceiling.
const minFontSize, maxFontSize float32 = 4, 72

// clampFontSize constrains size to [minFontSize, maxFontSize].
func clampFontSize(size float32) float32 {
	if size < minFontSize {
		return minFontSize
	}
	if size > maxFontSize {
		return maxFontSize
	}
	return size
}

// invalidateFontMetrics forces onDraw to remeasure the cell (cellW == 0) on the
// next frame, drops the cached glyph runs whose widths are now stale, bumps the
// draw version, and requests a repaint. Main-thread only (see AdjustFontSize).
func (t *Term) invalidateFontMetrics() {
	t.cellW = 0
	t.draw.runeCache = nil
	t.bumpVersion()
	t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
}
