package term

import (
	"math"
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// TestContrastRatio pins the WCAG definition against values that can be looked
// up independently. If this drifts, every threshold a user configures means
// something other than what the accessibility tooling they read it from said.
func TestContrastRatio(t *testing.T) {
	t.Parallel()

	black, white := gui.RGB(0, 0, 0), gui.RGB(255, 255, 255)
	cases := []struct {
		name string
		a, b gui.Color
		want float64
	}{
		{"black_on_white", black, white, 21.0},
		{"white_on_black", white, black, 21.0}, // symmetric
		{"identity", white, white, 1.0},
		// Mid gray on white: the canonical 4.5:1 example from the WCAG docs
		// sits near #767676.
		{"gray_767676_on_white", gui.RGB(0x76, 0x76, 0x76), white, 4.54},
	}
	for _, tc := range cases {
		got := contrastRatio(tc.a, tc.b)
		if math.Abs(got-tc.want) > 0.05 {
			t.Errorf("%s: ratio = %.2f, want %.2f", tc.name, got, tc.want)
		}
	}
}

// TestClampContrast covers the behavior the feature is for: the eza/starship
// case that motivated it — a truecolor orange picked for a dark background,
// landing on a light one at 1.8:1.
func TestClampContrast(t *testing.T) {
	t.Parallel()

	latte := gui.RGB(239, 241, 245) // Catppuccin Latte base
	dark := gui.RGB(30, 30, 46)     // Catppuccin Mocha base
	orange := gui.RGB(255, 161, 1)  // measured from a real starship prompt

	t.Run("raises_a_failing_color", func(t *testing.T) {
		if r := contrastRatio(orange, latte); r >= 3.0 {
			t.Fatalf("premise broken: orange already at %.2f on Latte", r)
		}
		got := clampContrast(orange, latte, 3.0)
		if r := contrastRatio(got, latte); r < 3.0 {
			t.Errorf("clamped to %v, ratio %.2f, want >= 3.0", got, r)
		}
	})

	t.Run("leaves_a_passing_color_alone", func(t *testing.T) {
		// The same orange on the dark theme it was chosen for.
		if got := clampContrast(orange, dark, 3.0); got != orange {
			t.Errorf("clamped %v to %v; it already passes", orange, got)
		}
	})

	t.Run("keeps_the_hue_recognizable", func(t *testing.T) {
		// Darkening an orange toward black must not turn it gray: the child's
		// color choice carries meaning, and a clamp that erased it would be
		// worse than the low contrast.
		got := clampContrast(orange, latte, 4.5)
		if got.R <= got.G || got.G <= got.B {
			t.Errorf("clamped orange %v lost its channel ordering", got)
		}
	})

	t.Run("moves_away_from_the_background", func(t *testing.T) {
		// On a light background the adjustment must darken, and on a dark one
		// lighten — blending toward the near extreme would make it worse
		// before it made it better.
		if got := clampContrast(orange, latte, 4.5); relLuminance(got) >= relLuminance(orange) {
			t.Error("light theme: clamp did not darken")
		}
		washed := gui.RGB(60, 60, 70) // too dark to read on Mocha
		if got := clampContrast(washed, dark, 4.5); relLuminance(got) <= relLuminance(washed) {
			t.Error("dark theme: clamp did not lighten")
		}
	})

	t.Run("picks_the_extreme_with_the_most_headroom", func(t *testing.T) {
		// A mid-tone background is where "is the background light?" and "which
		// direction actually has headroom?" disagree: the WCAG crossover sits
		// at luminance 0.179, so a 50% gray reads as dark while black is the
		// better direction by a factor of two. Answering the first question
		// caps the achievable ratio at ~4.0 and washes the text out to white
		// for nothing.
		mid := gui.RGB(128, 128, 128)
		if relLuminance(mid) > 0.5 {
			t.Fatal("premise broken: mid gray is not on the dark side of 0.5")
		}
		got := clampContrast(gui.RGB(120, 120, 130), mid, 4.5)
		if r := contrastRatio(got, mid); r < 4.5 {
			t.Errorf("clamped to %v, ratio %.2f, want >= 4.5", got, r)
		}
		if relLuminance(got) > relLuminance(mid) {
			t.Errorf("clamped to %v, which is lighter than the background", got)
		}
	})

	t.Run("disabled_is_identity", func(t *testing.T) {
		for _, ratio := range []float64{0, 1, contrastDisabled} {
			if got := clampContrast(orange, latte, ratio); got != orange {
				t.Errorf("ratio %v changed %v to %v", ratio, orange, got)
			}
		}
	})

	t.Run("unreachable_ratio_goes_to_the_extreme", func(t *testing.T) {
		// 21:1 against a mid gray is impossible. Best effort is the extreme,
		// not a panic and not the unchanged color.
		gray := gui.RGB(128, 128, 128)
		got := clampContrast(gui.RGB(130, 130, 130), gray, 21)
		if got != overlayBlack && got != overlayWhite {
			t.Errorf("got %v, want pure black or white", got)
		}
	})
}

// TestContrastMemo checks the cache returns what clampContrast would have, and
// that reset actually drops entries — a memo keyed by color pair but not by
// ratio is wrong, not merely stale, the moment the ratio changes.
func TestContrastMemo(t *testing.T) {
	t.Parallel()

	bg := gui.RGB(239, 241, 245)
	var m contrastMemo

	for _, fg := range []gui.Color{
		gui.RGB(255, 161, 1), gui.RGB(238, 137, 0), gui.RGB(0, 0, 0),
		gui.RGB(255, 255, 255), gui.RGB(30, 102, 245),
	} {
		want := clampContrast(fg, bg, 3.0)
		if got := m.lookup(fg, bg, 3.0); got != want {
			t.Errorf("miss: lookup(%v) = %v, want %v", fg, got, want)
		}
		if got := m.lookup(fg, bg, 3.0); got != want {
			t.Errorf("hit: lookup(%v) = %v, want %v", fg, got, want)
		}
	}

	orange := gui.RGB(255, 161, 1)
	m.reset()
	strict := m.lookup(orange, bg, 7.0)
	if want := clampContrast(orange, bg, 7.0); strict != want {
		t.Errorf("after reset: got %v, want %v", strict, want)
	}
}

// TestMemoKeyDistinguishesPairs guards the packing: a key collision would
// silently paint one color with another's clamp, which is the kind of bug that
// shows up as "one theme looks wrong" months later.
func TestMemoKeyDistinguishesPairs(t *testing.T) {
	t.Parallel()

	seen := map[uint64]bool{}
	for _, p := range [][2]gui.Color{
		{gui.RGB(1, 2, 3), gui.RGB(4, 5, 6)},
		{gui.RGB(4, 5, 6), gui.RGB(1, 2, 3)}, // swapped: a different question
		{gui.RGB(0, 0, 0), gui.RGB(0, 0, 0)},
		{gui.RGB(255, 255, 255), gui.RGB(255, 255, 255)},
	} {
		k := memoKey(p[0], p[1])
		if k == 0 {
			t.Errorf("%v: key is zero, which reads as an empty slot", p)
		}
		if seen[k] {
			t.Errorf("%v: key collision", p)
		}
		seen[k] = true
	}
}

// TestContrastMemoSpreadsSlots guards the slot fold. Correctness does not
// depend on it — a collision is a miss, and a miss recomputes — but a fold that
// does not reach every channel turns the common case into a thrash: a screen of
// syntax colors all share one background, so the foreground alone separates
// them, and it is routine for two of them to agree in two channels.
//
// The assertion is on spread, not on zero collisions: 64 keys in 256 slots
// collide about six times even with a perfect hash. A fold that drops a
// channel collapses one of these sweeps to a single slot, which is two orders
// of magnitude away from the floor below rather than a near miss.
func TestContrastMemoSpreadsSlots(t *testing.T) {
	t.Parallel()

	bg := gui.RGB(239, 241, 245)
	const sweep = 64
	const minDistinct = 40 // a perfect hash averages ~58

	channels := []struct {
		name string
		vary func(v uint8) gui.Color
	}{
		{"red", func(v uint8) gui.Color { return gui.RGB(v, 161, 1) }},
		{"green", func(v uint8) gui.Color { return gui.RGB(255, v, 1) }},
		{"blue", func(v uint8) gui.Color { return gui.RGB(255, 161, v) }},
	}
	for _, ch := range channels {
		t.Run(ch.name, func(t *testing.T) {
			seen := map[uint64]bool{}
			for i := range sweep {
				seen[memoSlot(memoKey(ch.vary(uint8(i*4)), bg))] = true
			}
			if len(seen) < minDistinct {
				t.Errorf("%d distinct slots over %d colors varying only in %s, want >= %d",
					len(seen), sweep, ch.name, minDistinct)
			}
		})
	}
}

// TestApplyMinContrastGate covers the cheap path: with the clamp off, the
// foreground color must come back untouched whatever the background is.
func TestApplyMinContrastGate(t *testing.T) {
	t.Parallel()

	g := newGrid(4, 8)
	fg, bg := gui.RGB(255, 161, 1), gui.RGB(239, 241, 245)
	if got := g.applyMinContrast(fg, bg); got != fg {
		t.Errorf("clamp off: got %v, want %v unchanged", got, fg)
	}
	g.MinContrast = 4.5
	got := g.applyMinContrast(fg, bg)
	if got == fg {
		t.Error("clamp on: color unchanged despite failing the floor")
	}
	if r := contrastRatio(got, bg); r < 4.5 {
		t.Errorf("clamp on: ratio %.2f, want >= 4.5", r)
	}
}

// BenchmarkCellRunKey measures the foreground pass's per-cell cost with the
// clamp off and on. Off must be indistinguishable from before the feature
// existed — the gate is one float comparison — and on must be cheap enough to
// run per cell, which is what the memo is for.
func BenchmarkCellRunKey(b *testing.B) {
	g := newGrid(24, 80)
	g.setTheme(CatppuccinLatteTheme)
	c := cell{Ch: 'x', Width: 1, FG: rgbColor(255, 161, 1)}
	style := gui.TextStyle{}

	b.Run("clamp_off", func(b *testing.B) {
		g.MinContrast = 0
		for b.Loop() {
			_ = cellRunKey(c, style, g, -1, -1, false, false)
		}
	})
	b.Run("clamp_on", func(b *testing.B) {
		g.MinContrast = 4.5
		for b.Loop() {
			_ = cellRunKey(c, style, g, -1, -1, false, false)
		}
	})
}

// TestCellRunKey_AppliesMinimumContrast is the wiring test: the clamp is only
// worth anything if it reaches the color the foreground pass actually paints.
// It also pins the ordering — dim halves a color, so a clamp applied before it
// would be undone by the very transform it is meant to survive.
func TestCellRunKey_AppliesMinimumContrast(t *testing.T) {
	t.Parallel()

	g := newGrid(4, 8)
	g.setTheme(CatppuccinLatteTheme)
	bg := g.Theme.DefaultBG
	style := gui.TextStyle{}

	orange := cell{Ch: 'x', Width: 1, FG: rgbColor(255, 161, 1)}
	if r := contrastRatio(cellRunKey(orange, style, g, -1, -1, false, false).color, bg); r >= 4.5 {
		t.Fatalf("premise broken: orange already at %.2f with the clamp off", r)
	}

	g.MinContrast = 4.5
	if r := contrastRatio(cellRunKey(orange, style, g, -1, -1, false, false).color, bg); r < 4.5 {
		t.Errorf("clamp did not reach the foreground pass: ratio %.2f", r)
	}

	// Dim runs before the clamp, so a dimmed cell must still clear the floor.
	dim := orange
	dim.Attrs |= attrDim
	if r := contrastRatio(cellRunKey(dim, style, g, -1, -1, false, false).color, bg); r < 4.5 {
		t.Errorf("dimmed cell below the floor: ratio %.2f", r)
	}

	// The underline color is deliberately exempt: it decorates text already
	// made legible, and clamping it too would flatten the distinction a
	// colored underline exists to draw.
	ul := orange
	ul.ULColor = rgbColor(255, 161, 1)
	ul.ULStyle = ulSingle
	if got := cellRunKey(ul, style, g, -1, -1, false, false).ulColor; got != gui.RGB(255, 161, 1) {
		t.Errorf("underline color %v was clamped, want it left alone", got)
	}
}
