package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// wordChars matches ~/.dots WORDCHARS='*?_[]~=&;!#$%^(){}<>'.
// Characters not in this set (and not letters/digits) are word delimiters.
// This means '/', '-', '.' and whitespace break words, matching zsh behavior.
const wordCharsSet = `*?_[]~=&;!#$%^(){}<>`

// isWordChar reports whether r is a word character under the configured WORDCHARS.
func isWordChar(r rune) bool {
	if r == ' ' || r == '\t' || r == '\n' {
		return false
	}
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
		return true
	}
	return strings.ContainsRune(wordCharsSet, r)
}

// wordLeftPos returns the cursor position after a word-left movement.
func wordLeftPos(runes []rune, pos int) int {
	// Skip non-word chars (delimiters) going left.
	for pos > 0 && !isWordChar(runes[pos-1]) {
		pos--
	}
	// Skip word chars going left (the actual word).
	for pos > 0 && isWordChar(runes[pos-1]) {
		pos--
	}
	return pos
}

// wordRightPos returns the cursor position after a word-right movement.
func wordRightPos(runes []rune, pos int) int {
	// Skip non-word chars (delimiters) going right.
	for pos < len(runes) && !isWordChar(runes[pos]) {
		pos++
	}
	// Skip word chars going right (the actual word).
	for pos < len(runes) && isWordChar(runes[pos]) {
		pos++
	}
	return pos
}

// deleteWordLeft deletes from pos back to the previous word boundary.
// Returns the new rune slice and the new cursor position.
func deleteWordLeft(runes []rune, pos int) ([]rune, int) {
	newPos := wordLeftPos(runes, pos)
	result := make([]rune, 0, len(runes)-(pos-newPos))
	result = append(result, runes[:newPos]...)
	result = append(result, runes[pos:]...)
	return result, newPos
}

// deleteWordRight deletes from pos to the next word boundary.
// Returns the new rune slice and the new cursor position.
func deleteWordRight(runes []rune, pos int) ([]rune, int) {
	newPos := wordRightPos(runes, pos)
	result := make([]rune, 0, len(runes)-(newPos-pos))
	result = append(result, runes[:pos]...)
	result = append(result, runes[newPos:]...)
	return result, pos
}

// applyWordNavTextinput handles word navigation keys for a textinput.Model.
// Returns true if the key was handled (caller should skip the normal Update).
func applyWordNavTextinput(msg tea.KeyMsg, m *textinput.Model) bool {
	switch msg.String() {
	case "alt+left", "alt+b":
		runes := []rune(m.Value())
		m.SetCursor(wordLeftPos(runes, m.Position()))
	case "alt+right", "alt+f":
		runes := []rune(m.Value())
		m.SetCursor(wordRightPos(runes, m.Position()))
	case "alt+backspace", "ctrl+w":
		runes := []rune(m.Value())
		newRunes, newPos := deleteWordLeft(runes, m.Position())
		m.SetValue(string(newRunes))
		m.SetCursor(newPos)
	case "alt+delete", "alt+d":
		runes := []rune(m.Value())
		newRunes, newPos := deleteWordRight(runes, m.Position())
		m.SetValue(string(newRunes))
		m.SetCursor(newPos)
	default:
		return false
	}
	return true
}

// applyWordNavTextarea handles word navigation keys for a textarea.Model.
// Returns true if the key was handled (caller should skip the normal Update).
// Also accepts a height-adjust callback called after delete operations so the
// caller can resize the textarea to fit the new content.
func applyWordNavTextarea(msg tea.KeyMsg, m *textarea.Model, adjustHeight func()) bool {
	switch msg.String() {
	case "alt+left", "alt+b":
		runes := []rune(m.Value())
		m.SetCursor(wordLeftPos(runes, m.LineInfo().CharOffset))
	case "alt+right", "alt+f":
		runes := []rune(m.Value())
		m.SetCursor(wordRightPos(runes, m.LineInfo().CharOffset))
	case "alt+backspace", "ctrl+w":
		runes := []rune(m.Value())
		newRunes, newPos := deleteWordLeft(runes, m.LineInfo().CharOffset)
		m.SetValue(string(newRunes))
		m.SetCursor(newPos)
		if adjustHeight != nil {
			adjustHeight()
		}
	case "alt+delete", "alt+d":
		runes := []rune(m.Value())
		newRunes, newPos := deleteWordRight(runes, m.LineInfo().CharOffset)
		m.SetValue(string(newRunes))
		m.SetCursor(newPos)
		if adjustHeight != nil {
			adjustHeight()
		}
	default:
		return false
	}
	return true
}
