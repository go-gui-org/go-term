package term

import "testing"

func TestShortcuts_AllFieldsPopulated(t *testing.T) {
	got := Shortcuts()
	if len(got) == 0 {
		t.Fatal("Shortcuts() returned no entries")
	}
	for i, s := range got {
		if s.Label == "" {
			t.Errorf("entry %d has empty Label", i)
		}
		if s.Keys == "" {
			t.Errorf("entry %d (%q) has empty Keys", i, s.Label)
		}
	}
}

func TestShortcuts_LabelsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Shortcuts() {
		if seen[s.Label] {
			t.Errorf("duplicate label %q", s.Label)
		}
		seen[s.Label] = true
	}
}
