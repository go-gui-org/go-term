# go-term: Roadmap

`go-term` is a full-featured terminal-emulator widget for
[go-gui](https://github.com/go-gui-org/go-gui). The API is frozen at v0.9.0
(the export audit + Godoc pass of Phases 54–56 landed there); the road to
v1.0.0 below is only the remaining gate. The pre-freeze phase history lives
in `ROADMAP-v0.md`.

Platforms: macOS, Linux, and Windows all supported. The Windows/ConPTY
backend (issue #15) shipped — including native toast notifications — so the
PTY boundary is the only platform-specific layer; everything above it is
platform-agnostic.

## Current state

- **v0.9.0** — API freeze: export audit, Godoc pass, `RunAction` dispatch
  table (unbound actions stay palette-invocable), Cmd+S workspace save,
  CHANGELOG narrative. Deprecation shims were not needed (nothing moved).
- The exported surface after the audit: `term` keeps the small public API
  documented in `CLAUDE.md` (widget, theme, actions, live setters, recording
  / replay, activity + input taps); `term/workspace` keeps `Workspace`, `New`,
  `Restore`, `Close`, `View`, `Cfg`, `Save`, `DefaultWorkspacePath`,
  `DefaultConfigPath`, `LiveTermCount`, `ActivePane`.

## Upcoming

Unshipped work only.

### Phase 57 — Tag v1.0.0 (blocked on go-gui v1.0)

When go-gui ships v1.0.0:

- Bump go-gui and go-glyph to v1.0.0 final
- Remove any deprecation shims left over from the v0.9.0 freeze
- `git tag v1.0.0` with release notes from CHANGELOG.md
- CI: add `apidiff` check against the v0.9.0 baseline

Until then, v0.9.0 is the stable surface users build against. Breaking
changes to it require a v0.10.0.

### Post-1.0 backlog

Tracked as issues without a phase number: smart selection (regex-driven
semantic units, unblocked by the copy-mode click-count and selection-mode
state), quake/dropdown window (needs go-gui support), pipe-scrollback /
open-in-`$EDITOR`, named profiles.

IME: macOS CJK input is fixed and verified end to end (issue #134, needs
go-gui ≥ v0.48.0). What remains is verification on Linux ibus — untested, no
Linux machine here.

## Architecture

```
examples/falcon/main.go
        │
        ▼
term/widget.go           Term struct, New, View, Close; reader goroutine.
term/widget_draw.go      OnDraw: bg/fg/graphics/cursor render passes.
term/widget_keyboard.go  onChar, onKeyDown, onKeyUp; KKP encoding.
term/action_dispatch.go  Action → handler table; RunAction direct dispatch.
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

## Completed

| #   | Description        | Unlocked                                          |
| --- | ------------------ | ------------------------------------------------- |
| 54–56 | API freeze at v0.9.0 | Frozen, documented surface; runnable actions    |

## Version policy

SemVer pre-1.0: the frozen surface is stable until v1.0.0, but breaking
changes (should any slip through) ship as v0.10.0, not into v0.9.x patches.
