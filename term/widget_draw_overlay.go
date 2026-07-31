package term

import (
	"math"
	"strconv"
	"time"

	glyph "github.com/go-gui-org/go-glyph"
	"github.com/go-gui-org/go-gui/gui"
)

// imeCompRuneLimit caps the number of runes accepted from the IME
// composition string. Typical compositions contain fewer than 50 runes;
// this prevents excessive allocation from a malformed or malicious
// platform input.
const imeCompRuneLimit = 256

// scrollbarGeometry computes the scrollbar thumb Y position and height.
// sbLen = len(Scrollback), viewH = canvas pixel height. Caller ensures sbLen > 0.
const minScrollbarThumbH float32 = 10

// scrollbarInset is the horizontal gap between the window's right edge and
// the drawn thumb, applied only to panes flush against that edge. macOS
// reserves an interior band just inside a resizable window's frame where
// mouseDown starts a live resize before the event reaches the content view;
// insetting the thumb keeps it (and its hit region) clear of that band.
const scrollbarInset float32 = 6

// scrollbarHitWidth is the minimum width of the clickable thumb region. The
// grabbable area extends inward (leftward) from the thumb's right edge so a
// narrow visual thumb is still easy to hit; the drawn thumb width is
// unchanged. Decoupling hit width from visual width is what makes an
// edge-hugging scrollbar usable despite the OS resize band.
const scrollbarHitWidth float32 = 16

func scrollbarGeometry(sbLen, rows int, viewOffset float32, viewH float32) (thumbY, thumbH float32) {
	if viewH <= 0 || math.IsNaN(float64(viewH)) || math.IsInf(float64(viewH), 0) ||
		math.IsNaN(float64(viewOffset)) || math.IsInf(float64(viewOffset), 0) {
		return
	}
	total := float32(sbLen + rows)
	if total <= 0 {
		return
	}
	thumbH = float32(rows) / total * viewH
	if thumbH < minScrollbarThumbH {
		thumbH = minScrollbarThumbH
	}
	thumbY = (float32(sbLen) - viewOffset) / total * viewH
	return
}

// scrollbarOffsetForY inverts scrollbarGeometry: given a desired thumb-top
// pixel y, it returns the fractional view offset (in rows, matching
// ViewOffset+ViewSubPx/cellH) that places the thumb there. Used for
// click-to-jump and thumb drag. Clamped to [0, sbLen]. Mirrors the thumbY
// formula: y = (sbLen - off)/total * viewH  ⇒  off = sbLen - y*total/viewH.
func scrollbarOffsetForY(sbLen, rows int, y, viewH float32) float32 {
	if viewH <= 0 || sbLen <= 0 ||
		math.IsNaN(float64(y)) || math.IsInf(float64(y), 0) {
		return 0
	}
	total := float32(sbLen + rows)
	off := float32(sbLen) - y*total/viewH
	if off < 0 {
		off = 0
	} else if off > float32(sbLen) {
		off = float32(sbLen)
	}
	return off
}

// Failure ticks: small red marks in the scrollbar track at the prompt row of
// every command that exited non-zero, so a failure buried in a long build log
// is findable by eye rather than only by chord.
var scrollbarFailColor = gui.RGB(214, 74, 66)

const (
	scrollbarTickH float32 = 2
	// Ticks stay painted when the scrollbar is idle — a marker you can only
	// see while already scrolling cannot be the thing that tells you where to
	// scroll. The low idle alpha keeps it a quiet lane rather than a
	// permanent red bar; it brightens with the thumb.
	scrollbarTickIdleAlpha   uint8 = 120
	scrollbarTickActiveAlpha uint8 = 235
)

// scrollbarRowY maps a content row to the y of its band in the scrollbar
// track, on the same total-rows scale as scrollbarGeometry so a tick and the
// thumb covering it agree to the pixel.
func scrollbarRowY(row, sbLen, rows int, viewH float32) float32 {
	if viewH <= 0 || math.IsNaN(float64(viewH)) || math.IsInf(float64(viewH), 0) {
		return 0
	}
	total := float32(sbLen + rows)
	if total <= 0 {
		return 0
	}
	y := float32(row) / total * viewH
	if y < 0 {
		return 0
	}
	if y > viewH {
		return viewH
	}
	return y
}

