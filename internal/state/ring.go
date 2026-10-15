package state

// ring is a fixed-capacity FIFO of killmail IDs. Together with the Store's set
// of the same IDs it bounds the posted history while keeping membership tests
// and insertions O(1).
type ring struct {
	buf  []int64
	head int // index of the oldest ID
	n    int
}

func newRing(capacity int) ring {
	return ring{buf: make([]int64, capacity)}
}

// push adds v as the newest ID. When the ring is full it also returns the ID it
// evicted to make room.
func (r *ring) push(v int64) (int64, bool) {
	if r.n < len(r.buf) {
		r.buf[(r.head+r.n)%len(r.buf)] = v
		r.n++
		return 0, false
	}
	evicted := r.buf[r.head]
	r.buf[r.head] = v
	r.head = (r.head + 1) % len(r.buf)
	return evicted, true
}

// ids returns the retained IDs, oldest first.
func (r *ring) ids() []int64 {
	out := make([]int64, 0, r.n)
	for i := range r.n {
		out = append(out, r.buf[(r.head+i)%len(r.buf)])
	}
	return out
}
