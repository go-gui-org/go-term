# go-term

![screenshot](screenshot.png)

Project wiki — <https://github.com/go-gui-org/go-term/wiki>.

A full-featured, embeddable terminal-emulator widget for the
[`go-gui`](https://github.com/go-gui-org/go-gui) framework. Spawns a real
shell over a PTY, renders through a GPU-accelerated `gui.DrawCanvas`, and
covers the protocol surface expected by modern CLI tools and TUI frameworks.

Targets macOS, Linux, and Windows (ConPTY).

## Falcon

The screenshot above is **falcon**, the example app: a full terminal emulator
with tabs, splits, workspace save/restore, themes, and session replay. It is
the reference embedder for `term/workspace` and a daily driver on macOS.

```bash
cd examples/falcon && go run .
```

See [examples/falcon/README.md](examples/falcon/README.md) for flags, key
bindings, bundling, and how the pieces fit together.

## Configuration

Fonts, theme, scrollback, bell, scrollbar and every keyboard shortcut can be
set in an optional INI file at `~/.config/go-term/config`, reloadable at
runtime with `Cmd+Shift+,`:

```ini
[font]
family = JetBrainsMono NFM
size   = 13

[general]
theme      = Tokyo Night
scrollback = 20000

[keybindings]
workspace.splitVertical = Cmd+D
term.find               = Cmd+G
```

See [docs/config.md](docs/config.md) for every section, key, default, and the
full list of rebindable actions.

## Shell integration

Prompt jumping, jump-to-last-failure, whole-output selection, and the
long-running-command notification all need the shell to mark where commands
begin and end. Source the hook for your shell — bash, zsh, and fish are in
[`scripts/shell-integration/`](scripts/shell-integration) — and add one line
to your rc file:

```bash
source /path/to/go-term/scripts/shell-integration/goterm.bash
```

Details, including what fish 4.x already does for itself, are in
[docs/config.md](docs/config.md#shell-integration).

## Session recording

Record a terminal session to a `.gtr` file and play it back through the
emulator itself — useful for demos, and for bug reports that reproduce the
problem instead of describing it.

```bash
falcon --record session.gtr    # or Cmd+Shift+R to toggle on the focused pane
falcon --replay session.gtr    # space pauses, +/- speed, . steps, 0 restarts

go run ./term/gotermrec info    session.gtr   # geometry, duration, frame counts
go run ./term/gotermrec play    session.gtr   # timed playback in any terminal
go run ./term/gotermrec export  session.gtr -cast session.cast   # asciicast v2
```

Recordings store the pty's bytes verbatim, so malformed output survives the
round trip; keystrokes are captured only when explicitly enabled
(`Cfg.RecordInput`).

## Testing

```bash
go test ./...
go test ./term -run EmulatorReplay   # replay-style emulator checks
```

The emulator checks run against JSON fixtures in `term/testdata/` — recorded
byte streams plus the grid state they should produce, so parser behaviour is
verified without a live PTY. See
[docs/fixture-capture.md](docs/fixture-capture.md) for capturing a real
terminal session and converting it into a fixture, and
[docs/terminal-verification.md](docs/terminal-verification.md) for the
capability matrix and the manual checks that still need a window.

---

## License

[MIT](LICENSE)
