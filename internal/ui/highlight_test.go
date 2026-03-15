package ui

import (
	"strings"
	"testing"
)

func TestHighlightLines_GoFile(t *testing.T) {
	lines := []string{
		"package main",
		"func hello() string {",
		`	return "world"`,
		"}",
	}
	result := HighlightLines(lines, "main.go")
	if len(result) != len(lines) {
		t.Fatalf("expected %d lines, got %d", len(lines), len(result))
	}
	// Highlighted lines should contain ANSI escape sequences
	for i, line := range result {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("line %d should contain ANSI codes: %q", i, line)
		}
	}
}

func TestHighlightLines_UnknownExtension(t *testing.T) {
	lines := []string{"just some text", "no highlighting"}
	result := HighlightLines(lines, "file.unknownext")
	// Should return lines unchanged
	for i, line := range result {
		if line != lines[i] {
			t.Errorf("line %d changed: %q -> %q", i, lines[i], line)
		}
	}
}

func TestHighlightLines_Empty(t *testing.T) {
	result := HighlightLines(nil, "test.go")
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestHighlightLines_PythonFile(t *testing.T) {
	lines := []string{
		"def hello():",
		"    print('world')",
	}
	result := HighlightLines(lines, "script.py")
	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	// Python keywords should be highlighted
	for _, line := range result {
		if !strings.Contains(line, "\x1b[") {
			t.Errorf("expected ANSI in Python highlight: %q", line)
		}
	}
}

func TestLexerForFile(t *testing.T) {
	tests := []struct {
		filename string
		wantNil  bool
	}{
		{"main.go", false},
		{"script.py", false},
		{"style.css", false},
		{"Makefile", false},
		{"Dockerfile", false},
		{"noext", true},
	}
	for _, tc := range tests {
		lexer := lexerForFile(tc.filename)
		if tc.wantNil && lexer != nil {
			t.Errorf("lexerForFile(%q) should be nil", tc.filename)
		}
		if !tc.wantNil && lexer == nil {
			t.Errorf("lexerForFile(%q) should not be nil", tc.filename)
		}
	}
}
