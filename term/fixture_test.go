package term

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// loadFixtures reads all .json fixture files from testdata/.
func loadFixtures(t *testing.T) []Fixture {
	t.Helper()
	ents, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var out []Fixture
	for _, e := range ents {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var f Fixture
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
func decodeFixtureInput(t *testing.T, f Fixture) []byte {
	t.Helper()
	input, err := base64.StdEncoding.DecodeString(f.InputB64)
	if err != nil {
		t.Fatalf("decode input for %s: %v", f.Name, err)
	}
	return input
}
