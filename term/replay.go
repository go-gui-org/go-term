package term

// Session replay: a recording pretending to be a pty.
//
// replayPTY satisfies ptyIO (see pty.go), so a Term built on it runs the
// entire normal stack — parser, grid, rendering, scrollback, selection,
// search, graphics, BiDi — against recorded bytes instead of a live shell.
// There is deliberately no separate "player" widget: anything that renders
// wrong during replay renders wrong for real, which is what makes a
// recording useful in a bug report.
//
// The one thing replay cannot reproduce is geometry. A Term's size comes
// from the pane it is drawn in, so recorded 'r' frames are advisory (they
// show up in `gotermrec info`) rather than enforced; replaying a session at
// a different size can reflow differently than the original.

import (
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-term/internal/recfmt"
)

// Playback speed bounds for the interactive +/- controls. Past these the
// playback is either a slideshow or a single flash, so clamp.
const (
	minReplaySpeed = 1.0 / 32
	maxReplaySpeed = 32
)

// ReplayCfg configures NewReplay.
type ReplayCfg struct {
	// Path is the .gtr recording to play. Required.
	Path string

	// Speed multiplies playback rate: 2 plays twice as fast. Zero or
	// negative means 1 (real time).
	Speed float64

	// IdleLimit caps any single gap between frames, so a session with a
	// coffee break in the middle stays watchable. Zero means no cap.
	IdleLimit time.Duration

	// Loop restarts from the beginning at end of stream instead of holding
	// on the final frame.
	Loop bool

	// Controls interprets keystrokes as playback commands rather than
	// discarding them: space pauses/resumes, +/- change speed, . or Right
	// steps one frame, 0 restarts.
	Controls bool
}

// replayPTY feeds a recording to a Term through the ptyIO interface.
type replayPTY struct {
	f    *os.File
	rr   *recfmt.Reader
	cond *sync.Cond

	// done is closed by Close. It unblocks the inter-frame sleep and the
	// end-of-stream park.
	done chan struct{}

	// sleep replaces the real timer in tests, so playback timing can be
	// asserted without spending the wall-clock time.
	sleep func(time.Duration)

	// pending holds the tail of a frame that did not fit in the caller's
	// buffer. Copied out of the reader's buffer, which the next frame
	// overwrites. Reader-goroutine only.
	pending []byte

	hdr recfmt.Header

	idle time.Duration

	// speed is a float64 in atomic bits: written by Write (main thread, via
	// the +/- controls) and read by the reader goroutine.
	speed atomic.Uint64

	mu sync.Mutex // guards paused, steps, closed; paired with cond

	// steps counts frames released while paused, by the step control.
	steps int

	// restartReq is set by the '0' control and consumed by the reader
	// goroutine. The rewind itself must happen there: rr and pending belong
	// to Read, and seeking them from the main thread would be a race.
	restartReq bool

	controls bool
	loop     bool
	paused   bool
	closed   bool
}

// errStepped signals gate that the frame was released by a step command, so
// its inter-frame delay should be skipped: stepping should feel immediate.
var errStepped = errors.New("stepped")

// openReplay opens a recording and prepares it for playback.
func openReplay(rc ReplayCfg) (*replayPTY, error) {
	if rc.Path == "" {
		return nil, errors.New("term: replay: no path")
	}
	f, err := os.Open(rc.Path)
	if err != nil {
		return nil, err
	}
	rr, err := recfmt.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	p := &replayPTY{
		f:        f,
		rr:       rr,
		hdr:      rr.Header(),
		idle:     rc.IdleLimit,
		loop:     rc.Loop,
		controls: rc.Controls,
		done:     make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	p.setSpeed(rc.Speed)
	return p, nil
}

// NewReplay returns a Term that plays back a recorded session instead of
// spawning a shell. cfg is honored as for New except that no child process
// exists: Cfg.Command, Args, Env, and Dir are ignored.
//
// The Term stays alive on the final frame rather than tearing down at end of
// stream, so the last screen remains on display; Close ends playback.
func NewReplay(w *gui.Window, cfg Cfg, rc ReplayCfg) (*Term, error) {
	p, err := openReplay(rc)
	if err != nil {
		return nil, err
	}
	t, err := newWithPTY(w, cfg, p)
	if err != nil {
		_ = p.Close()
		return nil, err
	}
	return t, nil
}

// setSpeed clamps and stores the playback rate.
func (p *replayPTY) setSpeed(s float64) {
	if s <= 0 || math.IsNaN(s) {
		s = 1
	}
	p.speed.Store(math.Float64bits(min(max(s, minReplaySpeed), maxReplaySpeed)))
}

// speedVal returns the current playback rate.
func (p *replayPTY) speedVal() float64 { return math.Float64frombits(p.speed.Load()) }

// Read delivers the next chunk of recorded output, first sleeping out the
// gap that separated it from the previous frame. Input ('i') and resize
// ('r') frames are skipped: they are context for a human reader, not part of
// the byte stream the emulator consumed.
//
// A frame larger than b (readLoop's buffer is 4 KiB) is returned in pieces
// across successive calls. That is not merely allowed but useful: a short
// read tells readLoop the burst has drained, which is exactly when a
// trailing grapheme cluster should be flushed.
func (p *replayPTY) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(p.pending) > 0 {
		n := copy(b, p.pending)
		p.pending = p.pending[n:]
		return n, nil
	}
	for {
		if p.takeRestart() {
			if err := p.restart(); err != nil {
				return 0, p.park() // unseekable: hold rather than tear down
			}
		}
		f, err := p.rr.Next()
		if err != nil {
			// Clean EOF or a truncated tail (a recording of a crash is
			// still worth watching): either way the stream is over.
			if p.loop && p.restart() == nil {
				continue
			}
			return 0, p.park()
		}
		if err := p.wait(f.Delta); err != nil {
			return 0, err
		}
		if f.Kind != recfmt.KindOutput {
			continue
		}
		n := copy(b, f.Data)
		if n < len(f.Data) {
			// f.Data aliases the reader's buffer, which the next frame
			// overwrites — copy the remainder out before returning.
			p.pending = append(p.pending[:0], f.Data[n:]...)
		}
		return n, nil
	}
}

