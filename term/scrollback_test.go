package term

import "testing"

// ---- helpers ----

func makeRow(s string) []cell {
	r := make([]cell, len(s))
	for i, ch := range s {
		r[i] = cell{Ch: ch, Width: 1}
	}
	return r
}

// ---- Len ----

func TestScrollbackRing_Len(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(10, 80)
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
	r.Push(makeRow("hello"), false)
	if r.Len() != 1 {
		t.Errorf("Len() = %d, want 1", r.Len())
	}
	for range 9 {
		r.Push(makeRow("x"), false)
	}
	if r.Len() != 10 {
		t.Errorf("Len() at cap = %d, want 10", r.Len())
	}
	r.Push(makeRow("overflow"), false)
	if r.Len() != 10 {
		t.Errorf("Len() past cap = %d, want 10", r.Len())
	}
}

// ---- Row ----

func TestScrollbackRing_Row(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)
	r.Push(makeRow("bbb"), false)
	r.Push(makeRow("ccc"), false)

	row0 := r.Row(0)
	if string(row0[0].Ch) != "a" {
		t.Errorf("Row(0)[0] = %c, want a", row0[0].Ch)
	}
	row2 := r.Row(2)
	if string(row2[0].Ch) != "c" {
		t.Errorf("Row(2)[0] = %c, want c", row2[0].Ch)
	}
}

func TestScrollbackRing_Row_Bounds(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)

	if r.Row(-1) != nil {
		t.Error("Row(-1) want nil")
	}
	if r.Row(1) != nil {
		t.Error("Row(Len) want nil")
	}
}

func TestScrollbackRing_Row_ZeroCols(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 0)
	r.Push(nil, false)
	if r.Row(0) != nil {
		t.Error("Row(0) with cols=0 want nil")
	}
}

// ---- Wrapped ----

func TestScrollbackRing_Wrapped(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)
	r.Push(makeRow("bbb"), true)

	if r.Wrapped(0) {
		t.Error("Wrapped(0) want false")
	}
	if !r.Wrapped(1) {
		t.Error("Wrapped(1) want true")
	}
}

func TestScrollbackRing_Wrapped_Bounds(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)

	if r.Wrapped(-1) {
		t.Error("Wrapped(-1) want false")
	}
	if r.Wrapped(1) {
		t.Error("Wrapped(Len) want false")
	}
}

// ---- Push ----

func TestScrollbackRing_Push_Eviction(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("111"), false)
	r.Push(makeRow("222"), false)
	r.Push(makeRow("333"), false)
	// ring full, this evicts "111"
	evicted := r.Push(makeRow("444"), false)
	if !evicted {
		t.Error("Push past cap want evicted=true")
	}
	if r.Len() != 3 {
		t.Errorf("Len() = %d, want 3", r.Len())
	}
	if r.Row(0)[0].Ch != '2' {
		t.Errorf("Row(0)[0] = %c, want 2", r.Row(0)[0].Ch)
	}
	if r.Row(2)[0].Ch != '4' {
		t.Errorf("Row(2)[0] = %c, want 4", r.Row(2)[0].Ch)
	}
}

func TestScrollbackRing_Push_WrapAround(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("111"), false)
	r.Push(makeRow("222"), false)
	r.Push(makeRow("333"), false)
	r.Push(makeRow("444"), false) // evicts 111, head=1
	r.Push(makeRow("555"), false) // evicts 222, head=2
	// ring wraps: head=2, so slot(0)=2, slot(1)=0, slot(2)=1
	// rows: 333, 444, 555
	if r.Row(0)[0].Ch != '3' {
		t.Errorf("Row(0)[0] = %c, want 3", r.Row(0)[0].Ch)
	}
	if r.Row(1)[0].Ch != '4' {
		t.Errorf("Row(1)[0] = %c, want 4", r.Row(1)[0].Ch)
	}
	if r.Row(2)[0].Ch != '5' {
		t.Errorf("Row(2)[0] = %c, want 5", r.Row(2)[0].Ch)
	}
}

func TestScrollbackRing_Push_ShortRow(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(2, 80)
	r.Push(makeRow("hi"), false)

	row := r.Row(0)
	if row[0].Ch != 'h' || row[1].Ch != 'i' {
		t.Error("short row: first two cells wrong")
	}
	// trailing cells zero-filled
	if row[2].Ch != 0 {
		t.Errorf("short row: trailing cell has Ch=%d, want 0", row[2].Ch)
	}
}

func TestScrollbackRing_Push_LongRow(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(2, 3)
	long := []cell{{Ch: 'a'}, {Ch: 'b'}, {Ch: 'c'}, {Ch: 'd'}}
	r.Push(long, false)

	row := r.Row(0)
	if len(row) != 3 {
		t.Fatalf("Row(0) len = %d, want 3", len(row))
	}
	if row[0].Ch != 'a' || row[1].Ch != 'b' || row[2].Ch != 'c' {
		t.Error("long row: expected truncation to cols")
	}
}

func TestScrollbackRing_Push_ZeroCap(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(0, 80)
	evicted := r.Push(makeRow("hi"), false)
	if evicted {
		t.Error("Push with cap=0 want evicted=false")
	}
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
}

func TestScrollbackRing_Push_ZeroCols(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 0)
	evicted := r.Push(makeRow("hi"), false)
	if evicted {
		t.Error("Push with cols=0 want evicted=false")
	}
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
}

// ---- Reset ----

func TestScrollbackRing_Reset(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)
	r.Push(makeRow("bbb"), false)
	r.Reset()

	if r.Len() != 0 {
		t.Errorf("Len() after Reset = %d, want 0", r.Len())
	}
	r.Push(makeRow("zzz"), false)
	if r.Row(0)[0].Ch != 'z' {
		t.Error("Push after Reset: cell wrong")
	}
}

