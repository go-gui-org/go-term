# Pre-1.0 Production Readiness Plan

## Current State (2026-05-29)

| Aspect | Status |
|--------|--------|
| Code | 20 source files + 18 test files in `term/`, 525 test funcs |
| Mod graph | `replace` directives pin go-glyph/go-gui to local siblings |
| CI | Checkout all 3 repos; vet `./term`, test `-race -count=1 ./term`, golangci-lint |
| Exports | 19 exported types, many implementation details |
| Replay tests | 4 basic cases, no real-world fixture files |
| Package docs | None — no `doc.go`, no examples |
| Benchmarks | 6 (parser feed, scrollback push, foreground pass, search, ensureGeom) |
| ROADMAP.md | Mostly archived (Phases 0–38), Phase 39 active, stale "Critical files" |

---

## Item 1: CI Alignment

Current CI is close but has gaps. Changes:

### Tasks
- [ ] Run `go vet ./...` instead of `go vet ./term`
- [ ] Run `go test ./...` instead of `go test ./term` (covers `cmd/demo` if tests added later)
- [ ] Add `go build ./cmd/demo` as an explicit build step
- [ ] Add `go test -race -count=1 ./...` — already present but only for `./term`
- [ ] Bump `actions/checkout` from v4 to v5
- [ ] Add Go module cache (`actions/cache` or `setup-go` `cache: true`) for speed
- [ ] Add optional scheduled workflow for fuzz tests (`go test -fuzz=. -fuzztime=30s ./term`)

**Effort:** ~1h. Mostly editing `.github/workflows/ci.yml`.

---

## Item 2: Dependency Cleanup

The `replace` directives prevent building from a clean clone without local siblings.

### Tasks
- [ ] Tag the current `go-gui` and `go-glyph` HEADs with compatible versions
- [ ] Replace `go.mod` `replace` directives with real version requirements
- [ ] Run `go mod tidy`
- [ ] Create `go.work.example` documenting `go work use ../go-gui ../go-glyph` for local dev
- [ ] Verify: `git clone` to `/tmp`, `go build ./cmd/demo` works

**Effort:** ~30 min. Depends on pushing tags to go-gui/go-glyph repos.

**Risk:** If go-gui/go-glyph aren't ready for a tag, keep `replace` but document the `go work` path as the canonical local-dev setup and CI's checkout approach as the CI solution.

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
- [ ] Move `Parser`, `Grid`, `Cell`, `PTY`, `Graphic`, `Mark`, `MarkKind` + related constants under `term/internal/`
- [ ] Keep `Cfg`, `Term`, `New`, `View`, `Close`, `Theme`, `NamedTheme`, `CursorShape`, `ContentPos`, `SearchMatch` exported
- [ ] Verify all tests still compile and pass
- [ ] Add `term/doc.go` with package-level documentation

**Effort:** 2–3h. The `internal/` move is mechanical but touches every test file.

**Alternative:** If `internal/` is too invasive, keep everything in `term/` but unexport symbols not in the public API contract. Less cleanup, faster, no import path changes.

---

## Item 4: Package Docs and Examples

### Tasks
- [ ] Add `term/doc.go` with:
  - Package overview and lifecycle expectations
  - OSC 52 security default
  - Theme configuration
  - Cwd
  - Supported platforms (macOS, Linux)
- [ ] Add `ExampleNew` — minimal window creation
- [ ] Add `ExampleCfg` — custom configuration
- [ ] Add `ExampleTerm_Close` — lifecycle
- [ ] Verify `go test ./...` runs examples

**Effort:** 1h.

---

## Item 5: Integration Replay Fixtures

The replay test has 4 hardcoded cases. Real fixtures give confidence for protocol coverage.

### Tasks
- [ ] Create `testdata/` directory with fixture files:
  - `testdata/shell_prompt.txt` — bash/zsh prompt with color
  - `testdata/vim_basic.txt` — vim open, edit, `:q!`
  - `testdata/htop_first_frame.txt` — alt-screen app
  - `testdata/ls_color.txt` — `ls --color` output
  - `testdata/resize_heavy.txt` — streams with multiple resize events
  - `testdata/sixel_example.txt` — sixel image output
  - `testdata/kitty_graphics.txt` — Kitty Graphics Protocol example
- [ ] Refactor `TestEmulatorReplay` into a table-driven test that reads `.txt` fixtures
- [ ] Each fixture includes expected assertions: final grid lines, cursor position, title, cwd, alt-screen state, replies
- [ ] Document how to capture new fixtures (using `script` or `ttyrec`)

**Effort:** 2–3h. Capturing real output streams is the time sink.

---

## Item 6: Term Boundary Refactor

Introduce internal interfaces so `Term` lifecycle can be tested without a GUI window.

### Tasks
- [ ] Define internal interfaces:
  - `ptyWriter` — PTY write side (exists implicitly via `*os.File`)
  - `commandScheduler` — abstracts `win.QueueCommand` (already a method on `*gui.Window`)
  - `notificationSender` — OSC 9/777 notifications
- [ ] Extract `New`'s body into focused helpers: `startPTY`, `startReader`, `configureWindow`
- [ ] Add tests for:
  - `New` configuration validation (rows, cols, scrollback bounds)
  - Parser reply dispatch (DA1, XTGETTCAP, DECRQSS)
  - OSC notification routing
  - `Close` PTY cleanup and goroutine shutdown
  - Resize scheduling (debounce behavior)
- [ ] Do NOT change the public API — keep `New(w *gui.Window, cfg Cfg) (*Term, error)` unchanged

**Effort:** 3–4h. Interface extraction is subtle — wrong abstraction increases complexity.

---

## Item 7: Roadmap Refresh

### Tasks
- [ ] Update "Critical files" section to reflect current file layout (20+ files by concern, not 5)
- [ ] Replace "Context" preamble — it describes post-MVP state, not current state
- [ ] Split Phase 39 into concrete sub-phases:
  - [ ] 39a: Pane model (pane struct, pane tree, create/destroy/focus)
  - [ ] 39b: Focus routing (keyboard/mouse dispatch to focused pane)
  - [ ] 39c: Split layout (horizontal/vertical split, resize drag handles)
  - [ ] 39d: Tab model (tab bar, tab create/close/switch)
  - [ ] 39e: Persistence/config (save/restore layout, session files)
- [ ] Add verification steps specific to each sub-phase

**Effort:** 30 min.

---

## Item 8: Benchmark Tracking

### Tasks
- [ ] Add benchmarks:
  - `BenchmarkResize_Reflow_DeepScrollback` — resize with 10k scrollback lines
  - `BenchmarkSixel_Decode` — sixel decode path
  - `BenchmarkGrid_Search_LargeScrollback` — search over 50k lines
  - `BenchmarkDrawPrep` — `HasDirtyRows` + dirty row iteration
- [ ] Document: `go test -bench=. -count=5 -benchmem ./term > bench.txt`
- [ ] Add `make bench` target or `justfile` entry
- [ ] Optional: add non-blocking CI step that runs benchmarks and archives results as artifacts (warn-only, no failure)

**Effort:** 1h.

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
