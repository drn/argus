package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestIsWordChar(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'a', true},
		{'Z', true},
		{'0', true},
		{'9', true},
		{'_', true},  // in WORDCHARS
		{'*', true},  // in WORDCHARS
		{'/', false}, // excluded from WORDCHARS
		{'-', false}, // excluded from WORDCHARS
		{'.', false}, // excluded from WORDCHARS
		{' ', false},
		{'\t', false},
		{'\n', false},
	}
	for _, tt := range tests {
		if got := isWordChar(tt.r); got != tt.want {
			t.Errorf("isWordChar(%q) = %v, want %v", tt.r, got, tt.want)
		}
	}
}

func TestWordLeftPos(t *testing.T) {
	tests := []struct {
		input string
		pos   int
		want  int
	}{
		// Basic word movement
		{"hello world", 11, 6}, // end → start of "world"
		{"hello world", 6, 0},  // start of "world" → start of "hello"
		{"hello world", 5, 0},  // space → start of "hello"
		// Slash delimiters
		{"foo/bar/baz", 11, 8}, // end → start of "baz"
		{"foo/bar/baz", 8, 4},  // start of "baz" → start of "bar"
		{"foo/bar/baz", 4, 0},  // start of "bar" → start of "foo"
		// Dot delimiters
		{"foo.bar.baz", 11, 8},
		{"foo.bar.baz", 8, 4},
		// Dash delimiters
		{"foo-bar-baz", 11, 8},
		{"foo-bar-baz", 8, 4},
		// Mixed
		{"a/b-c.d", 7, 6}, // end → start of "d"
		{"a/b-c.d", 6, 4}, // "d" → start of "c"
		{"a/b-c.d", 4, 2}, // "c" → start of "b"
		{"a/b-c.d", 2, 0}, // "b" → start of "a"
		// Edge cases
		{"hello", 0, 0},  // already at start
		{"hello", 5, 0},  // end of single word
		{"///", 3, 0},    // all delimiters
		{"  foo  ", 7, 2}, // trailing spaces
	}
	for _, tt := range tests {
		runes := []rune(tt.input)
		got := wordLeftPos(runes, tt.pos)
		if got != tt.want {
			t.Errorf("wordLeftPos(%q, %d) = %d, want %d", tt.input, tt.pos, got, tt.want)
		}
	}
}

func TestWordRightPos(t *testing.T) {
	tests := []struct {
		input string
		pos   int
		want  int
	}{
		// Basic word movement
		{"hello world", 0, 5},  // start → end of "hello"
		{"hello world", 5, 11}, // space → end of "world"
		{"hello world", 6, 11}, // start of "world" → end of "world"
		// Slash delimiters
		{"foo/bar/baz", 0, 3},  // start → end of "foo"
		{"foo/bar/baz", 3, 7},  // "/" → end of "bar"
		{"foo/bar/baz", 7, 11}, // "/" → end of "baz"
		// Dot delimiters
		{"foo.bar.baz", 0, 3},
		{"foo.bar.baz", 3, 7},
		// Dash delimiters
		{"foo-bar-baz", 0, 3},
		{"foo-bar-baz", 3, 7},
		// Edge cases
		{"hello", 5, 5},   // already at end
		{"hello", 0, 5},   // start of single word
		{"///", 0, 3},     // all delimiters
		{"  foo  ", 0, 5}, // leading spaces
	}
	for _, tt := range tests {
		runes := []rune(tt.input)
		got := wordRightPos(runes, tt.pos)
		if got != tt.want {
			t.Errorf("wordRightPos(%q, %d) = %d, want %d", tt.input, tt.pos, got, tt.want)
		}
	}
}

