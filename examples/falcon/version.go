package main

import (
	"regexp"
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

// releaseVersion is the latest tagged release (the tag verbatim, leading
// "v" included). It names the release line an unstamped dev build came
// from, so the About dialog can show it when appVersion knows only a
// VCS hash or nothing at all. Bump it in the same commit that adds the
// CHANGELOG entry — TestReleaseVersionMatchesChangelog fails otherwise.
// (A hand-maintained literal sat stale at 0.1.0 once before; the test is
// what makes this one safe to keep.)
const releaseVersion = "v0.12.0"

// aboutVersion resolves the version string for the About dialog. A
// linker-stamped tag, a post-tag describe string, or a module-proxy
// version already names its release, so those are shown verbatim.
// A build that names no release falls back to the release line plus a
// dev marker: "v0.12.0 (dev)". Which revision that dev build came from is
// the Commit row's job, not this string's.
func aboutVersion() string {
	return aboutVersionFor(appVersion())
}

// aboutVersionFor applies the About display rule to one version string.
// Split out so tests can cover the rule without stubbing the linker
// stamp or the toolchain's build info.
func aboutVersionFor(v string) string {
	if v == releaseVersion || strings.HasPrefix(v, releaseVersion+"-") {
		return v
	}
	// A module-proxy version (go install …@vX.Y.Z) names its own
	// release, even when it is older than the checkout's line.
	if strings.HasPrefix(v, "v") {
		return v
	}
	return releaseVersion + " (" + v + ")"
}

// pseudoVersionRe matches a Go module pseudo-version by its tail: a 14-digit
// UTC timestamp and a 12-character revision prefix. The base version in front
// varies (a bare "v0.0.0", a "-0." bump, or a "-rc.1.0." pre-release bump), so
// the tail is what identifies the shape, plus the optional "+dirty" build
// metadata a modified tree adds after it. Anchored at the end, and no tag
// ends this way.
var pseudoVersionRe = regexp.MustCompile(
	`^v[0-9]+\.[0-9]+\.[0-9]+[-.0-9A-Za-z]*[-.][0-9]{14}-` +
		`[0-9a-f]{12}(\+[0-9A-Za-z.-]+)?$`)

// isPseudoVersion reports whether v is a synthesized module version rather
// than a version anyone tagged.
func isPseudoVersion(v string) bool { return pseudoVersionRe.MatchString(v) }

// appVersion resolves the build's own version string. Preference order:
//
//  1. the linker-stamped tag (Makefile builds — the shipping path);
//  2. the module version, which is set when the binary came from
//     `go install …/examples/falcon@v0.6.0`;
//  3. "dev", for every local build — the revision it came from is
//     reported separately by buildCommit.
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
	//
	// A pseudo-version is rejected the same way. Recent toolchains
	// synthesize one ("v0.12.1-0.20260912165223-4b26046b8092") for any
	// build inside a tagged checkout, so this branch would otherwise
	// capture ordinary local builds and print a 40-character string
	// that names a release the repo has never tagged. The VCS stamps
	// below say the same thing honestly and in a tenth the width.
	if v := info.Main.Version; v != "" && v != "(devel)" &&
		!isPseudoVersion(v) {
		return v
	}
	// The revision is not folded in here: the About dialog gives the
	// commit its own row, so a "dev-1a2b3c4-dirty" version string would
	// print the same hash twice.
	return "dev"
}

// commitLen is how much of the revision hash the About dialog shows.
// Long enough to be unambiguous in a repo this size, short enough not to
// widen the dialog.
const commitLen = 9

// buildInfo is the toolchain's embedded build record, or nil when there is
// none. Every accessor below funnels through it so the pure *From variants
// stay testable without stubbing the toolchain.
func buildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}

// buildCommit returns the full git revision the binary was built from, or
// "" when the toolchain embedded none (a module-cache build, a tarball, or
// -buildvcs=false). The About dialog turns it into a link to the commit,
// so the full hash matters even though only commitLen characters show.
func buildCommit() string { return settingValue(buildInfo(), "vcs.revision") }

// buildDirty reports whether the working tree had uncommitted changes at
// build time. Shown next to the commit so a local build never looks like a
// clean checkout of that revision.
func buildDirty() bool {
	return settingValue(buildInfo(), "vcs.modified") == "true"
}

// buildDate returns the commit timestamp as a plain YYYY-MM-DD date, or ""
// when the build carried no VCS stamp. Falcon has no CI build counter to
// show where Ghostty shows one, and the date answers the same question —
// "how old is this binary" — without inventing a number.
func buildDate() string { return buildDateFrom(buildInfo()) }

// buildDateFrom applies the date rule to one build record.
func buildDateFrom(info *debug.BuildInfo) string {
	t := settingValue(info, "vcs.time")
	if t == "" {
		return ""
	}
	// vcs.time is RFC 3339 in UTC. Trim to the date rather than parse:
	// the dialog shows no clock, and a parse failure would have to be
	// handled for no gain.
	if i := strings.IndexByte(t, 'T'); i > 0 {
		return t[:i]
	}
	return t
}

// settingValue reads one vcs.* key out of the embedded build settings.
// A nil record has no settings, which reads as "unknown" everywhere.
func settingValue(info *debug.BuildInfo, key string) string {
	if info == nil {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}
