package term

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fixture is a replay-test scenario stored on disk. Input bytes are
// base64-encoded so control characters survive any text editor round-trip.
type fixture struct {
	Name      string   `json:"name"`
	Rows      int      `json:"rows"`
	Cols      int      `json:"cols"`
	InputB64  string   `json:"input_b64"`
	WantLines []string `json:"want_lines"`
	WantRow   int      `json:"want_row"`
	WantCol   int      `json:"want_col"`
	WantTitle string   `json:"want_title,omitempty"`
	WantCwd   string   `json:"want_cwd,omitempty"`
}

// loadFixtures reads all .json fixture files from testdata/.
func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var out []fixture
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var f fixture
		if err := json.Unmarshal(b, &f); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if f.Rows <= 0 {
			f.Rows = 24
		}
		if f.Cols <= 0 {
			f.Cols = 80
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Skip("no fixture files in testdata/")
	}
	return out
}

// decodeFixtureInput returns the decoded input bytes for a fixture.
func decodeFixtureInput(t *testing.T, f fixture) []byte {
	t.Helper()
	input, err := base64.StdEncoding.DecodeString(f.InputB64)
	if err != nil {
		t.Fatalf("decode input for %s: %v", f.Name, err)
	}
	return input
}

