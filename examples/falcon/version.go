package main

import (
	"runtime/debug"
	"strings"
)

// version is the build version shown in the About dialog. Stamped at link time
// by the Makefile from `git describe --tags`, so a tagged release reports its
// tag verbatim and an in-between build reports "v0.6.0-3-g1a2b3c4[-dirty]".
//
// Left empty on purpose rather than defaulting to a hand-maintained literal: a
// stale constant that looks like a real version is worse than an honest
// fallback, and this file previously carried one that sat at 0.1.0 while the
// repo shipped v0.6.0.
var version string

// appVersion resolves the version string for display. Preference order:
//
//  1. the linker-stamped tag (Makefile builds — the shipping path);
//  2. the module version, which is set when the binary came from
//     `go install …/examples/falcon@v0.6.0`;
//  3. the VCS revision Go embeds into any `go build` inside a git checkout;
//  4. "dev", for `go run` and -buildvcs=false builds, where nothing is known.
func appVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	// "(devel)" is what the toolchain reports for a locally built main
	// module — not a version, so fall through to the VCS stamps.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return vcsVersion(info)
}

// vcsVersion builds a short revision string from the vcs.* settings the Go
// toolchain embeds. Returns "dev" when the build carried none (the module
// cache, a tarball, or -buildvcs=false).
func vcsVersion(info *debug.BuildInfo) string {
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	// Abbreviate to the usual 7-character short hash; a 40-character SHA
	// would blow out the About dialog's width.
	if len(rev) > 7 {
		rev = rev[:7]
	}
	var b strings.Builder
	b.WriteString("dev-")
	b.WriteString(rev)
	if dirty {
		b.WriteString("-dirty")
	}
	return b.String()
}
