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
	"os"
	"path/filepath"
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
