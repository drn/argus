package tui2

import "testing"

func TestIsWordChar(t *testing.T) {
	tests := []struct {
		r    rune
		want bool
	}{
		{'a', true}, {'Z', true}, {'5', true},
		{'*', true}, {'_', true}, {'{', true},
		{' ', false}, {'\t', false}, {'\n', false},
		{'/', false}, {'-', false}, {'.', false},
		{'@', false},
	}
	for _, tt := range tests {
		if got := isWordChar(tt.r); got != tt.want {
			t.Errorf("isWordChar(%q) = %v, want %v", tt.r, got, tt.want)
		}
	}
}

func TestWordLeftPos(t *testing.T) {
	runes := []rune("hello world foo")
	tests := []struct {
		pos  int
		want int
	}{
		{15, 12}, // end → start of "foo"
		{12, 6},  // start of "foo" → start of "world"
		{6, 0},   // start of "world" → start of "hello"
		{0, 0},   // already at start
	}
	for _, tt := range tests {
		got := wordLeftPos(runes, tt.pos)
		if got != tt.want {
			t.Errorf("wordLeftPos(%d) = %d, want %d", tt.pos, got, tt.want)
		}
	}
}

func TestWordRightPos(t *testing.T) {
	runes := []rune("hello world foo")
	tests := []struct {
		pos  int
		want int
	}{
		{0, 5},   // start → end of "hello"
		{5, 11},  // end of "hello" → end of "world"
		{11, 15}, // end of "world" → end of "foo"
		{15, 15}, // already at end
	}
	for _, tt := range tests {
		got := wordRightPos(runes, tt.pos)
		if got != tt.want {
			t.Errorf("wordRightPos(%d) = %d, want %d", tt.pos, got, tt.want)
		}
	}
}

func TestDeleteWordLeft(t *testing.T) {
	runes := []rune("hello world")
	newRunes, newPos := deleteWordLeft(runes, 11) // delete "world"
	got := string(newRunes)
	if got != "hello " || newPos != 6 {
		t.Errorf("deleteWordLeft = (%q, %d), want (\"hello \", 6)", got, newPos)
	}
}

func TestDeleteWordRight(t *testing.T) {
	runes := []rune("hello world")
	newRunes, newPos := deleteWordRight(runes, 0) // delete "hello"
	got := string(newRunes)
	if got != " world" || newPos != 0 {
		t.Errorf("deleteWordRight = (%q, %d), want (\" world\", 0)", got, newPos)
	}
}

func TestWordBoundary_Delimiters(t *testing.T) {
	// '/' and '-' are delimiters (not in wordCharsSet)
	runes := []rune("foo/bar-baz")
	if got := wordRightPos(runes, 0); got != 3 {
		t.Errorf("wordRightPos past / = %d, want 3", got)
	}
	if got := wordRightPos(runes, 3); got != 7 {
		t.Errorf("wordRightPos past - = %d, want 7", got)
	}
}
