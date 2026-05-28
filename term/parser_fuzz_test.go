package term

import "testing"

func FuzzParserFeed(f *testing.F) {
	seeds := [][]byte{
		[]byte("hello world"),
		[]byte("\x1b[31mred\x1b[0m"),
		[]byte("\x1b[1;2H"),
		[]byte("\x1b]0;title\x07"),
		[]byte("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"),
		[]byte("\x1b_Gf=24,s=10,v=0;aGVsbG8=\x1b\\"),
		[]byte("\x1bPq\"p\x1b\\"),
		{},
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := NewGrid(24, 80)
		p := NewParser(g)
		g.Mu.Lock()
		defer g.Mu.Unlock()
		p.Feed(data)
	})
}

func FuzzCSIDispatch(f *testing.F) {
	seeds := []string{
		"m", "H", "A", "B", "C", "D",
		"2J", "1J", "0J", "K", "1K", "2K",
		"?25h", "?25l", "?1049h", "?1049l",
		">0u", "=1u", "?u",
		"?1000h", "?1000l", "?1002h", "?1006h",
		"0;0H", "1;1r", "@", "L", "M", "P",
		"3g", " q", "0 q", "2 q",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := NewGrid(24, 80)
		p := NewParser(g)
		buf := make([]byte, 0, len(data)+2)
		buf = append(buf, '\x1b', '[')
		buf = append(buf, data...)
		g.Mu.Lock()
		defer g.Mu.Unlock()
		p.Feed(buf)
	})
}

func FuzzOSCDispatch(f *testing.F) {
	seeds := []string{
		"0;hello",
		"2;My Window Title",
		"7;file:///Users/test",
		"8;;https://example.com",
		"52;c;dGVzdA==",
		"133;A",
		"1337;File=name=AAABAAAA;size=100",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := NewGrid(24, 80)
		p := NewParser(g)
		buf := make([]byte, 0, len(data)+3)
		buf = append(buf, '\x1b', ']')
		buf = append(buf, data...)
		buf = append(buf, '\x07') // BEL terminator
		g.Mu.Lock()
		defer g.Mu.Unlock()
		p.Feed(buf)
	})
}

func FuzzKittyAPC(f *testing.F) {
	seeds := []string{
		"Gf=24,s=10,v=0;aGVsbG8=",
		"Gf=32,s=20,v=0;c=1,r=1;",
		"Ga=d,I=1;",
		"Ga=p,I=1;",
		"Ga=D;",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		g := NewGrid(24, 80)
		p := NewParser(g)
		buf := make([]byte, 0, len(data)+4)
		buf = append(buf, '\x1b', '_')
		buf = append(buf, data...)
		buf = append(buf, '\x1b', '\\') // ST terminator
		g.Mu.Lock()
		defer g.Mu.Unlock()
		p.Feed(buf)
	})
}
