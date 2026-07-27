package term

import (
	"sync/atomic"
	"time"

	"github.com/go-gui-org/go-gui/gui"
)

// bellFlashDuration is the default visual-bell flash duration.
// Override via Cfg.BellFlashDuration; see effectiveBellDuration().
const bellFlashDuration = 100 * time.Millisecond

// maxBellDuration caps the user-configurable BellFlashDuration so that
// arithmetic (d + time.Millisecond) cannot overflow time.Duration.
const maxBellDuration = 5 * time.Second

// BellMode selects how a BEL (0x07) is signalled to the user.
type BellMode int

const (
	// BellAuto plays the system alert sound where the platform has one
	// and falls back to the visual flash where it does not (mobile,
	// wasm, Linux without canberra-gtk-play). This is the zero value:
	// an audible bell matches what native terminals do, respects the
	// user's system alert-sound and mute settings, and does not disturb
	// what is on screen.
	BellAuto BellMode = iota
	// BellAudible plays the system alert sound only, with no visual
	// fallback — silent on platforms that cannot beep.
	BellAudible
	// BellVisual flashes the pane only, never plays a sound.
	BellVisual
	// BellBoth flashes and plays the alert sound.
	BellBoth
	// BellNone ignores BEL entirely.
	BellNone
)

// beepInterval rate-limits the audible bell. A program emitting BEL in
// a tight loop (a stuck `yes $'\a'`, a pathological build log) would
// otherwise machine-gun the alert sound; on Linux each one also costs a
// process spawn. Bells inside this window after an audible one are
// dropped rather than queued, so the sound never lags the output.
const beepInterval = 250 * time.Millisecond

// bellFlashPeakAlpha is the alpha of the visual-bell overlay at the instant
// the BEL arrives; it eases to zero across the flash duration. Kept low
// deliberately — a bell is an incidental event (an ambiguous completion, a
// pager hitting the end of its buffer), so the flash should register
// peripherally without washing out the text underneath.
const bellFlashPeakAlpha = 22

// bellFadeFrame paces the intermediate frames of the bell fade. A hard
// on/off overlay reads as a jarring strobe, so the alpha is stepped down
// over roughly this interval (~60 Hz) for the life of the flash.
const bellFadeFrame = 16 * time.Millisecond

// bellState tracks bell signalling. flashUntil holds the UnixNano instant
// the visual flash ends (0 = no flash) and flashNanos the full duration of
// that flash; together they let onDraw derive how far the fade has
// progressed. Both are written from the ringBell command and read by onDraw,
// and are atomic because a scheduled Close can race the read.
// seenCount/readCount are touched only by applyChunk (reader goroutine).
// The remaining fields are main-thread only: startBellFlash, drawOverlays
// and allowBeep all run on the GUI thread (the first via queueCommand), and
// Close touches the timers only after readLoop has exited.
type bellState struct {
	flashUntil atomic.Int64
	flashNanos atomic.Int64
	flashTimer *time.Timer // reused per-BEL clear timer; lazy init
	fadeTimer  *time.Timer // reused fade-frame timer; lazy init
	lastBeep   time.Time   // rate-limit stamp; see beepInterval
	seenCount  uint64
	readCount  uint64
}

// ringBell signals a BEL according to Cfg.BellMode.
//
// Everything runs inside the queued command: the beep has to happen on
// the GUI thread (AppKit requires it), BeepAvailable is only answerable
// there, and routing the flash through the same path keeps the bell
// timers main-thread-only rather than shared with the reader goroutine.
func (t *Term) ringBell() {
	mode := BellMode(t.bellMode.Load())
	if mode == BellNone {
		return
	}
	t.queueCommand(func(w *gui.Window) {
		audible := mode != BellVisual && w.BeepAvailable()
		if audible && t.allowBeep(time.Now()) {
			w.Beep()
		}
		// BellAudible stays silent rather than flashing when the
		// platform cannot beep; BellAuto is the mode that degrades.
		flash := mode == BellVisual || mode == BellBoth ||
			(mode == BellAuto && !audible)
		if flash {
			t.startBellFlash()
		}
	})
}

// allowBeep reports whether enough time has passed since the last
// audible bell, and records now when it has. Main-thread only (called
// from the ringBell command).
func (t *Term) allowBeep(now time.Time) bool {
	if !t.bell.lastBeep.IsZero() && now.Sub(t.bell.lastBeep) < beepInterval {
		return false
	}
	t.bell.lastBeep = now
	return true
}

// startBellFlash arms the visual-bell overlay for the configured
// duration. Main-thread only (called from the ringBell command).
func (t *Term) startBellFlash() {
	d := t.effectiveBellDuration()
	if d <= 0 {
		return
	}
	// Store the duration alongside the end instant so drawOverlays can
	// derive fade progress without knowing the config.
	t.bell.flashNanos.Store(int64(d))
	t.bell.flashUntil.Store(time.Now().Add(d).UnixNano())
	t.bumpVersion()
	t.scheduleBellClear(d)
}

// effectiveBellDuration returns the configured visual-bell duration,
// falling back to the default when unset. Negative disables the flash.
func (t *Term) effectiveBellDuration() time.Duration {
	if t.cfg.BellFlashDuration < 0 {
		return 0
	}
	if t.cfg.BellFlashDuration > 0 {
		return min(t.cfg.BellFlashDuration, maxBellDuration)
	}
	return bellFlashDuration
}

// scheduleDelayedUpdate lazily creates or resets a *time.Timer field so
// that tmr fires after d, bumps the draw version, and schedules a window
// repaint. Used by scheduleBellClear, showScrollbar, and scheduleResizeWake
// to avoid duplicating the after-func / guard / bump / queue pattern.
// Safe to call from any goroutine; the callback checks closed before
// queueing work.
func (t *Term) scheduleDelayedUpdate(d time.Duration, tmr **time.Timer) {
	if *tmr == nil {
		*tmr = time.AfterFunc(d, func() {
			if t.closed.Load() || t.cmd == nil {
				return
			}
			t.bumpVersion()
			t.queueCommand(func(w *gui.Window) { w.UpdateWindow() })
		})
	} else {
		(*tmr).Reset(d)
	}
}

// scheduleBellClear schedules a redraw to clear the visual-bell flash
// overlay after d. Uses a single reusable timer so rapid BEL sequences
// don't accumulate goroutines. Safe to call from the QueueCommand
// callback (main thread).
func (t *Term) scheduleBellClear(d time.Duration) {
	// Guard against overflow from a misconfigured (or malicious) duration.
	// effectiveBellDuration already clamps to maxBellDuration, so this is
	// defense-in-depth — the arithmetic is safe even if called directly.
	if d > maxBellDuration {
		d = maxBellDuration
	}
	t.scheduleDelayedUpdate(d+time.Millisecond, &t.bell.flashTimer)
}
