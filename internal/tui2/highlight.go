package tui2

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
)

// styledChar is a single character with its tcell style from syntax highlighting.
type styledChar struct {
	ch    rune
	style tcell.Style
}

// highlightedLine is one line of syntax-highlighted text as styled characters.
type highlightedLine struct {
	cells []styledChar
}

// highlightLines applies Chroma syntax highlighting to plain-text lines,
// returning per-character tcell styles. Falls back to unstyled text if
// the language is not recognized.
func highlightLines(lines []string, filename string) []highlightedLine {
	result := make([]highlightedLine, len(lines))

	lexer := lexerForFile(filename)
	if lexer == nil {
		// No lexer — return unstyled
		for i, line := range lines {
			result[i] = plainLine(line)
		}
		return result
	}
	lexer = chroma.Coalesce(lexer) // merge adjacent same-type tokens for efficiency

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}

	for i, line := range lines {
		result[i] = tokenizeLine(lexer, style, line)
	}
	return result
}

// tokenizeLine runs the chroma lexer on a single line and maps token types
// to tcell styles using the provided chroma style.
func tokenizeLine(lexer chroma.Lexer, style *chroma.Style, line string) highlightedLine {
	iterator, err := lexer.Tokenise(nil, line)
	if err != nil {
		return plainLine(line)
	}

	var cells []styledChar
	for _, token := range iterator.Tokens() {
		tcStyle := tokenToStyle(style, token.Type)
		for _, r := range token.Value {
			if r == '\n' {
				continue // skip newlines within tokens
			}
			cells = append(cells, styledChar{ch: r, style: tcStyle})
		}
	}
	return highlightedLine{cells: cells}
}

// tokenToStyle maps a chroma token type to a tcell.Style using the given
// chroma style (e.g. monokai).
func tokenToStyle(s *chroma.Style, tokenType chroma.TokenType) tcell.Style {
	entry := s.Get(tokenType)
	ts := tcell.StyleDefault

	if entry.Colour.IsSet() {
		r, g, b := entry.Colour.Red(), entry.Colour.Green(), entry.Colour.Blue()
		ts = ts.Foreground(tcell.NewRGBColor(int32(r), int32(g), int32(b)))
	}
	if entry.Background.IsSet() {
		r, g, b := entry.Background.Red(), entry.Background.Green(), entry.Background.Blue()
		ts = ts.Background(tcell.NewRGBColor(int32(r), int32(g), int32(b)))
	}
	if entry.Bold == chroma.Yes {
		ts = ts.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		ts = ts.Italic(true)
	}
	if entry.Underline == chroma.Yes {
		ts = ts.Underline(true)
	}
	return ts
}

// plainLine returns an unhighlighted line.
func plainLine(line string) highlightedLine {
	cells := make([]styledChar, 0, len(line))
	for _, r := range line {
		cells = append(cells, styledChar{ch: r, style: tcell.StyleDefault})
	}
	return highlightedLine{cells: cells}
}

// lexerForFile returns a chroma lexer for the given filename, or nil if unknown.
// lexers.Match handles both full filenames (Makefile, Dockerfile) and extensions (.go, .py).
func lexerForFile(filename string) chroma.Lexer {
	// Strip common diff prefixes
	name := strings.TrimPrefix(filename, "a/")
	name = strings.TrimPrefix(name, "b/")
	return lexers.Match(name)
}
