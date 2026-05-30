// Command script2fixture converts a typescript file (produced by the
// Unix `script` command) into a replay fixture for the go-term test suite.
//
// Usage:
//
//	go run ./term/script2fixture \
//	  -name cursor_moves \
//	  -script fixture.script \
//	  -rows 24 -cols 80 \
//	  -out term/testdata/
//
// The tool feeds the typescript bytes through a fresh Grid+Parser,
// captures the final state, and writes a .json fixture.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mike-ward/go-term/term"
)

func main() {
	name := flag.String("name", "", "fixture name (used as filename: <name>.json)")
	script := flag.String("script", "", "path to typescript file from script(1)")
	rows := flag.Int("rows", 24, "terminal rows")
	cols := flag.Int("cols", 80, "terminal columns")
	out := flag.String("out", ".", "output directory for the .json fixture")
	flag.Parse()

	if *name == "" || *script == "" {
		fmt.Fprintf(os.Stderr, "usage: script2fixture -name <name> -script <path> [-rows 24] [-cols 80] [-out .]\n")
		os.Exit(2)
	}

	input, err := os.ReadFile(*script)
	if err != nil {
		fmt.Fprintf(os.Stderr, "script2fixture: read %s: %v\n", *script, err)
		os.Exit(1)
	}

	f := term.CaptureFixture(*name, *rows, *cols, input)

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "script2fixture: marshal: %v\n", err)
		os.Exit(1)
	}

	path := filepath.Join(*out, *name+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "script2fixture: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "script2fixture: write %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d rows × %d cols, %d input bytes)\n",
		path, *rows, *cols, len(input))
}
