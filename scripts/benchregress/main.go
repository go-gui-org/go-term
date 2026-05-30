// benchregress compares go test -bench output against a pre-computed baseline
// and reports regressions beyond a configurable threshold.
//
// Two modes:
//
//	-update         read raw benchmark output from stdin, write normalized
//	                baseline to stdout.
//
//	(default)       compare -current file against -base file; exit 0 if
//	                all benchmarks pass, 1 if any regression, 2 on error.
//
// Usage:
//
//	# Generate baseline from N runs.
//	go test -bench=. -count=10 -benchmem ./term \
//	  | benchregress -update > baseline.txt
//
//	# Check regressions.
//	go test -bench=. -count=10 -benchmem ./term > current.txt
//	benchregress -base baseline.txt -current current.txt
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

const usage = `usage: benchregress [-update] [-base FILE] [-current FILE] [flags]

In -update mode, read raw 'go test -bench' output from stdin and write
normalized baseline to stdout.

In compare mode (default), compare -current against -base and report
regressions. Exit 0 if all benchmarks pass, 1 if any regression, 2 on error.

Flags:
`

func main() {
	fs := flag.NewFlagSet("benchregress", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	update := fs.Bool("update", false, "read raw benchmark output from stdin, write normalized baseline to stdout")
	basePath := fs.String("base", ".github/benchmarks/baseline.txt", "path to baseline file")
	currentPath := fs.String("current", "/tmp/bench-current.txt", "path to current benchmark output")
	threshold := fs.Float64("threshold", 10.0, "regression threshold in percent")
	zeroAlloc := fs.String("zero-alloc", "BenchmarkForegroundPass", "comma-separated benchmark names requiring zero allocs")
	jsonOut := fs.Bool("json", false, "output in JSON format")
	_ = fs.Parse(os.Args[1:]) // flag.ExitOnError handles errors; linter needs explicit ignore.

	if *update {
		if err := runUpdate(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "benchregress: %v\n", err)
			os.Exit(2)
		}
		return
	}

	zeroAllocNames := parseZeroAlloc(*zeroAlloc)
	exitCode, err := runCompare(*basePath, *currentPath, *threshold, zeroAllocNames, *jsonOut, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "benchregress: %v\n", err)
		os.Exit(2)
	}
	os.Exit(exitCode)
}

func parseZeroAlloc(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

type benchRun struct {
	Name        string // e.g. "BenchmarkForegroundPass"
	Iterations  int
	NsPerOp     float64
	BytesPerOp  int64
	AllocsPerOp int64
}

type benchSummary struct {
	Name        string
	NsPerOp     float64 // arithmetic mean
	BytesPerOp  float64 // arithmetic mean
	AllocsPerOp float64 // arithmetic mean
}

type metricResult struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	DeltaPct float64 `json:"delta_pct"`
	Pass     bool    `json:"pass"`
	Message  string  `json:"message,omitempty"`
}

type benchCmp struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"` // "pass", "regression", "new", "missing", "zero-alloc-fail"
	Metrics []metricResult `json:"metrics,omitempty"`
}

type report struct {
	Baseline   string     `json:"baseline"`
	Benchmarks []benchCmp `json:"benchmarks"`
	Summary    summary    `json:"summary"`
}

type summary struct {
	Total            int `json:"total"`
	Passed           int `json:"passed"`
	Regressed        int `json:"regressed"`
	New              int `json:"new"`
	Missing          int `json:"missing"`
	ZeroAllocFails   int `json:"zero_alloc_fails"`
	ZeroAllocChecked int `json:"zero_alloc_checked"`
	ExitCode         int `json:"exit_code"`
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

// Matches lines like:
//
//	BenchmarkForegroundPass-14    1000000    1048 ns/op    0 B/op    0 allocs/op
//	BenchmarkParserFeed_PlainText-14  200000  6500 ns/op  629.29 MB/s  0 B/op  0 allocs/op
var benchLineRe = regexp.MustCompile(
	`^(\S+)\s+(\d+)\s+([\d.]+)\s+ns/op(?:\s+[\d.]+\s+MB/s)?\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op`,
)

// stripProcSuffix removes the -N GOMAXPROCS suffix from a benchmark name.
func stripProcSuffix(name string) string {
	idx := strings.LastIndexByte(name, '-')
	if idx < 0 {
		return name
	}
	suffix := name[idx+1:]
	if suffix == "" {
		return name
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return name // not a GOMAXPROCS suffix
		}
	}
	return name[:idx]
}

