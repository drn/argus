package tui2

import "strings"

// wordCharsSet matches ~/.dots WORDCHARS='*?_[]~=&;!#$%^(){}<>'.
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
