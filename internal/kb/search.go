package kb

import "strings"

// SanitizeQuery removes FTS5 special characters from a search query.
// FTS5 special chars: " * ( ) : ^ { } - +
func SanitizeQuery(q string) string {
	var b strings.Builder
	b.Grow(len(q))
	for _, r := range q {
		switch r {
		case '"', '*', '(', ')', ':', '^', '{', '}', '-', '+':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
