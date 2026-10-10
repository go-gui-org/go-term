# Contributing

Thanks for your interest in `go-term`. This is a small, deliberately narrow
project — please read this file before opening a PR.

## Development setup

`go-term` depends on sibling working trees. Copy `go.work.example` to `go.work`
to wire them into the module graph via Go workspace mode (the `go.work` file is
gitignored — each developer opts in).

Clone all three repos as siblings:

```bash
git clone https://github.com/go-gui-org/go-glyph.git
git clone https://github.com/go-gui-org/go-gui.git
git clone https://github.com/go-gui-org/go-term.git
```

Edits in `../go-glyph` and `../go-gui` are picked up immediately by `go build`
from `go-term`.

## Toolchain

- Go 1.26+
- macOS, Linux, or Windows

## Common commands

Run the full local validation gate before pushing a branch:

```bash
make prepush
```

`make prepush` approximates the CI matrix from one host: race-enabled tests,
`go vet`, lint at the version CI pins, the prod falcon build, the Windows
cross-compile (`make cross-windows`), and the benchmark regression gate
(`make bench-regress`). It aborts on the first failing target.

`bench-regress` is the long pole — `-count=10` over `./term`. Unlike go-gui,
whose benchmark gate needs a baseline cached from `main`, this repo's baseline
is committed at `.github/benchmarks/baseline.txt`, so the gate runs locally and
at the same `-threshold 30` CI uses. Re-baseline intentional performance changes
with `make bench-save`.

Individual targets for a tighter loop while iterating:

```bash
# Run the demo window
cd examples/falcon && go run .

make build          # build everything
make test           # tests (grid, parser, widget helpers, replay fixtures)
make test-race      # tests with the race detector
make vet            # go vet ./...
make lint           # golangci-lint, pinned to the CI version
make cross-windows  # CGO-free windows vet + build of term/ and internal/
make bench          # quick benchmark pass

go mod tidy         # tidy module graph
```

Gate targets run with `GOWORK=off` so they resolve the versions in `go.mod`,
which is what CI does — the local `go.work` pointing at `../go-gui` and
`../go-glyph` would otherwise validate something CI never sees. The falcon and
`.app` build targets keep using the workspace.

### CI-only validation

- Native macOS and Windows test execution. `make cross-windows` covers the
  Windows job's vet and build, but its tests cannot run cross.
- The fuzz jobs, which are schedule- and diff-gated.
- `release.yml` packaging.

## Scope

Before adding a feature, check the open issues. Backlog items there were left
out of the v1.0 scope deliberately. If you want one of them, comment on its
issue first to discuss whether the cost is worth carrying — the goal is to keep
the codebase approachable. The v1.0.0 gate is tracked in #266.

The public API in `term/` is small on purpose and frozen at v0.9.0 — widget,
themes, actions, recording/replay, the live setters, and the activity/input taps
(see `CLAUDE.md` for the full list). Add unexported helpers freely; expand the
public surface only when there is a clear caller need.

## Version policy

SemVer pre-1.0: the frozen surface (as amended by v0.10.0) is stable until
v1.0.0, but breaking changes (should any slip through) ship as a new minor, not
into patches.

## Architecture

```
examples/falcon/main.go
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw*.go     OnDraw: bg/fg/graphics/cursor/overlay render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/keybind.go          RunAction direct dispatch; SetKeyBindings.
term/shortcuts.go        Action table; defaultBindings; help overlay data.
term/widget_mouse.go     Mouse button/motion/wheel; SGR/X10/urxvt encoding.
term/widget_clipboard.go Cmd+C/V; opt-in OSC 52 clipboard write.
term/widget_scroll.go    Scrollbar, momentum scroll.
term/widget_copymode.go  Vim-keyed copy mode; frozen output.
term/widget_hints.go     Keyboard link hints (open/copy).
term/widget_notify.go    Desktop notifications; OSC 9/777.
term/widget_record.go    Session recording; Start/StopRecording.
        │
        ▼
term/parser.go           VT state machine. Bytes → grid mutations.
term/parser_csi.go       CSI dispatch (SGR, cursor, erase, modes, …)
term/parser_osc.go       OSC dispatch (title, CWD, clipboard, …)
term/parser_dcs.go       DCS dispatch (DECRQSS, sixel, sync)
term/parser_apc.go       APC dispatch (Kitty Graphics)
        │
        ▼
term/grid.go             Cell buffer + cursor state + alt-screen.
term/grid_*.go           Scroll, reflow, search, selection, marks, BiDi, graphics.
term/scrollback.go       Ring buffer.
term/pty.go              ptyIO interface; creack/pty (Unix) + ConPTY (Windows).
term/palette.go          256-color table.

term/replay.go           replayPTY (a recording as a ptyIO); NewReplay.

term/workspace/          Panes/tabs/persistence — sits above term, public API only.
internal/recfmt/         .gtr session-recording container (Recorder + Reader).
term/gotermrec/          CLI over a recording: info/cat/play/fixture/export.
```

## Architectural rules

- Dependencies flow strictly downward: `widget → parser → grid`. The parser must
  not reach into go-gui — it is grid-only.
- `Grid.Mu` is the single lock. The reader goroutine takes it to feed the
  parser; `OnDraw` takes it to read cells. Never hold it across a go-gui call
  from the reader goroutine.
- `*gui.Window` state is touched only on the main thread.
  `win.QueueCommand(...)` is the only thread-safe path from the reader
  goroutine.
- `OnDraw` runs every frame. Avoid per-cell heap allocations in the inner loops
  — use the existing patterns (e.g. `runeString` for ASCII) rather than
  `string(rune)`.

## Code style

- Comments wrap at ~90 columns when practical.
- Error handling: log and continue at boundaries that aren't fatal (e.g.
  `pty.Resize`); return errors from constructors.
- Modern Go (1.26+) idioms — `for i := range n`, `slices`, `maps`, `cmp.Or`,
  `errors.Is`/`As`, `t.Context()` in tests, etc.
- Bound user-supplied counts and sizes. `clampDim` and `clampWinsize` exist for
  this reason; reuse them.

## Pull request checklist

1. `make prepush` passes (build, vet, race tests, lint, Windows cross-compile,
   benchmark gate)
2. New tests for new pure-logic code
3. Manual smoke test of `examples/falcon` for any change touching `widget.go`,
   `pty.go`, or render/input paths
4. CHANGELOG entry under `## [Unreleased]`

## Reporting bugs

Include:

- OS and version
- Go version (`go version`)
- Shell (`echo $SHELL`)
- Minimal repro: what was typed, what was expected, what happened

## License

By contributing, you agree your contributions are licensed under [MIT](LICENSE),
the same license as the project.
