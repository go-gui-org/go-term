.PHONY: bench bench-verbose bench-save bench-regress test test-race vet lint \
	lint-bin build clean app clean-app build-falcon cross-windows prepush

# Repo-local bin for the pinned linter. The pinned VERSION itself lives in
# tools/lint/go.mod -- see the $(LINT_BIN) rule below.
LINT_DIR = $(CURDIR)/.bin
LINT_BIN = $(LINT_DIR)/golangci-lint
LINT_ARGS ?=

# Gate recipes resolve modules from go.mod, not from a go.work workspace.
# go.work here points at ../go-gui and ../go-glyph, which CI never sees, so
# a gate that used it would answer a different question than "will CI go
# green". The app/falcon build targets deliberately keep a bare `go` so
# local development against sibling checkouts still works.
GO := GOWORK=off go

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
# Code-signing identity for the bundle. Empty (the default) means buildapp
# signs ad-hoc, and an ad-hoc signature has no certificate for TCC to key a
# permission grant against — TCC falls back to the cdhash, which changes on
# every build, so each `make app` silently revokes Screen Recording, the
# microphone, accessibility and the rest while System Settings keeps showing
# them as granted. Set this to a self-signed code-signing certificate from
# Keychain Access to keep grants across rebuilds:
#   make app SIGN_IDENTITY="My Dev Cert"
# BUILDAPP_SIGN_IDENTITY in the environment does the same without the flag.
# Left empty so CI and contributors without a certificate keep the old path.
SIGN_IDENTITY ?=
SIGN_FLAG    := $(if $(SIGN_IDENTITY),-sign "$(SIGN_IDENTITY)",)
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

# Build the pinned linter on demand, into a repo-local bin.
#
# The version lives in tools/lint/go.mod and nowhere else, so local and CI
# cannot drift -- the old scheme pinned v2.12 here and v2.12 (floating
# patch) in CI, and checked the pin with a substring grep that also
# accepted 2.12.10.
#
# tools/lint is a SEPARATE module on purpose: a `tool` directive in the root
# go.mod grew it from 40 to 246 lines and go.sum from 119 to 997; every
# downstream sibling (go-charts, go-edit, go-kite, go-term, go-map) would
# inherit that module graph for a linter they never run.
#
# GOWORK=off because tools/lint sits inside the repo but is deliberately not
# a go.work member; without it go refuses to build a module the workspace
# does not use. The Go build cache makes every run after the first fast.
#
# GOOS/GOARCH/CGO_ENABLED/CC are neutralised for the BUILD of the linter:
# callers set them to pick the target being ANALYSED, and inheriting them
# here would cross-compile the linter itself into a binary the runner
# cannot execute. An empty GOOS or GOARCH means "host default" to the go
# command; CGO_ENABLED=0 makes the caller's CC irrelevant, which
# golangci-lint (pure Go) never needs.
$(LINT_BIN): tools/lint/go.mod tools/lint/go.sum
	GOWORK=off GOOS= GOARCH= CGO_ENABLED=0 GOFLAGS= GOBIN=$(LINT_DIR) \
	  go -C tools/lint install \
	  github.com/golangci/golangci-lint/v2/cmd/golangci-lint

# Named entry point for callers that want the binary without linting.
lint-bin: $(LINT_BIN)

# Run golangci-lint at the pinned version. LINT_ARGS passes extra flags
# through.
lint: $(LINT_BIN)
	$(LINT_BIN) run $(LINT_ARGS) ./...

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
		$(SIGN_FLAG) -version $(BUNDLE_VER) $(DEMO_BIN)

clean-app:
	rm -f $(DEMO_BIN)
	rm -rf $(APP_NAME).app
	rm -rf build

# Clean test cache and built binaries.
clean:
	rm -rf $(LINT_DIR)
	go clean -testcache ./...
