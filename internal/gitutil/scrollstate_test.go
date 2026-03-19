package gitutil

import "testing"

func TestScrollState_CursorUpDown(t *testing.T) {
	var s ScrollState
	s.CursorDown(10, 5)
	if s.Cursor() != 1 {
		t.Errorf("cursor = %d, want 1", s.Cursor())
	}
	s.CursorUp()
	if s.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", s.Cursor())
	}
	// Can't go below 0
	s.CursorUp()
	if s.Cursor() != 0 {
		t.Errorf("cursor = %d, want 0", s.Cursor())
	}
}

func TestScrollState_ClampCursor(t *testing.T) {
	var s ScrollState
	s.SetCursor(10)
	s.ClampCursor(5)
	if s.Cursor() != 4 {
		t.Errorf("cursor = %d, want 4", s.Cursor())
	}
}

func TestScrollState_Reset(t *testing.T) {
	var s ScrollState
	s.SetCursor(5)
	s.SetOffset(3)
	s.Reset()
	if s.Cursor() != 0 || s.Offset() != 0 {
		t.Errorf("after reset: cursor=%d, offset=%d", s.Cursor(), s.Offset())
	}
}
