//go:build fixture_capture

package term

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureScenario describes a single fixture to capture.
type fixtureScenario struct {
	name  string
	rows  int
	cols  int
	input []byte
	setup func(g *grid, p *parser)
}

// TestCaptureAllFixtures generates all "whole app" replay fixtures.
// Run manually:
//
//	go test -tags fixture_capture -run TestCaptureAllFixtures -count=1 ./term
func TestCaptureAllFixtures(t *testing.T) {

	scenarios := []fixtureScenario{
		tmuxPaneFixture(),
		bracketedPasteFixture(),
		kittySixelFixture(t),
		bidiFixture(),
		mouseSGRFixture(),
	}

	for _, sc := range scenarios {
		g := newGrid(sc.rows, sc.cols)
		g.CellPxW, g.CellPxH = 8, 16
		p := newParser(g)
		p.SetGraphicsDir(t.TempDir())
		var gotTitle string
		p.SetTitleHandler(func(s string) { gotTitle = s })
		if sc.setup != nil {
			sc.setup(g, p)
		}

		feed(t, g, p, sc.input)

		// gridLines won't include graphics; that's fine — the replay
		// test asserts text cells only.
		f := Fixture{
			Name:      sc.name,
			Rows:      sc.rows,
			Cols:      sc.cols,
			InputB64:  base64.StdEncoding.EncodeToString(sc.input),
			WantLines: gridLines(g),
			WantRow:   g.CursorR,
			WantCol:   g.CursorC,
			WantTitle: gotTitle,
			WantCwd:   g.Cwd,
		}

		b, err := json.MarshalIndent(f, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join("testdata")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, sc.name+".json")
		if err := os.WriteFile(path, b, 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d rows × %d cols, %d input bytes)",
			path, sc.rows, sc.cols, len(sc.input))
	}
}

// tmuxPaneFixture simulates a tmux session with two vertical panes and a
// status bar. Covers alt-screen, DECSCUSR, OSC title, focus reporting,
// box-drawing chars, reverse video, SGR colors, and cursor positioning.
func tmuxPaneFixture() fixtureScenario {
	rows, cols := 12, 70
	dividerCol := 34 // 0-indexed column for the vertical divider
	statusRow := rows - 1

	var s strings.Builder

	// Alt screen on + clear + home.
	s.WriteString("\x1b[?1049h")
	s.WriteString("\x1b[2J\x1b[H")

	// ---- Left pane: shell (cols 0-33) ----
	s.WriteString("\x1b[32m") // green
	s.WriteString("user@host:~/src$ ls -la")
	s.WriteString("\x1b[0m")
	s.WriteString(fmt.Sprintf("\x1b[2;1H"))
	s.WriteString("total 42")
	s.WriteString(fmt.Sprintf("\x1b[3;1H"))
	s.WriteString("drwxr-xr-x  5 user  160 May 30 .")
	s.WriteString(fmt.Sprintf("\x1b[4;1H"))
	s.WriteString("drwxr-xr-x 20 user  640 May 30 ..")
	s.WriteString(fmt.Sprintf("\x1b[5;1H"))
	s.WriteString("-rw-r--r--  1 user  123 May 30 m")
	s.WriteString(fmt.Sprintf("\x1b[6;1H"))
	s.WriteString("-rw-r--r--  1 user  456 May 30 p")
	// Prompt
	s.WriteString(fmt.Sprintf("\x1b[7;1H"))
	s.WriteString("\x1b[32m")
	s.WriteString("user@host:~/src$ ")
	s.WriteString("\x1b[0m")
	// Cursor at prompt
	s.WriteString(fmt.Sprintf("\x1b[7;17H"))
	s.WriteString("\x1b[?25h")
	s.WriteString("\x1b[5 q") // DECSCUSR: blinking bar

	// ---- Right pane: process list (cols 35-69) ----
	rp := dividerCol + 2 // right pane start column (1-indexed)
	s.WriteString(fmt.Sprintf("\x1b[1;%dH", rp))
	s.WriteString("\x1b[33m") // yellow
	s.WriteString("PID COMMAND %CPU %MEM")
	s.WriteString("\x1b[0m")
	s.WriteString(fmt.Sprintf("\x1b[2;%dH", rp))
	s.WriteString("1   launchd   0.0  0.1")
	s.WriteString(fmt.Sprintf("\x1b[3;%dH", rp))
	s.WriteString("234 WindowSer  2.1  1.2")
	s.WriteString(fmt.Sprintf("\x1b[4;%dH", rp))
	s.WriteString("\x1b[1m") // bold
	s.WriteString("567 Terminal   0.5  0.3")
	s.WriteString("\x1b[0m")
	s.WriteString(fmt.Sprintf("\x1b[5;%dH", rp))
	s.WriteString("890 go-term    3.2  2.1")
	s.WriteString(fmt.Sprintf("\x1b[6;%dH", rp))
	s.WriteString("…")

	// ---- Vertical divider ----
	for r := 0; r < statusRow; r++ {
		s.WriteString(fmt.Sprintf("\x1b[%d;%dH", r+1, dividerCol+1))
		s.WriteString("\x1b[36m│\x1b[0m") // cyan box-drawing
	}

	// ---- Status bar (reverse video) ----
	s.WriteString(fmt.Sprintf("\x1b[%d;1H", statusRow+1))
	s.WriteString("\x1b[7m") // reverse video
	status := `[tmux] 0:bash* 1:htop  "hostname" 14:30`
	s.WriteString(status)
	for i := len(status); i < cols; i++ {
		s.WriteString(" ")
	}
	s.WriteString("\x1b[0m")

	// ---- Window title + focus reporting ----
	s.WriteString("\x1b]0;tmux:session\x07")
	s.WriteString("\x1b[?1004h")

	// Cursor back to left pane prompt.
	s.WriteString(fmt.Sprintf("\x1b[7;17H"))

	return fixtureScenario{
		name:  "tmux_panes",
		rows:  rows,
		cols:  cols,
		input: []byte(s.String()),
	}
}

