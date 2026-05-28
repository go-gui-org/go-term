package term

import (
	"log"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mike-ward/go-gui/gui"
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

// mouseModBits encodes shift/alt/ctrl modifier bits into the xterm
// mouse-button byte. Values from xterm ctlseqs: shift=4, alt/meta=8,
// ctrl=16. Super/Cmd has no standard mapping and is ignored.
func mouseModBits(m gui.Modifier) int {
	bits := 0
	if m.Has(gui.ModShift) {
		bits += 4
	}
	if m.Has(gui.ModAlt) {
		bits += 8
	}
	if m.Has(gui.ModCtrl) {
		bits += 16
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

// shouldReport reports whether mouse events should encode to the PTY
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
	r := int(y / t.cellH)
	c := int(x / t.cellW)
	if r < 0 {
		r = 0
	}
	if c < 0 {
		c = 0
	}
	t.grid.Mu.Lock()
	if r >= t.grid.Rows {
		r = t.grid.Rows - 1
	}
	if c >= t.grid.Cols {
		c = t.grid.Cols - 1
	}
	t.grid.Mu.Unlock()
	return r, c
}

// writeMouse emits an SGR mouse report. When pixels is true (?1016 active),
// pixX/pixY (0-based widget pixels) are used; otherwise col/row (0-based
// cell indices) are used. Both forms report 1-based coordinates per spec.
func (t *Term) writeMouse(cb, col, row int, pixX, pixY float32, pixels, press bool) {
	var buf [32]byte
	var out []byte
	if pixels {
		out = encodeMouseSGR(buf[:0], cb, int(pixX), int(pixY), press)
	} else {
		out = encodeMouseSGR(buf[:0], cb, col, row, press)
	}
	if err := t.writeHost(out); err != nil {
		log.Printf("term: pty mouse: %v", err)
	}
}

// onClick handles a button-down event. Under mouse reporting, encodes
// a press report for any supported button and arms drag tracking.
// Otherwise (the default) starts a left-button selection anchor.
func (t *Term) onClick(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.shouldReport() {
		base, ok := mouseSGRBaseButton(e.MouseButton)
		if !ok {
			return
		}
		cb := base + mouseModBits(e.Modifiers)
		t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
		t.dragging = true
		t.dragButton = e.MouseButton
		t.dragReport = true
		t.lastMouseR, t.lastMouseC = r, c
		e.IsHandled = true
		return
	}
	if e.MouseButton != gui.MouseLeft {
		return
	}
	t.grid.Mu.Lock()
	contentR := t.grid.viewportToContent(r)
	t.grid.SelAnchor = ContentPos{Row: contentR, Col: c}
	t.grid.SelHead = ContentPos{Row: contentR, Col: c}
	t.grid.SelActive = false
	t.grid.Mu.Unlock()
	t.dragging = true
	t.dragButton = e.MouseButton
	t.dragReport = false
	w.MouseLock(gui.MouseLockCfg{
		MouseMove: t.onMouseMove,
		MouseUp:   t.onMouseUp,
	})
	t.bumpVersion()
	w.UpdateWindow()
	e.IsHandled = true
}

// onMouseMove handles pointer motion. Under ?1002 with a button held,
// emits a drag report; under ?1003 even with no button, emits an
// any-motion report. Falls through to selection extension when this
// drag was started outside of a reporting mode.
func (t *Term) onMouseMove(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	// Any pointer motion means the user's hand is on the input device again;
	// cancel a coasting momentum scroll so they get immediate control.
	t.momentumMu.Lock()
	coasting := t.momentumCoasting
	t.momentumMu.Unlock()
	if coasting {
		t.cancelMomentum()
	}
	r, c := t.posToCell(e.MouseX, e.MouseY)
	snap := t.mouseSnap()
	if snap.sgr && snap.live {
		// Dedupe: only emit when crossing a cell boundary.
		if r == t.lastMouseR && c == t.lastMouseC {
			if t.dragReport {
				return
			}
			// Local-selection drag: still fall through to update
			// SelHead at unchanged coords (cheap; avoids stale state).
		}
		switch {
		case t.dragReport && snap.drag:
			base, ok := mouseSGRBaseButton(t.dragButton)
			if !ok {
				return
			}
			cb := base + mouseModBits(e.Modifiers) + 32
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		case !t.dragging && snap.any:
			cb := 35 + mouseModBits(e.Modifiers) // 3+32 = motion, no button
			t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, true)
			t.lastMouseR, t.lastMouseC = r, c
			return
		}
	}
	if !t.dragging || t.dragReport {
		// Update hover for hyperlink highlighting even when not dragging.
		t.updateHover(r, c, w)
		return
	}
	t.grid.Mu.Lock()
	rows := t.grid.Rows
	widgetH := float32(rows) * t.cellH
	if t.cellH > 0 {
		switch {
		case e.MouseY < 0:
			t.grid.ScrollView(1)
		case e.MouseY > widgetH:
			t.grid.ScrollView(-1)
		}
	}
	contentR := t.grid.viewportToContent(r)
	t.grid.SelHead = ContentPos{Row: contentR, Col: c}
	if t.grid.SelHead != t.grid.SelAnchor {
		t.grid.SelActive = true
	}
	t.grid.Mu.Unlock()
	// Persist scroll direction so autoScrollLoop keeps scrolling if
	// onMouseMove stops firing (mouse above title bar / window edge).
	if t.cellH > 0 {
		switch {
		case e.MouseY < 0:
			t.autoScrollDir.Store(1)
		case e.MouseY > widgetH:
			t.autoScrollDir.Store(-1)
		default:
			t.autoScrollDir.Store(0)
		}
	}
	t.bumpVersion()
	w.UpdateWindow()
	t.updateHover(r, c, w)
}

// updateHover updates t.hoverR/C and requests a redraw when entering or
// leaving a hyperlinked cell run.
func (t *Term) updateHover(r, c int, w *gui.Window) {
	oldR, oldC := int(t.hoverR.Load()), int(t.hoverC.Load())
	if oldR == r && oldC == c {
		return
	}
	t.hoverR.Store(int32(r))
	t.hoverC.Store(int32(c))
	t.grid.Mu.Lock()
	var prevLink, curLink uint16
	if oldR >= 0 && oldC >= 0 {
		prevLink = t.grid.ViewCellAt(oldR, oldC).LinkID
	}
	curLink = t.grid.ViewCellAt(r, c).LinkID
	t.grid.Mu.Unlock()
	if prevLink != 0 || curLink != 0 {
		t.bumpVersion()
		w.UpdateWindow()
	}
}

// onMouseUp handles button-release. A drag started under reporting
// emits a release report regardless of whether the mode is still on
// (the host expects every press to be paired with a release).
func (t *Term) onMouseUp(_ *gui.Layout, e *gui.Event, w *gui.Window) {
	if !t.dragging {
		return
	}
	t.autoScrollDir.Store(0)
	w.MouseUnlock()
	r, c := t.posToCell(e.MouseX, e.MouseY)
	if t.dragReport {
		snap := t.mouseSnap()
		if snap.sgr {
			base, ok := mouseSGRBaseButton(t.dragButton)
			if ok {
				cb := base + mouseModBits(e.Modifiers)
				t.writeMouse(cb, c, r, e.MouseX, e.MouseY, snap.pixels, false)
			}
		}
		t.dragging = false
		t.dragReport = false
		e.IsHandled = true
		return
	}
	t.dragging = false
	// Single click (no drag) with Cmd/Ctrl on a hyperlink → open URL.
	if !t.grid.SelActive {
		if e.Modifiers&gui.ModSuper != 0 || e.Modifiers&gui.ModCtrl != 0 {
			t.grid.Mu.Lock()
			cell := t.grid.ViewCellAt(r, c)
			url := t.grid.LinkURL(cell.LinkID)
			t.grid.Mu.Unlock()
			if url != "" {
				openURL(url)
				e.IsHandled = true
				return
			}
		}
	}
	if !t.copySelection(w) {
		t.grid.Mu.Lock()
		t.grid.ClearSelection()
		t.grid.Mu.Unlock()
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
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err == nil {
		go func() { _ = cmd.Wait() }()
	}
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
		t.writeMouse(base+mouseModBits(e.Modifiers), c, r, e.MouseX, e.MouseY, snap.pixels, true)
		e.IsHandled = true
		return
	}
	if !realNumber(e.ScrollY) || !finite(t.cellH) {
		return
	}

	// Mouse wheel detection: SDL populates PreciseY (float) for trackpad and
	// falls back to integer Y for discrete mouse wheels. go-gui merges them
	// into ScrollY, so integer-valued ScrollY reliably signals a mouse wheel.
	isMouseWheel := e.ScrollY == float32(int32(e.ScrollY))

	// Pixel-perfect scroll: pass the raw scaled delta directly to ScrollViewPx
	// which accumulates it into ViewOffset + ViewSubPx. No integer truncation.
	const scrollSensitivity float32 = 15
	deltaPx := e.ScrollY * scrollSensitivity
	t.grid.Mu.Lock()
	prevOff, prevSub := t.grid.ViewOffset, t.grid.ViewSubPx
	t.grid.ScrollViewPx(deltaPx, t.cellH)
	changed := t.grid.ViewOffset != prevOff || t.grid.ViewSubPx != prevSub
	t.grid.Mu.Unlock()
	if changed {
		t.showScrollbar()
		t.bumpVersion()
		w.UpdateWindow()
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
		momentumScale = 12.0 // match scrollSensitivity so coast starts at live-scroll speed
		momentumCap   = 600.0
		coastDelay    = 40 * time.Millisecond
	)
	t.momentumMu.Lock()
	newVel := math.Max(-momentumCap, math.Min(momentumCap, float64(e.ScrollY)*momentumScale))
	if math.Abs(newVel) >= math.Abs(t.momentumVel) || (t.momentumVel > 0) != (newVel > 0) {
		t.momentumVel = newVel
	}
	t.momentumCellH = t.cellH
	t.momentumCoasting = false
	t.momentumMu.Unlock()
	if t.momentumTimer == nil {
		t.momentumTimer = time.AfterFunc(coastDelay, t.kickMomentum)
	} else {
		t.momentumTimer.Reset(coastDelay)
	}
	e.IsHandled = true
}

// cancelMomentum stops any in-progress momentum coast immediately.
func (t *Term) cancelMomentum() {
	if t.momentumTimer != nil {
		t.momentumTimer.Stop()
	}
	t.momentumMu.Lock()
	t.momentumVel = 0
	t.momentumCoasting = false
	t.momentumMu.Unlock()
}

// kickMomentum is the AfterFunc callback fired 80 ms after the last scroll
// event. It marks the momentum state as coasting and wakes momentumLoop.
func (t *Term) kickMomentum() {
	t.momentumMu.Lock()
	t.momentumCoasting = true
	t.momentumMu.Unlock()
	select {
	case t.momentumKick <- struct{}{}:
	default:
	}
}

// momentumLoop decelerates the scroll velocity after the user lifts their
// finger. Ticks at ~60 fps; each tick passes the decaying pixel velocity
// to ScrollViewPx for sub-cell-accurate smooth scrolling.
func (t *Term) momentumLoop() {
	defer t.loopWg.Done()
	const (
		tickDur       = 16 * time.Millisecond
		frictionFast  = 0.90  // decelerate at high speed — avoids linear feel
		frictionCoast = 0.95  // gentle tail once slow — covers real distance
		phaseVel      = 120.0 // px/tick threshold between phases
		minVel        = 1.0   // px/tick below which coast stops
	)
	tk := time.NewTicker(tickDur)
	defer tk.Stop()
	for {
		select {
		case <-t.blinkDone:
			return
		case <-t.momentumKick:
			// coasting flag already set; next tick starts the coast
		case <-tk.C:
			t.momentumMu.Lock()
			if !t.momentumCoasting {
				t.momentumMu.Unlock()
				continue
			}
			friction := frictionCoast
			if math.Abs(t.momentumVel) > phaseVel {
				friction = frictionFast
			}
			t.momentumVel *= friction
			cellH := t.momentumCellH
			if math.Abs(t.momentumVel) < minVel {
				t.momentumCoasting = false
				t.momentumVel = 0
			}
			deltaPx := float32(t.momentumVel)
			t.momentumMu.Unlock()
			if deltaPx != 0 && finite(cellH) {
				t.grid.Mu.Lock()
				t.grid.ScrollViewPx(deltaPx, cellH)
				t.grid.Mu.Unlock()
				t.bumpVersion()
				t.win.QueueCommand(func(w *gui.Window) {
					if t.closed.Load() {
						return
					}
					t.showScrollbar()
					w.UpdateWindow()
				})
			}
		}
	}
}