// drawScrollbarFailures paints one tick per failed command. The failure rows
// are cached against the mark version, so the per-frame cost is a version
// compare plus a bounded loop; adjacent ticks closer than one tick height are
// skipped, which keeps a dense run of failures from becoming a solid bar and
// bounds the draw calls regardless of how many commands failed.
func (t *Term) drawScrollbarFailures(ds *drawState, sw, inset float32, thumbVisible bool) {
	g := ds.g
	rows := g.failureRows()
	if len(rows) == 0 {
		return
	}
	alpha := scrollbarTickIdleAlpha
	if thumbVisible || t.scrollbar.hovered {
		alpha = scrollbarTickActiveAlpha
	}
	c := scrollbarFailColor
	c.A = alpha

	x := ds.dc.Width - sw - inset
	sb := g.Scrollback.Len()
	prevY := float32(-1e9)
	for _, row := range rows {
		y := scrollbarRowY(row, sb, g.Rows, ds.dc.Height)
		if y-prevY < scrollbarTickH {
			continue // coalesce ticks that would overlap anyway
		}
		prevY = y
		ds.dc.FilledRect(x, y, sw, scrollbarTickH, c)
	}
}

// drawIME renders the IME composition string at the cursor position. Fills
// the background under the composition, draws each rune, and underlines the
// full span. Populates ds.ime* fields for consumption by drawCursor.
func (t *Term) drawIME(ds *drawState) {
	if !t.ime.composing {
		return
	}
	g := ds.g
	ds.imeComposing = true
	ds.imeRunes = []rune(t.ime.compText)
	if len(ds.imeRunes) > imeCompRuneLimit {
		ds.imeRunes = ds.imeRunes[:imeCompRuneLimit]
	}
	ds.imeWidths = make([]int, len(ds.imeRunes))
	var totalCols int
	for i, r := range ds.imeRunes {
		w := max(runeWidth(r), 1)
		ds.imeWidths[i] = w
		totalCols += w
	}
	ds.imeCursor = min(t.ime.compCursor, len(ds.imeRunes))

	if len(ds.imeRunes) == 0 || g.CursorR < ds.renderTop || g.CursorR >= ds.renderRows ||
		g.ViewOffset != 0 || ds.renderYOff != 0 {
		return
	}
	startX := float32(g.CursorC) * t.cellW
	rowY := float32(g.CursorR)*t.cellH + ds.renderYOff

	// DECSCNM-aware: the composition strip sits on top of terminal cells, so
	// it has to follow the same reversal they do.
	bgCol := g.defaultBG()
	ds.dc.FilledRect(startX, rowY, float32(totalCols)*t.cellW, t.cellH, bgCol)

	cs := ds.style
	cs.Color = g.defaultFG()
	cs.Underline = false

	currX := startX
	for i, r := range ds.imeRunes {
		ds.dc.Text(currX, rowY, t.termRuneStr(r), cs)
		currX += float32(ds.imeWidths[i]) * t.cellW
	}
	t.drawUnderlineDecor(ds.dc, startX, rowY, float32(totalCols)*t.cellW, ulSingle, cs.Color)
}

