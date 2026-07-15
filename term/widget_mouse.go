package term

import (
	"log"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// mouseSGRBaseButton maps a go-gui MouseButton to its SGR (?1006) base
// code. Returns false for unsupported buttons (e.g. MouseInvalid),
// signaling "do not report".
func mouseSGRBaseButton(b gui.MouseButton) (int, bool) {
	switch b {
	case gui.MouseLeft:
		return 0, true
	case gui.MouseMiddle:
		return 1, true
	case gui.MouseRight:
		return 2, true
	}
	return 0, false
}

// SGR mouse modifier bits, per xterm ctlseqs documentation.
const (
	mouseModShift = 4
	mouseModAlt   = 8
	mouseModCtrl  = 16
)

// mouseModBits encodes shift/alt/ctrl modifier bits into the xterm
// mouse-button byte. Values from xterm ctlseqs: shift=4, alt/meta=8,
// ctrl=16. Super/Cmd has no standard mapping and is ignored.
func mouseModBits(m gui.Modifier) int {
	bits := 0
	if m.Has(gui.ModShift) {
		bits += mouseModShift
	}
	if m.Has(gui.ModAlt) {
		bits += mouseModAlt
	}
	if m.Has(gui.ModCtrl) {
		bits += mouseModCtrl
	}
	return bits
}

// encodeMouseSGR appends an SGR-1006 mouse report to buf:
// "\x1b[<{cb};{col};{row}{M|m}". Coordinates are converted to 1-based
// per spec. press=true emits 'M' (press / motion / wheel-tick);
// press=false emits 'm' (release).
func encodeMouseSGR(buf []byte, cb, col, row int, press bool) []byte {
	final := byte('M')
	if !press {
		final = 'm'
	}
	buf = append(buf, '\x1b', '[', '<')
	buf = strconv.AppendInt(buf, int64(cb), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(col+1), 10)
	buf = append(buf, ';')
	buf = strconv.AppendInt(buf, int64(row+1), 10)
	buf = append(buf, final)
	return buf
}

// mouseSnap reports the current mouse-mode state under the grid lock.
// Reporting requires SGR encoding (?1006) and a live viewport — when
// scrolled back into history we suppress reports so the user can
// select / scroll without the host consuming the events.
type mouseSnap struct {
	report bool // any of ?1000/?1002/?1003 active
	drag   bool // ?1002
	any    bool // ?1003
	sgr    bool // ?1006
	pixels bool // ?1016 — pixel-precise SGR coordinates
	live   bool // ViewOffset == 0 && ViewSubPx == 0
}

func (t *Term) mouseSnap() mouseSnap {
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	return mouseSnap{
		report: t.grid.MouseReporting(),
		drag:   t.grid.MouseTrackBtn,
		any:    t.grid.MouseTrackAny,
		sgr:    t.grid.MouseSGR,
		pixels: t.grid.MouseSGRPixels,
		live:   t.grid.ViewOffset == 0 && t.grid.ViewSubPx == 0,
	}
}

// shouldReport reports whether mouse events should encode to the pty
// rather than drive local selection. Requires reporting on, SGR
// encoding on, and a live viewport.
func (m mouseSnap) shouldReport() bool { return m.report && m.sgr && m.live }

// posToCell maps shape-local (x, y) pixels to viewport (row, col).
// Returns clamped coordinates so out-of-bounds drag positions still
// pin to the nearest cell. NaN/Inf inputs collapse to (0, 0) — int()
// of a non-finite float is undefined and would otherwise leak through
// to selection logic as a pseudo-random row/col.
func (t *Term) posToCell(x, y float32) (int, int) {
	if !finite(t.cellW) || !finite(t.cellH) {
		return 0, 0
	}
	if !realNumber(x) {
		x = 0
	}
	if !realNumber(y) {
		y = 0
	}
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	// When smooth-scrolled the renderer shifts every visible row down by
	// ViewSubPx (drawState.renderYOff), so viewport row r spans the pixel
	// band [r*cellH+ViewSubPx, (r+1)*cellH+ViewSubPx). Invert with the same
	// offset — floor, not truncation, so the band math holds at r == 0 —
	// or clicks land a row off while the viewport is between cells.
	r := int(math.Floor(float64((y - t.grid.ViewSubPx) / t.cellH)))
	c := int(x / t.cellW)
	if r < 0 {
		r = 0
	}
	if c < 0 {
		c = 0
	}
	if r >= t.grid.Rows {
		r = t.grid.Rows - 1
	}
	if c >= t.grid.Cols {
		c = t.grid.Cols - 1
	}
	return r, c
}

// posToSelCol maps shape-local x pixels to a selection boundary column in
// [0, Cols]. Selection endpoints are cell *boundaries* (nearest edge), not cell
// indices, so a one-cell drag spans exactly one cell and the selected span is
// half-open [min, max) — matching macOS Terminal / Ghostty. Nearest-edge
// snapping (round, not floor) keeps the behavior symmetric in both drag
// directions. Compare posToCell, which floors to a cell index for xterm mouse
// reports.
func (t *Term) posToSelCol(x float32) int {
	if !finite(t.cellW) {
		return 0
	}
	if !realNumber(x) {
		x = 0
	}
	t.grid.Mu.Lock()
	defer t.grid.Mu.Unlock()
	b := int(math.Floor(float64(x/t.cellW) + 0.5))
	if b < 0 {
		b = 0
	}
	if b > t.grid.Cols {
		b = t.grid.Cols
	}
	return b
}

// writeMouse emits an SGR mouse report. When pixels is true (?1016 active),
// pixX/pixY (0-based widget pixels) are used; otherwise col/row (0-based
// cell indices) are used. Both forms report 1-based coordinates per spec.
func (t *Term) writeMouse(cb, col, row int, pixX, pixY float32, pixels, press bool) {
	var buf [32]byte
	var out []byte
	if pixels {
		// Guard against NaN/Inf pixel coords before int() conversion.
		// posToCell sanitizes x/y for cell-mode paths; pixel-mode paths
		// receive raw MouseX/MouseY from the GUI framework directly.
		if !realNumber(pixX) {
			pixX = 0
		}
		if !realNumber(pixY) {
			pixY = 0
		}
		out = encodeMouseSGR(buf[:0], cb, int(pixX), int(pixY), press)
	} else {
		out = encodeMouseSGR(buf[:0], cb, col, row, press)
	}
	if _, err := t.pw.Write(out); err != nil {
		log.Printf("term: pty mouse: %v", err)
	}
}

// lockMouse engages a gui mouse lock routing motion/release to the widget's
// handlers, and records that lock-callback coordinates are now absolute.
// Every MouseLock must go through here so t.mouse.locked stays truthful.
func (t *Term) lockMouse(w *gui.Window) {
	if w == nil {
		return
	}
	t.mouse.locked = true
	w.MouseLock(gui.MouseLockCfg{
		MouseMove: t.onMouseMove,
		MouseUp:   t.onMouseUp,
	})
}

// unlockMouse releases the mouse lock and clears the absolute-coordinate
// flag. Safe to call when no lock is held. Counterpart to lockMouse; every
// MouseUnlock must go through here, including the stuck-drag safety nets.
func (t *Term) unlockMouse(w *gui.Window) {
	t.mouse.locked = false
	if w != nil {
		w.MouseUnlock()
	}
}

// toCanvasRel converts a mouse event's coordinates from absolute window
// space to canvas-relative when the event arrived through a mouse-lock
// callback. Lock callbacks bypass go-gui's callRelative, so without this a
// canvas offset by UI chrome (e.g. a tab bar) shifts every hit-test by the
// chrome's height. Events dispatched to the same handlers via the canvas
// config are already relative and must be left alone.
func (t *Term) toCanvasRel(e *gui.Event) {
	if !t.mouse.locked {
		return
	}
	e.MouseX -= t.ime.layoutX
	e.MouseY -= t.ime.layoutY
}

// onClick handles a button-down event. Under mouse reporting, encodes
// a press report for any supported button and arms drag tracking.
// Otherwise (the default) starts a left-button selection anchor.
func (t *Term) onClick(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if t.cfg.OnClickFocus != nil {
		t.cfg.OnClickFocus()
	}
	// Scrollbar takes priority over selection and host mouse reporting: it is
	// a local overlay drawn on top of the grid. Only interactive while the
	// thumb is visible, so a faded scrollbar never swallows clicks.
	if e.MouseButton == gui.MouseLeft && t.scrollbarClick(e, w) {
		e.IsHandled = true
		return
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.shouldReport() {
		base, ok := mouseSGRBaseButton(e.MouseButton)
		if !ok {
			return
		}
		cb := base + mouseModBits(e.Modifiers)
		t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
		t.mouse.dragging = true
		t.mouse.dragButton = e.MouseButton
		t.mouse.dragReport = true
		t.mouse.lastR, t.mouse.lastC = r, c
		e.IsHandled = true
		return
	}
	if e.MouseButton != gui.MouseLeft {
		return
	}
	selCol := t.posToSelCol(e.MouseX)
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		contentR := t.grid.viewportToContent(r)
		t.grid.SelAnchor = contentPos{Row: contentR, Col: selCol}
		t.grid.SelHead = contentPos{Row: contentR, Col: selCol}
		t.grid.SelActive = false
	}()
	t.mouse.dragging = true
	t.mouse.dragButton = e.MouseButton
	t.mouse.dragReport = false
	t.lockMouse(w)
	t.bumpVersion()
	w.UpdateWindow()
	e.IsHandled = true
}

// onMouseMove handles pointer motion. Under ?1002 with a button held,
// emits a drag report; under ?1003 even with no button, emits an
// any-motion report. Falls through to selection extension when this
// drag was started outside of a reporting mode.
func (t *Term) onMouseMove(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	t.toCanvasRel(e)
	// Track scrollbar hover for thumb brightness.
	if t.scrollbar.active && realNumber(e.MouseX) && realNumber(e.MouseY) {
		inHit := e.MouseX >= t.scrollbar.hitX0 &&
			e.MouseY >= 0 && e.MouseY < t.scrollbar.viewH
		if inHit != t.scrollbar.hovered {
			t.scrollbar.hovered = inHit
			t.bumpVersion()
			w.UpdateWindow()
		}
	}

	// Scrollbar thumb drag: repositions the viewport, independent of the
	// selection / mouse-report paths below.
	if t.scrollbar.dragging {
		t.scrollbarDrag(e, w)
		return
	}
	// Any pointer motion means the user's hand is on the input device again;
	// cancel a coasting momentum scroll so they get immediate control.
	t.momentum.mu.Lock()
	coasting := t.momentum.coasting
	t.momentum.mu.Unlock()
	if coasting {
		t.cancelMomentum()
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.sgr && snap.live {
		// Dedupe: only emit when crossing a cell boundary.
		if r == t.mouse.lastR && c == t.mouse.lastC {
			if t.mouse.dragReport {
				return
			}
			// Local-selection drag: still fall through to update
			// SelHead at unchanged coords (cheap; avoids stale state).
		}
		switch {
		case t.mouse.dragReport && snap.drag:
			base, ok := mouseSGRBaseButton(t.mouse.dragButton)
			if !ok {
				return
			}
			cb := base + mouseModBits(e.Modifiers) + 32
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.mouse.lastR, t.mouse.lastC = r, c
			return
		case !t.mouse.dragging && snap.any:
			cb := 35 + mouseModBits(e.Modifiers) // 3+32 = motion, no button
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.mouse.lastR, t.mouse.lastC = r, c
			return
		}
	}
	if !t.mouse.dragging || t.mouse.dragReport {
		// Update hover for hyperlink highlighting even when not dragging.
		t.updateHover(r, c, w)
		return
	}
	selCol := t.posToSelCol(e.MouseX)
	func() {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		rows := t.grid.Rows
		widgetH := float32(rows) * t.cellH
		if t.cellH > 0 {
			switch {
			case e.MouseY < 0:
				t.grid.ScrollView(1)
				t.autoScrollDir.Store(1)
			case e.MouseY > widgetH:
				t.grid.ScrollView(-1)
				t.autoScrollDir.Store(-1)
			default:
				t.autoScrollDir.Store(0)
			}
		}
		contentR := t.grid.viewportToContent(r)
		t.grid.SelHead = contentPos{Row: contentR, Col: selCol}
		if t.grid.SelHead != t.grid.SelAnchor {
			t.grid.SelActive = true
		}
	}()
	t.bumpVersion()
	w.UpdateWindow()
	t.updateHover(r, c, w)
}

// updateHover updates t.hoverR/C and requests a redraw when entering or
// leaving a hyperlinked cell run.
func (t *Term) updateHover(r, c int, w *gui.Window) {
	oldR, oldC := int(t.mouse.hoverR.Load()), int(t.mouse.hoverC.Load())
	if oldR == r && oldC == c {
		return
	}
	t.mouse.hoverR.Store(int32(r))
	t.mouse.hoverC.Store(int32(c))
	prevLink, curLink := func() (uint16, uint16) {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		var prev, cur uint16
		if oldR >= 0 && oldC >= 0 {
			prev = t.grid.ViewCellAt(oldR, oldC).LinkID
		}
		cur = t.grid.ViewCellAt(r, c).LinkID
		return prev, cur
	}()
	if prevLink != 0 || curLink != 0 {
		t.bumpVersion()
		w.UpdateWindow()
	}
}

// onMouseUp handles button-release. A drag started under reporting
// emits a release report regardless of whether the mode is still on
// (the host expects every press to be paired with a release).
func (t *Term) onMouseUp(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	t.toCanvasRel(e)
	// Scrollbar thumb drag release: unlock and stop dragging. The scrollbar
	// drag path never sets t.mouse.dragging, so handle it before that guard.
	if t.scrollbar.dragging {
		t.scrollbar.dragging = false
		t.unlockMouse(w)
		t.scheduleViewUpdate(w)
		e.IsHandled = true
		return
	}
	if !t.mouse.dragging {
		return
	}
	t.autoScrollDir.Store(0)
	t.unlockMouse(w)
	r, c := t.posToCell(e.MouseX, e.MouseY)
	if t.mouse.dragReport {
		snap := t.mouseSnap()
		if snap.sgr {
			base, ok := mouseSGRBaseButton(t.mouse.dragButton)
			if ok {
				cb := base + mouseModBits(e.Modifiers)
				t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, false)
			}
		}
		t.mouse.dragging = false
		t.mouse.dragReport = false
		e.IsHandled = true
		return
	}
	t.mouse.dragging = false
	// Single click (no drag) with Cmd/Ctrl on a hyperlink → open URL.
	if !t.grid.SelActive {
		if e.Modifiers&gui.ModSuper != 0 || e.Modifiers&gui.ModCtrl != 0 {
			url := func() string {
				t.grid.Mu.Lock()
				defer t.grid.Mu.Unlock()
				cell := t.grid.ViewCellAt(r, c)
				return t.grid.LinkURL(cell.LinkID)
			}()
			if url != "" {
				openURL(url)
				e.IsHandled = true
				return
			}
		}
	}
	if !t.copySelection(w) {
		func() {
			t.grid.Mu.Lock()
			defer t.grid.Mu.Unlock()
			t.grid.ClearSelection()
		}()
	}
	t.bumpVersion()
	w.UpdateWindow()
	e.IsHandled = true
}

// openURL opens url with the OS default browser/handler.
// Only http, https, and mailto schemes are permitted; other URI schemes
// (file://, custom handlers, javascript:) are silently dropped to prevent
// a malicious OSC 8 hyperlink from invoking arbitrary OS handlers.
func openURL(rawURL string) {
	switch {
	case strings.HasPrefix(rawURL, "https://"),
		strings.HasPrefix(rawURL, "http://"),
		strings.HasPrefix(rawURL, "mailto:"):
		// permitted
	default:
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
}

// wheelSensitivity converts a discrete mouse-wheel delta into scroll
// pixels. The Metal backend pre-scales one notch to ~2.5, so this lands
// a notch at ~12.5px before macOS scroll acceleration.
const wheelSensitivity float32 = 5

// trackpadSensitivity converts a precise (trackpad / high-res) delta
// into scroll pixels. The Metal backend pre-scales precise deltas by
// 0.075; at 10 the effective finger-travel multiplier is ~0.75×.
const trackpadSensitivity float32 = 10

// scrollSensitivityFor selects the delta→pixel factor for a scroll
// event. Backends that don't distinguish (everything non-Metal today)
// leave ScrollPrecise false and get the wheel factor.
func scrollSensitivityFor(precise bool) float32 {
	if precise {
		return trackpadSensitivity
	}
	return wheelSensitivity
}

// maxWheelTicks caps the SGR wheel reports emitted for a single scroll
// event so a huge accelerated delta cannot flood the pty.
const maxWheelTicks = 32

// wheelReportTicks converts a wheel delta into the number of SGR wheel
// reports to emit. The delta is scaled to pixels (wheel or trackpad
// factor per the precise flag) and divided by the cell height, so one
// report is sent per row of scroll distance — matching how far the
// local scrollback viewport would move. The fractional remainder
// accumulates in mouse.wheelResidual across events (reset on direction
// change) so trackpad pans add up instead of being truncated. Always at
// least 1 tick, so a single slow notch never feels dead. Main-thread only.
func (t *Term) wheelReportTicks(scrollY float32, precise bool) int {
	if !realNumber(scrollY) || !finite(t.cellH) {
		return 1
	}
	dir := 1
	if scrollY < 0 {
		dir = -1
	}
	if dir != t.mouse.wheelDir {
		t.mouse.wheelDir = dir
		t.mouse.wheelResidual = 0
	}
	total := float64(t.mouse.wheelResidual) +
		math.Abs(float64(scrollY))*float64(scrollSensitivityFor(precise))
	ticks := int(total / float64(t.cellH))
	if ticks < 1 {
		// Below one row of travel: emit a single tick and drop the
		// residual so slow continuous pans stay at one tick per event
		// (the pre-multiplier behavior) rather than bursting later.
		t.mouse.wheelResidual = 0
		return 1
	}
	if ticks > maxWheelTicks {
		ticks = maxWheelTicks
		total = float64(maxWheelTicks) * float64(t.cellH)
	}
	t.mouse.wheelResidual = float32(total - float64(ticks)*float64(t.cellH))
	return ticks
}

// onMouseScroll forwards wheel events to the application as SGR mouse
// reports when reporting + SGR are active and the viewport is live;
// otherwise moves the local scrollback viewport. Positive ScrollY
// reveals older content (wheel-up); negative reveals newer (down).
// Each event also feeds the momentum EMA so that releasing the trackpad
// produces a brief coast rather than an abrupt stop.
func (t *Term) onMouseScroll(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	// Zero-delta: macOS sends this when a finger touches the trackpad during
	// a momentum coast. Cancel immediately so the user gets instant control.
	if e.ScrollY == 0 {
		t.cancelMomentum()
		return
	}
	snap := t.mouseSnap()
	if snap.shouldReport() {
		r, c := t.posToCell(e.MouseX, e.MouseY)
		base := 64
		if e.ScrollY < 0 {
			base = 65
		}
		cb := base + mouseModBits(e.Modifiers)
		for range t.wheelReportTicks(e.ScrollY, e.ScrollPrecise) {
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
		}
		e.IsHandled = true
		return
	}
	if !realNumber(e.ScrollY) || !finite(t.cellH) {
		return
	}

	// Mouse-wheel vs trackpad: the backend flags precise (trackpad /
	// high-res) deltas via ScrollPrecise. Non-precise deltas are discrete
	// wheel notches — no momentum. Backends that never set the flag get
	// wheel behavior throughout, matching the pre-flag feel.
	isMouseWheel := !e.ScrollPrecise

	// Pixel-perfect scroll: pass the raw scaled delta directly to ScrollViewPx
	// which accumulates it into ViewOffset + ViewSubPx. No integer truncation.
	deltaPx := e.ScrollY * scrollSensitivityFor(e.ScrollPrecise)
	changed := func() bool {
		t.grid.Mu.Lock()
		defer t.grid.Mu.Unlock()
		prevOff, prevSub := t.grid.ViewOffset, t.grid.ViewSubPx
		t.grid.ScrollViewPx(deltaPx, t.cellH)
		return t.grid.ViewOffset != prevOff || t.grid.ViewSubPx != prevSub
	}()
	if changed {
		t.scheduleViewUpdate(w)
	}

	// Mouse wheel: no momentum. Cancel any in-progress coast and return.
	if isMouseWheel {
		t.cancelMomentum()
		e.IsHandled = true
		return
	}

	// Track peak velocity of the current gesture so coast starts at
	// live-scroll speed. Ignore decelerating OS-momentum samples by only
	// updating when the new sample is larger in magnitude or direction
	// reverses. Cap prevents a huge flick from coasting forever.
	const (
		momentumScale = float64(trackpadSensitivity) // coast starts at live-scroll speed
		momentumCap   = 600.0
		coastDelay    = 50 * time.Millisecond
	)
	func() {
		t.momentum.mu.Lock()
		defer t.momentum.mu.Unlock()
		newVel := math.Max(-momentumCap, math.Min(momentumCap, float64(e.ScrollY)*momentumScale))
		if math.Abs(newVel) >= math.Abs(t.momentum.vel) || (t.momentum.vel > 0) != (newVel > 0) {
			t.momentum.vel = newVel
		}
		t.momentum.cellH = t.cellH
		t.momentum.coasting = false
	}()
	if t.momentum.timer == nil {
		t.momentum.timer = time.AfterFunc(coastDelay, t.kickMomentum)
	} else {
		t.momentum.timer.Reset(coastDelay)
	}
	e.IsHandled = true
}

// cancelMomentum stops any in-progress momentum coast immediately.
func (t *Term) cancelMomentum() {
	if t.momentum.timer != nil {
		t.momentum.timer.Stop()
	}
	t.momentum.mu.Lock()
	defer t.momentum.mu.Unlock()
	t.momentum.vel = 0
	t.momentum.coasting = false
}

// kickMomentum is the AfterFunc callback fired 80 ms after the last scroll
// event. It marks the momentum state as coasting and wakes momentumLoop.
func (t *Term) kickMomentum() {
	t.momentum.mu.Lock()
	defer t.momentum.mu.Unlock()
	t.momentum.coasting = true
	select {
	case t.momentum.kick <- struct{}{}:
	default:
	}
}

// momentumLoop decelerates the scroll velocity after the user lifts their
// finger. Ticks at ~60 fps; each tick passes the decaying pixel velocity
// to ScrollViewPx for sub-cell-accurate smooth scrolling.
func (t *Term) momentumLoop() {
	defer t.loopWg.Done()
	defer recoverLoop("momentumLoop")
	const (
		tickDur       = 16 * time.Millisecond
		frictionFast  = 0.82  // decelerate at high speed — avoids linear feel
		frictionCoast = 0.88  // gentle tail once slow — covers real distance
		phaseVel      = 120.0 // px/tick threshold between phases
		minVel        = 2.0   // px/tick below which coast stops
	)
	tk := time.NewTicker(tickDur)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-t.momentum.kick:
			// coasting flag already set; next tick starts the coast
		case <-tk.C:
			deltaPx, cellH, coasting := func() (float32, float32, bool) {
				t.momentum.mu.Lock()
				defer t.momentum.mu.Unlock()
				if !t.momentum.coasting {
					return 0, 0, false
				}
				friction := frictionCoast
				if math.Abs(t.momentum.vel) > phaseVel {
					friction = frictionFast
				}
				t.momentum.vel *= friction
				cellH := t.momentum.cellH
				if math.Abs(t.momentum.vel) < minVel {
					t.momentum.coasting = false
					t.momentum.vel = 0
				}
				return float32(t.momentum.vel), cellH, t.momentum.coasting
			}()
			if !coasting {
				continue
			}
			if deltaPx != 0 && finite(cellH) {
				func() {
					t.grid.Mu.Lock()
					defer t.grid.Mu.Unlock()
					t.grid.ScrollViewPx(deltaPx, cellH)
				}()
				t.bumpVersion()
				t.queueCommand(func(w *gui.Window) {
					t.showScrollbar()
					w.UpdateWindow()
				})
			}
		}
	}
}
