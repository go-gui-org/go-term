.PHONY: bench bench-verbose bench-save bench-regress test test-race vet lint \
	lint-pin build clean app clean-app build-falcon cross-windows prepush

# golangci-lint version pinned by CI (.github/workflows/ci.yml).
LINT_VERSION := v2.12

# Gate recipes resolve modules from go.mod, not from a go.work workspace.
# go.work here points at ../go-gui and ../go-glyph, which CI never sees, so
# a gate that used it would answer a different question than "will CI go
# green". The app/falcon build targets deliberately keep a bare `go` so
# local development against sibling checkouts still works.
GO := GOWORK=off go

# golangci-lint is its own binary, so $(GO) does not cover it — but it
# honours go.work the same way the toolchain does. Without GOWORK=off it
# type-checks against the sibling working copies and reports breakage that
# CI, which builds the pinned versions, will never see.
LINT := GOWORK=off golangci-lint

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
# -threshold 30 matches the CI bench-regress job; without it the tool
# defaults to 10 and this target gated harder than CI did.
bench-regress:
	$(GO) test -bench=. -count=10 -benchmem -run='^$$' ./term \
	  > /tmp/bench-current.txt
	$(GO) run ./scripts/benchregress \
	  -threshold 30 \
	  -base .github/benchmarks/baseline.txt \
	  -current /tmp/bench-current.txt

test:
	$(GO) test ./...

test-race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

# Verify golangci-lint is installed at the version CI pins, so a local pass
# and a CI pass mean the same thing.
lint-pin:
	@golangci-lint --version | grep -q "$(LINT_VERSION:v%=%)" || \
	  { echo "::error::golangci-lint $(LINT_VERSION) required. Run: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(LINT_VERSION)"; exit 1; }

lint: lint-pin
	$(LINT) run ./...

build:
	$(GO) build ./...

# Mirror of the CI windows job's compile half: term/ and internal/ are pure
# Go (only examples/falcon needs cgo via go-gui), so they vet and build for
# Windows from any host with no C toolchain. The test half of that job
# cannot run cross, so it stays CI-only.
cross-windows:
	CGO_ENABLED=0 GOOS=windows $(GO) vet ./term/... ./internal/...
	CGO_ENABLED=0 GOOS=windows $(GO) build ./term/... ./internal/...

# Build the falcon binary (ensures it compiles). Shipping path: excludes the
# go-gui inspector via the prod tag.
build-falcon:
	$(GO) build $(PROD_TAGS) -ldflags '$(LDFLAGS)' ./examples/falcon

# Recommended full local validation before pushing (issue go-gui#314).
# Approximates the CI matrix from one host: race tests, vet, lint, the prod
# falcon build, the Windows cross-compile, and the benchmark regression gate.
# Aborts on the first failing target.
#
# Unlike go-gui, whose benchmark gate needs a baseline cached from main,
# this repo's baseline is committed at .github/benchmarks/baseline.txt, so
# bench-regress runs locally. It is the long pole: -count=10 over ./term.
#
# Omissions vs CI, by design:
#   - native macOS and Windows test execution (the OS matrix)
#   - the fuzz jobs, which are schedule- and diff-gated
#   - release.yml packaging
prepush: test-race vet lint build-falcon cross-windows bench-regress

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