// bracketedPasteFixture simulates a multiline heredoc paste into a shell
// with bracketed paste mode (?2004) enabled. The shell echoes PS2 prompts
// for each continuation line.
func bracketedPasteFixture() fixtureScenario {
	rows, cols := 10, 60

	var s strings.Builder

	s.WriteString("\x1b[2J\x1b[H") // clear + home
	s.WriteString("\x1b[32m")      // green prompt
	s.WriteString("user@host:~$ ")
	s.WriteString("\x1b[0m")
	s.WriteString("\x1b[?2004h") // enable bracketed paste

	// Pasted heredoc — shell echoes each line with PS2 continuation.
	s.WriteString("cat << 'EOF'")
	s.WriteString("\r\n")
	s.WriteString("\x1b[33m>\x1b[0m ") // PS2
	s.WriteString("line one")
	s.WriteString("\r\n")
	s.WriteString("\x1b[33m>\x1b[0m ")
	s.WriteString("line two")
	s.WriteString("\r\n")
	s.WriteString("\x1b[33m>\x1b[0m ")
	s.WriteString("EOF")
	s.WriteString("\r\n")

	// Heredoc output.
	s.WriteString("line one\r\n")
	s.WriteString("line two\r\n")

	// Final prompt.
	s.WriteString("\x1b[32m")
	s.WriteString("user@host:~$ ")
	s.WriteString("\x1b[0m")

	// Disable mode + position cursor.
	s.WriteString("\x1b[?2004l")

	return fixtureScenario{
		name:  "bracketed_paste",
		rows:  rows,
		cols:  cols,
		input: []byte(s.String()),
	}
}

// kittySixelFixture exercises both sixel (DCS) and Kitty Graphics Protocol
// (APC) image rendering in a single scenario.
func kittySixelFixture(t *testing.T) fixtureScenario {
	t.Helper()
	rows, cols := 24, 80

	var s strings.Builder

	// --- Sixel: 10×12 red rectangle ---
	// Sixel body: define color register 1 as red (RGB), then paint 10
	// columns in two bands. cellPxH=16 → ceil(12/16)=1 cell row tall.
	// cellPxW=8 → ceil(10/8)=2 cell cols wide.
	sixelBody := "#1;2;100;0;0" // register 1 = pure red
	sixelBody += "#1!10~"       // band 0: 10 columns
	sixelBody += "-"            // next band
	sixelBody += "#1!10~"       // band 1: 10 columns
	sixelPayload := "0;0;0q" + sixelBody
	s.WriteByte(0x1b)
	s.WriteString("P" + sixelPayload)
	s.WriteByte(0x1b)
	s.WriteString("\\")

	// --- Text after sixel ---
	// Cursor moves below sixel (occupies row 0, cols 0-1).
	s.WriteString("\x1b[2;1H")
	s.WriteString("\x1b[35m") // magenta
	s.WriteString("▲ sixel: 10×12 red rectangle at (0,0)")
	s.WriteString("\x1b[0m")

	// --- Kitty graphics: 4×4 green PNG via APC ---
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0xFF, B: 0, A: 0xFF})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	// APC: ESC _ G a=T,f=100,s=4,v=4,q=1;<b64> ESC \
	s.WriteByte(0x1b)
	s.WriteString("_Ga=T,f=100,s=4,v=4,q=1;")
	s.WriteString(b64)
	s.WriteByte(0x1b)
	s.WriteString("\\")

	// --- Text after kitty graphic ---
	// Kitty image is 4×4 px → ceil(4/16)=1 cell row, ceil(4/8)=1 cell col.
	s.WriteString("\x1b[4;1H")
	s.WriteString("\x1b[36m") // cyan
	s.WriteString("▲ kitty: 4×4 green PNG at row 3")
	s.WriteString("\x1b[0m")

	// --- A second sixel right next to the first text line ---
	// Blue sixel at row 5.
	sixel2 := "#2;2;0;0;100" // register 2 = blue
	sixel2 += "#2!15~"       // 15 columns wide
	sixel2 += "-"
	sixel2 += "#2!15~"
	s.WriteByte(0x1b)
	s.WriteString("P0;0;0q" + sixel2)
	s.WriteByte(0x1b)
	s.WriteString("\\")

	s.WriteString("\x1b[7;1H")
	s.WriteString("\x1b[34m")
	s.WriteString("▲ sixel: 15×12 blue rectangle at row 5")
	s.WriteString("\x1b[0m")

	return fixtureScenario{
		name:  "kitty_sixel",
		rows:  rows,
		cols:  cols,
		input: []byte(s.String()),
	}
}

