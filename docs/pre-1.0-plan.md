# Pre-1.0 Production Readiness Plan

## Current State (2026-05-29)

| Aspect | Status |
|--------|--------|
| Code | 20 source files + 18 test files in `term/`, 525 test funcs |
| Mod graph | `replace` directives pin go-glyph/go-gui to local siblings |
| CI | Checkout all 3 repos; vet `./term`, test `-race -count=1 ./term`, golangci-lint |
| Exports | 7 public types (Cfg, Term, Theme, NamedTheme, ContentPos, SearchMatch, CursorShape; + New/View/Close methods + DefaultColor/MaxGridDim/MaxScrollbackCap constants) |

## Status Summary (2026-05-29)

| Item | Status |
|------|--------|
| 1. CI Alignment | ✅ Done |
| 2. Dependency Cleanup | 🔶 Partial — `go.work.example` exists, waiting on go-gui/go-glyph tags |
| 3. Public API Audit | ✅ Done — chose unexport over `internal/` |
| 4. Package Docs/Examples | ✅ Done |
| 5. Integration Replay Fixtures | 🔶 Mostly done — 7 JSON fixtures, missing real-terminal capture docs |
| 6. Term Boundary Refactor | 🔶 Mostly done — helpers extracted, tests added, no formal interfaces |
| 7. Roadmap Refresh | ✅ Done |
| 8. Benchmark Tracking | 🔶 Partial — CI bench job exists, missing 3 specific benchmarks |

Remaining work: tag go-gui/go-glyph (Item 2), real-terminal fixture docs (Item 5), 3 benchmarks (Item 8). Items 2, 5, 8 are ~2h total.
| Replay tests | 4 hardcoded cases + 7 JSON fixtures, `TestCaptureFixture` helper |
| Package docs | `doc.go` + `example_test.go` added |
| Benchmarks | 7 (parser feed, scrollback push, foreground pass, search, ensureGeom, draw prep dirty rows) |
| ROADMAP.md | Refreshed: archived Phases 0–38, Phase 39 split into 39a–39e, architecture diagram, verification steps |

---

## Item 1: CI Alignment

Current CI is close but has gaps. Changes:

### Tasks
- [x] Run `go vet ./...` instead of `go vet ./term`
- [x] Run `go test ./...` instead of `go test ./term` (covers `examples/demo` if tests added later)
- [x] Add `go build ./examples/demo` as an explicit build step
- [x] Add `go test -race -count=1 ./...` — already present but only for `./term`
- [x] Bump `actions/checkout` from v4 to v5
- [x] Add Go module cache (`actions/cache` or `setup-go` `cache: true`) for speed
- [x] Add optional scheduled workflow for fuzz tests (`go test -fuzz=. -fuzztime=30s ./term`)
- [x] Add scheduled benchmark job with artifact upload

**Status:** Done. Also added `golangci-lint` job.

---

## Item 2: Dependency Cleanup

The `replace` directives prevent building from a clean clone without local siblings.

### Tasks
- [ ] Tag the current `go-gui` and `go-glyph` HEADs with compatible versions
- [ ] Replace `go.mod` `replace` directives with real version requirements
- [x] Run `go mod tidy`
- [x] Create `go.work.example` documenting `go work use ../go-gui ../go-glyph` for local dev
- [ ] Verify: `git clone` to `/tmp`, `go build ./examples/demo` works

**Status:** Partially done. `go.work.example` exists. `replace` directives still active — waiting on go-gui/go-glyph tags. CI uses multi-checkout workaround.

---

## Item 3: Public API Audit

19 exported types is too many. The intentional public API should be 6–8 types.

### Current exports and classification

| Symbol | Classification | Action |
|--------|---------------|--------|
| `Cfg` | Public API | Keep |
| `Term` | Public API | Keep |
| `New` | Public API | Keep |
| `View` (method) | Public API | Keep |
| `Close` (method) | Public API | Keep |
| `Theme` | Public API | Keep |
| `NamedTheme` | Public API | Keep |
| `CursorShape` | Public API (Cfg uses it) | Keep |
| `ContentPos` | Public API (selection) | Keep |
| `SearchMatch` | Public API (search) | Keep |
| `Cell` | Internal detail | Unexport or move to `internal/` |
| `Grid` | Internal detail | Unexport or move to `internal/` |
| `NewGrid` | Internal detail | Unexport |
| `Parser` | Internal detail | Unexport |
| `NewParser` | Internal detail | Unexport |
| `PTY` | Internal detail | Unexport |
| `Start` | Internal detail | Unexport |
| `Graphic` | Internal detail | Unexport |
| `Mark` | Internal detail | Unexport |
| `MarkKind` | Internal detail | Unexport |
| `MaxGridDim`, `MaxScrollbackCap` | Configuration | Keep as public constants |
| `AttrBold`, etc. | Cell attributes | Unexport or move with Cell |
| `ULNone`, etc. | Underline styles | Unexport or move with Cell |
| `DefaultColor` | Color constant | Keep public |

### Tasks
- [x] Move `Parser`, `Grid`, `Cell`, `PTY`, `Graphic`, `Mark`, `MarkKind` + related constants to unexported
- [x] Keep `Cfg`, `Term`, `New`, `View`, `Close`, `Theme`, `NamedTheme`, `CursorShape`, `ContentPos`, `SearchMatch` exported
- [x] Verify all tests still compile and pass
- [x] Add `term/doc.go` with package-level documentation

**Status:** Done. Chose the unexport alternative (types stay in `term/` as `grid`, `parser`, `cell`, `ptyDev`, `graphic`, `mark`, `markKind`). `DefaultColor`, `MaxGridDim`, `MaxScrollbackCap` kept public. `AttrBold` etc. unexported. `ULNone` etc. unexported.

---

