package term

import "testing"

// putRowAt writes s starting at column 0 of live row row.
func putRowAt(g *grid, row int, s string) {
	g.CursorR, g.CursorC = row, 0
	for _, r := range s {
		g.Put(r)
	}
}

// linkCells marks live-row cells [c0, c1] as an OSC 8 hyperlink to url.
func linkCells(g *grid, row, c0, c1 int, url string) {
	id := g.internLink(url)
	for c := c0; c <= c1; c++ {
		g.Cells[row*g.Cols+c].LinkID = id
	}
}

// urls extracts just the URL text of each target, for order-sensitive asserts.
func urls(targets []hintTarget) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.url
	}
	return out
}

func TestHintTargets_ImplicitReadingOrder(t *testing.T) {
	g := newGrid(4, 60)
	putRowAt(g, 0, "a https://go.dev and https://example.com/x here")
	putRowAt(g, 2, "later https://third.example")

	got := urls(g.hintTargets(nil))
	want := []string{"https://go.dev", "https://example.com/x", "https://third.example"}
	if len(got) != len(want) {
		t.Fatalf("got %d targets %v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHintTargets_WrappedURLIsOneTarget(t *testing.T) {
	// 20 columns forces the URL to wrap; it must come back as one target with
	// one span per row, not two separate links.
	g := newGrid(4, 20)
	putRowAt(g, 0, "https://example.com/a/very/long/path")

	targets := g.hintTargets(nil)
	if len(targets) != 1 {
		t.Fatalf("got %d targets %v, want 1", len(targets), urls(targets))
	}
	if targets[0].url != "https://example.com/a/very/long/path" {
		t.Errorf("url = %q, want the whole wrapped URL", targets[0].url)
	}
	if len(targets[0].spans) < 2 {
		t.Errorf("spans = %v, want one per wrapped row", targets[0].spans)
	}
}

func TestHintTargets_OSC8Link(t *testing.T) {
	g := newGrid(3, 40)
	putRowAt(g, 0, "click here please")
	linkCells(g, 0, 6, 9, "https://osc8.example") // "here"

	targets := g.hintTargets(nil)
	if len(targets) != 1 {
		t.Fatalf("got %d targets %v, want 1", len(targets), urls(targets))
	}
	if targets[0].url != "https://osc8.example" {
		t.Errorf("url = %q, want the registry URL, not the cell text", targets[0].url)
	}
	if len(targets[0].spans) != 1 || targets[0].spans[0].C0 != 6 || targets[0].spans[0].C1 != 9 {
		t.Errorf("spans = %v, want one span over cols 6..9", targets[0].spans)
	}
}

func TestHintTargets_OSC8SuppressesOverlappingImplicit(t *testing.T) {
	// An OSC 8 link whose visible text is itself a URL must yield exactly one
	// target — the explicit one, matching what Cmd+click resolves.
	g := newGrid(3, 40)
	putRowAt(g, 0, "see https://visible.example now")
	linkCells(g, 0, 4, 26, "https://real-target.example")

	targets := g.hintTargets(nil)
	if len(targets) != 1 {
		t.Fatalf("got %d targets %v, want 1 (OSC 8 wins)", len(targets), urls(targets))
	}
	if targets[0].url != "https://real-target.example" {
		t.Errorf("url = %q, want the OSC 8 destination", targets[0].url)
	}
}

func TestHintTargets_NoLinks(t *testing.T) {
	g := newGrid(3, 40)
	putRowAt(g, 0, "nothing to see here")
	if targets := g.hintTargets(nil); len(targets) != 0 {
		t.Fatalf("got %v, want no targets", urls(targets))
	}
}

func TestHintTargets_ReusesBuffer(t *testing.T) {
	g := newGrid(3, 40)
	putRowAt(g, 0, "https://a.example https://b.example")

	first := g.hintTargets(nil)
	if len(first) != 2 {
		t.Fatalf("got %d targets, want 2", len(first))
	}
	// A second pass into the same backing array must produce the same result —
	// hints re-enters this path on every activation.
	second := g.hintTargets(first)
	got, want := urls(second), []string{"https://a.example", "https://b.example"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("repeat pass target %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHintTargets_ScrolledViewportOnly(t *testing.T) {
	// A link scrolled out of the viewport must not be hintable: the overlay has
	// nowhere to draw its label.
	g := newGrid(2, 40)
	g.ScrollbackCap = 10
	putRowAt(g, 0, "https://old.example")
	g.ScrollUp(1)
	g.ScrollUp(1)
	putRowAt(g, 1, "https://new.example")

	got := urls(g.hintTargets(nil))
	if len(got) != 1 || got[0] != "https://new.example" {
		t.Fatalf("got %v, want only the on-screen link", got)
	}
}
