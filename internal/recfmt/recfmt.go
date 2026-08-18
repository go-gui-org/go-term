// Package recfmt implements the ".gtr" session-recording container used by
// go-term's record and replay features.
//
// It is internal on purpose: the file format is a support artefact, not part
// of the widget's public API. Both the term package (which writes recordings
// and plays them back through a Term) and the gotermrec command (which
// inspects, plays, and converts them) sit on top of it.
//
// The container:
//
// A recording is a JSON header line followed by binary frames:
//
//	{"goterm":1,"width":80,"height":24,"timestamp":1785000000,"env":{…}}\n
//	'o' <uvarint dtMicros> <uvarint n> <n raw bytes>     output
//	'i' <uvarint dtMicros> <uvarint n> <n raw bytes>     input (opt-in)
//	'r' <uvarint dtMicros> <uvarint rows> <uvarint cols> resize
//
// Payloads are the exact bytes the pty master delivered — no transcoding,
// no escaping. That is the whole point of not reusing asciicast v2, whose
// JSON string events cannot carry malformed UTF-8; malformed byte sequences
// are precisely the emulator bugs worth filing a recording about. Export to
// asciicast is a separate, lossy step (see term/gotermrec).
//
// The header key names deliberately mirror asciicast v2 so that export is a
// re-serialisation rather than a translation. The "goterm" key — where
// asciicast has "version":2 — is what a format sniffer keys on.
//
// Concatenating every 'o' payload reproduces the raw byte stream exactly, so
// a recording still feeds `cat`-into-a-reference-terminal debugging and the
// CaptureFixture / EmulatorReplay harness (`gotermrec cat`).
package recfmt

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Frame kinds. Single bytes, chosen to be readable in a hex dump and to
// match asciicast's "o"/"i" event codes.
const (
	KindOutput = 'o'
	KindInput  = 'i'
	KindResize = 'r'
)

// FormatVersion is the value of the header's "goterm" key. Bump only for
// a breaking container change; new optional header fields do not need it.
const FormatVersion = 1

// Decoder limits. A recording is untrusted input the moment it is shared in
// a bug report, so both the header line and each frame payload are bounded
// before any allocation happens (same reasoning as maxWorkspaceSize in
// workspace/persist.go).
const (
	MaxHeader = 64 << 10 // 64 KiB — a real header is ~150 bytes
	MaxFrame  = 16 << 20 // 16 MiB — one pty read is 4 KiB; graphics blobs are larger
)

// maxDim bounds a decoded resize frame. Deliberately looser than
// term.MaxGridDim: this package must not depend on the widget, and the value
// only has to keep a hostile uvarint out of arithmetic that assumes sane
// dimensions.
const maxDim = 1 << 16

// ErrHeaderTooLong reports a header line past MaxHeader with no newline.
var ErrHeaderTooLong = errors.New("term: recording header too long")

// Header is the JSON object on line 1 of a recording.
type Header struct {
	// Env carries the recording environment (SHELL, TERM). Informational:
	// replay does not apply it.
	Env map[string]string `json:"env,omitempty"`

	// Goterm is the container version. Its presence distinguishes a .gtr
	// file from an asciicast, which uses "version" instead.
	Goterm int `json:"goterm"`

	// Width/Height are the grid size when recording started. Advisory —
	// replay renders at whatever size the host pane happens to be.
	Width  int `json:"width"`
	Height int `json:"height"`

	// Timestamp is the Unix start time in seconds, as in asciicast v2.
	Timestamp int64 `json:"timestamp"`
}

// Frame is one decoded frame. Data aliases the reader's internal buffer
// and is only valid until the next call to Reader.next — copy it if it
// must outlive that.
type Frame struct {
	Data  []byte        // 'o' / 'i' payload; nil for 'r'
	Delta time.Duration // wall-clock gap since the previous frame
	Rows  int           // 'r' only
	Cols  int           // 'r' only
	Kind  byte
}

// ---------------------------------------------------------------------------
// Recorder (write side)
// ---------------------------------------------------------------------------