## Item 4: Package Docs and Examples

### Tasks
- [x] Add `term/doc.go` with:
  - Package overview and lifecycle expectations
  - OSC 52 security default
  - Theme configuration
  - Cwd
  - Supported platforms (macOS, Linux)
- [x] Add `ExampleNew` — minimal window creation
- [x] Add `ExampleCfg` — custom configuration
- [x] Add `ExampleTerm_Close` — lifecycle
- [x] Verify `go test ./...` runs examples

**Status:** Done.

---

## Item 5: Integration Replay Fixtures

The replay test has 4 hardcoded cases. Real fixtures give confidence for protocol coverage.

### Tasks
- [x] Create `testdata/` directory with fixture files:
  - `testdata/shell_prompt.json` — bash/zsh prompt with color
  - `testdata/vim_edit.json` — vim open, edit, `:q!`
  - `testdata/cursor_moves.json` — cursor movement sequences
  - `testdata/scroll_region.json` — scroll region tests
  - `testdata/sgr_colors.json` — SGR color sequences
  - `testdata/erase_modes.json` — erase in line/display
  - `testdata/example.json` — basic example fixture
- [x] Refactor `TestEmulatorReplay` into table-driven + `TestEmulatorReplayFixtures` that reads `.json` fixtures
- [x] Each fixture includes expected assertions: final grid lines, cursor position, title, cwd
- [x] `TestCaptureFixture` helper to record new fixtures from Go test code
- [ ] Document how to capture new fixtures from real terminal sessions

**Status:** Mostly done. Fixtures are JSON (base64-encoded input) rather than raw terminal dumps. `TestCaptureFixture` records from Go, not from real terminals.

---

## Item 6: Term Boundary Refactor

Introduce internal interfaces so `Term` lifecycle can be tested without a GUI window.

### Tasks
- [ ] Define internal interfaces (`ptyWriter`, `commandScheduler`, `notificationSender`)
- [x] Extract `New`'s body into focused helpers: `startPTY`, `applyScrollbackConfig`, `buildThemeMenu`, `applyTheme`, `sendDesktopNotify`
- [x] Add tests for:
  - Scrollback config bounds (default, custom, clamped, disabled)
  - Config helper functions (`buildThemeMenu`, `applyTheme`)
  - Parser reply dispatch (DA1 covered by emulator replay tests)
  - OSC notification dedup (`TestTerm_NotifyBusy_ExtrasDropped`)
  - `Close` idempotency (`TestClose_Idempotent` — guard only, not full path)
  - Resize scheduling (`TestScheduleResizeWake_*`)
  - `flushPendingReplies` empty/error paths
- [x] Public API unchanged: `New(w *gui.Window, cfg Cfg) (*Term, error)` kept

**Status:** Mostly done. Internal interfaces not formally defined (`writeHost` func field already serves as ptyWriter/test seam). Full Close integration test needs a real pty — partial coverage.

---

## Item 7: Roadmap Refresh

### Tasks
- [x] Update "Critical files" section — removed, replaced with architecture diagram
- [x] Replace "Context" preamble — now describes full-featured state, 38 phases complete
- [x] Split Phase 39 into concrete sub-phases:
  - [ ] 39a: Pane model (pane struct, pane tree, create/destroy/focus)
  - [ ] 39b: Focus routing (keyboard/mouse dispatch to focused pane)
  - [ ] 39c: Split layout (horizontal/vertical split, resize drag handles)
  - [ ] 39d: Tab model (tab bar, tab create/close/switch)
  - [ ] 39e: Persistence/config (save/restore layout, session files)
- [x] Add verification steps specific to each sub-phase

**Status:** Done.

---

## Item 8: Benchmark Tracking

### Tasks
- [x] Add benchmarks:
  - `BenchmarkDrawPrep_DirtyRows` — `HasDirtyRows` + `ClearDirty` cycle
  - `BenchmarkForegroundPass` — run-key computation for full 80×24 screen
- [ ] Add `BenchmarkResize_Reflow_DeepScrollback` — resize with 10k scrollback lines
- [ ] Add `BenchmarkSixel_Decode` — sixel decode path
- [ ] Add `BenchmarkGrid_Search_LargeScrollback` — search over 50k lines
- [x] Document: `go test -bench=. -count=5 -benchmem ./term` (CI bench job exists)
- [x] Add scheduled CI step that runs benchmarks and archives results as artifacts
- [ ] Add `make bench` target or `justfile` entry

**Status:** Partially done. CI bench job runs on schedule. Missing deep-scrollback resize, sixel decode, and large-scrollback search benchmarks.

---

## Execution Order

1. **CI alignment** — fast, improves safety immediately
2. **Dependency cleanup** — unblocks external contributors
3. **Public API audit** — highest design risk, want early feedback
4. **Package docs/examples** — low effort, high polish
5. **Integration replay fixtures** — medium effort, high confidence gain
6. **Term boundary refactor** — most invasive, builds on prior items
7. **Roadmap refresh** — quick, can happen anytime
8. **Benchmark tracking** — low urgency, can happen anytime

---

## Unresolved Questions

1. **Tag go-gui/go-glyph first?** Items 1 and 2 are tied — CI already works via multi-checkout, so tagging is nice-to-have not blocking. Is there a reason to hold off tagging those repos?

2. **`internal/` or just unexport?** Moving Grid/Parser/PTY to `term/internal/` is cleaner but breaks all internal import paths. Un-exporting keeps tests in-package. Preference?

3. **Fixture capture method.** `script` captures terminal output with timing but includes control chars. `ttyrec` is heavier. Could also write Go test helpers that record `Parser` output to files. How would you prefer to capture real terminal streams?

4. **Scheduled CI for fuzz/benchmarks.** Worth setting up now, or defer until after the other items land?