// drawCursor renders the text cursor at the current grid position, honoring
// DECSCUSR shape, blink phase, scrollback state, and IME composition offset.
// When the cursor row has BiDi reordering the logical column is mapped to a
// visual column.
func (t *Term) drawCursor(ds *drawState) {
	g := ds.g
	// Copy mode draws its own cursor. Two cursors on screen at once is worse
	// than none — on entry they overlap exactly, so the user cannot tell which
	// one their motion keys are moving.
	if t.copy.active {
		return
	}
	if !g.CursorVisible || g.CursorR < ds.renderTop || g.CursorR >= ds.renderRows ||
		g.ViewOffset != 0 || ds.renderYOff != 0 {
		return
	}
	if t.cursorBlinkOff(ds.now) {
		return
	}
	cc := g.CursorC
	if ds.imeComposing {
		colOffset := 0
		for i := range ds.imeCursor {
			colOffset += ds.imeWidths[i]
		}
		cc = g.CursorC + colOffset
	}
	if cc >= ds.cols {
		cc = ds.cols - 1
	}
	// When the cursor's row has bidi reordering, find the visual column
	// that corresponds to the logical cursor column.
	if cr := g.CursorR; cr >= 0 && cr < ds.renderRows && ds.bidiV2LRows[cr] != nil {
		if !ds.imeComposing {
			for v, l := range ds.bidiV2LRows[cr] {
				if l == g.CursorC {
					cc = v
					break
				}
			}
		}
	}

	cursorCell := cell{Ch: ' '}
	if cell := g.At(g.CursorR, g.CursorC); cell != nil {
		// Masked like the fg pass: a block cursor redraws the glyph beneath
		// it, which would otherwise expose a concealed character.
		cursorCell = maskGlyph(*cell, ds.blinkOff)
	}
	if ds.imeComposing && ds.imeCursor >= 0 && ds.imeCursor < len(ds.imeRunes) {
		cursorCell.Ch = ds.imeRunes[ds.imeCursor]
	}
	t.drawCursorShape(ds.dc, cc, g.CursorR, cursorCell, g.cursorShape, ds.style)

	// Report cursor rect to the platform for candidate window placement.
	if ds.imeComposing && t.win != nil {
		imeX := t.ime.layoutX + float32(cc)*t.cellW
		imeY := t.ime.layoutY + float32(g.CursorR)*t.cellH
		t.win.IMESetRect(imeX, imeY, t.cellW, t.cellH)
	}
}

// drawOverlays paints the scrollbar thumb, search bar, and visual-bell flash.
// All three are drawn on top of the terminal content.
func (t *Term) drawOverlays(ds *drawState) {
	g := ds.g
	// Scrollbar: pill-shaped thumb on the right edge. Visible while scrolled
	// back or within scrollbarDuration of the last scroll event. Held visible
	// for the whole drag so releasing the thumb doesn't hide it mid-gesture.
	sb := g.Scrollback.Len()
	sw := t.effectiveScrollbarWidth()
	visible := ds.now.Before(t.scrollbar.until) || g.ViewOffset > 0 || g.ViewSubPx > 0 || t.scrollbar.dragging
	active := visible && sb > 0 && ds.dc.Width >= sw && sw > 0
	t.scrollbar.active = active

	// Inset the thumb from the window's right edge only for panes flush
	// against it, so the thumb clears the OS window-resize band. Hoisted
	// above the active check because the failure ticks share it.
	inset := t.scrollbarEdgeInset(ds.dc.Width)

	// Failure ticks go under the thumb: the thumb is only alpha 120, so a
	// tick beneath it tints through instead of disappearing.
	if sb > 0 && sw > 0 && ds.dc.Width >= sw && !g.AltActive {
		t.drawScrollbarFailures(ds, sw, inset, active)
	}

	if active {
		// The clickable region extends inward (leftward) to scrollbarHitWidth
		// so the grabbable area stays wide even when the visual thumb is narrow.
		thumbX := ds.dc.Width - sw - inset
		viewOffsetVal := float32(g.ViewOffset) + g.ViewSubPx/t.cellH
		thumbY, thumbH := scrollbarGeometry(sb, g.Rows, viewOffsetVal, ds.dc.Height)
		thumbColor := gui.RGBA(128, 128, 128, 120)
		if t.scrollbar.hovered {
			thumbColor = gui.RGBA(180, 180, 180, 150)
		}
		ds.dc.FilledRoundedRect(thumbX, thumbY, sw, thumbH,
			sw/2, thumbColor)
		hitX0 := thumbX + sw - scrollbarHitWidth
		if hitX0 < 0 {
			hitX0 = 0
		}
		t.scrollbar.hitX0 = hitX0
		t.scrollbar.viewH = ds.dc.Height
	} else {
		t.scrollbar.hovered = false
	}

	// Copy mode: its cursor, then its status bar. The two bars no longer
	// contend — copy's is at the top, search's at the bottom — so a search
	// opened from copy mode shows both, which is what the state actually is.
	if t.copy.active {
		t.drawCopyCursor(ds)
		t.drawCopyBar(ds)
	}
	if t.search.active {
		t.drawSearchBar(ds.dc, ds.style)
	}

	// Recording indicator: nothing else tells the user the session is being
	// written to disk, and an unnoticed recording is a privacy problem, not
	// just a surprise.
	t.drawRecordIndicator(ds)

	// Size readout while resizing. Without it the cell dimensions are
	// unknowable during a drag, which makes hitting an exact geometry
	// (80×24 for vttest, a width a tool assumes) guesswork.
	t.drawSizeBadge(ds)

	// Visual bell: a faint white wash that eases out over the flash
	// duration rather than switching on and off, so an incidental BEL
	// registers peripherally instead of strobing the whole pane.
	t.drawBellFlash(ds)
}