func TestDeleteWordLeft(t *testing.T) {
	tests := []struct {
		input   string
		pos     int
		wantStr string
		wantPos int
	}{
		{"hello world", 11, "hello ", 6},
		{"hello world", 6, "world", 0},
		{"foo/bar/baz", 11, "foo/bar/", 8},
		{"foo/bar/baz", 8, "foo/baz", 4},
		{"foo/bar/baz", 4, "bar/baz", 0},
		{"hello", 0, "hello", 0}, // at start, nothing deleted
	}
	for _, tt := range tests {
		runes := []rune(tt.input)
		gotRunes, gotPos := deleteWordLeft(runes, tt.pos)
		gotStr := string(gotRunes)
		if gotStr != tt.wantStr || gotPos != tt.wantPos {
			t.Errorf("deleteWordLeft(%q, %d) = (%q, %d), want (%q, %d)",
				tt.input, tt.pos, gotStr, gotPos, tt.wantStr, tt.wantPos)
		}
	}
}

// newTestTextarea returns a textarea initialized with the given value,
// cursor positioned at the given absolute rune offset.
func newTestTextarea(value string, absPos int) textarea.Model {
	ta := textarea.New()
	ta.SetWidth(200) // wide enough to avoid soft wraps in tests
	ta.SetValue(value)
	// SetValue resets cursor to (0,0); navigate to absPos.
	textareaSetAbsCursorPos(&ta, value, absPos)
	return ta
}

func TestTextareaAbsCursorPos(t *testing.T) {
	tests := []struct {
		value  string
		absPos int // cursor position to set before the call
		want   int
	}{
		// Single line
		{"hello world", 0, 0},
		{"hello world", 6, 6},
		{"hello world", 11, 11},
		// Multi-line: "hello\nfoo bar" — newline at index 5
		{"hello\nfoo bar", 0, 0},
		{"hello\nfoo bar", 5, 5},  // end of first line (at \n)
		{"hello\nfoo bar", 6, 6},  // start of second line ('f')
		{"hello\nfoo bar", 13, 13}, // end of second line
	}
	for _, tt := range tests {
		ta := newTestTextarea(tt.value, tt.absPos)
		got := textareaAbsCursorPos(&ta)
		if got != tt.want {
			t.Errorf("textareaAbsCursorPos value=%q absPos=%d: got %d, want %d (line=%d charOffset=%d)",
				tt.value, tt.absPos, got, tt.want, ta.Line(), ta.LineInfo().CharOffset)
		}
	}
}

func TestTextareaSetAbsCursorPos(t *testing.T) {
	tests := []struct {
		value    string
		absPos   int
		wantLine int
		wantCol  int
	}{
		// Single line
		{"hello world", 0, 0, 0},
		{"hello world", 6, 0, 6},
		{"hello world", 11, 0, 11},
		// Multi-line: "hello\nfoo bar"
		{"hello\nfoo bar", 0, 0, 0},
		{"hello\nfoo bar", 5, 0, 5},  // just before \n — still line 0
		{"hello\nfoo bar", 6, 1, 0},  // just after \n — line 1, col 0
		{"hello\nfoo bar", 10, 1, 4}, // "foo " = 4 chars into line 1
		{"hello\nfoo bar", 13, 1, 7}, // end of "foo bar"
	}
	for _, tt := range tests {
		ta := textarea.New()
		ta.SetWidth(200)
		ta.SetValue(tt.value)
		textareaSetAbsCursorPos(&ta, tt.value, tt.absPos)
		gotLine := ta.Line()
		gotCol := ta.LineInfo().CharOffset
		if gotLine != tt.wantLine || gotCol != tt.wantCol {
			t.Errorf("textareaSetAbsCursorPos value=%q absPos=%d: got (line=%d col=%d), want (line=%d col=%d)",
				tt.value, tt.absPos, gotLine, gotCol, tt.wantLine, tt.wantCol)
		}
	}
}

