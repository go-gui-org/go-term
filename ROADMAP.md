# go-term: Roadmap

`go-term` is a full-featured terminal-emulator widget for
[go-gui](https://github.com/go-gui-org/go-gui). 49 of 56 phases shipped;
work remaining: 49–51 (competitive feature parity) then 52–54 (API
stabilisation, which runs last so it audits the final surface). Phase 55
(v1.0.0) blocked on go-gui v1.0.

Platforms: macOS, Linux, and Windows all supported. The Windows/ConPTY
backend (issue #15) shipped — including native toast notifications — so the
PTY boundary is the only platform-specific layer; everything above it is
platform-agnostic.

## Architecture

```
examples/falcon/main.go
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw.go      OnDraw: bg/fg/graphics/cursor render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/widget_mouse.go     Mouse button/motion/wheel; SGR encoding.
term/widget_clipboard.go Cmd+C/V; opt-in OSC 52 clipboard write.
term/widget_scroll.go    Scrollbar, momentum scroll.
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

Public API: `Cfg`, `Term`, `Theme`, `NamedTheme`, `New`, `View`, `Close`, `Cwd`,
`SetTheme`, `Rows`, `Cols`, `Write`, `PID`, `Alive`, `SetFocused`, `HandleWindowEvent`,
`ShortcutInfo`/`Shortcuts` (help overlay), `ThemeMenuItems`,
`Action`/`KeyMap`/`SetKeyBindings`/`KeyBindings`/`ParseAction` (rebindable
shortcuts), `SetTextStyle`/`SetScrollbackRows`/`SetBellMode`/`SetScrollbarWidth`/
`SetMiddleClickPaste` (live settings),
`StartRecording`/`StopRecording`/`Recording`, `NewReplay`/`ReplayCfg`.

## Upcoming

Unshipped work only. A phase that lands is deleted from here and recorded as
one row in [Completed](#completed).

### Phase 49 — Protocol odds and ends (issue #131)

Four small gaps, batched because each is 30–60 lines of parser plus a fixture;
separate issues would cost more ceremony than code.

- [ ] Mode 2031 + DSR ?996 — color-scheme-change notification, emitted on
      `setTheme` so neovim/delta follow a runtime theme switch
- [ ] OSC 22 — mouse cursor shape (hover plumbing already exists)
- [ ] OSC 9;4 — ConEmu progress reporting; render in the scrollbar track
      alongside the Phase 47 failure ticks
- [ ] XTSMGRAPHICS — sixel geometry / color-register queries, which
      `img2sixel` issues before emitting
- [ ] Conformance fixtures for each

### Phase 50 — Shell integration, completed (issue #132)

Phase 26 records OSC 133 marks and Phase 47 built jumping and output selection
on them — but nothing fires until the user hand-writes shell hooks this repo
never shipped. Three snippets turn on a feature set already paid for.

- [ ] `scripts/shell-integration/` — bash, zsh, fish emitting OSC 133 A/B/C/D
      with exit status plus OSC 7 CWD; idempotent, no clobbering an existing
      hook chain
- [ ] `docs/config.md` install section + README pointer
- [ ] Notify on long-running command completion, derived from `commandSpan`:
      duration over threshold and the window unfocused
- [ ] `[general] notify-after` config key (0 disables), live-reloadable
- [ ] Tab activity / bell / silence indicators in `term/workspace`

### Phase 51 — Keyboard-driven discovery (issue #133)

Copy mode removed the mouse requirement for selection. Opening a URL still
needs Cmd+click, and the ~60 rebindable actions are discoverable only through
the help overlay. Both are overlays over existing machinery — `grid_urls.go`
detection and the `Shortcuts()`/`KeyMap` metadata.

- [ ] `term.hints` — label visible URLs, press a letter to open; open vs.
      copy-to-clipboard variants
- [ ] Command palette: fuzzy search over `actionOrder` + `copyActionOrder`,
      reading chords from `defaultBindings` rather than a second list
- [ ] Both registered as Actions so they are rebindable and appear in the
      overlay
- [ ] `docs/config.md` sections

### Post-1.0 backlog

Tracked as issues without a phase number: smart selection (regex-driven
semantic units, now unblocked by Phase 48's click-count and selection-mode
state), quake/dropdown window (needs go-gui support), pipe-scrollback /
open-in-`$EDITOR`, named profiles.

IME preedit (issue #134) is a correctness gap rather than a feature and should
block Phase 55 independently: nothing in `term/` handles composition, so CJK
input may be impossible today.

### Phase 52 — Export audit + Godoc pass

Runs _after_ 49–51. Each of those phases adds public surface (new Actions,
config keys), so auditing first means auditing twice.

Every exported symbol gets a deliberate reason and complete doc comment.

**term:**

- `Cfg`, `New`, `Term`, `Theme`, `NamedTheme`, predef themes, `ShortcutInfo`, `Shortcuts`, `ThemeMenuItems` — keep
- `Action` + action constants, `KeyMap`, `Cfg.KeyBindings`, `Term.SetKeyBindings`, `Term.KeyBindings`, `Term.Shortcuts` — keep
- `MaxGridDim`, `MaxScrollbackCap` — keep
- `DefaultColor` — unexport (internal encoding detail)
- `Fixture`, `CaptureFixture` — keep with doc disclaimer, or move to `term/termtest` with deprecation shim
- `FocusID()` — keep, document multi-Term contract

**workspace:**

- `Workspace`, `New`, `Restore`, `Close`, `View`, `Cfg`, `Save`, `DefaultWorkspacePath` — keep
- `Tab` — exported with no methods → unexport
- `SplitDir`, `SplitVertical`, `SplitHorizontal` — no public consumer → unexport
- `AddTab`, `CloseTab`, `ClosePane`, `*Tab`, `*Pane`, `SplitPane`, `ToggleHelp`, `CycleTheme`, `LiveTermCount`, `GoToTab`, `ActivePane`, `FocusPane` — review which ones external callers need; unexport the rest

- [ ] term: export audit + Godoc pass
- [ ] workspace: export audit + Godoc pass

### Phase 53 — Deprecation shims + deferred items

- [ ] If `Fixture`/`CaptureFixture` moved, add `// Deprecated:` forwarding re-exports
- [ ] Document `FocusID` multi-Term contract (if kept public)
- [ ] Resolve: auto-load/auto-save — keep explicit `--workspace`/`--save-workspace` flags; no implicit auto-load
- [ ] Implement `Cmd+S` workspace save command (deferred from 39e-5)

### Phase 54 — Changelog + ROADMAP finalisation

- [ ] Write `CHANGELOG.md` — narrative by theme (rendering, input, clipboard, graphics, workspace)
- [ ] Archive: `ROADMAP.md` → `ROADMAP-v0.md`; new thin `ROADMAP.md` for v1.0+ items
- [ ] Update README with v1.0 API examples

### Phase 55 — Tag v1.0.0 (blocked on go-gui v1.0)

When go-gui ships v1.0.0:

- Bump go-gui and go-glyph to v1.0.0 final
- Remove Phase 53 deprecation shims
- `git tag v1.0.0` with release notes from CHANGELOG.md
- CI: add `apidiff` check against v1.0.0 baseline

## Completed

| #   | Description                             | Unlocked                                      |
| --- | --------------------------------------- | --------------------------------------------- |
| 0   | Roadmap                                 | Planning                                      |
| 1   | 256-color + 24-bit truecolor            | `ls --color`, vim themes                      |
| 2   | Cursor save/restore + show/hide         | `tput civis`/`cnorm`                          |
| 3   | Scrollback + wheel + PgUp/PgDn          | `seq 1 5000`                                  |
| 4   | Text selection + copy                   | Drag-select, Cmd+C                            |
| 5   | Paste + bracketed paste                 | Cmd+V, no auto-execute                        |
| 6   | Scroll regions + IL/DL/ICH/DCH + IND/RI | vim, `less`                                   |
| 7   | Alt screen (1049/47/1047)               | vim, `htop`                                   |
| 8   | OSC title + CWD                         | `printf '\x1b]0;hello\x07'`                   |
| 9   | Mouse reporting (X10 + SGR 1006)        | tmux, vim `mouse=a`                           |
| 10  | Cursor style (DECSCUSR) + blink         | bar/underline/block cursors                   |
| 11  | Wide chars + emoji                      | CJK, `🍣`                                     |
| 12  | Italic, Dim, Strikethrough              | Rich text formatting                          |
| 13  | Logical reflow on resize                | Window resize reflows text                    |
| 14  | OSC 52 clipboard + OSC 8 hyperlinks     | Remote clipboard, `ls --hyperlink`            |
| 15  | Search in scrollback                    | Cmd+F "error"                                 |
| 16  | Coalesced text + caching                | 37µs foreground pass, 0 allocs                |
| 17  | Persistent selection                    | Survives scroll/resize                        |
| 18  | Visual bell                             | `printf '\a'` flash                           |
| 19  | Scrollbar indicator                     | Right-edge position thumb                     |
| 20  | Extended underlines + colors            | Curly, dotted, dashed                         |
| 21  | Customizable tab stops (HTS/TBC)        | Legacy CLI tab layouts                        |
| 22  | Meta/Alt key encoding                   | Alt+F word-forward                            |
| 23  | Enhanced keypad + F-keys                | `mc`, `htop`                                  |
| 24  | Color themes + palette API              | Runtime theme switching                       |
| 25  | Dirty row tracking                      | `top`/`htop` lower CPU                        |
| 26  | Semantic shell marks (OSC 133)          | Cmd+Up/Down prompt jumping                    |
| 27  | Kitty Keyboard Protocol                 | Neovim distinct Tab/Ctrl+I                    |
| 28  | SGR-Pixels mouse (1016)                 | Pixel-precise coords                          |
| 29  | Regex search                            | `[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+`              |
| 30  | OSC 10/11/12 dynamic colors             | `printf '\x1b]11;rgb:ff/00/00\x07'`           |
| 31  | Disk-backed scrollback                  | **SKIPPED**                                   |
| 32  | Sixel graphics                          | `img2sixel`                                   |
| 33  | OS notifications (OSC 9/777)            | Desktop notify                                |
| 34  | iTerm2 inline images (OSC 1337)         | `imgcat image.png`                            |
| 35  | Pixel-perfect scrolling                 | Sub-cell delta, momentum                      |
| 36  | Kitty Graphics Protocol                 | `kitten icat`                                 |
| 37  | Font ligatures                          | Fira Code `!=` → single glyph                 |
| 38  | BiDi / RTL text                         | `echo "שלום"`                                 |
| 39  | Splits, panes, tabs, persistence        | Built-in multiplexing; workspace save/restore |
| 40  | Tab reordering                          | Cmd+Alt+[/] move tab left/right               |
| 41  | OSC 4 palette modification              | `printf '\x1b]4;1;#00ff00\a'`                 |
| 42  | DECSCA + VT420 rectangular areas        | DEC forms apps, vttest menu 8                 |
| 43  | Session recording and replay            | `falcon --record` / `--replay`, `gotermrec`   |
| 44  | OSC 1337 file download + sizing keys    | `imgcat -d file`, `imgcat -W 40`              |
| 45  | User config file (INI, live reload)     | `~/.config/go-term/config`, `Cmd+Shift+,`     |
| 46  | Copy mode                               | `Cmd+Shift+Space`, vim keys, `y`              |
| 47  | OSC 133 failures + output selection     | `Cmd+Shift+E`, `Cmd+Shift+O`, scrollbar ticks |
| 48  | Mouse selection semantics               | Double/triple click, Alt+drag block, middle-click paste, alt-screen wheel |

## Commands

```bash
go build ./...                     # Build all
go test -race -count=1 ./...       # Test suite
golangci-lint run ./...            # Lint
cd examples/falcon && go run .       # Visual verification
```
