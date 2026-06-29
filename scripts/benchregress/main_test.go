package main

import "testing"

// findMetric returns the metricResult for the named metric of the named
// benchmark, or nil if absent.
func findMetric(r report, bench, metric string) *metricResult {
	for _, bc := range r.Benchmarks {
		if bc.Name != bench {
			continue
		}
		for i := range bc.Metrics {
			if bc.Metrics[i].Metric == metric {
				return &bc.Metrics[i]
			}
		}
	}
	return nil
}

// A large ns/op regression must not fail the build: ns/op is advisory because
// the shared CI fleet's CPU varies per run. The metric is still reported.
func TestNsPerOpRegressionIsAdvisory(t *testing.T) {
	base := []benchSummary{{Name: "BenchmarkX", NsPerOp: 1000, BytesPerOp: 0, AllocsPerOp: 0}}
	cur := []benchSummary{{Name: "BenchmarkX", NsPerOp: 2000, BytesPerOp: 0, AllocsPerOp: 0}}

	r := compare(base, cur, 30, nil)

	if r.Summary.ExitCode != 0 {
		t.Fatalf("ns/op +100%% must not fail build; got exit code %d", r.Summary.ExitCode)
	}
	if r.Summary.Regressed != 0 {
		t.Fatalf("ns/op swing must not count as regression; got %d", r.Summary.Regressed)
	}
	m := findMetric(r, "BenchmarkX", "ns/op")
	if m == nil || !m.Advisory {
		t.Fatalf("ns/op metric should be present and advisory: %+v", m)
	}
}

// allocs/op and B/op are deterministic across machines and stay hard-gated.
func TestAllocAndBytesRegressionsStillFail(t *testing.T) {
	for _, tc := range []struct {
		name string
		cur  benchSummary
	}{
		{"allocs", benchSummary{Name: "BenchmarkX", NsPerOp: 1000, AllocsPerOp: 5}},
		{"bytes", benchSummary{Name: "BenchmarkX", NsPerOp: 1000, BytesPerOp: 4096}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := []benchSummary{{Name: "BenchmarkX", NsPerOp: 1000, BytesPerOp: 0, AllocsPerOp: 0}}
			r := compare(base, []benchSummary{tc.cur}, 30, nil)
			if r.Summary.ExitCode != 1 {
				t.Fatalf("%s regression must fail build; got exit code %d", tc.name, r.Summary.ExitCode)
			}
		})
	}
}

// The zero-alloc hard gate still trips when a tagged benchmark allocates.
func TestZeroAllocGateStillFires(t *testing.T) {
	base := []benchSummary{{Name: "BenchmarkX", NsPerOp: 1000, AllocsPerOp: 0}}
	cur := []benchSummary{{Name: "BenchmarkX", NsPerOp: 1000, AllocsPerOp: 1}}
	r := compare(base, cur, 30, []string{"BenchmarkX"})
	if r.Summary.ExitCode != 1 || r.Summary.ZeroAllocFails != 1 {
		t.Fatalf("zero-alloc gate must fire: exit=%d zeroAllocFails=%d", r.Summary.ExitCode, r.Summary.ZeroAllocFails)
	}
}

// The reproduced CI failure: a stable, reproducible ns/op gap from running on a
// different CPU than the baseline must now produce a green build.
func TestScrollUpRegressionScenarioPasses(t *testing.T) {
	base := []benchSummary{{Name: "BenchmarkGrid_ScrollUpRegion_FullScreen", NsPerOp: 1712}}
	cur := []benchSummary{{Name: "BenchmarkGrid_ScrollUpRegion_FullScreen", NsPerOp: 2275}}
	r := compare(base, cur, 30, nil)
	if r.Summary.ExitCode != 0 {
		t.Fatalf("ScrollUp +32.9%% ns/op scenario must pass; got exit code %d", r.Summary.ExitCode)
	}
}
