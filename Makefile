.PHONY: bench bench-verbose bench-save bench-regress test test-race vet lint \
	build clean app clean-app build-falcon

DEMO_BIN     := falcon
APP_NAME     := Falcon
# Version reported by the About dialog, stamped into main.version at link
# time. --always keeps a shallow or tag-less checkout building (falls back to a
# bare hash); --dirty marks uncommitted trees so a local build can't be
# mistaken for the release it was cut from.
VERSION      := $(shell git describe --tags --always --dirty 2>/dev/null)
# Soft heap limit baked into the binary (bytes), applied at startup by
# main.go via debug.SetMemoryLimit. The limit value is the post-burst RSS
# governor: the runtime returns freed memory toward the live set instead of
# leaving clean pages resident, so a heavy session (ucs-detect sweep) settles
# at ~230MB instead of lingering at the peak. It must stay above the live
# working set of such a session (~300-400MB) — a limit below it forces
# constant GC and turns heavy sessions laggy. It is soft: genuine pressure
# exceeds it rather than hard-capping. A GOMEMLIMIT env var at runtime
# overrides both this and the in-code default. Tune per build:
# MEMLIMIT=268435456 make app.
MEMLIMIT     := 536870912
LDFLAGS      := -X main.version=$(VERSION) -w -X main.memLimit=$(MEMLIMIT)
# CFBundleShortVersionString wants a bare number, so drop the tag's leading v.
BUNDLE_VER   := $(patsubst v%,%,$(VERSION))
# Pre-built .icns (see examples/falcon/icon/README.md); buildapp copies it
# into the bundle verbatim, so no sips/iconutil conversion runs here.
APP_ICON     := examples/falcon/icon/falcon.icns
# buildapp comes from the go-gui module graph (go.mod's pin), not a sibling
# checkout, so `make app` works without go.work or CI-side workarounds. With a
# go.work active the workspace resolution wins, but the module pin is always a
# valid fallback.
BUILDAPP_PKG := github.com/go-gui-org/go-gui/cmd/buildapp
BUILDAPP_BIN := build/buildapp
# Shipping builds exclude the go-gui F12 inspector overlay. Dev builds
# (`go run .`, plain `go build`) keep it.
PROD_TAGS    := -tags prod

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

# Build the falcon binary (ensures it compiles). Shipping path: excludes the
# go-gui inspector via the prod tag.
build-falcon:
	go build $(PROD_TAGS) -ldflags '$(LDFLAGS)' ./examples/falcon

# Package falcon as a macOS .app bundle.
app: $(APP_NAME).app

$(BUILDAPP_BIN):
	mkdir -p build
	go build -o $@ $(BUILDAPP_PKG)

# Depends on the icon so swapping artwork forces a rebundle; Go source
# changes are caught by go build itself, not by make's timestamp check.
$(APP_NAME).app: $(BUILDAPP_BIN) $(APP_ICON)
	cd examples/falcon && go build $(PROD_TAGS) -ldflags '$(LDFLAGS)' -o $(CURDIR)/$(DEMO_BIN) .
	$(BUILDAPP_BIN) -bundle-deps -o . -name $(APP_NAME) \
		-id github.com.go-gui-org.go-term -icon $(APP_ICON) \
		-version $(BUNDLE_VER) $(DEMO_BIN)

clean-app:
	rm -f $(DEMO_BIN)
	rm -rf $(APP_NAME).app
	rm -rf build

# Clean test cache and built binaries.
clean:
	go clean -testcache ./...
