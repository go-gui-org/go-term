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

func TestVCSVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{{
		name: "clean revision abbreviates to a short hash",
		info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1a2b3c4d5e6f7a8b9c0d"},
			{Key: "vcs.modified", Value: "false"},
		}},
		want: "dev-1a2b3c4",
	}, {
		name: "modified tree is marked dirty",
		info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "1a2b3c4d5e6f7a8b9c0d"},
			{Key: "vcs.modified", Value: "true"},
		}},
		want: "dev-1a2b3c4-dirty",
	}, {
		// -buildvcs=false, a module-cache build, or a source tarball.
		name: "no vcs settings",
		info: &debug.BuildInfo{},
		want: "dev",
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vcsVersion(tt.info); got != tt.want {
				t.Errorf("vcsVersion() = %q, want %q", got, tt.want)
			}
		})
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