// drawBellFlash paints the visual-bell overlay at an alpha derived from how
// much of the flash duration remains, and schedules the next fade frame
// while the flash is still running. Main-thread only (called from
// drawOverlays), which is what makes the unsynchronized fadeTimer access
// safe.
func (t *Term) drawBellFlash(ds *drawState) {
	fu := t.bell.flashUntil.Load()
	if fu == 0 {
		return
	}
	remaining := fu - ds.now.UnixNano()
	if remaining <= 0 {
		return
	}
	total := t.bell.flashNanos.Load()
	if total <= 0 {
		return
	}
	// progress runs 0→1 across the flash; clamped because a BEL landing
	// between the Store of flashNanos and flashUntil can briefly make
	// remaining exceed total.
	progress := 1 - float64(remaining)/float64(total)
	if progress < 0 {
		progress = 0
	}
	// Ease out quadratically: near-peak at the leading edge where the eye
	// catches the event, then a long shallow tail instead of a cliff.
	fade := (1 - progress) * (1 - progress)
	alpha := uint8(float64(bellFlashPeakAlpha)*fade + 0.5)
	if alpha == 0 {
		return
	}
	ds.dc.FilledRect(0, 0, ds.dc.Width, ds.dc.Height,
		gui.RGBA(255, 255, 255, alpha))

	// Drive the next step of the fade. scheduleBellClear already covers the
	// final repaint that removes the overlay; this only fills in the frames
	// between, and stops on its own once remaining hits zero.
	next := time.Duration(remaining)
	if next > bellFadeFrame {
		next = bellFadeFrame
	}
	t.scheduleDelayedUpdate(next, &t.bell.fadeTimer)
}

// scrollbarEdgeInset returns the horizontal gap to leave between the drawn
// scrollbar thumb and the pane's right edge. It is scrollbarInset only when
// the pane is flush against the window's right edge (where the OS reserves a
// resize band); interior panes get 0. canvasW is the pane's canvas width;
// t.ime.layoutX is the pane's absolute left X (set by onAmendLayout).
func (t *Term) scrollbarEdgeInset(canvasW float32) float32 {
	if t.win == nil || !realNumber(canvasW) {
		return 0
	}
	winW, _ := t.win.WindowSize()
	if winW <= 0 {
		return 0
	}
	rightEdge := t.ime.layoutX + canvasW
	// 1px tolerance absorbs fractional layout/rounding at the window edge.
	if float32(winW)-rightEdge <= 1 {
		return scrollbarInset
	}
	return 0
}

// cursorBlinkOff reports whether the cursor is currently in the
// hidden half of its blink cycle. Returns false (always visible) for
// steady cursors. Caller holds grid.Mu.
func (t *Term) cursorBlinkOff(now time.Time) bool {
	if !t.cursorBlinks() {
		return false
	}
	elapsed := now.Sub(t.cursorEpoch)
	return (elapsed/cursorBlinkPeriod)%2 == 1
}

