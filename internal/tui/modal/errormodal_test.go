package modal

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/testutil"
)

func TestErrorModal_NotClosedByDefault(t *testing.T) {
	m := NewErrorModal("Create failed", "boom")
	testutil.Equal(t, m.Closed(), false)
	testutil.Equal(t, m.Title(), "Create failed")
	testutil.Equal(t, m.Body(), "boom")
}

func TestErrorModal_AnyKeyDismisses(t *testing.T) {
	tests := []struct {
		name string
		key  *tcell.EventKey
	}{
		{"enter", tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)},
		{"escape", tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)},
		{"rune q", tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone)},
		{"space", tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewErrorModal("t", "b")
			handler := m.InputHandler()
			handler(tt.key, nil)
			testutil.Equal(t, m.Closed(), true)
		})
	}
}

func TestErrorModal_Draw(t *testing.T) {
	sim := drawAt(t, 100, 40)
	m := NewErrorModal("Create failed", "worktree: git worktree add: chdir Development/Personal/hera: no such file or directory")
	m.SetRect(0, 0, 100, 40)
	m.Draw(sim)
	sim.Sync()

	body := screenString(sim)
	testutil.Contains(t, body, "Create failed")
	testutil.Contains(t, body, "no such file")
	testutil.Contains(t, body, "dismiss")
}

func TestErrorModal_DrawEmptyTitleFallsBackToError(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewErrorModal("", "something broke")
	m.SetRect(0, 0, 80, 24)
	m.Draw(sim)
	sim.Sync()
	testutil.Contains(t, screenString(sim), "Error")
}

func TestErrorModal_DrawZeroSizeNoOp(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewErrorModal("t", "b")
	m.SetRect(0, 0, 0, 0)
	m.Draw(sim) // must not panic
}

func TestErrorModal_DrawTinyArea(t *testing.T) {
	sim := drawAt(t, 80, 24)
	m := NewErrorModal("t", "b")
	m.SetRect(0, 0, 8, 3) // narrow + short — must not panic
	m.Draw(sim)
}

func TestWrapErrorBody(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  []string
	}{
		{"empty", "", 20, nil},
		{"zero width", "hello world", 0, nil},
		{"single line", "short msg", 20, []string{"short msg"}},
		{"wraps on words", "one two three four", 8, []string{"one two", "three", "four"}},
		{
			name:  "hard-breaks long token",
			text:  "abcdefghij",
			width: 4,
			want:  []string{"abcd", "efgh", "ij"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapErrorBody(tt.text, tt.width)
			testutil.DeepEqual(t, got, tt.want)
			for _, line := range got {
				if len(line) > tt.width {
					t.Errorf("line %q exceeds width %d", line, tt.width)
				}
			}
		})
	}
}

func TestWrapErrorBody_NoLineExceedsWidth(t *testing.T) {
	// A realistic long error with an unsplittable path must still fit.
	msg := "worktree: git worktree add: chdir /Users/aaron/Development/Personal/very-long-project-name-here: no such file or directory"
	for _, w := range []int{10, 20, 40, 58} {
		for _, line := range wrapErrorBody(msg, w) {
			if len(line) > w {
				t.Fatalf("width=%d: line %q exceeds %d", w, line, w)
			}
		}
		// Round-trip: joining fields back should preserve all words.
		joined := strings.Join(wrapErrorBody(msg, w), " ")
		testutil.Equal(t, strings.Contains(joined, "no") && strings.Contains(joined, "directory"), true)
	}
}
