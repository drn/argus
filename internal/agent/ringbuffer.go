package agent

// ringBuffer is a fixed-size circular byte buffer.
// When full, new writes overwrite the oldest data.
type ringBuffer struct {
	data []byte
	size int
	pos  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		data: make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer.
func (rb *ringBuffer) Write(p []byte) {
	for _, b := range p {
		rb.data[rb.pos] = b
		rb.pos = (rb.pos + 1) % rb.size
		if rb.pos == 0 {
			rb.full = true
		}
	}
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