// drawCursorShape renders the cursor at viewport (row, col) using the
// current shape. Block inverts the cell (filled bg + cell glyph in
// fg's color); underline/bar overlay a thin filled rect on top of the
// regular foreground glyph already drawn in the foreground pass.
func (t *Term) drawCursorShape(dc *gui.DrawContext, col, row int, cell cell,
	shape cursorShape, style gui.TextStyle) {
	x := float32(col) * t.cellW
	y := float32(row) * t.cellH

	// Dim the cursor to 40% when the terminal doesn't have pane focus.
	opacity := float32(1.0)
	if !t.focused.Load() {
		opacity = 0.4
	}

	switch shape {
	case cursorUnderline:
		// Bottom-aligned bar 1/8th of the cell height (min 2px) so it
		// stays visible at smaller font sizes.
		h := t.cellH / 8
		if h < 2 {
			h = 2
		}
		dc.FilledRect(x, y+t.cellH-h, t.cellW, h,
			t.grid.fgOf(cell).WithOpacity(opacity))
	case cursorBar:
		w := t.cellW / 6
		if w < 2 {
			w = 2
		}
		dc.FilledRect(x, y, w, t.cellH,
			t.grid.fgOf(cell).WithOpacity(opacity))
	default: // cursorBlock
		fillColor := t.grid.fgOf(cell)
		if t.grid.CursorColor != DefaultColor {
			fillColor = rgbToGUIColor(t.grid.CursorColor)
		}
		dc.FilledRect(x, y, t.cellW, t.cellH, fillColor.WithOpacity(opacity))
		cs := style
		cs.Color = t.grid.bgOf(cell)
		cs.EmojiBoxWidth = float32(cell.Width) * t.cellW
		dc.Text(x, y, t.cellText(cell), cs)
	}
}

// drawUnderlineDecor renders underline decorations for a text run.
// x,y are the top-left of the run; w is its pixel width. Handles all
// ULStyle values including ulSingle (drawn as a rect so ulColor is honored).
func (t *Term) drawUnderlineDecor(dc *gui.DrawContext, x, y, w float32, ulStyle uint8, ulColor gui.Color) {
	thick := t.cellH / 14
	if thick < 1 {
		thick = 1
	}
	baseY := y + t.cellH - 2*thick - 1
	switch ulStyle {
	case ulSingle:
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ulDouble:
		dc.FilledRect(x, baseY-thick-1, w, thick, ulColor)
		dc.FilledRect(x, baseY, w, thick, ulColor)
	case ulCurly:
		// Approximate curly as alternating up/down segments.
		seg := t.cellW * 2
		if seg < 4 {
			seg = 4
		}
		xi := x
		up := true
		for xi < x+w {
			ww := seg
			if xi+ww > x+w {
				ww = x + w - xi
			}
			yy := baseY
			if up {
				yy = baseY - thick - 1
			}
			dc.FilledRect(xi, yy, ww, thick, ulColor)
			xi += ww
			up = !up
		}
	case ulDotted:
		step := thick * 3
		if step < 3 {
			step = 3
		}
		xi := x
		for xi+thick <= x+w {
			dc.FilledRect(xi, baseY, thick, thick, ulColor)
			xi += step
		}
	case ulDashed:
		dash := t.cellW * 3
		if dash < 6 {
			dash = 6
		}
		gap := dash / 2
		xi := x
		for xi < x+w {
			ww := dash
			if xi+ww > x+w {
				ww = x + w - xi
			}
			dc.FilledRect(xi, baseY, ww, thick, ulColor)
			xi += dash + gap
		}
	}
}

// recordIndicatorPad is the padding inside the REC pill, and its inset from
// the pane's top-right corner.
const recordIndicatorPad = 6

