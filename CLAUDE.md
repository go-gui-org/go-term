# CLAUDE.md

## Roadmap

`ROADMAP.md` is thin now: the API froze at v0.9.0, and the only remaining
phase is the v1.0.0 tag, blocked on go-gui v1.0. Pre-1.0 phase history lives
in `ROADMAP-v0.md` and is not maintained — do not edit it to add entries.

When the v1.0.0 tag ships, delete the phase from `Upcoming`, add one row to
`Completed` (number, short description, what it unlocked), and move
`ROADMAP-v0.md` back or archive it — do not keep finished checklists in the
live file.

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

### Measuring input latency

`GOTERM_LATENCY=1 go run .` in `examples/falcon` logs percentiles every 25
keystrokes, splitting keystroke-to-frame into the child's echo round-trip and
go-term's own scheduling + paint. See `term/latency.go` for what each span
covers. It stops at the end of `onDraw`: GPU submit, compositor and vsync are
not observable in-process, so compare against an external measurement (a
240 fps camera) before concluding the widget is at fault.

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
  `Theme` (incl. `Theme.IsDark`, which an embedder needs to match its own
  chrome to the pane), `BellMode`, `New`, `View`, `Close`, `Cwd`, `SetTheme`, `Rows`,
  `Cols`, `Write`, `SendInput`, `PID`, `Alive`, `SetFocused`, `HandleWindowEvent`,
  `StartRecording`/`StopRecording`/`Recording`, `NewReplay`/`ReplayCfg`, plus
  `Shortcuts`/`ShortcutInfo` (display metadata for help overlays) and
  `Action`/`KeyMap`/`SetKeyBindings`/`KeyBindings`/`ParseAction` (rebindable
  Term-level shortcuts), and the live setters a config reload needs —
  `SetTextStyle`, `SetScrollbackRows`, `SetBellMode`, `SetScrollbarWidth`,
  `SetMiddleClickPaste`, `SetMinimumContrast`, `SetNotifyAfter`,
  plus `ActivityKind`/`Cfg.OnActivity` — the pane-event tap a tab bar needs for
  its bell and command-result indicators (bells and OSC 133 command ends only;
  screen output is deliberately not reported — see the type's doc),
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

# context-mode — MANDATORY routing rules

You have context-mode MCP tools available. These rules are NOT optional — they protect your context window from flooding. A single unrouted command can dump 56 KB into context and waste the entire session.

## BLOCKED commands — do NOT attempt these

### curl / wget — BLOCKED
Any Bash command containing `curl` or `wget` is intercepted and replaced with an error message. Do NOT retry.
Instead use:
- `ctx_fetch_and_index(url, source)` to fetch and index web pages
- `ctx_execute(language: "javascript", code: "const r = await fetch(...)")` to run HTTP calls in sandbox

### Inline HTTP — BLOCKED
Any Bash command containing `fetch('http`, `requests.get(`, `requests.post(`, `http.get(`, or `http.request(` is intercepted and replaced with an error message. Do NOT retry with Bash.
Instead use:
- `ctx_execute(language, code)` to run HTTP calls in sandbox — only stdout enters context

### WebFetch — BLOCKED
WebFetch calls are denied entirely. The URL is extracted and you are told to use `ctx_fetch_and_index` instead.
Instead use:
- `ctx_fetch_and_index(url, source)` then `ctx_search(queries)` to query the indexed content

## REDIRECTED tools — use sandbox equivalents

### Bash (>20 lines output)
Bash is ONLY for: `git`, `mkdir`, `rm`, `mv`, `cd`, `ls`, `npm install`, `pip install`, and other short-output commands.
For everything else, use:
- `ctx_batch_execute(commands, queries)` — run multiple commands + search in ONE call
- `ctx_execute(language: "shell", code: "...")` — run in sandbox, only stdout enters context

### Read (for analysis)
If you are reading a file to **Edit** it → Read is correct (Edit needs content in context).
If you are reading to **analyze, explore, or summarize** → use `ctx_execute_file(path, language, code)` instead. Only your printed summary enters context. The raw file content stays in the sandbox.

### Grep (large results)
Grep results can flood context. Use `ctx_execute(language: "shell", code: "grep ...")` to run searches in sandbox. Only your printed summary enters context.

## Tool selection hierarchy

1. **GATHER**: `ctx_batch_execute(commands, queries)` — Primary tool. Runs all commands, auto-indexes output, returns search results. ONE call replaces 30+ individual calls.
2. **FOLLOW-UP**: `ctx_search(queries: ["q1", "q2", ...])` — Query indexed content. Pass ALL questions as array in ONE call.
3. **PROCESSING**: `ctx_execute(language, code)` | `ctx_execute_file(path, language, code)` — Sandbox execution. Only stdout enters context.
4. **WEB**: `ctx_fetch_and_index(url, source)` then `ctx_search(queries)` — Fetch, chunk, index, query. Raw HTML never enters context.
5. **INDEX**: `ctx_index(content, source)` — Store content in FTS5 knowledge base for later search.

## Subagent routing

When spawning subagents (Agent/Task tool), the routing block is automatically injected into their prompt. Bash-type subagents are upgraded to general-purpose so they have access to MCP tools. You do NOT need to manually instruct subagents about context-mode.

## Output constraints

- Keep responses under 500 words.
- Write artifacts (code, configs, PRDs) to FILES — never return them as inline text. Return only: file path + 1-line description.
- When indexing content, use descriptive source labels so others can `ctx_search(source: "label")` later.

## ctx commands

| Command | Action |
|---------|--------|
| `ctx stats` | Call the `ctx_stats` MCP tool and display the full output verbatim |
| `ctx doctor` | Call the `ctx_doctor` MCP tool, run the returned shell command, display as checklist |
| `ctx upgrade` | Call the `ctx_upgrade` MCP tool, run the returned shell command, display as checklist |
