# go-term: Roadmap

## Context

`go-term` reached MVP: spawns shell via PTY, renders 16-color cell grid,
basic CSI/SGR + cursor positioning, no scrollback/alt-screen/mouse. Goal
now is to extend the widget toward modern terminal feature parity
(ghostty/iTerm2/kitty) without losing the deliberately small,
single-file-per-layer design.

Each phase below is sized for one focused PR, demo-testable by running
`cd cmd/demo && go run .` and exercising one new behavior. Performance
tuning is deferred — correctness and feature breadth come first.

The architecture stays three layers (`grid.go` → `parser.go` →
`widget.go`) plus `pty.go` and `palette.go`. New state lives in the
existing layer that owns its concept (e.g. scrollback in `grid.go`,
alt-screen toggle in `parser.go` / `widget.go`).

Progress is tracked via the checkboxes below. Tick each box as the
work lands.

## Phase ordering rationale

Phases are ordered by (a) prerequisite chain, (b) user-visible impact,
(c) implementation simplicity. Early phases unlock obviously-broken
behavior in common tools (vim colors, copy/paste, scrollback). Later
phases unlock advanced apps (tmux, mouse-aware editors) and polish.

---

## Active

### Phase 39 — Native Splits, Panes, and Tabs

**Why:** A defining feature of modern terminals is their native window multiplexing, turning the emulator into a full workspace without relying on `tmux`.

- [ ] Create a higher-level layout manager above `Term`.
- [ ] Support vertical/horizontal splits and route keystrokes/PTY IO to the focused pane.

**Demo test:** Cmd+D splits the terminal pane; Cmd+[ switches focus between them.

---

## Completed (archived)

Phases 0–38 are done. Phase 31 (Disk-Backed Scrollback) was skipped — deferred
until real-world memory pressure warrants it.

| Phase | Description | Key capability unlocked |
|-------|-------------|------------------------|
| 0 | Landed roadmap in repo | Planning infrastructure |
| 1 | 256-color + 24-bit truecolor | `ls --color`, `bat`, vim themes |
| 2 | Cursor save/restore + show/hide | `tput civis`/`cnorm`, Ctrl+L |
| 3 | Scrollback ring buffer + mouse wheel + PgUp/PgDn | `seq 1 5000`, scroll up |
| 4 | Text selection + copy to clipboard | Drag-select, Cmd+C |
| 5 | Paste + bracketed paste mode | Cmd+V, no auto-execute |
| 6 | Scroll regions + IL/DL/ICH/DCH + IND/RI | vim, `less` smooth scrolling |
| 7 | Alt screen (DECSET 1049/47/1047) | vim full-screen, `htop` |
| 8 | OSC: window title + CWD | `printf '\x1b]0;hello\x07'` |
| 9 | Mouse reporting (X10 + SGR 1006) | tmux pane-click, vim `mouse=a` |
| 10 | Cursor style (DECSCUSR) + blink | `printf '\x1b[6 q'` bar cursor |
| 11 | Wide chars + emoji (East Asian Wide) | `echo 你好 🍣` |
| 12 | Italic, Dim, Strikethrough | `printf '\x1b[3mITALIC\x1b[0m'` |
| 13 | Logical line wrapping (reflow) | Resize, text reflows |
| 14 | OSC 52 clipboard + OSC 8 hyperlinks | `ls --hyperlink`, remote clipboard |
| 15 | Search in scrollback | Cmd+F, find "error" |
| 16 | Coalesced text + caching | 37µs foreground pass, 0 allocs |
| 17 | Persistent selection | Selection survives scroll/resize |
| 18 | Visual bell | `printf '\a'` flash |
| 19 | Scrollbar indicator | Scroll-position thumb on right edge |
| 20 | Extended underline styles + colors | Curly, dotted, dashed underlines |
| 21 | Customizable tab stops (HTS/TBC) | Legacy CLI app tab layouts |
| 22 | Meta/Alt key encoding | Alt+F for word-forward |
| 23 | Enhanced keypad + function keys | `mc`, `htop` F-keys |
| 24 | Color themes + palette API | Runtime theme switching |
| 25 | Dirty row tracking | `top`/`htop` lower CPU |
| 26 | Semantic shell integration (OSC 133) | Cmd+Up/Down prompt jumping |
| 27 | Kitty Keyboard Protocol | Neovim distinct Tab/Ctrl+I |
| 28 | SGR-Pixels mouse (1016) | Pixel-precise mouse coords |
| 29 | Regular expression search | `[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}` |
| 30 | External control / API (OSC 10/11/12) | `printf '\x1b]11;rgb:ff/00/00\x07'` |
| 31 | Disk-backed scrollback | **SKIPPED** |
| 32 | Sixel graphics | `img2sixel` image previews |
| 33 | OS notifications (OSC 9/777) | Desktop notify on build complete |
| 34 | iTerm2 inline images (OSC 1337) | `imgcat image.png` |
| 35 | Smooth pixel-perfect scrolling | Sub-cell scroll delta, momentum |
| 36 | Kitty Graphics Protocol | `kitten icat` high-perf images |
| 37 | Font ligatures | Fira Code `!=` → single glyph |
| 38 | Bidirectional text (BiDi) + RTL | `echo "שלום"` RTL rendering |

## Critical files

All edits stay in:
- `term/grid.go`
- `term/parser.go`
- `term/widget.go`
- `term/palette.go`
- `term/graphics.go`

## End-to-end verification (every phase)

1. `go vet ./...` clean.
2. `go build ./...` clean.
3. `go test ./term/...` passes.
4. `cd cmd/demo && go run .` and verify visually.
5. Smoke matrix: `ls --color`, `cat /etc/hosts`, `vim` + `:q!`, resize → `stty size`, Ctrl+C interrupts `sleep 100`.

## Out of scope

- IME / dead keys
- Windows / ConPTY
- GPU-accelerated rendering

## Resolved decisions

1. **Color encoding:** pack RGB+flag into a single `uint32` per FG/BG.
2. **Scrollback cap:** default 5000, bounded at 100k.
3. **Selection mouse:** left-drag select + Cmd/Ctrl+C copy. No middle-click PRIMARY.
4. **Alt-screen scrollback:** suppress while alt is active (kitty/iTerm/ghostty default).
5. **Cursor blink:** honor DECSCUSR; allow `Cfg.CursorBlink *bool` override.
