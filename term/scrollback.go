package term

// scrollbackRing stores scrolled-off rows in a fixed-capacity ring. Its
// storage is one contiguous slab (cells) carved into per-slot rows. Push
// overwrites the oldest slot when full, so steady-state scrolling allocates
// zero per-row memory.
//
// PushSwap goes further and copies nothing: it stores the caller's row slice
// itself and hands back the slot's previous storage for the caller to reuse.
// After swaps, rows[slot] may point into the screen's slab and the screen may
// hold rows carved from this ring's slab. Anything that reuses or drops the
// slab (SetGeom, an EnsureGeom realloc, DropBacking) bumps gen so the grid
// knows to copy its borrowed rows out before swapping again — see
// grid.scrollUpRegion.
//
// Row returns a slice that aliases ring storage; callers must not retain it
// across a subsequent Push/PushSwap/SetGeom/Reset. All current readers
// consume the returned cells immediately under grid.Mu.
type scrollbackRing struct {
	cells   []cell   // slab, len = cap*cols (nil when disabled)
	rows    [][]cell // len = cap; rows[slot] is that slot's storage
	wrapped []bool   // len = cap     (nil when disabled)
	cols    int
	cap     int
	head    int // slot of oldest row
	size    int // 0..cap
	// gen changes whenever rows is re-carved from a new, reused or dropped
	// slab. A row handed out by PushSwap under an older gen may alias a slot
	// of the current slab.
	gen uint32
}

// maxRingCells bounds a single ring slab in cells, not bytes. A cell is
// roughly two dozen bytes, so this still permits gigabytes in theory; the
// real defense is the upstream clamp (clampScrollback + clampDim). This is
// belt-and-braces so a wild capacity can never hand make() a length that
// panics or exhausts memory.
const maxRingCells = 1 << 28

// clampRingGeom clamps negative inputs to zero and bounds capacity so
// capacity*cols never exceeds maxRingCells. The product is computed in
// int64 so a hostile capacity cannot overflow before the comparison.
func clampRingGeom(capacity, cols int) (int, int) {
	if capacity < 0 {
		capacity = 0
	}
	if cols < 0 {
		cols = 0
	}
	if capacity > 0 && cols > 0 && int64(capacity)*int64(cols) > maxRingCells {
		capacity = maxRingCells / cols
	}
	return capacity, cols
}

func (r *scrollbackRing) Len() int { return r.size }

// slot maps logical index i (0 = oldest) to its storage slot. Callers must
// hold only valid i (0 <= i < size); the cap guard is belt-and-braces so a
// zero ring returns slot 0 instead of panicking on % 0.
func (r *scrollbackRing) slot(i int) int {
	if r.cap <= 0 {
		return 0
	}
	return (r.head + i) % r.cap
}

func (r *scrollbackRing) Row(i int) []cell {
	if i < 0 || i >= r.size || r.cap <= 0 || r.cols <= 0 {
		return nil
	}
	return r.rows[r.slot(i)]
}

func (r *scrollbackRing) Wrapped(i int) bool {
	if i < 0 || i >= r.size || r.cap <= 0 || r.cols <= 0 {
		return false
	}
	return r.wrapped[r.slot(i)]
}

// carve points every slot at its own cols-wide piece of the slab. Each row is
// capped so an append can never spill into the next slot.
func (r *scrollbackRing) carve() {
	if cap(r.rows) >= r.cap {
		r.rows = r.rows[:r.cap]
	} else {
		r.rows = make([][]cell, r.cap)
	}
	for s := range r.cap {
		o := s * r.cols
		r.rows[s] = r.cells[o : o+r.cols : o+r.cols]
	}
}

// ensureBacking lazily allocates the slab dropped by DropBacking. The cap is
// re-bounded here too: the fields were clamped by SetGeom, but a ring built
// by direct field assignment must still never hand make() an absurd length.
func (r *scrollbackRing) ensureBacking() {
	if r.cells != nil {
		return
	}
	if r.cap <= 0 || r.cols <= 0 {
		return
	}
	if int64(r.cap)*int64(r.cols) > maxRingCells {
		r.cap = maxRingCells / r.cols
	}
	r.cells = make([]cell, r.cap*r.cols)
	r.wrapped = make([]bool, r.cap)
	r.carve()
}

// advance claims the slot for a new newest row and reports whether the oldest
// row was evicted to make room.
func (r *scrollbackRing) advance() (slot int, evicted bool) {
	evicted = r.size == r.cap
	if evicted {
		slot = r.head
		r.head = (r.head + 1) % r.cap
	} else {
		slot = (r.head + r.size) % r.cap
		r.size++
	}
	return slot, evicted
}

