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

func TestScrollbackRing_DropBacking_ReleasesMemory(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(10, 100)
	for range r.cap {
		r.Push(makeRow("x"), false)
	}
	if r.cells == nil {
		t.Fatal("cells nil before DropBacking")
	}
	r.DropBacking()
	if r.cells != nil {
		t.Error("cells want nil after DropBacking")
	}
	if r.wrapped != nil {
		t.Error("wrapped want nil after DropBacking")
	}
	if r.head != 0 || r.size != 0 {
		t.Errorf("head=%d size=%d, want 0,0", r.head, r.size)
	}
}

func TestScrollbackRing_DropBacking_LazyReallocOnPush(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	r.Push(makeRow("aaa"), false)
	r.DropBacking()
	// Push after drop: backing must be lazy-allocated.
	evicted := r.Push(makeRow("zzz"), false)
	if evicted {
		t.Error("first Push after drop: want evicted=false")
	}
	if r.Len() != 1 {
		t.Errorf("Len() = %d, want 1", r.Len())
	}
	if r.Row(0)[0].Ch != 'z' {
		t.Error("row content wrong after lazy realloc")
	}
}

func TestScrollbackRing_DropBacking_ZeroCap(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(0, 80)
	r.DropBacking()
	// Push with cap=0 after drop must remain safe.
	evicted := r.Push(makeRow("hi"), false)
	if evicted {
		t.Error("Push after drop with cap=0 want evicted=false")
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

func TestScrollbackRing_SetGeom_ShrinkRegrow(t *testing.T) {
	r := scrollbackRing{}
	// Allocate a large backing: 10 rows × 100 cols = 1K cells.
	r.SetGeom(10, 100)
	for _, s := range []string{"1", "2", "3", "4", "5"} {
		r.Push(makeRow(s), false)
	}
	if r.Len() != 5 {
		t.Fatalf("after fill: Len()=%d, want 5", r.Len())
	}

	// Shrink cols enough to trigger the >4x waste heuristic:
	// old cap=1000, new need=10*5=50, 1000 > 200 → shrink.
	// Content is dropped because cols change.
	r.SetGeom(10, 5)
	if r.Len() != 0 {
		t.Fatalf("after shrink: Len()=%d, want 0", r.Len())
	}
	// No rows accessible beyond size.
	if r.Row(0) != nil {
		t.Error("Row(0) after shrink: want nil")
	}

	// Push fresh rows into the shrunk backing.
	r.Push(makeRow("aa"), false)
	r.Push(makeRow("bb"), false)
	if r.Len() != 2 {
		t.Fatalf("after shrink-push: Len()=%d, want 2", r.Len())
	}
	if r.Row(0)[0].Ch != 'a' {
		t.Errorf("shrink row 0 Ch=%q, want 'a'", r.Row(0)[0].Ch)
	}
	if r.Row(1)[1].Ch != 'b' {
		t.Errorf("shrink row 1 Ch=%q, want 'b'", r.Row(1)[1].Ch)
	}
	if r.Row(2) != nil {
		t.Error("Row(2) beyond size: want nil")
	}

	// Regrow back to original cols — fresh alloc needed.
	r.SetGeom(10, 100)
	if r.Len() != 0 {
		t.Fatalf("after regrow: Len()=%d, want 0", r.Len())
	}
	r.Push(makeRow("zzzz"), false)
	if r.Row(0)[0].Ch != 'z' {
		t.Errorf("regrow row 0 Ch=%q, want 'z'", r.Row(0)[0].Ch)
	}
	if r.Row(1) != nil {
		t.Error("Row(1) beyond size after regrow: want nil")
	}
}

// ---- slot / zero-ring hardening ----

func TestScrollbackRing_ZeroRing_NoPanic(t *testing.T) {
	var r scrollbackRing // never had SetGeom: cap 0, cols 0
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
	if r.Row(0) != nil {
		t.Error("Row(0) on zero ring want nil")
	}
	if r.Wrapped(0) {
		t.Error("Wrapped(0) on zero ring want false")
	}
	if r.Wrapped(-1) {
		t.Error("Wrapped(-1) on zero ring want false")
	}
}

func TestScrollbackRing_Wrapped_ZeroCols(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 0)
	r.Push(nil, false)
	if r.Wrapped(0) {
		t.Error("Wrapped(0) with cols=0 want false")
	}
}

// ---- PushSwap ----

func TestScrollbackRing_PushSwap_MovesStorageByReference(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	mk := func(ch rune) []cell {
		row := make([]cell, 5)
		for i := range row {
			row[i] = cell{Ch: ch, Width: 1}
		}
		return row
	}
	r1, r2, r3 := mk('1'), mk('2'), mk('3')

	spare, evicted := r.PushSwap(r1, false)
	if evicted {
		t.Error("first PushSwap want evicted=false")
	}
	if len(spare) != 5 {
		t.Fatalf("spare len = %d, want 5", len(spare))
	}
	if &spare[0] == &r1[0] {
		t.Error("spare aliases the just-stored row: swap did not exchange")
	}
	// Newest entry is the caller's slice itself, not a copy.
	if newest := r.Row(r.Len() - 1); len(newest) == 0 || &newest[0] != &r1[0] {
		t.Error("ring did not keep the caller's row by reference")
	}

	if _, evicted := r.PushSwap(r2, true); evicted {
		t.Error("second PushSwap want evicted=false")
	}
	if _, evicted := r.PushSwap(r3, false); evicted {
		t.Error("third PushSwap at cap want evicted=false")
	}
	if !r.Wrapped(1) {
		t.Error("Wrapped(1) want true after swapped push with wrapped=true")
	}

	// Full ring: the next swap evicts the oldest and hands its storage back.
	r4 := mk('4')
	spare, evicted = r.PushSwap(r4, false)
	if !evicted {
		t.Error("PushSwap past cap want evicted=true")
	}
	if len(spare) == 0 || &spare[0] != &r1[0] {
		t.Error("spare past cap is not the evicted oldest row's storage")
	}
	if newest := r.Row(r.Len() - 1); len(newest) == 0 || &newest[0] != &r4[0] {
		t.Error("newest row past cap is not the caller's slice")
	}
	if got := r.Row(0)[0].Ch; got != '2' {
		t.Errorf("Row(0)[0] = %c, want 2 after eviction", got)
	}
}

func TestScrollbackRing_PushSwap_Fallback_WrongLen(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(2, 5)
	short := []cell{{Ch: 'a', Width: 1}, {Ch: 'b', Width: 1}}

	spare, evicted := r.PushSwap(short, false)
	if evicted {
		t.Error("fallback PushSwap want evicted=false")
	}
	// Fallback returns the row itself: same backing signals "not swapped".
	if len(spare) == 0 || &spare[0] != &short[0] {
		t.Error("fallback spare is not the input row itself")
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
	got := r.Row(0)
	if len(got) != 5 {
		t.Fatalf("Row(0) len = %d, want 5", len(got))
	}
	if got[0].Ch != 'a' || got[1].Ch != 'b' {
		t.Error("fallback copy lost the input cells")
	}
	if got[2].Ch != 0 {
		t.Errorf("fallback copy: trailing Ch=%d, want 0-padded", got[2].Ch)
	}
	if &got[0] == &short[0] {
		t.Error("fallback stored an alias of the input, want a copy")
	}
}

func TestScrollbackRing_PushSwap_Fallback_Disabled(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(0, 80)
	row := makeRow("hi")
	spare, evicted := r.PushSwap(row, false)
	if evicted {
		t.Error("disabled PushSwap want evicted=false")
	}
	if len(spare) == 0 || &spare[0] != &row[0] {
		t.Error("disabled spare is not the input row itself")
	}
	if r.Len() != 0 {
		t.Errorf("Len() = %d, want 0", r.Len())
	}
}

func TestScrollbackRing_PushSwap_LazyReallocAfterDrop(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	row := make([]cell, 5)
	for i := range row {
		row[i] = cell{Ch: 'a', Width: 1}
	}
	if _, evicted := r.PushSwap(row, false); evicted {
		t.Fatal("setup PushSwap want evicted=false")
	}
	r.DropBacking()
	next := make([]cell, 5)
	for i := range next {
		next[i] = cell{Ch: 'z', Width: 1}
	}
	spare, evicted := r.PushSwap(next, true)
	if evicted {
		t.Error("first PushSwap after drop want evicted=false")
	}
	if len(spare) != 5 {
		t.Fatalf("spare len = %d, want 5", len(spare))
	}
	if r.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", r.Len())
	}
	if got := r.Row(0); len(got) == 0 || &got[0] != &next[0] {
		t.Error("ring did not keep the swapped row by reference after drop")
	}
	if !r.Wrapped(0) {
		t.Error("Wrapped(0) want true")
	}
	// The spare must be live ring-slab storage, safe for the caller to reuse.
	for i := range spare {
		spare[i] = cell{Ch: 'q', Width: 1}
	}
	if got := r.Row(0)[0].Ch; got != 'z' {
		t.Errorf("writing spare corrupted the ring: Ch=%c, want z", got)
	}
}

// ---- gen ----

func TestScrollbackRing_Gen(t *testing.T) {
	r := scrollbackRing{}
	r.SetGeom(3, 5)
	base := r.gen
	r.Push(makeRow("aaa"), false)
	if r.gen != base {
		t.Error("Push bumped gen: copies never alias, want unchanged")
	}
	r.Reset()
	if r.gen != base {
		t.Error("Reset bumped gen: slab unchanged, want unchanged")
	}
	r.SetGeom(3, 5)
	if r.gen == base {
		t.Error("SetGeom did not bump gen")
	}
	base = r.gen
	r.Push(makeRow("aaa"), false)
	r.EnsureGeom(3, 5) // noop
	if r.gen != base {
		t.Error("noop EnsureGeom bumped gen")
	}
	r.EnsureGeom(5, 5) // realloc: newest rows preserved on a new slab
	if r.gen == base {
		t.Error("EnsureGeom realloc did not bump gen")
	}
	base = r.gen
	r.DropBacking()
	if r.gen == base {
		t.Error("DropBacking did not bump gen")
	}
}

// ---- wild-capacity clamp ----

// The clamp is allocation-free by construction: clampRingGeom bounds the
// product before any make(), so pin it directly instead of allocating a
// maxRingCells slab in the test.
func TestClampRingGeom(t *testing.T) {
	capacity, cols := clampRingGeom(1<<40, 80)
	if want := maxRingCells / 80; capacity != want || cols != 80 {
		t.Errorf("clampRingGeom(1<<40, 80) = (%d, %d), want (%d, 80)",
			capacity, cols, want)
	}
	if int64(capacity)*int64(cols) > maxRingCells {
		t.Errorf("product %d exceeds maxRingCells", int64(capacity)*int64(cols))
	}
	if c, co := clampRingGeom(-5, -10); c != 0 || co != 0 {
		t.Errorf("clampRingGeom(-5, -10) = (%d, %d), want (0, 0)", c, co)
	}
	if c, co := clampRingGeom(10, 80); c != 10 || co != 80 {
		t.Errorf("clampRingGeom(10, 80) = (%d, %d), want (10, 80)", c, co)
	}
}

// ---- Benchmarks ----

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
