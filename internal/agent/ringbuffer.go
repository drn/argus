package agent

// RingBuffer is a fixed-size circular byte buffer.
// When full, new writes overwrite the oldest data.
type RingBuffer struct {
	data  []byte
	size  int
	pos   int
	full  bool
	total uint64 // monotonic count of bytes written
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
	rb.total += uint64(len(p))
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
func (rb *RingBuffer) TotalWritten() uint64 {
	return rb.total
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

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.pos = 0
	rb.full = false
	if rb.size == 0 {
		rb.data = rb.data[:0]
	}
}
