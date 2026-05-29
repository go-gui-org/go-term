//go:build fixture_capture

package term

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestCaptureFixture records a terminal input stream for later replay.
// Gated behind the "fixture_capture" build tag so ordinary test runs
// never write to testdata/. Run manually:
//
//	go test -tags fixture_capture -run TestCaptureFixture -count=1 ./term
//
// Edit name, rows, cols, and input below, run the test, and the fixture
// will be written to testdata/.
func TestCaptureFixture(t *testing.T) {
	// Set name, rows, cols, and input here, then run the test.
	name := "example"
	rows := 3
	cols := 20
	input := []byte("hello\x1b[2;3HX\x1b[K")

	g := newGrid(rows, cols)
	p := newParser(g)
	var gotTitle string
	p.SetTitleHandler(func(s string) { gotTitle = s })
	feed(t, g, p, input)

	f := fixture{
		Name:      name,
		Rows:      rows,
		Cols:      cols,
		InputB64:  base64.StdEncoding.EncodeToString(input),
		WantLines: gridLines(g),
		WantRow:   g.CursorR,
		WantCol:   g.CursorC,
		WantTitle: gotTitle,
		WantCwd:   g.Cwd,
	}

	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", name+".json")
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", path)
}