func TestApplyWordNavTextarea_MultiLine(t *testing.T) {
	altLeft := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	altRight := tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	altBackspace := tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}
	altDelete := tea.KeyMsg{Type: tea.KeyDelete, Alt: true}

	t.Run("alt+left from middle of second line", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at end (abs=13), alt+left → start of "bar" (abs=10)
		ta := newTestTextarea("hello\nfoo bar", 13)
		applyWordNavTextarea(altLeft, &ta, nil)
		got := textareaAbsCursorPos(&ta)
		if got != 10 {
			t.Errorf("alt+left: got absPos %d, want 10", got)
		}
	})

	t.Run("alt+left across line boundary", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at start of second line (abs=6)
		// alt+left should skip \n and land at start of "hello" (abs=0)
		ta := newTestTextarea("hello\nfoo bar", 6)
		applyWordNavTextarea(altLeft, &ta, nil)
		got := textareaAbsCursorPos(&ta)
		if got != 0 {
			t.Errorf("alt+left across newline: got absPos %d, want 0", got)
		}
	})

	t.Run("alt+right from middle of first line", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at abs=0, alt+right → end of "hello" (abs=5)
		ta := newTestTextarea("hello\nfoo bar", 0)
		applyWordNavTextarea(altRight, &ta, nil)
		got := textareaAbsCursorPos(&ta)
		if got != 5 {
			t.Errorf("alt+right: got absPos %d, want 5", got)
		}
	})

	t.Run("alt+right across line boundary", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at abs=5 (at \n), alt+right skips \n and space → end of "foo" (abs=9)
		ta := newTestTextarea("hello\nfoo bar", 5)
		applyWordNavTextarea(altRight, &ta, nil)
		got := textareaAbsCursorPos(&ta)
		if got != 9 {
			t.Errorf("alt+right across newline: got absPos %d, want 9", got)
		}
	})

	t.Run("alt+backspace on second line", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at end (abs=13), alt+backspace deletes "bar"
		ta := newTestTextarea("hello\nfoo bar", 13)
		applyWordNavTextarea(altBackspace, &ta, nil)
		gotVal := ta.Value()
		gotPos := textareaAbsCursorPos(&ta)
		if gotVal != "hello\nfoo " {
			t.Errorf("alt+backspace value: got %q, want %q", gotVal, "hello\nfoo ")
		}
		if gotPos != 10 {
			t.Errorf("alt+backspace absPos: got %d, want 10", gotPos)
		}
	})

	t.Run("alt+delete on second line", func(t *testing.T) {
		// "hello\nfoo bar" — cursor at abs=6 (start of "foo"), alt+delete deletes "foo"
		ta := newTestTextarea("hello\nfoo bar", 6)
		applyWordNavTextarea(altDelete, &ta, nil)
		gotVal := ta.Value()
		gotPos := textareaAbsCursorPos(&ta)
		if gotVal != "hello\n bar" {
			t.Errorf("alt+delete value: got %q, want %q", gotVal, "hello\n bar")
		}
		if gotPos != 6 {
			t.Errorf("alt+delete absPos: got %d, want 6", gotPos)
		}
	})
}

func TestDeleteWordRight(t *testing.T) {
	tests := []struct {
		input   string
		pos     int
		wantStr string
		wantPos int
	}{
		{"hello world", 0, " world", 0},
		{"hello world", 5, "hello", 5},
		{"hello world", 6, "hello ", 6},
		{"foo/bar/baz", 0, "/bar/baz", 0},
		{"foo/bar/baz", 3, "foo/baz", 3},
		{"foo/bar/baz", 7, "foo/bar", 7},
		{"hello", 5, "hello", 5}, // at end, nothing deleted
	}
	for _, tt := range tests {
		runes := []rune(tt.input)
		gotRunes, gotPos := deleteWordRight(runes, tt.pos)
		gotStr := string(gotRunes)
		if gotStr != tt.wantStr || gotPos != tt.wantPos {
			t.Errorf("deleteWordRight(%q, %d) = (%q, %d), want (%q, %d)",
				tt.input, tt.pos, gotStr, gotPos, tt.wantStr, tt.wantPos)
		}
	}
}
