package term

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParser_SGR_ColonTruecolor(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38:2::255:100:0m"))
	if got, want := g.CurFG, rgbColor(255, 100, 0); got != want {
		t.Errorf("38:2:: fg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[48:2::10:20:30m"))
	if got, want := g.CurBG, rgbColor(10, 20, 30); got != want {
		t.Errorf("48:2:: bg: got %#x want %#x", got, want)
	}
	feed(t, g, p, []byte("\x1b[58:2::1:2:3m"))
	if got, want := g.CurULColor, rgbColor(1, 2, 3); got != want {
		t.Errorf("58:2:: ul: got %#x want %#x", got, want)
	}
}

func TestParser_SGR_ColonIndexed(t *testing.T) {
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38:5:200m"))
	if got, want := g.CurFG, paletteColor(200); got != want {
		t.Errorf("38:5 fg: got %#x want %#x", got, want)
	}
}

func TestParser_SGR_ColonTruecolorTrailing(t *testing.T) {
	// Trailing SGR after a colon color still applies — including the
	// cs-less shape with a semicolon separator before the next param.
	g, p := newParserGrid(1, 1)
	feed(t, g, p, []byte("\x1b[38:2::255:100:0;1m"))
	if got, want := g.CurFG, rgbColor(255, 100, 0); got != want {
		t.Errorf("fg: got %#x want %#x", got, want)
	}
	if g.CurAttrs&attrBold == 0 {
		t.Errorf("trailing bold not applied after 38:2::")
	}
	g2, p2 := newParserGrid(1, 1)
	feed(t, g2, p2, []byte("\x1b[38:2:10:20:30;1m"))
	if got, want := g2.CurFG, rgbColor(10, 20, 30); got != want {
		t.Errorf("cs-less fg: got %#x want %#x", got, want)
	}
	if g2.CurAttrs&attrBold == 0 {
		t.Errorf("trailing bold not applied after cs-less 38:2")
	}
}

func TestParser_XTGETTCAP_HugePartBounded(t *testing.T) {
	g, p := newParserGrid(1, 5)
	var replies []string
	p.SetReplyHandler(func(b []byte) { replies = append(replies, string(b)) })
	huge := strings.Repeat("Z", 8192)
	feed(t, g, p, []byte("\x1bP+q"+huge+"\x1b\\"))
	if len(replies) != 1 {
		t.Fatalf("reply count=%d", len(replies))
	}
	if len(replies[0]) > 256 {
		t.Errorf("huge XTGETTCAP part echoed: reply len=%d", len(replies[0]))
	}
}

func TestParser_APC_TruncatedDropped(t *testing.T) {
	g, p := newParserGrid(1, 5)
	got := 0
	p.SetReplyHandler(func([]byte) { got++ })
	payload := "Ga=q,i=77,q=0;" + strings.Repeat("A", 9000)
	feed(t, g, p, []byte("\x1b_"+payload+"\x1b\\"))
	if got != 0 {
		t.Errorf("truncated APC dispatched: replies=%d", got)
	}
}

func TestParser_OSC_CANAborts(t *testing.T) {
	g, p := newParserGrid(1, 10)
	p.SetTitleHandler(func(string) {})
	feed(t, g, p, []byte("\x1b]0;HACK\x18HELLO\x07"))
	if got := g.At(0, 0).Ch; got != 'H' {
		t.Errorf("CAN in OSC did not abort: cell=%q", got)
	}
}

func TestParser_DCS_CANAborts(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1bP+q544e\x18X"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Errorf("CAN in DCS did not abort: cell=%q", got)
	}
}

func TestParser_APC_CANAborts(t *testing.T) {
	g, p := newParserGrid(1, 5)
	feed(t, g, p, []byte("\x1b_Ga=d\x18X"))
	if got := g.At(0, 0).Ch; got != 'X' {
		t.Errorf("CAN in APC did not abort: cell=%q", got)
	}
}

func TestSanitizeDownloadName_UTF8Safe(t *testing.T) {
	raw := strings.Repeat("a", 254) + "é" + strings.Repeat("b", 10)
	b64 := base64.StdEncoding.EncodeToString([]byte(raw))
	got := sanitizeDownloadName(b64)
	if len(got) > maxDownloadName {
		t.Errorf("name len=%d over cap", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("name splits UTF-8: %q", got)
	}
}