// parseBenchOutput parses raw go test -bench output and returns individual runs.
func parseBenchOutput(r io.Reader) ([]benchRun, error) {
	var runs []benchRun
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "goos:") ||
			strings.HasPrefix(line, "goarch:") ||
			strings.HasPrefix(line, "pkg:") ||
			strings.HasPrefix(line, "cpu:") ||
			strings.HasPrefix(line, "ok ") ||
			strings.HasPrefix(line, "FAIL") ||
			strings.HasPrefix(line, "? ") {
			continue
		}
		matches := benchLineRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}
		name := stripProcSuffix(matches[1])
		iters, _ := strconv.Atoi(matches[2])
		nsPerOp, _ := strconv.ParseFloat(matches[3], 64)
		bytesPerOp, _ := strconv.ParseInt(matches[4], 10, 64)
		allocsPerOp, _ := strconv.ParseInt(matches[5], 10, 64)
		runs = append(runs, benchRun{
			Name:        name,
			Iterations:  iters,
			NsPerOp:     nsPerOp,
			BytesPerOp:  bytesPerOp,
			AllocsPerOp: allocsPerOp,
		})
	}
	return runs, sc.Err()
}

// summarize groups runs by name and computes arithmetic means.
func summarize(runs []benchRun) []benchSummary {
	m := make(map[string][]benchRun)
	var order []string
	for _, r := range runs {
		if _, ok := m[r.Name]; !ok {
			order = append(order, r.Name)
		}
		m[r.Name] = append(m[r.Name], r)
	}
	out := make([]benchSummary, 0, len(order))
	for _, name := range order {
		rs := m[name]
		var nsSum, bSum, aSum float64
		for _, r := range rs {
			nsSum += r.NsPerOp
			bSum += float64(r.BytesPerOp)
			aSum += float64(r.AllocsPerOp)
		}
		n := float64(len(rs))
		out = append(out, benchSummary{
			Name:        name,
			NsPerOp:     nsSum / n,
			BytesPerOp:  bSum / n,
			AllocsPerOp: aSum / n,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Baseline format parser
// ---------------------------------------------------------------------------

// parseBaseline reads the normalized baseline format.
func parseBaseline(r io.Reader) ([]benchSummary, error) {
	var out []benchSummary
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		nsPerOp, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			continue
		}
		bytesPerOp, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			continue
		}
		allocsPerOp, err := strconv.ParseFloat(fields[3], 64)
		if err != nil {
			continue
		}
		out = append(out, benchSummary{
			Name:        fields[0],
			NsPerOp:     nsPerOp,
			BytesPerOp:  bytesPerOp,
			AllocsPerOp: allocsPerOp,
		})
	}
	return out, sc.Err()
}

// writeBaseline writes normalized baseline format.
func writeBaseline(w io.Writer, summaries []benchSummary) error {
	for _, s := range summaries {
		// Round allocs/bytes to integers for clean diffs.
		_, err := fmt.Fprintf(w, "%s %.0f %.0f %.0f\n",
			s.Name, math.Round(s.NsPerOp),
			math.Round(s.BytesPerOp), math.Round(s.AllocsPerOp))
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Comparison logic
// ---------------------------------------------------------------------------

func compare(base, current []benchSummary, threshold float64, zeroAlloc []string) report {
	baseMap := make(map[string]benchSummary, len(base))
	for _, s := range base {
		baseMap[s.Name] = s
	}
	curMap := make(map[string]benchSummary, len(current))
	for _, s := range current {
		curMap[s.Name] = s
	}

	zeroAllocSet := make(map[string]bool, len(zeroAlloc))
	for _, n := range zeroAlloc {
		zeroAllocSet[n] = true
	}

	// Collect all names in deterministic order.
	allNames := make(map[string]bool)
	for _, s := range base {
		allNames[s.Name] = true
	}
	for _, s := range current {
		allNames[s.Name] = true
	}
	names := make([]string, 0, len(allNames))
	for n := range allNames {
		names = append(names, n)
	}
	sort.Strings(names)

	var r report
	r.Baseline = "baseline"
	for _, name := range names {
		baseS, inBase := baseMap[name]
		curS, inCur := curMap[name]

		if inBase && inCur {
			bc := compareBench(name, baseS, curS, threshold, zeroAllocSet)
			r.Benchmarks = append(r.Benchmarks, bc)
		} else if inCur {
			r.Benchmarks = append(r.Benchmarks, benchCmp{
				Name:   name,
				Status: "new",
			})
		} else {
			r.Benchmarks = append(r.Benchmarks, benchCmp{
				Name:   name,
				Status: "missing",
			})
		}
	}

	// Summary.
	for _, bc := range r.Benchmarks {
		r.Summary.Total++
		switch bc.Status {
		case "pass":
			r.Summary.Passed++
		case "regression":
			r.Summary.Regressed++
		case "zero-alloc-fail":
			r.Summary.Regressed++
			r.Summary.ZeroAllocFails++
		case "new":
			r.Summary.New++
			r.Summary.Passed++ // new benchmarks are not regressions
		case "missing":
			r.Summary.Missing++
			r.Summary.Passed++ // missing benchmarks are not regressions
		}
		if _, ok := zeroAllocSet[bc.Name]; ok {
			r.Summary.ZeroAllocChecked++
		}
	}
	if r.Summary.Regressed > 0 {
		r.Summary.ExitCode = 1
	}
	return r
}

func compareBench(name string, base, cur benchSummary, threshold float64, zeroAllocSet map[string]bool) benchCmp {
	bc := benchCmp{Name: name, Status: "pass"}

	// Zero-alloc hard gate: checked before percentage thresholds.
	if zeroAllocSet[bc.Name] && cur.AllocsPerOp > 0 {
		bc.Status = "zero-alloc-fail"
		bc.Metrics = append(bc.Metrics, metricResult{
			Metric:   "allocs/op",
			Baseline: base.AllocsPerOp,
			Current:  cur.AllocsPerOp,
			DeltaPct: math.Inf(1),
			Pass:     false,
			Message:  "zero-alloc benchmark has allocations — regressed from 0 to >0",
		})
		return bc
	}

	metrics := []struct {
		name     string
		baseline float64
		current  float64
	}{
		{"ns/op", base.NsPerOp, cur.NsPerOp},
		{"B/op", base.BytesPerOp, cur.BytesPerOp},
		{"allocs/op", base.AllocsPerOp, cur.AllocsPerOp},
	}

	for _, m := range metrics {
		mr := compareMetric(threshold, m.name, m.baseline, m.current)
		bc.Metrics = append(bc.Metrics, mr)
		if !mr.Pass {
			bc.Status = "regression"
		}
	}
	return bc
}

func compareMetric(threshold float64, name string, baseline, current float64) metricResult {
	mr := metricResult{
		Metric:   name,
		Baseline: baseline,
		Current:  current,
		Pass:     true,
	}

	switch {
	case baseline == 0 && current == 0:
		mr.DeltaPct = 0
	case baseline == 0 && current > 0:
		mr.DeltaPct = math.Inf(1)
		mr.Pass = false
		mr.Message = "regressed from zero baseline"
	default:
		mr.DeltaPct = ((current - baseline) / baseline) * 100
		if mr.DeltaPct > threshold {
			mr.Pass = false
			mr.Message = fmt.Sprintf("regressed by %.1f%% (threshold: %.0f%%)", mr.DeltaPct, threshold)
		}
	}
	return mr
}

// ---------------------------------------------------------------------------
// Run modes
// ---------------------------------------------------------------------------

func runUpdate(in io.Reader, out io.Writer) error {
	runs, err := parseBenchOutput(in)
	if err != nil {
		return fmt.Errorf("parsing stdin: %w", err)
	}
	if len(runs) == 0 {
		return fmt.Errorf("no benchmark lines found in stdin")
	}
	summaries := summarize(runs)
	// Write header. Write errors on stdout are ignored — if stdout is
	// broken the shell will report it.
	_, _ = fmt.Fprintf(out, "# go-term benchmark baseline\n")
	_, _ = fmt.Fprintf(out, "# go test -bench=. -count=%d -benchmem ./term\n", len(runs)/len(summaries))
	_, _ = io.WriteString(out, "# Each line: Name  NsPerOp  BytesPerOp  AllocsPerOp\n")
	return writeBaseline(out, summaries)
}

func runCompare(basePath, currentPath string, threshold float64, zeroAlloc []string, jsonOut bool, stdout io.Writer) (int, error) {
	bf, err := os.Open(basePath)
	if err != nil {
		return 2, fmt.Errorf("reading baseline %s: %w", basePath, err)
	}
	defer func() { _ = bf.Close() }()

	base, err := parseBaseline(bf)
	if err != nil {
		return 2, fmt.Errorf("parsing baseline %s: %w", basePath, err)
	}
	if len(base) == 0 {
		return 2, fmt.Errorf("no benchmarks found in baseline %s", basePath)
	}

	cf, err := os.Open(currentPath)
	if err != nil {
		return 2, fmt.Errorf("reading current %s: %w", currentPath, err)
	}
	defer func() { _ = cf.Close() }()

	currentRuns, err := parseBenchOutput(cf)
	if err != nil {
		return 2, fmt.Errorf("parsing current %s: %w", currentPath, err)
	}
	if len(currentRuns) == 0 {
		return 2, fmt.Errorf("no benchmark lines found in current %s", currentPath)
	}
	current := summarize(currentRuns)

	rep := compare(base, current, threshold, zeroAlloc)
	rep.Baseline = basePath

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			return 2, fmt.Errorf("encoding JSON: %w", err)
		}
	} else {
		printTextReport(stdout, rep)
	}
	return rep.Summary.ExitCode, nil
}

// ---------------------------------------------------------------------------
// Text reporter
// ---------------------------------------------------------------------------

// printlnf is a small helper to avoid repetitive errcheck noise for stdout
// writes. If stdout fails the process is dying anyway.
func printlnf(w io.Writer, format string, args ...interface{}) {
	_, _ = fmt.Fprintf(w, format+"\n", args...)
}

func printTextReport(w io.Writer, r report) {
	_, _ = fmt.Fprintln(w, "=== Benchmark Regression Report ===")
	_, _ = fmt.Fprintf(w, "Baseline: %s\n\n", r.Baseline)

	// Table header.
	_, _ = fmt.Fprintf(w, "%-4s %-38s %-8s %12s %12s %8s %s\n",
		"", "Benchmark", "Metric", "Baseline", "Current", "Delta", "Status")
	_, _ = fmt.Fprintln(w, strings.Repeat("-", 100))

	for _, bc := range r.Benchmarks {
		status := statusIcon(bc.Status)

		switch bc.Status {
		case "new":
			_, _ = fmt.Fprintf(w, "%-4s %-38s %-8s %12s %12s %8s (new benchmark, no baseline)\n",
				status, bc.Name, "—", "—", "—", "—")
		case "missing":
			_, _ = fmt.Fprintf(w, "%-4s %-38s %-8s %12s %12s %8s (not in current run)\n",
				status, bc.Name, "—", "—", "—", "—")
		default:
			for i, m := range bc.Metrics {
				nameCol := ""
				if i == 0 {
					nameCol = bc.Name
				}
				deltaStr := fmt.Sprintf("%+.1f%%", m.DeltaPct)
				if math.IsInf(m.DeltaPct, 1) {
					deltaStr = "∞"
				}
				passStr := "✓"
				if !m.Pass {
					passStr = "✗"
				}
				_, _ = fmt.Fprintf(w, "%-4s %-38s %-8s %12.0f %12.0f %8s %s\n",
					status, nameCol, m.Metric, m.Baseline, m.Current, deltaStr, passStr)
				if m.Message != "" && !m.Pass {
					_, _ = fmt.Fprintf(w, "      → %s\n", m.Message)
				}
			}
		}
	}

	_, _ = fmt.Fprintf(w, "\n---\n")
	printlnf(w, "Benchmarks checked: %d", r.Summary.Total)
	printlnf(w, "  Passed:    %d", r.Summary.Passed)
	printlnf(w, "  Regressed: %d", r.Summary.Regressed)
	printlnf(w, "  New:       %d", r.Summary.New)
	printlnf(w, "  Missing:   %d", r.Summary.Missing)
	if r.Summary.ZeroAllocChecked > 0 {
		printlnf(w, "  Zero-alloc verified: %d", r.Summary.ZeroAllocChecked)
	}
	_, _ = fmt.Fprintf(w, "\nBenchmark regression check: ")
	if r.Summary.ExitCode == 0 {
		_, _ = fmt.Fprintln(w, "PASSED")
	} else {
		_, _ = fmt.Fprintln(w, "FAILED")
	}
}

func statusIcon(status string) string {
	switch status {
	case "pass":
		return "PASS"
	case "regression", "zero-alloc-fail":
		return "FAIL"
	case "new":
		return "NEW "
	case "missing":
		return "MISS"
	default:
		return "??  "
	}
}
