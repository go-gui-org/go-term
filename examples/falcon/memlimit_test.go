package main

import (
	"testing"
)

// withMemLimit runs f with the -X memLimit stamp set to stamp and any
// GOMEMLIMIT env forced empty, restoring both afterwards.
func withMemLimit(t *testing.T, stamp string, f func()) {
	old := memLimit
	t.Cleanup(func() { memLimit = old })
	t.Setenv("GOMEMLIMIT", "")
	memLimit = stamp
	f()
}

// TestResolveMemLimit_EnvWins guards the precedence rule: an explicit
// GOMEMLIMIT env var must never be overridden by the linker stamp or the
// default, because SetMemoryLimit would otherwise clobber the runtime's own
// env handling (and its "512MiB" spelling).
func TestResolveMemLimit_EnvWins(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "256MiB")
	memLimit = "12345"
	if _, ok := resolveMemLimit(); ok {
		t.Error("resolveMemLimit() = ok with GOMEMLIMIT set; want not ok")
	}
}

// TestResolveMemLimit_Default covers the dev-build path: no stamp, no env,
// so the in-code default governs.
func TestResolveMemLimit_Default(t *testing.T) {
	withMemLimit(t, "", func() {
		limit, ok := resolveMemLimit()
		if !ok {
			t.Fatal("resolveMemLimit() = not ok; want the default installed")
		}
		if limit != int64(defaultMemLimit) {
			t.Errorf("resolveMemLimit() = %d, want default %d", limit, int64(defaultMemLimit))
		}
	})
}

// TestResolveMemLimit_StampWins covers the shipping path: whatever the
// Makefile linked in is applied verbatim.
func TestResolveMemLimit_StampWins(t *testing.T) {
	withMemLimit(t, "268435456", func() {
		limit, ok := resolveMemLimit()
		if !ok {
			t.Fatal("resolveMemLimit() = not ok; want the stamp installed")
		}
		if limit != 268435456 {
			t.Errorf("resolveMemLimit() = %d, want 268435456", limit)
		}
	})
}

// TestResolveMemLimit_InvalidStamps guards the fallback: a stamp that is not
// a positive integer must never reach debug.SetMemoryLimit (a negative value
// would mean "no limit" to the runtime; a parse failure would panic nothing
// but would silently ship a wrong limit).
func TestResolveMemLimit_InvalidStamps(t *testing.T) {
	for _, stamp := range []string{"abc", "1.5", "0", "-1"} {
		t.Run(stamp, func(t *testing.T) {
			withMemLimit(t, stamp, func() {
				limit, ok := resolveMemLimit()
				if !ok {
					t.Fatal("resolveMemLimit() = not ok; want fallback to default")
				}
				if limit != int64(defaultMemLimit) {
					t.Errorf("resolveMemLimit() = %d, want default %d", limit, int64(defaultMemLimit))
				}
			})
		})
	}
}
