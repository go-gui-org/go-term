# go-term: Roadmap

`go-term` is a full-featured terminal-emulator widget for
[go-gui](https://github.com/go-gui-org/go-gui). The API froze at v0.9.0 (the
export audit + Godoc pass of Phases 54–56 landed there) and was amended once
since, by the v0.10.0 `CursorBlink` change below; the road to v1.0.0 below is
only the remaining gate. The pre-freeze phase history lives in `ROADMAP-v0.md`.

Platforms: macOS, Linux, and Windows all supported. The Windows/ConPTY backend
(issue #15) shipped — including native toast notifications — so the PTY boundary
is the only platform-specific layer; everything above it is platform-agnostic.

## Current state

- **v0.9.0** — API freeze: export audit, Godoc pass, `RunAction` dispatch table
  (unbound actions stay palette-invocable), Cmd+S workspace save, CHANGELOG
  narrative. Deprecation shims were not needed (nothing moved).
- **v0.10.0** — Cursor appearance as a user setting (`cursor-style`,
  `cursor-blink`, `cursor-lock`); pane activity indicators (`OnActivity`,
  `NotifyAfter`); falcon state moved to `~/.config/falcon`. **Breaking:**
  `Cfg.CursorBlink` is now a `bool` seeding the cursor, not a `*bool` override;
  the override half moved to `CursorLocked`.
- **v0.11.0** — cgo-free Linux/Windows builds (pure-Go ConPTY, go-glyph, purego
  GL); dual-arch release archives; universal macOS `.dmg`.
- **v0.12.0** — OSC 0/1/2 title sanitization; `--`-first notification delivery;
  Cmd+hover/click links inside mouse-reading apps.
- **v0.13.0** — Row-map scrolling (no screen copies); `term.select-all`
  (`Cmd+A`); XTWINOPS reports (`CSI 18t` and kin).
- **v0.14.0** — Mono rungs migrated to go-gui semantic text roles (go-gui
  v0.78.0).
- The exported surface after the audit: `term` keeps the small public API
  documented in `CLAUDE.md` (widget, theme, actions, live setters, recording /
  replay, activity + input taps); `term/workspace` keeps `Workspace`, `New`,
  `Restore`, `Close`, `View`, `Cfg`, `Save`, `DefaultWorkspacePath`,
  `DefaultConfigPath`, `LiveTermCount`, `ActivePane` — plus the fields added
  since (`Identity`, `RecordDir`, `RecordInput`, `DownloadDir`,
  `ExitWhenLastShellExits`, `OnLastShellExit`, `OnColorScheme`,
  `ExtraCommands`).

## Upcoming

Unshipped work only.

### Phase 57 — Tag v1.0.0 (blocked on go-gui v1.0)

When go-gui ships v1.0.0:

- Bump go-gui and go-glyph to v1.0.0 final
- Remove any deprecation shims left over from the v0.9.0 freeze
- `git tag v1.0.0` with release notes from CHANGELOG.md
- CI: add `apidiff` check against the v0.9.0 baseline

Until then, v0.9.0 is the stable surface users build against. Breaking changes
to it require a v0.10.0.

### Post-1.0 backlog

Tracked as issues without a phase number: smart selection (regex-driven semantic
units, unblocked by the copy-mode click-count and selection-mode state),
quake/dropdown window (needs go-gui support), pipe-scrollback /
open-in-`$EDITOR`, named profiles.

IME: macOS CJK input is fixed and verified end to end (issue #134, needs go-gui
≥ v0.48.0). What remains is verification on Linux ibus — untested, no Linux
machine here.

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

## Completed

| #     | Description          | Unlocked                                     |
| ----- | -------------------- | -------------------------------------------- |
| 54–56 | API freeze at v0.9.0 | Frozen, documented surface; runnable actions |

## Version policy

SemVer pre-1.0: the frozen surface (as amended by v0.10.0) is stable until
v1.0.0, but breaking changes (should any slip through) ship as a new minor, not
into patches.
