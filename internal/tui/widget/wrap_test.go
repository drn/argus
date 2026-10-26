package widget

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestWrapPreservingLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		in    string
		width int
		want  []string
	}{
		{"zero width", "abc", 0, nil},
		{"keeps newlines and blanks", "a\n\nb", 10, []string{"a", "", "b"}},
		{"word wraps", "one two three", 7, []string{"one two", "three"}},
		{"hard breaks long token", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"hard break flushes current line", "hi abcdefgh", 4, []string{"hi", "abcd", "efgh"}},
		{"rune counted", "ééé ééé", 3, []string{"ééé", "ééé"}},
		{"wide runes count two columns", "漢字漢字", 4, []string{"漢字", "漢字"}},
		{"wide word wraps by width", "ab 漢字", 4, []string{"ab", "漢字"}},
		{"rune wider than line emitted alone", "漢", 1, []string{"漢"}},
		{"zero-width only word skipped", "a \u200b b", 10, []string{"a b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, WrapPreservingLines(tc.in, tc.width), tc.want)
		})
	}
}