// wait sleeps out one inter-frame gap, honoring pause, speed, and the idle
// cap. Returns io.EOF if Close happened while waiting.
func (p *replayPTY) wait(d time.Duration) error {
	switch err := p.gate(); {
	case errors.Is(err, errStepped):
		return nil // stepping advances immediately
	case err != nil:
		return err
	}
	if p.idle > 0 && d > p.idle {
		d = p.idle
	}
	if s := p.speedVal(); s > 0 {
		d = time.Duration(float64(d) / s)
	}
	if d <= 0 {
		return nil
	}
	if p.sleep != nil {
		p.sleep(d) // test hook: no real time passes
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-p.done:
		return io.EOF
	}
}

// gate blocks while playback is paused. It returns errStepped when the step
// control released this one frame, and io.EOF when Close happened.
func (p *replayPTY) gate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.paused && p.steps == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.closed {
		return io.EOF
	}
	if p.paused && p.steps > 0 {
		p.steps--
		return errStepped
	}
	return nil
}

// park holds at the end of the stream until Close. Returning io.EOF here
// instead would end readLoop, fire Cfg.OnExit, and — under a pane manager —
// close the pane the user is still looking at.
func (p *replayPTY) park() error {
	<-p.done
	return io.EOF
}

// restart seeks back to the first frame. Used by Loop and by the '0'
// control.
func (p *replayPTY) restart() error {
	if _, err := p.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rr, err := recfmt.NewReader(p.f)
	if err != nil {
		return err
	}
	p.rr = rr
	p.pending = nil
	return nil
}

// Write interprets playback controls when ReplayCfg.Controls is set, and
// otherwise discards. Keystrokes already funnel to the pty writer, so
// routing them here gives interactive playback control without the widget
// needing to know replay exists.
//
// Reports the full length as written either way: a replay pane must never
// look like a pty with a full input buffer.
func (p *replayPTY) Write(b []byte) (int, error) {
	if !p.controls {
		return len(b), nil
	}
	// Right arrow (CSI C) steps, matching the '.' key. Checked before the
	// byte loop so its ESC and '[' are not read as separate commands.
	if string(b) == "\x1b[C" {
		p.step()
		return len(b), nil
	}
	for _, c := range b {
		switch c {
		case ' ':
			p.togglePause()
		case '+', '=':
			p.setSpeed(p.speedVal() * 2)
		case '-', '_':
			p.setSpeed(p.speedVal() / 2)
		case '.':
			p.step()
		case '0':
			p.requestRestart()
		}
	}
	return len(b), nil
}

// togglePause pauses or resumes playback.
func (p *replayPTY) togglePause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = !p.paused
	p.cond.Broadcast()
}

// isPaused reports whether playback is currently paused.
func (p *replayPTY) isPaused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// requestRestart asks the reader goroutine to rewind before its next frame.
// A step is granted alongside it so a paused playback still shows frame one.
func (p *replayPTY) requestRestart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restartReq = true
	if p.paused {
		p.steps++
	}
	p.cond.Broadcast()
}

// takeRestart consumes a pending restart request. Reader goroutine only.
func (p *replayPTY) takeRestart() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	req := p.restartReq
	p.restartReq = false
	return req
}

// step releases exactly one frame and leaves playback paused.
func (p *replayPTY) step() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = true
	p.steps++
	p.cond.Broadcast()
}

// Resize is a no-op: a recording has no child process to inform. The pane's
// own geometry drives the grid, as it does for a live pty.
func (p *replayPTY) Resize(int, int) error { return nil }

// PID reports 0 — there is no child process.
func (p *replayPTY) PID() int { return 0 }

// Close ends playback, unblocking a parked or sleeping Read.
func (p *replayPTY) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.done)
	p.cond.Broadcast()
	p.mu.Unlock()
	return p.f.Close()
}