// bidiFixture exercises bidirectional text with mixed LTR/RTL content.
// Places Arabic and Hebrew text alongside English to test the bidi
// detection path (UAX#9).
func bidiFixture() fixtureScenario {
	rows, cols := 6, 50

	var s strings.Builder
	s.WriteString("\x1b[2J\x1b[H")

	// Row 0: purely LTR header.
	s.WriteString("\x1b[1;1H")
	s.WriteString("BiDi Text Display — Mixed LTR / RTL")

	// Row 1: English label + Arabic RTL text.
	s.WriteString("\x1b[2;1H")
	s.WriteString("Arabic: ")
	s.WriteString("مرحبا بالعالم") // "Hello world" in Arabic

	// Row 2: English label + Hebrew RTL text.
	s.WriteString("\x1b[3;1H")
	s.WriteString("Hebrew: ")
	s.WriteString("שלום עולם") // "Hello world" in Hebrew

	// Row 3: Mixed LTR/RTL on one line.
	s.WriteString("\x1b[4;1H")
	s.WriteString("Hello ")
	s.WriteString("مرحبا") // Arabic
	s.WriteString(" World ")
	s.WriteString("שלום") // Hebrew
	s.WriteString(" !")

	// Row 4: RTL text with Latin-script numbers.
	s.WriteString("\x1b[5;1H")
	s.WriteString("RTL + nums: ")
	// Arabic text with Arabic-Indic digits.
	s.WriteString("السعر ١٢٣٫٤٥ درهم")

	return fixtureScenario{
		name:  "bidi_mixed",
		rows:  rows,
		cols:  cols,
		input: []byte(s.String()),
	}
}

// mouseSGRFixture exercises mouse tracking modes (?1000/?1002/?1003) and
// SGR encoding modes (?1006/?1016). These modes don't change grid pixels
// but affect how the terminal reports mouse events. The fixture enables
// them and renders interactive content that would be clicked/hovered.
func mouseSGRFixture() fixtureScenario {
	rows, cols := 8, 60

	var s strings.Builder

	// Enable mouse tracking modes.
	s.WriteString("\x1b[?1000h") // normal tracking
	s.WriteString("\x1b[?1002h") // button-event tracking
	s.WriteString("\x1b[?1003h") // any-event tracking
	s.WriteString("\x1b[?1006h") // SGR encoding
	s.WriteString("\x1b[?1016h") // SGR pixel coordinates

	// Clear and draw an interactive menu-like UI.
	s.WriteString("\x1b[2J\x1b[H")

	// Menu bar (reverse video).
	s.WriteString("\x1b[7m")
	s.WriteString("  File  Edit  View  Help                                  ")
	s.WriteString("\x1b[0m\r\n")

	// Highlighted menu item (bold).
	s.WriteString("\x1b[1m  → New File       Ctrl+N\x1b[0m\r\n")
	s.WriteString("    Open File...   Ctrl+O\r\n")
	s.WriteString("    Save           Ctrl+S\r\n")
	s.WriteString("    Save As...     Shift+Ctrl+S\r\n")
	s.WriteString("    ───────────────────────\r\n")
	s.WriteString("    Exit           Alt+F4\r\n")

	// Hide cursor (app often hides cursor during mouse interaction).
	s.WriteString("\x1b[?25l")

	// Keep mouse modes enabled in final state so the fixture captures
	// the mode flags. The grid output is the visual menu.

	return fixtureScenario{
		name:  "mouse_sgr",
		rows:  rows,
		cols:  cols,
		input: []byte(s.String()),
	}
}
