# CLAUDE.md

## Roadmap

Post-MVP feature plan with progress checkboxes lives in `ROADMAP.md`
at the repo root. Tick boxes there as work lands.

## Common commands

Standard `go build`/`test`/`vet`/`mod tidy` apply. The non-obvious ones:

```bash
# Run the demo window
cd examples/falcon && go run .

# Run the replay-style emulator checks only
go test ./term -run EmulatorReplay
```

### Debugging rendering bugs

Capture and replay procedures — `.gtr` session recording (`--record`/`--replay`,
`Cmd+Shift+R`), the `gotermrec` CLI, and the `GOTERM_CAPTURE` raw byte tee —
live in the `capture-terminal-bug` skill. Prefer a `.gtr` recording for anything
a user should be able to produce and send you.

There are automated tests for the grid, parser, PTY, widget helpers,
and replay-style emulator behavior. The widget itself is still partly
GUI-bound, so keep validating visually by running `examples/falcon` and trying
`ls`, `cat`, ANSI color output, window resize, selection/copy, and
full-screen apps such as `vim` or `less`.

## Local dev with sibling dependencies

`go.mod` references published versions of `go-gui` and `go-glyph`.
For local development against in-progress sibling changes, copy
`go.work.example` to `go.work` at the repo root. The workspace file
wires sibling working trees at `../go-gui` and `../go-glyph` into the
module graph so `go build` picks up uncommitted edits immediately.

Both sibling repos must be present at those paths. Remove or unset
`GOWORK` to switch back to the published versions.

## Architecture

Three layers; dependencies flow strictly downward — widget (`term/widget*.go`)
→ parser (`term/parser*.go`) → grid (`term/grid*.go`, pure data). Each layer is
split across multiple files by concern; the layering invariant is what matters,
not the file count. `internal/recfmt` is a leaf package importing nothing from
the repo, so both `term` and `term/gotermrec` can sit on it.

Don't let parser code reach into go-gui — it must stay grid-only.

Per-subsystem detail (render loop, parser scope, grapheme clusters, keyboard
input) lives in `term/CLAUDE.md`, loaded when working under `term/`.

### Concurrency model

- One PTY reader goroutine, started in `term.New`.
- `Grid.Mu` is the single lock. The reader goroutine takes it to feed
  the parser; `OnDraw` takes it to read cells. Never hold it across a
  go-gui call.
- After feeding bytes, the reader calls `win.QueueCommand(...)` to
  schedule a redraw on the main thread. **Never touch `*gui.Window`
  state directly from the reader goroutine** — `QueueCommand` is the
  only thread-safe path.

## Conventions

- Comments wrap at ~90 columns.
- Public API in `term/` is small on purpose: `Cfg`, `Term`, `NamedTheme`,
  `Theme`, `BellMode`, `New`, `View`, `Close`, `Cwd`, `SetTheme`, `Rows`,
  `Cols`, `Write`, `SendInput`, `PID`, `Alive`, `SetFocused`, `HandleWindowEvent`,
  `StartRecording`/`StopRecording`/`Recording`, `NewReplay`/`ReplayCfg`, plus
  `Shortcuts`/`ShortcutInfo` (display metadata for help overlays) and
  `Action`/`KeyMap`/`SetKeyBindings`/`KeyBindings`/`ParseAction` (rebindable
  Term-level shortcuts), and the live setters a config reload needs —
  `SetTextStyle`, `SetScrollbackRows`, `SetBellMode`, `SetScrollbarWidth`,
  plus `InputKind`/`Cfg.OnInput`/`SendInput` — the symmetric input tap and
  injection pair a pane manager needs to mirror keys and pastes to sibling
  panes (`term/workspace` broadcast mode). What the tap hands out, `SendInput`
  takes back in; keep them symmetric, and keep the per-kind encoding rules in
  `SendInput` rather than in the embedder.
  Keep it that way; add unexported helpers freely. The recording
  *format* stays in `internal/recfmt` precisely so it never becomes public API.
- User settings (fonts, theme, terminal settings, keybindings) are parsed and
  applied by `term/workspace`, never by `term/` — `term.Cfg` has no
  serialization. `term/workspace/config.go` owns the INI; the settings it
  resolves ride on the workspace's effective `Cfg` (see `termOpts`), so panes,
  tabs, and restore all build from one object. `docs/config.md` is the
  user-facing reference and must be updated with any new key.
- Keyboard shortcuts resolve through the binding table in `shortcuts.go`
  (`defaultBindings`/`mergeBindings`), which is the single source of truth for
  both matching and the help overlay. The table decides only *whether a chord
  matched*; conditional passthrough to the child (no-selection Ctrl+C, alt-screen
  PageUp) stays in the handlers. `Cfg.KeyBindings` seeds it, `SetKeyBindings`
  replaces it — both via `mergeBindings`, so they can't diverge.
- Performance target: reduce heap allocations. The OnDraw hot path
  must not allocate per cell — keep `string(rune)` conversions and
  slice growth out of the inner loop if perf work begins.
- `Grid.Mu` is the single lock — don't add per-feature mutexes.
- `grid.pal` (the effective 256-color table) is derived state. Never assign
  `grid.Theme` directly — call `grid.setTheme`, which rebuilds it. The merge
  happens there so the per-cell `resolveColor` stays a single indexed load
  (it must keep inlining into `fgOf`/`bgOf`; a wrapper that pushes it past
  the inline budget costs ~45% on the foreground pass).
- `Term.queueCommand` (which wraps `cmd.QueueCommand` with a closed-Term
  guard) is the only thread-safe path from reader goroutine to gui state.
  Title updates, clipboard writes, and notifications triggered by the
  parser must go through it. Never call `cmd.QueueCommand` directly.
- `dispatchCSI`, `dispatchOSC`, `dispatchDCS`, `dispatchAPC` are the
  single dispatch sites for their respective sequences — extend, don't
  add parallel dispatchers.
- When rendering visual dividers (horizontal or vertical lines in UI
  overlays), use `gui.Rectangle` with `FillFixed` (horizontal: fill width,
  fixed height) or `FixedFill` (vertical: fixed width, fill height).
  `Rectangle` has no padding, no axis, and no child layout — unlike
  `gui.Column`/`gui.Row` wrappers, it won't pick up theme container
  padding that indents the edges.
