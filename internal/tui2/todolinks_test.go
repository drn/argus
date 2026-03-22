package tui2

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/testutil"
)

func TestExtractLinks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []Link
	}{
		{
			name:    "no links",
			content: "just plain text\nno urls here",
			want:    nil,
		},
		{
			name:    "single bare URL",
			content: "check out https://example.com/page for details",
			want:    []Link{{Label: "https://example.com/page", URL: "https://example.com/page"}},
		},
		{
			name:    "single markdown link",
			content: "see [Example](https://example.com/page) for info",
			want:    []Link{{Label: "Example", URL: "https://example.com/page"}},
		},
		{
			name:    "markdown link and bare URL",
			content: "[Docs](https://docs.example.com)\nAlso see https://other.example.com",
			want: []Link{
				{Label: "Docs", URL: "https://docs.example.com"},
				{Label: "https://other.example.com", URL: "https://other.example.com"},
			},
		},
		{
			name:    "duplicate URL in markdown and bare form",
			content: "[My Site](https://example.com) and https://example.com",
			want:    []Link{{Label: "My Site", URL: "https://example.com"}},
		},
		{
			name:    "multiple markdown links",
			content: "[A](https://a.com) and [B](https://b.com)",
			want: []Link{
				{Label: "A", URL: "https://a.com"},
				{Label: "B", URL: "https://b.com"},
			},
		},
		{
			name:    "http scheme",
			content: "link: http://insecure.example.com/path",
			want:    []Link{{Label: "http://insecure.example.com/path", URL: "http://insecure.example.com/path"}},
		},
		{
			name:    "URL with query parameters",
			content: "see https://example.com/search?q=test&page=1",
			want:    []Link{{Label: "https://example.com/search?q=test&page=1", URL: "https://example.com/search?q=test&page=1"}},
		},
		{
			name:    "github PR URL",
			content: "PR: https://github.com/org/repo/pull/123",
			want:    []Link{{Label: "https://github.com/org/repo/pull/123", URL: "https://github.com/org/repo/pull/123"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractLinks(tt.content)
			if tt.want == nil {
				testutil.Equal(t, len(got), 0)
				return
			}
			testutil.Equal(t, len(got), len(tt.want))
			for i := range tt.want {
				testutil.Equal(t, got[i].Label, tt.want[i].Label)
				testutil.Equal(t, got[i].URL, tt.want[i].URL)
			}
		})
	}
}

func TestLinkPickerModal_Navigation(t *testing.T) {
	links := []Link{
		{Label: "First", URL: "https://first.com"},
		{Label: "Second", URL: "https://second.com"},
		{Label: "Third", URL: "https://third.com"},
	}

	t.Run("initial state", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		testutil.Equal(t, m.Selected(), false)
		testutil.Equal(t, m.Canceled(), false)
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("down arrow moves cursor", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("j key moves down", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("k key moves up", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("up at top stays at top", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://first.com")
	})

	t.Run("down at bottom stays at bottom", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.SelectedLink().URL, "https://third.com")
	})

	t.Run("enter selects", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(p tview.Primitive) {})
		handler(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.Selected(), true)
		testutil.Equal(t, m.SelectedLink().URL, "https://second.com")
	})

	t.Run("escape cancels", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(p tview.Primitive) {})
		testutil.Equal(t, m.Canceled(), true)
		testutil.Equal(t, m.Selected(), false)
	})
}

func TestOpenURL_RejectsNonHTTP(t *testing.T) {
	// openURL should silently reject non-http schemes.
	// We can't easily test exec.Command didn't fire, but we verify no panic.
	openURL("file:///etc/passwd")
	openURL("javascript:alert(1)")
	openURL("")
	// Valid schemes should not panic either.
	// (We can't stop "open" from actually launching, so just verify no crash.)
}

func TestToDosView_OpenLinks_Callback(t *testing.T) {
	v := NewToDosView()
	items := []ToDoItem{
		{
			Name:    "multi-link",
			Path:    "/tmp/multi.md",
			Content: "[A](https://a.com) and [B](https://b.com)",
		},
	}
	v.list.SetItems(items)

	var receivedLinks []Link
	v.OnOpenLinks = func(links []Link) {
		receivedLinks = links
	}

	// Simulate pressing 'o'
	v.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'o', tcell.ModNone))

	testutil.Equal(t, len(receivedLinks), 2)
	testutil.Equal(t, receivedLinks[0].Label, "A")
	testutil.Equal(t, receivedLinks[1].Label, "B")
}
