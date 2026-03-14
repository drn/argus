package ui

// ScrollState tracks cursor position and scroll offset for scrollable lists.
type ScrollState struct {
	cursor int
	offset int
}

// CursorUp moves the cursor up one position, adjusting scroll offset if needed.
func (s *ScrollState) CursorUp() {
	if s.cursor > 0 {
		s.cursor--
		if s.cursor < s.offset {
			s.offset = s.cursor
		}
	}
}

// CursorDown moves the cursor down one position, adjusting scroll offset if needed.
func (s *ScrollState) CursorDown(total, visible int) {
	if s.cursor < total-1 {
		s.cursor++
		if s.cursor >= s.offset+visible {
			s.offset = s.cursor - visible + 1
		}
	}
}

// ClampCursor ensures the cursor is within bounds after the item count changes.
func (s *ScrollState) ClampCursor(total int) {
	if s.cursor >= total {
		s.cursor = max(0, total-1)
	}
}

// Cursor returns the current cursor position.
func (s *ScrollState) Cursor() int { return s.cursor }

// Offset returns the current scroll offset.
func (s *ScrollState) Offset() int { return s.offset }

// Reset sets cursor and offset to zero.
func (s *ScrollState) Reset() {
	s.cursor = 0
	s.offset = 0
}
