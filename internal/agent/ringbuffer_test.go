package agent

import (
	"bytes"
	"testing"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := newRingBuffer(10)
	rb.Write([]byte("hello"))

	if rb.Len() != 5 {
		t.Errorf("Len() = %d, want 5", rb.Len())
	}
	if !bytes.Equal(rb.Bytes(), []byte("hello")) {
		t.Errorf("Bytes() = %q, want %q", rb.Bytes(), "hello")
	}
}

func TestRingBuffer_Wrap(t *testing.T) {
	rb := newRingBuffer(5)
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
	rb := newRingBuffer(4)
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
	rb := newRingBuffer(10)
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
	rb := newRingBuffer(10)
	if rb.Len() != 0 {
		t.Errorf("Len() = %d", rb.Len())
	}
	if len(rb.Bytes()) != 0 {
		t.Errorf("Bytes() = %q", rb.Bytes())
	}
}