// Recorder appends frames to an open file. Three goroutines feed one
// Recorder — the pty reader ('o'), resizeLoop ('r'), and the main thread
// ('i') — so writes are serialised by mu. Contention is nil in practice: a
// frame is one buffer append plus one write syscall.
//
// Every method is nil-receiver safe, so the hot-path hooks read
// `rec.output(b)` with no extra branch beyond the load.
//
// There is deliberately no bufio between the frames and the file. A
// recording exists to explain a crash; a buffered tail lost to that very
// crash would defeat the purpose.
type Recorder struct {
	f     *os.File
	start time.Time // recording start, for the on-screen elapsed timer

	// last is when the previous frame was written; the next frame's delta
	// is measured from it. Guarded by mu.
	last time.Time

	// buf is the reusable frame-assembly buffer: header varints and payload
	// are appended here so the file sees one Write per frame. Reused across
	// frames, so a steady-state recording allocates nothing. Guarded by mu.
	buf []byte

	path string

	mu sync.Mutex

	// raw makes this a payload-only tee — the GOTERM_CAPTURE debug format,
	// which is a bare byte stream with no header and no framing.
	raw bool

	// input enables 'i' frames. Off by default: keystroke capture records
	// whatever the user typed, including anything typed into a password
	// prompt, so it must be asked for explicitly.
	input bool
}

// NewRawRecorder wraps an already-open file as a payload-only tee. This is
// the GOTERM_CAPTURE debug format: raw pty bytes, nothing else.
func NewRawRecorder(f *os.File) *Recorder {
	if f == nil {
		return nil
	}
	return &Recorder{f: f, path: f.Name(), raw: true, start: time.Now(), last: time.Now()}
}

// NewRecorder creates path and writes the header line. rows/cols describe the
// grid at this instant; env is copied into the header for context.
// path is always supplied by the calling process's own CLI/API/config
// (Cfg.RecordPath, Term.StartRecording, gotermrec) — never by terminal
// output — so it crosses no trust boundary (Aikido SAST).
func NewRecorder(path string, rows, cols int, env map[string]string, input bool) (*Recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	hdr, err := json.Marshal(Header{
		Goterm:    FormatVersion,
		Width:     cols,
		Height:    rows,
		Timestamp: now.Unix(),
		Env:       env,
	})
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Write(append(hdr, '\n')); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Recorder{f: f, path: path, start: now, last: now, input: input}, nil
}

// output records a chunk of pty output.
func (r *Recorder) Output(b []byte) { r.writeFrame(KindOutput, b, 0, 0) }

// recordInput records bytes sent to the child (keystrokes, paste). No-op
// unless the recording was opened with input capture enabled.
func (r *Recorder) Input(b []byte) {
	if r == nil || !r.input {
		return
	}
	r.writeFrame(KindInput, b, 0, 0)
}

// resize records a new grid geometry.
func (r *Recorder) Resize(rows, cols int) { r.writeFrame(KindResize, nil, rows, cols) }

// writeFrame assembles one frame and hands it to the file in a single write.
// A write failure disables the Recorder rather than stalling pty throughput:
// losing a debug artefact is always preferable to wedging the terminal.
func (r *Recorder) writeFrame(kind byte, data []byte, rows, cols int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return // never opened, already closed, or disabled by an earlier error
	}
	// Raw tee: no framing, no timing — just the bytes, exactly as today's
	// GOTERM_CAPTURE files. Written straight from the caller's slice so the
	// debug path costs no extra copy.
	if r.raw {
		if kind != KindOutput {
			return
		}
		r.write(data)
		return
	}
	now := time.Now()
	delta := now.Sub(r.last)
	if delta < 0 {
		delta = 0 // clock went backwards; monotonic time makes this unreachable
	}
	r.last = now

	buf := append(r.buf[:0], kind)
	buf = binary.AppendUvarint(buf, uint64(delta.Microseconds()))
	if kind == KindResize {
		buf = binary.AppendUvarint(buf, uint64(max(rows, 0)))
		buf = binary.AppendUvarint(buf, uint64(max(cols, 0)))
	} else {
		buf = binary.AppendUvarint(buf, uint64(len(data)))
		buf = append(buf, data...)
	}
	r.buf = buf // keep the grown capacity for the next frame
	r.write(buf)
}

