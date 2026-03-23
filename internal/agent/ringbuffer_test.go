package agent

import (
	"bytes"
	"runtime"
	"testing"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write([]byte("hello"))

	if rb.Len() != 5 {
		t.Errorf("Len() = %d, want 5", rb.Len())
	}
	if !bytes.Equal(rb.Bytes(), []byte("hello")) {
		t.Errorf("Bytes() = %q, want %q", rb.Bytes(), "hello")
	}
}

func TestRingBuffer_Wrap(t *testing.T) {
	rb := NewRingBuffer(5)
	rb.Write([]byte("abcde"))   // fills buffer
	rb.Write([]byte("fg"))      // overwrites a, b

	if rb.Len() != 5 {
		t.Errorf("Len() = %d, want 5", rb.Len())
	}
	got := rb.Bytes()
	if !bytes.Equal(got, []byte("cdefg")) {
		t.Errorf("Bytes() = %q, want %q", got, "cdefg")
	}
}

func TestRingBuffer_LargeWrite(t *testing.T) {
	rb := NewRingBuffer(4)
	rb.Write([]byte("abcdefgh")) // 2x buffer size

	if rb.Len() != 4 {
		t.Errorf("Len() = %d, want 4", rb.Len())
	}
	got := rb.Bytes()
	if !bytes.Equal(got, []byte("efgh")) {
		t.Errorf("Bytes() = %q, want %q", got, "efgh")
	}
}

func TestRingBuffer_Reset(t *testing.T) {
	rb := NewRingBuffer(10)
	rb.Write([]byte("data"))
	rb.Reset()

	if rb.Len() != 0 {
		t.Errorf("Len() after reset = %d", rb.Len())
	}
	if len(rb.Bytes()) != 0 {
		t.Errorf("Bytes() after reset = %q", rb.Bytes())
	}
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(10)
	if rb.Len() != 0 {
		t.Errorf("Len() = %d", rb.Len())
	}
	if len(rb.Bytes()) != 0 {
		t.Errorf("Bytes() = %q", rb.Bytes())
	}
}

func TestRingBuffer_Unbounded(t *testing.T) {
	rb := NewRingBuffer(0)

	if rb.Len() != 0 {
		t.Errorf("Len() = %d, want 0", rb.Len())
	}

	rb.Write([]byte("hello"))
	rb.Write([]byte(" world"))

	if rb.Len() != 11 {
		t.Errorf("Len() = %d, want 11", rb.Len())
	}
	if !bytes.Equal(rb.Bytes(), []byte("hello world")) {
		t.Errorf("Bytes() = %q, want %q", rb.Bytes(), "hello world")
	}
	if rb.TotalWritten() != 11 {
		t.Errorf("TotalWritten() = %d, want 11", rb.TotalWritten())
	}
}

func TestRingBuffer_Unbounded_NeverWraps(t *testing.T) {
	rb := NewRingBuffer(0)
	// Write more data than a fixed 256KB buffer would hold
	big := make([]byte, 1024*1024) // 1MB
	for i := range big {
		big[i] = byte(i % 256)
	}
	rb.Write(big)

	if rb.Len() != len(big) {
		t.Errorf("Len() = %d, want %d", rb.Len(), len(big))
	}
	if !bytes.Equal(rb.Bytes(), big) {
		t.Error("Bytes() does not match original — data was lost")
	}
}

func TestRingBuffer_Unbounded_Reset(t *testing.T) {
	rb := NewRingBuffer(0)
	rb.Write([]byte("data"))
	rb.Reset()

	if rb.Len() != 0 {
		t.Errorf("Len() after reset = %d", rb.Len())
	}
	if len(rb.Bytes()) != 0 {
		t.Errorf("Bytes() after reset = %q", rb.Bytes())
	}
}

func TestRingBuffer_Tail(t *testing.T) {
	tests := []struct {
		name string
		size int    // 0 = unbounded
		data string // written to buffer
		n    int
		want string
	}{
		{"bounded_no_wrap", 10, "hello", 3, "llo"},
		{"bounded_no_wrap_exact", 10, "hello", 5, "hello"},
		{"bounded_no_wrap_over", 10, "hello", 10, "hello"},
		{"bounded_wrapped", 5, "abcdefg", 3, "efg"},
		{"bounded_wrapped_span", 5, "abcdefg", 5, "cdefg"},
		{"bounded_wrapped_all", 5, "abcdefg", 10, "cdefg"},
		{"unbounded", 0, "hello world", 5, "world"},
		{"unbounded_all", 0, "hello", 10, "hello"},
		{"unbounded_exact", 0, "hello", 5, "hello"},
		{"zero_n", 10, "hello", 0, ""},
		{"negative_n", 10, "hello", -1, ""},
		{"empty_buffer", 10, "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rb := NewRingBuffer(tt.size)
			if tt.data != "" {
				rb.Write([]byte(tt.data))
			}
			got := string(rb.Tail(tt.n))
			if got != tt.want {
				t.Errorf("Tail(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestRingBuffer_TotalWritten_Concurrent(t *testing.T) {
	rb := NewRingBuffer(256)
	done := make(chan struct{})

	// Writer goroutine.
	go func() {
		for i := 0; i < 1000; i++ {
			rb.Write([]byte("x"))
		}
		close(done)
	}()

	// Concurrent reads — the race detector will flag any data race.
	for {
		tw := rb.TotalWritten()
		if tw > 0 {
			// Sanity: value should be monotonically increasing.
			if tw2 := rb.TotalWritten(); tw2 < tw {
				t.Errorf("TotalWritten went backwards: %d -> %d", tw, tw2)
			}
		}
		select {
		case <-done:
			if rb.TotalWritten() != 1000 {
				t.Errorf("final TotalWritten() = %d, want 1000", rb.TotalWritten())
			}
			return
		default:
			runtime.Gosched() // yield to writer on GOMAXPROCS=1
		}
	}
}

func TestRingBuffer_TotalWritten(t *testing.T) {
	rb := NewRingBuffer(5)
	if rb.TotalWritten() != 0 {
		t.Errorf("TotalWritten() = %d, want 0", rb.TotalWritten())
	}
	rb.Write([]byte("abc"))
	if rb.TotalWritten() != 3 {
		t.Errorf("TotalWritten() = %d, want 3", rb.TotalWritten())
	}
	rb.Write([]byte("defgh"))
	if rb.TotalWritten() != 8 {
		t.Errorf("TotalWritten() = %d, want 8", rb.TotalWritten())
	}
	// TotalWritten keeps counting even after wrap
	rb.Write([]byte("ij"))
	if rb.TotalWritten() != 10 {
		t.Errorf("TotalWritten() = %d, want 10", rb.TotalWritten())
	}
}
