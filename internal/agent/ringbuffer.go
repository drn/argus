package agent

// ringBuffer is a fixed-size circular byte buffer.
// When full, new writes overwrite the oldest data.
type ringBuffer struct {
	data  []byte
	size  int
	pos   int
	full  bool
	total uint64 // monotonic count of bytes written
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		data: make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer using bulk copy operations.
func (rb *ringBuffer) Write(p []byte) {
	rb.total += uint64(len(p))
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
func (rb *ringBuffer) TotalWritten() uint64 {
	return rb.total
}

// Bytes returns the buffered data in order (oldest first).
func (rb *ringBuffer) Bytes() []byte {
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
func (rb *ringBuffer) Len() int {
	if rb.full {
		return rb.size
	}
	return rb.pos
}

// Reset clears the buffer.
func (rb *ringBuffer) Reset() {
	rb.pos = 0
	rb.full = false
}
