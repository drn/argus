package agent

import "sync/atomic"

// RingBuffer is a fixed-size circular byte buffer.
// When full, new writes overwrite the oldest data.
//
// Thread safety: callers must hold an external mutex for Write, Bytes, Tail,
// Len, and Reset. TotalWritten is the sole exception — it uses an atomic
// counter and is safe to call without any lock.
type RingBuffer struct {
	data  []byte
	size  int
	pos   int
	full  bool
	total atomic.Uint64 // monotonic count of bytes written; lock-free reads
}

// NewRingBuffer creates a ring buffer of the given size.
// If size is 0, the buffer grows without bound and never overwrites old data.
func NewRingBuffer(size int) *RingBuffer {
	if size == 0 {
		return &RingBuffer{size: 0}
	}
	return &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer using bulk copy operations.
// In unbounded mode (size == 0), the buffer grows without limit.
func (rb *RingBuffer) Write(p []byte) {
	rb.total.Add(uint64(len(p)))
	if rb.size == 0 {
		rb.data = append(rb.data, p...)
		rb.pos = len(rb.data)
		return
	}
	for len(p) > 0 {
		n := copy(rb.data[rb.pos:], p)
		p = p[n:]
		rb.pos += n
		if rb.pos >= rb.size {
			rb.pos = 0
			rb.full = true
		}
	}
}

// TotalWritten returns the monotonic count of bytes written to the buffer.
// Safe to call without holding the caller's mutex (atomic load).
func (rb *RingBuffer) TotalWritten() uint64 {
	return rb.total.Load()
}

// Bytes returns the buffered data in order (oldest first).
func (rb *RingBuffer) Bytes() []byte {
	if rb.size == 0 {
		// Unbounded: return a copy of all data
		return append([]byte(nil), rb.data...)
	}
	if !rb.full {
		return append([]byte(nil), rb.data[:rb.pos]...)
	}
	// Full: data from pos..end, then 0..pos
	out := make([]byte, rb.size)
	copy(out, rb.data[rb.pos:])
	copy(out[rb.size-rb.pos:], rb.data[:rb.pos])
	return out
}

// Len returns the number of bytes stored.
func (rb *RingBuffer) Len() int {
	if rb.size == 0 {
		return rb.pos
	}
	if rb.full {
		return rb.size
	}
	return rb.pos
}

// Tail returns the last n bytes from the buffer without copying the entire thing.
// Returns fewer bytes if the buffer contains less than n.
func (rb *RingBuffer) Tail(n int) []byte {
	if n <= 0 {
		return nil
	}
	stored := rb.Len()
	if n >= stored {
		return rb.Bytes()
	}
	if rb.size == 0 {
		// Unbounded: tail is just the last n bytes
		return append([]byte(nil), rb.data[len(rb.data)-n:]...)
	}
	// Bounded circular buffer: reconstruct last n bytes
	if !rb.full {
		// Not wrapped yet: data is contiguous in [0..pos)
		return append([]byte(nil), rb.data[rb.pos-n:rb.pos]...)
	}
	// Full/wrapped: logical order is [pos..size) + [0..pos)
	// pos is the write pointer (oldest byte when full).
	// [pos..size) has (size-pos) bytes, [0..pos) has pos bytes.
	tailStart := stored - n // bytes to skip from the start of logical order
	if tailStart >= rb.size-rb.pos {
		// Tail is entirely within the [0..pos) segment
		offset := tailStart - (rb.size - rb.pos)
		return append([]byte(nil), rb.data[offset:rb.pos]...)
	}
	// Tail spans both segments
	out := make([]byte, n)
	firstStart := rb.pos + tailStart
	firstLen := rb.size - firstStart
	copy(out, rb.data[firstStart:])
	copy(out[firstLen:], rb.data[:rb.pos])
	return out
}

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.pos = 0
	rb.full = false
	if rb.size == 0 {
		rb.data = rb.data[:0]
	}
}
