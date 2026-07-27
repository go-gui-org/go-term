# go-term

![screenshot](screenshot.png)

A full-featured, embeddable terminal-emulator widget for the
[`go-gui`](https://github.com/go-gui-org/go-gui) framework. Spawns a real
shell over a PTY, renders through a GPU-accelerated `gui.DrawCanvas`, and
covers the protocol surface expected by modern CLI tools and TUI frameworks.

Targets macOS, Linux, and Windows (ConPTY).

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

---

## License

[MIT](LICENSE)
