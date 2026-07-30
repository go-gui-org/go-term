package main

import (
	"runtime/debug"
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
