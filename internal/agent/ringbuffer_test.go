package agent

import (
	"bytes"
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