// drawRecordIndicator paints a "● REC m:ss" pill in the pane's top-right
// corner while a session recording is running. Sits left of the scrollbar
// lane so the two never overlap. Called under Mu (inside onDraw).
func (t *Term) drawRecordIndicator(ds *drawState) {
	if !t.Recording() {
		return
	}
	// Built with strconv rather than fmt: this file stays out of the
	// reflection-based formatting path, and the label is rebuilt every
	// frame while recording.
	d := t.recordingElapsed()
	sec := int(d.Seconds()) % 60
	label := "● REC " + strconv.Itoa(int(d.Minutes())) + ":"
	if sec < 10 {
		label += "0"
	}
	label += strconv.Itoa(sec)

	cs := ds.style
	cs.Color = gui.RGB(255, 235, 235)
	cs.Typeface = glyph.TypefaceRegular
	textW := ds.dc.TextWidth(label, cs)
	if textW <= 0 || !realNumber(textW) {
		return // metrics not ready yet, or NaN; a later frame will paint it
	}
	w := textW + 2*recordIndicatorPad
	h := t.cellH + recordIndicatorPad
	x := ds.dc.Width - w - recordIndicatorPad - t.effectiveScrollbarWidth()
	if x < 0 {
		x = 0
	}
	y := float32(recordIndicatorPad)
	// Semi-transparent so it dims rather than hides the cells underneath —
	// the terminal content matters more than the badge.
	ds.dc.FilledRoundedRect(x, y, w, h, h/2, gui.RGBA(150, 30, 30, 210))
	ds.dc.Text(x+recordIndicatorPad, y+recordIndicatorPad/2, label, cs)
}

// sizeBadgeDuration is how long the size readout lingers after the last
// dimension change. Comfortably longer than resizeDebounce so the badge does
// not blink out between drag frames, short enough that it clears itself once
// the drag stops.
const sizeBadgeDuration = 900 * time.Millisecond

// sizeBadgePad is the padding inside the size-readout pill.
const sizeBadgePad = 10

// showSizeBadge records dims to display and (re)starts the badge's linger
// window. Called from prepareResize on the main thread whenever the canvas
// implies dims other than the grid's.
func (t *Term) showSizeBadge(rows, cols int, now time.Time) {
	t.resize.badgeRows, t.resize.badgeCols = rows, cols
	if !t.resize.sized {
		return // initial sizing, not a user gesture
	}
	t.resize.badgeUntil = now.Add(sizeBadgeDuration)
	// Repaint once more after the linger window so the badge actually
	// disappears when the drag stops producing frames.
	t.scheduleDelayedUpdate(sizeBadgeDuration+time.Millisecond, &t.resize.badgeTimer)
}

// drawSizeBadge paints a centered "COLS × ROWS" pill while a resize is in
// flight or has just finished. Called under Mu (inside onDraw).
func (t *Term) drawSizeBadge(ds *drawState) {
	if t.resize.badgeUntil.IsZero() || !ds.now.Before(t.resize.badgeUntil) {
		return
	}
	// strconv rather than fmt, matching drawRecordIndicator: the label is
	// rebuilt every frame of a drag.
	label := strconv.Itoa(t.resize.badgeCols) + " × " + strconv.Itoa(t.resize.badgeRows)

	cs := ds.style
	cs.Color = gui.RGB(240, 240, 240)
	cs.Typeface = glyph.TypefaceRegular
	textW := ds.dc.TextWidth(label, cs)
	if textW <= 0 || !realNumber(textW) {
		return // metrics not ready yet; a later frame will paint it
	}
	w := textW + 2*sizeBadgePad
	h := t.cellH + 2*sizeBadgePad
	x := (ds.dc.Width - w) / 2
	y := (ds.dc.Height - h) / 2
	if x < 0 || y < 0 {
		return // pane too small to hold the badge without clipping
	}
	// Dark and mostly opaque: this is a momentary HUD read at a glance, so
	// legibility beats seeing the cells behind it.
	ds.dc.FilledRoundedRect(x, y, w, h, h/4, gui.RGBA(20, 20, 20, 225))
	ds.dc.Text(x+sizeBadgePad, y+sizeBadgePad, label, cs)
}

// copyCursorColor is the copy-mode cursor's fill. Deliberately not the theme's
// cursor color: the copy cursor must be distinguishable from the terminal
// cursor at a glance, or there is no feedback that a motion key did anything —
// on entry the two sit on the same cell.
var copyCursorColor = gui.RGB(255, 176, 0)

