.PHONY: bench bench-verbose bench-save bench-regress test test-race vet lint \
	build clean

# Default benchmark run — quick pass over all benchmarks.
# -run=^$ skips tests so stale timers don't fire during benchmark runs.
bench:
	go test -bench=. -count=5 -benchmem -run='^$$' ./term

# Benchmarks with verbose test output prepended (useful for sanity checks).
bench-verbose:
	go test -bench=. -count=5 -benchmem -run='^$$' -v ./term

# Save current benchmark results as the new regression baseline.
# Run this before committing intentional performance changes.
bench-save:
	go test -bench=. -count=10 -benchmem -run='^$$' ./term \
	  | go run ./scripts/benchregress -update \
	  > .github/benchmarks/baseline.txt

# Run benchmarks and check for regressions against the committed baseline.
# Fails with exit code 1 if any benchmark regresses beyond the threshold.
bench-regress:
	go test -bench=. -count=10 -benchmem -run='^$$' ./term \
	  > /tmp/bench-current.txt
	go run ./scripts/benchregress \
	  -base .github/benchmarks/baseline.txt \
	  -current /tmp/bench-current.txt

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

build:
	go build ./...

# Build the demo binary (ensures it compiles).
build-demo:
	go build ./examples/demo

# Clean test cache and built binaries.
clean:
	go clean -testcache ./...