// Push appends src (cols wide) as the newest row. Returns true when an
// existing row was evicted. Short src is zero-padded; over-long src is
// truncated.
func (r *scrollbackRing) Push(src []cell, wrapped bool) bool {
	if r.cap == 0 || r.cols == 0 {
		return false
	}
	r.ensureBacking()
	slot, evicted := r.advance()
	dst := r.rows[slot]
	n := copy(dst, src)
	clear(dst[n:])
	r.wrapped[slot] = wrapped
	return evicted
}

// PushSwap appends row as the newest entry by reference — no cells are
// copied — and returns the storage that slot held before (the evicted oldest
// row, or an unused slot) as spare. spare has stale contents; the caller owns
// it from here on and must blank it before use. The ring keeps row, so the
// caller must stop using it.
//
// row must be exactly cols wide. Anything else (or a disabled ring) falls
// back to a copying Push and returns row itself as spare, so spare == row
// (same backing) signals the fallback and the caller must not treat the
// screen row as borrowed.
func (r *scrollbackRing) PushSwap(row []cell, wrapped bool) (spare []cell, evicted bool) {
	if r.cap == 0 || r.cols == 0 || len(row) != r.cols {
		return row, r.Push(row, wrapped)
	}
	r.ensureBacking()
	slot, evicted := r.advance()
	spare = r.rows[slot]
	r.rows[slot] = row[:r.cols:r.cols]
	r.wrapped[slot] = wrapped
	return spare, evicted
}

// Reset clears the length without touching the slab. No gen bump is needed:
// the slab is unchanged, so the ring-owned and screen-owned row sets stay
// disjoint — unlike SetGeom/EnsureGeom/DropBacking, nothing reuses or drops
// storage the other side still holds.
func (r *scrollbackRing) Reset() { r.head, r.size = 0, 0 }

// DropBacking releases the backing arrays (cells and wrapped) so the GC can
// reclaim them. cap and cols are preserved; Push will lazy-allocate fresh
// backing on the next write.
func (r *scrollbackRing) DropBacking() {
	r.cells = nil
	r.rows = nil
	r.wrapped = nil
	r.head = 0
	r.size = 0
	r.gen++
}

// SetGeom reallocates at (capacity, cols), dropping stored rows. Inputs are
// normally clamped upstream — the clamp here is belt-and-braces.
func (r *scrollbackRing) SetGeom(capacity, cols int) {
	capacity, cols = clampRingGeom(capacity, cols)
	r.cap, r.cols = capacity, cols
	r.head, r.size = 0, 0
	// Reusing the slab below can put a slot on top of a row the screen
	// borrowed through PushSwap; the new gen tells the grid to copy it out.
	r.gen++
	if capacity > 0 && cols > 0 {
		need := capacity * cols
		if cap(r.cells) >= need && cap(r.cells) <= need*4 {
			r.cells = r.cells[:need]
		} else {
			r.cells = make([]cell, need)
		}
		if cap(r.wrapped) >= capacity && cap(r.wrapped) <= capacity*4 {
			r.wrapped = r.wrapped[:capacity]
		} else {
			r.wrapped = make([]bool, capacity)
		}
		r.carve()
		return
	}
	r.cells = nil
	r.rows = nil
	r.wrapped = nil
}

// EnsureGeom adjusts geometry if it differs. cols change resets content
// (caller repushes). Capacity-only change preserves the newest rows up to
// the new capacity in a single copy pass; oldest are discarded when
// shrinking.
func (r *scrollbackRing) EnsureGeom(capacity, cols int) {
	capacity, cols = clampRingGeom(capacity, cols)
	if r.cap == capacity && r.cols == cols {
		return
	}
	if cols != r.cols || r.size == 0 || capacity == 0 || cols == 0 {
		r.SetGeom(capacity, cols)
		return
	}
	keep := min(r.size, capacity)
	newCells := make([]cell, capacity*cols)
	newWrap := make([]bool, capacity)
	for i := range keep {
		// Read through rows, not the slab: after swaps a slot's storage can
		// live anywhere.
		s := r.slot(r.size - keep + i)
		copy(newCells[i*cols:(i+1)*cols], r.rows[s])
		newWrap[i] = r.wrapped[s]
	}
	r.cells = newCells
	r.wrapped = newWrap
	r.cap = capacity
	r.head = 0
	r.size = keep
	r.carve()
	r.gen++
}
