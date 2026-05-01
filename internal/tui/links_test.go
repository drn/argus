package tui

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
		{
			name:    "URL with ANSI escape sequences",
			content: "see \x1b[34mhttps://example.com/page\x1b[0m for info",
			want:    []Link{{Label: "https://example.com/page", URL: "https://example.com/page"}},
		},
		{
			name:    "ANSI mid-URL stripped before matching",
			content: "visit https://example.com/\x1b[1mpath\x1b[0m done",
			want:    []Link{{Label: "https://example.com/path", URL: "https://example.com/path"}},
		},
		{
			name:    "duplicate URLs after ANSI stripping",
			content: "\x1b[34mhttps://example.com\x1b[0m and https://example.com",
			want:    []Link{{Label: "https://example.com", URL: "https://example.com"}},
		},
		{
			name:    "trailing punctuation stripped",
			content: "see https://example.com/page. Also https://other.com,",
			want: []Link{
				{Label: "https://example.com/page", URL: "https://example.com/page"},
				{Label: "https://other.com", URL: "https://other.com"},
			},
		},
		{
			// Display text ("link") is not preserved as label — only the URL
			// embedded in the OSC 8 tag is extracted. Dedup with the bare URL.
			name:    "OSC 8 hyperlink with BEL terminator",
			content: "\x1b]8;;https://example.com\x07link\x1b]8;;\x07 and https://example.com",
			want:    []Link{{Label: "https://example.com", URL: "https://example.com"}},
		},
		{
			name:    "OSC 8 hyperlink with ST terminator",
			content: "\x1b]8;;https://circleci.com/gh/org/repo/123\x1b\\CircleCI\x1b]8;;\x1b\\",
			want:    []Link{{Label: "https://circleci.com/gh/org/repo/123", URL: "https://circleci.com/gh/org/repo/123"}},
		},
		{
			name:    "OSC 8 hyperlink URL not spliced with display text",
			content: "\x1b]8;;https://circleci.com/gh/org/repo/456\x1b\\CircleCI Build\x1b]8;;\x1b\\ passed",
			want:    []Link{{Label: "https://circleci.com/gh/org/repo/456", URL: "https://circleci.com/gh/org/repo/456"}},
		},
		{
			name:    "multiple OSC 8 hyperlinks",
			content: "\x1b]8;;https://github.com/org/repo/pull/1\x1b\\PR #1\x1b]8;;\x1b\\ and \x1b]8;;https://circleci.com/gh/org/repo/99\x1b\\CI\x1b]8;;\x1b\\",
			want: []Link{
				{Label: "https://github.com/org/repo/pull/1", URL: "https://github.com/org/repo/pull/1"},
				{Label: "https://circleci.com/gh/org/repo/99", URL: "https://circleci.com/gh/org/repo/99"},
			},
		},
		{
			name:    "OSC 8 hyperlink with params field",
			content: "\x1b]8;id=link1;https://example.com/page\x1b\\click here\x1b]8;;\x1b\\",
			want:    []Link{{Label: "https://example.com/page", URL: "https://example.com/page"}},
		},
		{
			name:    "cursor movement prevents text merging",
			content: "https://github.com/org/repo/pull/123\x1b[5C\x1b[1Bpublished",
			want:    []Link{{Label: "https://github.com/org/repo/pull/123", URL: "https://github.com/org/repo/pull/123"}},
		},
		{
			name:    "quoted URL stops at double quote",
			content: `"https://github.com/org/repo/pull/123",URL,"https://github.com/org/repo/pull/123")`,
			want:    []Link{{Label: "https://github.com/org/repo/pull/123", URL: "https://github.com/org/repo/pull/123"}},
		},
		{
			name:    "backtick-wrapped URL cleaned",
			content: "see `https://example.com/path` for details",
			want:    []Link{{Label: "https://example.com/path", URL: "https://example.com/path"}},
		},
		{
			name:    "JSON-embedded URL stops at braces",
			content: `{"url":"https://example.com/page","title":"test"}`,
			want:    []Link{{Label: "https://example.com/page", URL: "https://example.com/page"}},
		},
		{
			name:    "trailing backtick stripped",
			content: "https://example.com/path`",
			want:    []Link{{Label: "https://example.com/path", URL: "https://example.com/path"}},
		},
		{
			name:    "trailing curly brace stripped",
			content: "https://example.com/path}",
			want:    []Link{{Label: "https://example.com/path", URL: "https://example.com/path"}},
		},
		{
			name:    "trailing asterisks stripped",
			content: "**https://example.com/path**",
			want:    []Link{{Label: "https://example.com/path", URL: "https://example.com/path"}},
		},
		{
			name:    "cursor right does not merge text into URL",
			content: "https://example.com/articles/16\x1b[39m\x1b[2C\x1b[1Bextra",
			want:    []Link{{Label: "https://example.com/articles/16", URL: "https://example.com/articles/16"}},
		},
		{
			name:    "bare URL with unicode ellipsis excluded",
			content: "see https://example.com/very/long/path/that/gets\u2026 for info",
			want:    nil,
		},
		{
			name:    "bare URL with three-dot ellipsis excluded",
			content: "see https://example.com/very/long/path/that/gets... for info",
			want:    nil,
		},
		{
			name:    "markdown link with ellipsis in URL excluded",
			content: "[Truncated](https://example.com/path\u2026)",
			want:    nil,
		},
		{
			name:    "ellipsis URL excluded but valid URL kept",
			content: "https://example.com/truncated\u2026 and https://example.com/valid",
			want:    []Link{{Label: "https://example.com/valid", URL: "https://example.com/valid"}},
		},
		{
			name:    "github compare range with triple dots not excluded",
			content: "https://github.com/org/repo/compare/v1.0...v1.1",
			want:    []Link{{Label: "https://github.com/org/repo/compare/v1.0...v1.1", URL: "https://github.com/org/repo/compare/v1.0...v1.1"}},
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

	t.Run("ctrl+q cancels", func(t *testing.T) {
		m := NewLinkPickerModal(links)
		handler := m.InputHandler()
		handler(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), func(p tview.Primitive) {})
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