// write performs the file write and disables the Recorder on failure.
// Caller holds mu.
func (r *Recorder) write(b []byte) {
	if _, err := r.f.Write(b); err != nil {
		log.Printf("term: recording %s: %v", r.path, err)
		_ = r.f.Close()
		r.f = nil
	}
}

// close finishes the recording. Safe on a nil or already-closed Recorder.
func (r *Recorder) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// Path reports the file this recorder writes to. Empty for a nil Recorder.
func (r *Recorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Active reports whether the Recorder still holds an open file. False for a
// nil Recorder, after Close, and after a write failure disabled it.
func (r *Recorder) Active() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f != nil
}

// elapsed reports how long this recording has been running, for the
// on-screen indicator. Zero for a nil Recorder.
func (r *Recorder) Elapsed() time.Duration {
	if r == nil {
		return 0
	}
	return time.Since(r.start)
}

// ---------------------------------------------------------------------------
// Reader (read side)
// ---------------------------------------------------------------------------

// Reader streams frames out of a recording. Construct with NewReader,
// then call next until it returns io.EOF.
type Reader struct {
	br  *bufio.Reader
	buf []byte // reusable payload buffer; frames alias it
	hdr Header
}

// NewReader reads and validates the header line, leaving the reader
// positioned at the first frame.
func NewReader(r io.Reader) (*Reader, error) {
	// The bufio buffer is sized to MaxHeader precisely so that an
	// over-long first line surfaces as ErrBufferFull instead of growing
	// without bound on a garbage file.
	br := bufio.NewReaderSize(r, MaxHeader)
	line, err := br.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, ErrHeaderTooLong
	}
	if err != nil {
		return nil, fmt.Errorf("term: recording header: %w", err)
	}
	var hdr Header
	if err := json.Unmarshal(line, &hdr); err != nil {
		return nil, fmt.Errorf("term: recording header: %w", err)
	}
	if hdr.Goterm != FormatVersion {
		return nil, fmt.Errorf("term: unsupported recording version %d (want %d)",
			hdr.Goterm, FormatVersion)
	}
	return &Reader{br: br, hdr: hdr}, nil
}

// header returns the parsed header line.
func (rr *Reader) Header() Header { return rr.hdr }

// next decodes the following frame. It returns io.EOF at a clean frame
// boundary and io.ErrUnexpectedEOF (wrapped) for a truncated tail — the
// caller decides whether a partial recording is still worth showing. A
// returned frame's Data aliases the reader's buffer until the next call.
func (rr *Reader) Next() (Frame, error) {
	kind, err := rr.br.ReadByte()
	if err != nil {
		return Frame{}, err // io.EOF here means a clean end of stream
	}
	micros, err := binary.ReadUvarint(rr.br)
	if err != nil {
		return Frame{}, truncated(err)
	}
	f := Frame{Kind: kind, Delta: time.Duration(micros) * time.Microsecond}
	switch kind {
	case KindResize:
		rows, err := binary.ReadUvarint(rr.br)
		if err != nil {
			return Frame{}, truncated(err)
		}
		cols, err := binary.ReadUvarint(rr.br)
		if err != nil {
			return Frame{}, truncated(err)
		}
		// Clamp rather than reject: a silly geometry in a shared recording
		// should not make the whole file unreadable. Resize frames are
		// advisory anyway — replay renders at the host pane's size.
		f.Rows, f.Cols = int(min(rows, maxDim)), int(min(cols, maxDim))
	case KindOutput, KindInput:
		n, err := binary.ReadUvarint(rr.br)
		if err != nil {
			return Frame{}, truncated(err)
		}
		if n > MaxFrame {
			return Frame{}, fmt.Errorf("term: recording frame too large (%d bytes)", n)
		}
		if uint64(cap(rr.buf)) < n {
			rr.buf = make([]byte, n)
		}
		rr.buf = rr.buf[:n]
		if _, err := io.ReadFull(rr.br, rr.buf); err != nil {
			return Frame{}, truncated(err)
		}
		f.Data = rr.buf
	default:
		return Frame{}, fmt.Errorf("term: unknown recording frame kind %#x", kind)
	}
	return f, nil
}

// truncated maps a mid-frame EOF to ErrUnexpectedEOF so callers can tell a
// clean end of stream from a recording cut short by a crash.
func truncated(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
