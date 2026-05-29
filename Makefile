.PHONY: bench bench-verbose test test-race vet lint build clean

# Default benchmark run — quick pass over all benchmarks.
# -run=^$ skips tests so stale timers don't fire during benchmark runs.
bench:
	go test -bench=. -count=5 -benchmem -run='^$$' ./term

# Benchmarks with verbose test output prepended (useful for sanity checks).
bench-verbose:
	go test -bench=. -count=5 -benchmem -run='^$$' -v ./term

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
