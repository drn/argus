package ui

import (
	"path/filepath"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// HighlightLines applies syntax highlighting to a slice of plain-text lines
// based on the file extension. Returns ANSI-colored lines.
func HighlightLines(lines []string, filename string) []string {
	if len(lines) == 0 {
		return lines
	}
	lexer := lexerForFile(filename)
	if lexer == nil {
		return lines
	}
	lexer = chroma.Coalesce(lexer)
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return lines
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	result := make([]string, len(lines))
	for i, line := range lines {
		result[i] = highlightLine(lexer, formatter, style, line)
	}
	return result
}

// highlightLine highlights a single line of code and returns ANSI output.
func highlightLine(lexer chroma.Lexer, formatter chroma.Formatter, style *chroma.Style, line string) string {
	iterator, err := lexer.Tokenise(nil, line)
	if err != nil {
		return line
	}
	var b strings.Builder
	err = formatter.Format(&b, style, iterator)
	if err != nil {
		return line
	}
	// The formatter appends a trailing newline — strip it
	s := b.String()
	s = strings.TrimRight(s, "\n")
	return s
}

// lexerForFile returns a chroma lexer for the given filename, or nil if unknown.
func lexerForFile(filename string) chroma.Lexer {
	// Try by filename first (handles Makefile, Dockerfile, etc.)
	lexer := lexers.Match(filename)
	if lexer != nil {
		return lexer
	}
	// Fall back to extension
	ext := filepath.Ext(filename)
	if ext == "" {
		return nil
	}
	return lexers.Get(ext)
}