// ---- SetGeom ----

func TestScrollbackRing_SetGeom_Basic(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(10, 40)
	if r.cap != 10 || r.cols != 40 {
		t.Errorf("SetGeom(10,40) got cap=%d cols=%d", r.cap, r.cols)
	}
	if len(r.cells) != 400 {
		t.Errorf("len(cells) = %d, want 400", len(r.cells))
	}
	if len(r.wrapped) != 10 {
		t.Errorf("len(wrapped) = %d, want 10", len(r.wrapped))
	}
}

func TestScrollbackRing_SetGeom_NegativeClamp(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(-5, -10)
	if r.cap != 0 || r.cols != 0 {
		t.Errorf("SetGeom(-5,-10) got cap=%d cols=%d, want 0,0", r.cap, r.cols)
	}
	if r.cells != nil {
		t.Error("cells want nil")
	}
}

func TestScrollbackRing_SetGeom_ZeroAlloc(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(0, 80)
	if r.cells != nil {
		t.Error("cells with cap=0 want nil")
	}
	r.SetGeom(10, 0)
	if r.cells != nil {
		t.Error("cells with cols=0 want nil")
	}
}

// ---- EnsureGeom ----

func TestScrollbackRing_EnsureGeom_Noop(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(5, 80)
	r.Push(makeRow("aaa"), false)
	r.Push(makeRow("bbb"), false)
	oldLen := r.Len()
	r.EnsureGeom(5, 80)
	if r.Len() != oldLen {
		t.Errorf("noop EnsureGeom changed Len from %d to %d", oldLen, r.Len())
	}
	if r.Row(0)[0].Ch != 'a' {
		t.Error("noop EnsureGeom changed row content")
	}
}

func TestScrollbackRing_EnsureGeom_ColChange(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(5, 80)
	r.Push(makeRow("aaa"), false)
	r.EnsureGeom(5, 40) // cols differ: drops content
	if r.Len() != 0 {
		t.Errorf("cols-change Len() = %d, want 0", r.Len())
	}
	if r.cols != 40 {
		t.Errorf("cols = %d, want 40", r.cols)
	}
}

func TestScrollbackRing_EnsureGeom_CapShrink(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(5, 80)
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		r.Push(makeRow(s), false)
	}
	r.EnsureGeom(2, 80) // shrink: keep newest 2
	if r.Len() != 2 {
		t.Errorf("shrink Len() = %d, want 2", r.Len())
	}
	if r.Row(0)[0].Ch != '4' || r.Row(1)[0].Ch != '5' {
		t.Errorf("shrink rows: want 4,5 got %c,%c", r.Row(0)[0].Ch, r.Row(1)[0].Ch)
	}
}

func TestScrollbackRing_EnsureGeom_CapGrow(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(2, 80)
	r.Push(makeRow("1"), false)
	r.Push(makeRow("2"), false)
	r.EnsureGeom(5, 80)
	if r.Len() != 2 {
		t.Errorf("grow Len() = %d, want 2", r.Len())
	}
	if r.cap != 5 {
		t.Errorf("cap = %d, want 5", r.cap)
	}
	if r.Row(0)[0].Ch != '1' || r.Row(1)[0].Ch != '2' {
		t.Errorf("grow rows: want 1,2 got %c,%c", r.Row(0)[0].Ch, r.Row(1)[0].Ch)
	}
}

func TestScrollbackRing_EnsureGeom_CapShrinkWrapped(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("111"), false)
	r.Push(makeRow("222"), false)
	r.Push(makeRow("333"), false)
	r.Push(makeRow("444"), false) // evicts 111, head=1
	// rows: 222(slot=1), 333(slot=2), 444(slot=0)
	r.EnsureGeom(2, 5) // shrink: keep newest 2 (333, 444)
	if r.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", r.Len())
	}
	if r.Row(0)[0].Ch != '3' || r.Row(1)[0].Ch != '4' {
		t.Errorf("shrink wrapped: want 3,4 got %c,%c", r.Row(0)[0].Ch, r.Row(1)[0].Ch)
	}
}

func TestScrollbackRing_EnsureGeom_ZeroEmpty(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(5, 80)
	r.Push(makeRow("aaa"), false)
	r.EnsureGeom(0, 80)
	if r.Len() != 0 {
		t.Errorf("zero-cap Len() = %d, want 0", r.Len())
	}
	if r.cells != nil {
		t.Error("zero-cap cells want nil")
	}
}

func TestScrollbackRing_EnsureGeom_NegativeClamp(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(5, 80)
	r.Push(makeRow("aaa"), false)
	r.EnsureGeom(-1, -2)
	if r.cap != 0 || r.cols != 0 {
		t.Errorf("negative EnsureGeom: got cap=%d cols=%d, want 0,0", r.cap, r.cols)
	}
}

// ---- Benchmarks ----

func BenchmarkScrollback_Push(b *testing.B) {
	r := scrollbackRing{}
	r.SetGeom(10000, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = cell{Ch: 'x', Width: 1}
	}
	// pre-fill to capacity so every Push evicts
	for range r.cap {
		r.Push(row, false)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		r.Push(row, false)
	}
}

func BenchmarkScrollback_EnsureGeom(b *testing.B) {
	r := scrollbackRing{}
	r.SetGeom(1000, 80)
	row := make([]cell, 80)
	for i := range row {
		row[i] = cell{Ch: 'x', Width: 1}
	}
	for range r.cap {
		r.Push(row, false)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		r.EnsureGeom(500, 80)
		r.EnsureGeom(1000, 80)
	}
}
