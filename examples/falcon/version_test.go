package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

// TestAppVersion_StampWins covers the shipping path: whatever the Makefile
// linked in is reported verbatim, with no build-info lookup behind it.
func TestAppVersion_StampWins(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v1.2.3"
	if got := appVersion(); got != "v1.2.3" {
		t.Errorf("appVersion() = %q, want v1.2.3", got)
	}
}

// TestAppVersion_Unstamped guards the fallback chain against returning an
// empty string — "Version " with nothing after it is the one output the About
// dialog must never show.
func TestAppVersion_Unstamped(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	if got := appVersion(); got == "" {
		t.Error("appVersion() is empty with no stamp; want a fallback")
	}
}

// TestAboutVersionFor covers the About display rule: builds that already
// name a release show verbatim, while VCS/dev fallbacks gain the release
// line so the dialog never shows a bare hash or "dev".
func TestAboutVersionFor(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"stamped release shows verbatim", releaseVersion, releaseVersion},
		{
			"post-tag describe string shows verbatim",
			releaseVersion + "-7-gc61ccae-dirty",
			releaseVersion + "-7-gc61ccae-dirty",
		},
		// A go install at an older tag names its own release.
		{"module version shows verbatim", "v0.9.0", "v0.9.0"},
		{
			"vcs fallback gains the release line",
			"dev-1a2b3c4",
			releaseVersion + " (dev-1a2b3c4)",
		},
		{"bare dev gains the release line", "dev", releaseVersion + " (dev)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := aboutVersionFor(tt.in); got != tt.want {
				t.Errorf("aboutVersionFor(%q) = %q, want %q",
					tt.in, got, tt.want)
			}
		})
	}
}

// TestAboutVersion_DialogInvariant guards what showAbout renders: the
// dialog must name a version on every build path — never empty, never a
// bare "dev". The unstamped half depends on the toolchain's build info,
// so it asserts the invariant rather than an exact string.
func TestAboutVersion_DialogInvariant(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v9.9.9"
	if got := aboutVersion(); got != "v9.9.9" {
		t.Errorf("aboutVersion() stamped = %q, want v9.9.9", got)
	}

	version = ""
	got := aboutVersion()
	if got == "" || got == "dev" {
		t.Errorf("aboutVersion() unstamped = %q, want a release-naming string", got)
	}
	if !strings.Contains(got, releaseVersion) && !strings.HasPrefix(got, "v") {
		t.Errorf("aboutVersion() unstamped = %q, want %q or a v-prefixed version",
			got, releaseVersion)
	}
}

// TestReleaseVersionMatchesChangelog keeps the releaseVersion literal in
// step with the newest CHANGELOG entry. Bump both in the release commit;
// a literal that drifts from the changelog is the stale-constant trap
// version.go documents.
func TestReleaseVersionMatchesChangelog(t *testing.T) {
	path := filepath.Join("..", "..", "CHANGELOG.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "## [") {
			continue
		}
		rest := strings.TrimPrefix(line, "## [")
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			t.Fatalf("malformed changelog header: %q", line)
		}
		ver := rest[:end]
		if ver == "Unreleased" {
			continue
		}
		if want := "v" + ver; want != releaseVersion {
			t.Errorf("releaseVersion = %q, changelog newest is %q",
				releaseVersion, want)
		}
		return
	}
	t.Fatal("no versioned ## [x.y.z] entry in CHANGELOG.md")
}

// TestBuildDateFrom covers the Built row's value: an RFC 3339 stamp shows as
// a bare date, and every shape that carries no stamp yields "" so the row is
// dropped rather than rendered empty.
func TestBuildDateFrom(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{{
		name: "rfc3339 stamp trims to the date",
		info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.time", Value: "2026-09-12T10:04:22Z"},
		}},
		want: "2026-09-12",
	}, {
		name: "date-only stamp passes through",
		info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.time", Value: "2026-09-12"},
		}},
		want: "2026-09-12",
	}, {
		name: "no vcs settings",
		info: &debug.BuildInfo{},
		want: "",
	}, {
		name: "no build info at all",
		info: nil,
		want: "",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildDateFrom(tt.info); got != tt.want {
				t.Errorf("buildDateFrom() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSettingValue_NilInfo guards the accessors against a build with no
// embedded record: they must report "unknown", not panic.
func TestSettingValue_NilInfo(t *testing.T) {
	if got := settingValue(nil, "vcs.revision"); got != "" {
		t.Errorf("settingValue(nil) = %q, want empty", got)
	}
}

// TestCommitLenFitsShortHash keeps the About dialog's abbreviation inside a
// real hash: a commitLen past 40 would slice out of range.
func TestCommitLenFitsShortHash(t *testing.T) {
	if commitLen < 7 || commitLen > 40 {
		t.Errorf("commitLen = %d, want 7..40", commitLen)
	}
}

// TestIsPseudoVersion keeps the module-version branch of appVersion from
// capturing a synthesized version. A pseudo-version names a release nobody
// tagged and is far too wide for the dialog, so it must fall through to the
// release line plus "dev".
func TestIsPseudoVersion(t *testing.T) {
	pseudo := []string{
		"v0.12.1-0.20260912165223-4b26046b8092",
		"v0.0.0-20260101000000-000000000000",
		"v1.2.3-rc.1.0.20260912165223-4b26046b8092",
		// A modified tree appends build metadata after the hash.
		"v0.12.1-0.20260912165223-4b26046b8092+dirty",
	}
	for _, v := range pseudo {
		if !isPseudoVersion(v) {
			t.Errorf("isPseudoVersion(%q) = false, want true", v)
		}
	}
	real := []string{
		"v0.12.0",
		"v0.12.0-3-g1a2b3c4",
		"v1.0.0-rc.1",
		"dev",
		"",
	}
	for _, v := range real {
		if isPseudoVersion(v) {
			t.Errorf("isPseudoVersion(%q) = true, want false", v)
		}
	}
}

// TestAppVersion_NoBareHash locks in the split the About dialog's Commit row
// created: the version string never carries a revision, so the two rows can
// never print the same hash twice.
func TestAppVersion_NoBareHash(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = ""
	if got := appVersion(); strings.Contains(got, "dev-") {
		t.Errorf("appVersion() = %q, want no revision suffix", got)
	}
}
