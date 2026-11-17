// Command gotermrec inspects, plays, and converts go-term session
// recordings (.gtr files produced by falcon --record, Cfg.RecordPath, or
// Term.StartRecording).
//
// Usage:
//
//	gotermrec info    <file.gtr>                       header, frame counts, duration
//	gotermrec cat     <file.gtr>                       raw output bytes to stdout
//	gotermrec play    <file.gtr> [-speed 2] [-idle 2s] timed playback to stdout
//	gotermrec fixture <file.gtr> -name X [-rows] [-cols] [-out dir]
//	gotermrec export  <file.gtr> -cast out.cast        convert to asciicast v2
//	gotermrec import-cast <file.cast> [-out session.gtr] convert from asciicast v2
//
// `cat` reproduces the byte stream exactly, which is what the GOTERM_CAPTURE
// debugging workflow wants: pipe it into a reference terminal to decide
// whether a rendering bug belongs to go-term or to the application that
// produced the bytes.
//
// `play` needs no GUI, so a recording can be reviewed over SSH. To watch one
// inside go-term itself — with scrollback, selection, and search — use
// `falcon --replay <file.gtr>`.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-gui-org/go-term/internal/recfmt"
	"github.com/go-gui-org/go-term/term"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "info":
		err = cmdInfo(args)
	case "cat":
		err = cmdCat(args)
	case "play":
		err = cmdPlay(args)
	case "fixture":
		err = cmdFixture(args)
	case "export":
		err = cmdExport(args)
	case "import-cast":
		err = cmdImportCast(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "gotermrec: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gotermrec %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: gotermrec <command> <file.gtr> [flags]

commands:
  info     print header, frame counts, and duration
  cat      write the raw recorded output bytes to stdout
  play     replay to stdout with the original timing
  fixture  convert to a replay-test fixture (.json)
  export   convert to asciicast v2 (-cast out.cast)
  import-cast convert from asciicast v2 (-out session.gtr)

run "gotermrec <command> -h" for that command's flags
`)
}

// openArg pulls the recording path out of args (it is positional and must
// come first), parses the remaining flags, and opens the file.
func openArg(fs *flag.FlagSet, args []string) (*os.File, *recfmt.Reader, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return nil, nil, flag.ErrHelp
		}
		return nil, nil, errors.New("no recording given")
	}
	path := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	rr, err := recfmt.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, rr, nil
}

// eachFrame walks every frame, calling fn. A truncated tail is reported to
// stderr rather than failing the command: a recording cut short by the crash
// it was capturing is still the useful part.
func eachFrame(rr *recfmt.Reader, fn func(recfmt.Frame) error) error {
	for {
		fr, err := rr.Next()
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case errors.Is(err, io.ErrUnexpectedEOF):
			fmt.Fprintln(os.Stderr, "gotermrec: recording is truncated; showing what survived")
			return nil
		case err != nil:
			return err
		}
		if err := fn(fr); err != nil {
			return err
		}
	}
}

// cmdInfo summarises a recording without rendering it.
func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	f, rr, err := openArg(fs, args)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	hdr := rr.Header()
	var (
		total              time.Duration
		bytesOut           int64
		nOut, nIn, nResize int
		lastRows, lastCols = hdr.Height, hdr.Width
	)
	err = eachFrame(rr, func(fr recfmt.Frame) error {
		total += fr.Delta
		switch fr.Kind {
		case recfmt.KindOutput:
			nOut++
			bytesOut += int64(len(fr.Data))
		case recfmt.KindInput:
			nIn++
		case recfmt.KindResize:
			nResize++
			lastRows, lastCols = fr.Rows, fr.Cols
		}
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("file:      %s\n", f.Name())
	fmt.Printf("version:   %d\n", hdr.Goterm)
	fmt.Printf("size:      %d×%d (cols×rows) at start\n", hdr.Width, hdr.Height)
	if nResize > 0 {
		fmt.Printf("final size: %d×%d after %d resize(s)\n", lastCols, lastRows, nResize)
	}
	if hdr.Timestamp > 0 {
		fmt.Printf("recorded:  %s\n", time.Unix(hdr.Timestamp, 0).Format(time.RFC3339))
	}
	for _, k := range []string{"SHELL", "TERM"} {
		if v := hdr.Env[k]; v != "" {
			fmt.Printf("%-10s %s\n", strings.ToLower(k)+":", v)
		}
	}
	fmt.Printf("duration:  %s\n", total.Round(time.Millisecond))
	fmt.Printf("frames:    %d output, %d input, %d resize\n", nOut, nIn, nResize)
	fmt.Printf("output:    %d bytes\n", bytesOut)
	return nil
}

// cmdCat writes the recorded output bytes with no timing and no framing —
// byte-for-byte what the pty produced.
func cmdCat(args []string) error {
	fs := flag.NewFlagSet("cat", flag.ContinueOnError)
	f, rr, err := openArg(fs, args)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return eachFrame(rr, func(fr recfmt.Frame) error {
		if fr.Kind != recfmt.KindOutput {
			return nil
		}
		_, werr := os.Stdout.Write(fr.Data)
		return werr
	})
}

// cmdPlay writes the recorded output to stdout at the original pace.
func cmdPlay(args []string) error {
	fs := flag.NewFlagSet("play", flag.ContinueOnError)
	speed := fs.Float64("speed", 1, "playback speed multiplier")
	idle := fs.Duration("idle-limit", 0, "cap on any single gap between frames (0 = none)")
	f, rr, err := openArg(fs, args)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if *speed <= 0 {
		*speed = 1
	}
	return eachFrame(rr, func(fr recfmt.Frame) error {
		d := fr.Delta
		if *idle > 0 && d > *idle {
			d = *idle
		}
		if d > 0 {
			time.Sleep(time.Duration(float64(d) / *speed))
		}
		if fr.Kind != recfmt.KindOutput {
			return nil
		}
		_, werr := os.Stdout.Write(fr.Data)
		return werr
	})
}

// cmdFixture converts a recording into a replay-test fixture, the same
// artefact script2fixture produces from a script(1) typescript.
func cmdFixture(args []string) error {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	name := fs.String("name", "", "fixture name (used as filename: <name>.json)")
	rows := fs.Int("rows", 24, "terminal rows")
	cols := fs.Int("cols", 80, "terminal columns")
	out := fs.String("out", ".", "output directory for the .json fixture")
	f, rr, err := openArg(fs, args)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if *name == "" {
		return errors.New("-name is required")
	}

	var input []byte
	if err := eachFrame(rr, func(fr recfmt.Frame) error {
		if fr.Kind == recfmt.KindOutput {
			input = append(input, fr.Data...)
		}
		return nil
	}); err != nil {
		return err
	}

	fix := term.CaptureFixture(*name, *rows, *cols, input)
	b, err := json.MarshalIndent(fix, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(*out, *name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d rows × %d cols, %d input bytes)\n", path, *rows, *cols, len(input))
	return nil
}

// cmdExport converts to asciicast v2 for sharing — asciinema play, the web
// player, agg for a GIF.
//
// This direction is lossy by construction: asciicast events are JSON
// strings, so bytes that are not valid UTF-8 cannot survive. Rather than
// corrupt them silently, each affected frame is reported on stderr.
func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	cast := fs.String("cast", "", "output asciicast v2 file (required)")
	f, rr, err := openArg(fs, args)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if *cast == "" {
		return errors.New("-cast <out.cast> is required")
	}

	out, err := os.Create(*cast)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	hdr := rr.Header()
	castHdr := map[string]any{
		"version": 2,
		"width":   hdr.Width,
		"height":  hdr.Height,
	}
	if hdr.Timestamp > 0 {
		castHdr["timestamp"] = hdr.Timestamp
	}
	if len(hdr.Env) > 0 {
		castHdr["env"] = hdr.Env
	}
	enc := json.NewEncoder(out)
	if err := enc.Encode(castHdr); err != nil {
		return err
	}

	var (
		elapsed  time.Duration
		n, lossy int
	)
	err = eachFrame(rr, func(fr recfmt.Frame) error {
		elapsed += fr.Delta
		var code string
		switch fr.Kind {
		case recfmt.KindOutput:
			code = "o"
		case recfmt.KindInput:
			code = "i"
		default:
			return nil // resize frames have no asciicast v2 equivalent
		}
		if !utf8.Valid(fr.Data) {
			lossy++
			fmt.Fprintf(os.Stderr,
				"gotermrec: frame %d at %s contains invalid UTF-8; "+
					"asciicast cannot carry it verbatim\n", n, elapsed.Round(time.Millisecond))
		}
		n++
		// The event is a 3-element array [time, code, data]; encoding/json
		// replaces invalid UTF-8 with U+FFFD, which is exactly the loss
		// reported above.
		return enc.Encode([]any{elapsed.Seconds(), code, string(fr.Data)})
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d events", *cast, n)
	if lossy > 0 {
		fmt.Fprintf(os.Stderr, ", %d with invalid UTF-8 replaced", lossy)
	}
	fmt.Fprintln(os.Stderr, ")")
	return nil
}

// cmdImportCast converts an asciicast v2 file back into a .gtr recording —
// the reverse of cmdExport. External repros and demo libraries held as
// asciicast (docs pipelines, bug reports) re-enter the ecosystem this way:
// the result feeds falcon --replay, cat/play, and fixture capture.
//
// The import streams the cast twice. Pass 1 validates the whole file
// (version, geometry, monotonic times, resize payloads, the 16 MiB event
// bound) and scans for input frames, which NewRecorder needs to know up
// front; buffering every frame instead would trade bounded memory for one
// fewer pass. Pass 2 re-decodes and writes the frames. Frame timing comes
// from the Recorder's wall clock — recfmt offers no delta setter and
// sleeping through idle gaps would stall the import — so cast times are
// validated but not reproduced as deltas.
//
// Lossy like cmdExport, and honest about it: markers are dropped and unknown
// event codes skipped, each counted in one per-file stderr warning.
func cmdImportCast(args []string) error {
	fs := flag.NewFlagSet("import-cast", flag.ContinueOnError)
	out := fs.String("out", "", "output .gtr file (default: input with .cast replaced by .gtr)")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fs.Usage()
			return flag.ErrHelp
		}
		return errors.New("no asciicast file given")
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	inPath := args[0]
	outPath := *out
	if outPath == "" {
		outPath = strings.TrimSuffix(inPath, ".cast") + ".gtr"
	}

	hdr, env, st, err := scanCast(inPath)
	if err != nil {
		return err
	}
	if st.droppedEnv > 0 {
		fmt.Fprintf(os.Stderr, "gotermrec: dropped %d non-string env entries (no .gtr equivalent)\n",
			st.droppedEnv)
	}

	rec, err := recfmt.NewRecorder(outPath, hdr.Height, hdr.Width, env, st.hasInput)
	if err != nil {
		return err
	}
	if err := writeCastFrames(inPath, rec); err != nil {
		_ = rec.Close()
		return err
	}
	if err := rec.Close(); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d output, %d input, %d resize",
		outPath, st.nOut, st.nIn, st.nResize)
	if st.nMarkers > 0 {
		fmt.Fprintf(os.Stderr, ", %d markers dropped", st.nMarkers)
	}
	if st.nUnknown > 0 {
		fmt.Fprintf(os.Stderr, ", %d unknown skipped", st.nUnknown)
	}
	fmt.Fprintln(os.Stderr, ")")
	if st.nMarkers > 0 {
		fmt.Fprintf(os.Stderr,
			"gotermrec: dropped %d marker events; asciicast markers have no .gtr equivalent\n",
			st.nMarkers)
	}
	if st.nUnknown > 0 {
		fmt.Fprintf(os.Stderr,
			"gotermrec: skipped %d events with unknown codes; the spec reserves them for extensions\n",
			st.nUnknown)
	}
	if st.hasInput {
		fmt.Fprintln(os.Stderr,
			"gotermrec: input frames present; the .gtr may carry passwords (owner-only 0600)")
	}
	return nil
}

// castHeader is the first JSON value of an asciicast v2 file. Timestamp is
// informational (NewRecorder stamps the import with now) but decoded so a
// mistyped field fails fast rather than silently.
type castHeader struct {
	Version   int            `json:"version"`
	Width     int            `json:"width"`
	Height    int            `json:"height"`
	Timestamp float64        `json:"timestamp"`
	Env       map[string]any `json:"env"`
}

// castStats tallies one pass over a cast's events.
type castStats struct {
	nOut, nIn, nResize int
	nMarkers, nUnknown int
	droppedEnv         int
	hasInput           bool
}

// decodeCastHeader reads and validates the first JSON value of a cast.
// Geometry is load-bearing for NewRecorder, so out-of-range values are
// rejected rather than clamped (the 1<<16 bound mirrors recfmt's maxDim,
// which is unexported).
func decodeCastHeader(dec *json.Decoder, path string) (castHeader, error) {
	var hdr castHeader
	if err := dec.Decode(&hdr); err != nil {
		return hdr, fmt.Errorf("%s: bad asciicast header: %w", path, err)
	}
	if hdr.Version != 2 {
		return hdr, fmt.Errorf("%s: unsupported asciicast version %d (want 2)", path, hdr.Version)
	}
	if hdr.Width < 1 || hdr.Height < 1 || hdr.Width > 1<<16 || hdr.Height > 1<<16 {
		return hdr, fmt.Errorf("%s: bad geometry %dx%d (want 1..65536 cols×rows)",
			path, hdr.Width, hdr.Height)
	}
	return hdr, nil
}

// parseCastEvent validates one decoded `[time, code, data]` envelope. prev
// is the previous event's time; times must be non-negative and
// non-decreasing, and known codes must carry string data within MaxFrame.
// Unknown codes keep no data — the caller skips them — but their raw
// payload is still bounded so a hostile extension event cannot blow up
// memory (raw length is a conservative proxy: it includes JSON framing).
// JSON has no NaN literal, but a hostile number is rejected explicitly all
// the same.
func parseCastEvent(raw []json.RawMessage, path string, index int, prev float64) (code, data string, now float64, err error) {
	if len(raw) != 3 {
		return "", "", prev, fmt.Errorf("%s: event %d has %d fields, want 3", path, index, len(raw))
	}
	var t float64
	if err := json.Unmarshal(raw[0], &t); err != nil {
		return "", "", prev, fmt.Errorf("%s: event %d has a non-numeric time", path, index)
	}
	if math.IsNaN(t) || t < 0 {
		return "", "", prev, fmt.Errorf("%s: event %d has invalid time %v", path, index, t)
	}
	if t < prev {
		return "", "", prev, fmt.Errorf("%s: event %d goes back in time (%v after %v)", path, index, t, prev)
	}
	if err := json.Unmarshal(raw[1], &code); err != nil {
		return "", "", prev, fmt.Errorf("%s: event %d has a non-string code", path, index)
	}
	if len(raw[2]) > recfmt.MaxFrame {
		return "", "", prev, fmt.Errorf("%s: event %d exceeds %d bytes", path, index, recfmt.MaxFrame)
	}
	switch code {
	case "o", "i", "m", "r":
		if err := json.Unmarshal(raw[2], &data); err != nil {
			return "", "", prev, fmt.Errorf("%s: event %d %q has non-string data", path, index, code)
		}
		if len(data) > recfmt.MaxFrame {
			return "", "", prev, fmt.Errorf("%s: event %d exceeds %d bytes", path, index, recfmt.MaxFrame)
		}
	}
	return code, data, t, nil
}

// parseResizePayload splits an "r" payload of the form "COLSxROWS"
// (asciicast order: columns first) into rows and cols. Both must be
// positive; absurd-but-positive values survive, since the .gtr reader
// clamps those on read anyway.
func parseResizePayload(payload, path string, index int) (rows, cols int, err error) {
	parts := strings.Split(payload, "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%s: event %d has malformed resize payload %q (want \"COLSxROWS\")",
			path, index, payload)
	}
	cols, cerr := strconv.Atoi(parts[0])
	rows, rerr := strconv.Atoi(parts[1])
	if cerr != nil || rerr != nil {
		return 0, 0, fmt.Errorf("%s: event %d has malformed resize payload %q (want \"COLSxROWS\")",
			path, index, payload)
	}
	if rows < 1 || cols < 1 {
		return 0, 0, fmt.Errorf("%s: event %d has non-positive resize %q", path, index, payload)
	}
	return rows, cols, nil
}

// scanCast validates a whole cast file and tallies its events without
// retaining them: pass 1 of cmdImportCast. No output is created, so a
// malformed file fails before any .gtr exists.
func scanCast(path string) (castHeader, map[string]string, castStats, error) {
	var st castStats
	f, err := os.Open(path)
	if err != nil {
		return castHeader{}, nil, st, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	hdr, err := decodeCastHeader(dec, path)
	if err != nil {
		return hdr, nil, st, err
	}
	env := make(map[string]string, len(hdr.Env))
	for k, v := range hdr.Env {
		s, ok := v.(string)
		if !ok {
			st.droppedEnv++
			continue
		}
		env[k] = s
	}
	var prev float64
	for index := 0; ; index++ {
		var raw []json.RawMessage
		if derr := dec.Decode(&raw); derr != nil {
			if errors.Is(derr, io.EOF) {
				return hdr, env, st, nil
			}
			return hdr, nil, st, fmt.Errorf("%s: bad event %d: %w", path, index, derr)
		}
		code, data, t, perr := parseCastEvent(raw, path, index, prev)
		if perr != nil {
			return hdr, nil, st, perr
		}
		prev = t
		switch code {
		case "o":
			st.nOut++
		case "i":
			st.nIn++
			st.hasInput = true
		case "r":
			if _, _, rerr := parseResizePayload(data, path, index); rerr != nil {
				return hdr, nil, st, rerr
			}
			st.nResize++
		case "m":
			st.nMarkers++
		default:
			st.nUnknown++
		}
	}
}

// writeCastFrames re-decodes a validated cast and appends its frames: pass 2
// of cmdImportCast. Every event re-validates; only a file changed between
// the two passes can fail here.
func writeCastFrames(path string, rec *recfmt.Recorder) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(f)
	if _, err := decodeCastHeader(dec, path); err != nil {
		return err
	}
	var prev float64
	for index := 0; ; index++ {
		var raw []json.RawMessage
		if derr := dec.Decode(&raw); derr != nil {
			if errors.Is(derr, io.EOF) {
				return nil
			}
			return fmt.Errorf("%s: bad event %d: %w", path, index, derr)
		}
		code, data, t, perr := parseCastEvent(raw, path, index, prev)
		if perr != nil {
			return perr
		}
		prev = t
		switch code {
		case "o":
			rec.Output([]byte(data))
		case "i":
			rec.Input([]byte(data))
		case "r":
			rows, cols, rerr := parseResizePayload(data, path, index)
			if rerr != nil {
				return rerr
			}
			rec.Resize(rows, cols)
		}
	}
}
