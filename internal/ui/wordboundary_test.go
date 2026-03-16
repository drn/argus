package ui

import (
	"testing"
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