// drawCopyCursor paints the copy-mode cursor at its viewport position.
// Separate from drawCursor, which deliberately bails whenever ViewOffset != 0 —
// copy mode is mostly used while scrolled back, which is exactly the case
// drawCursor skips. Called under Mu (inside onDraw).
func (t *Term) drawCopyCursor(ds *drawState) {
	g := ds.g
	vr, ok := g.ContentRowToViewport(t.copy.cursor.Row)
	if !ok || vr < ds.renderTop || vr >= ds.renderRows {
		return
	}
	cc := clamp(t.copy.cursor.Col, 0, max(ds.cols-1, 0))
	x := float32(cc) * t.cellW
	y := float32(vr)*t.cellH + ds.renderYOff

	// Steady, never blinking: it marks a position the user is aiming with, and
	// a blinking target is harder to track than a still one.
	ds.dc.FilledRect(x, y, t.cellW, t.cellH, copyCursorColor)

	cell := maskGlyph(g.ViewCellAt(vr, cc), ds.blinkOff)
	cs := ds.style
	cs.Color = g.Theme.DefaultBG
	cs.EmojiBoxWidth = float32(cell.Width) * t.cellW
	ds.dc.Text(x, y, t.cellText(cell), cs)
}

// copyBarHint is the key cheatsheet shown in copy mode's status bar. The flat
// help overlay lists only the entry chord (see copyActionOrder), so this is
// where the in-mode keys are discoverable.
const copyBarHint = "hjkl move  w/b word  v/V select  y yank  / search  [ ] prompt  Esc exit"

// copyBarLabels is the bar's full text for each selection state, indexed by
// copySelMode. Built once rather than concatenated per frame: drawCopyBar runs
// in the draw path, where the repo's rule is not to allocate.
//
// "output paused" is stated explicitly because the mode freezes the viewport —
// a user watching a live log would otherwise think the terminal had hung.
var copyBarLabels = [...]string{
	copySelNone: "COPY  output paused  ·  " + copyBarHint,
	copySelChar: "COPY [select]  output paused  ·  " + copyBarHint,
	copySelLine: "COPY [select line]  output paused  ·  " + copyBarHint,
}

// drawCopyBar paints copy mode's status bar across the top cellH pixels. Top,
// not bottom: the row it covers is not painted (drawState.renderTop), and
// giving up the oldest visible row costs less than giving up the newest — and
// it leaves the bottom band free for the search bar copy mode can open.
// Called under Mu (inside onDraw).
func (t *Term) drawCopyBar(ds *drawState) {
	dc := ds.dc
	y := float32(0)
	dc.FilledRect(0, y, dc.Width, t.cellH, gui.RGB(30, 70, 45))

	label := copyBarLabels[copySelNone]
	if int(t.copy.sel) < len(copyBarLabels) {
		label = copyBarLabels[t.copy.sel]
	}

	cs := ds.style
	cs.Color = gui.RGB(220, 220, 220)
	cs.Typeface = glyph.TypefaceRegular
	dc.Text(0, y, label, cs)
}

// drawSearchBar paints a status bar over the bottom cellH pixels of the
// canvas showing the active search query. Called under Mu (inside onDraw).
func (t *Term) drawSearchBar(dc *gui.DrawContext, style gui.TextStyle) {
	y := dc.Height - t.cellH
	noMatch := t.search.query != "" && len(t.search.matches) == 0
	bgColor := gui.RGB(40, 40, 90)
	if noMatch {
		bgColor = gui.RGB(90, 20, 20)
	}
	dc.FilledRect(0, y, dc.Width, dc.Height-y, bgColor)
	var label string
	switch {
	case t.search.regex && t.search.reErr != nil:
		label = "/re/ " + t.search.query + " [invalid]▌"
	case t.search.regex:
		label = "/re/ " + t.search.query + "▌"
	default:
		label = "Find (^R=regex): " + t.search.query + "▌"
	}
	cs := style
	cs.Color = gui.RGB(220, 220, 220)
	cs.Typeface = glyph.TypefaceRegular
	dc.Text(0, y, label, cs)
}
