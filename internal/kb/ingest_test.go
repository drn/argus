package kb

import (
	"strings"
	"testing"
)

func TestParseDocument_NoFrontmatter(t *testing.T) {
	content := "# My Title\n\nSome body text here."
	doc := ParseDocument("notes/test.md", content)

	if doc.Title != "My Title" {
		t.Errorf("title: got %q, want %q", doc.Title, "My Title")
	}
	if doc.Path != "notes/test.md" {
		t.Errorf("path: got %q, want %q", doc.Path, "notes/test.md")
	}
	if len(doc.Tags) != 0 {
		t.Errorf("tags: got %v, want empty", doc.Tags)
	}
	if doc.Tier != "hot" {
		t.Errorf("tier: got %q, want hot", doc.Tier)
	}
}

func TestParseDocument_FrontmatterTitle(t *testing.T) {
	content := "---\ntitle: Frontmatter Title\ntags: [go, testing]\n---\n\nBody content."
	doc := ParseDocument("path.md", content)

	if doc.Title != "Frontmatter Title" {
		t.Errorf("title: got %q, want %q", doc.Title, "Frontmatter Title")
	}
	if len(doc.Tags) != 2 {
		t.Errorf("tags count: got %d, want 2", len(doc.Tags))
	}
	if doc.Tags[0] != "go" || doc.Tags[1] != "testing" {
		t.Errorf("tags: got %v", doc.Tags)
	}
	if doc.Body != "Body content." {
		t.Errorf("body: got %q", doc.Body)
	}
}

func TestParseDocument_FallbackToFilename(t *testing.T) {
	content := "No heading here."
	doc := ParseDocument("notes/my-note.md", content)

	if doc.Title != "my-note" {
		t.Errorf("title: got %q, want %q", doc.Title, "my-note")
	}
}

func TestParseDocument_WordCount(t *testing.T) {
	content := "one two three four five"
	doc := ParseDocument("test.md", content)

	if doc.WordCount != 5 {
		t.Errorf("word count: got %d, want 5", doc.WordCount)
	}
}

func TestParseYAMLFrontmatter_NoFrontmatter(t *testing.T) {
	title, tags, body := parseYAMLFrontmatter("just plain text")
	if title != "" {
		t.Errorf("title: got %q, want empty", title)
	}
	if len(tags) != 0 {
		t.Errorf("tags: got %v, want empty", tags)
	}
	if body != "just plain text" {
		t.Errorf("body: got %q", body)
	}
}

func TestParseYAMLFrontmatter_FullFrontmatter(t *testing.T) {
	content := "---\ntitle: Test Doc\ntags: [alpha, beta]\n---\nBody here."
	title, tags, body := parseYAMLFrontmatter(content)

	if title != "Test Doc" {
		t.Errorf("title: got %q", title)
	}
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Errorf("tags: got %v", tags)
	}
	if body != "Body here." {
		t.Errorf("body: got %q", body)
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello world", 2},
		{"  spaces  between  words  ", 3},
		{"hello,world", 2},
	}
	for _, tc := range tests {
		got := countWords(tc.input)
		if got != tc.want {
			t.Errorf("countWords(%q): got %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestRenderMarkdown_WithTags(t *testing.T) {
	doc := &Document{
		Title: "Test Doc",
		Tags:  []string{"go", "testing"},
		Body:  "Some body content.",
	}
	got := RenderMarkdown(doc)

	if !strings.Contains(got, "title: \"Test Doc\"") {
		t.Errorf("missing title in frontmatter:\n%s", got)
	}
	if !strings.Contains(got, "tags: [go, testing]") {
		t.Errorf("missing tags in frontmatter:\n%s", got)
	}
	if !strings.Contains(got, "Some body content.") {
		t.Errorf("missing body:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("should end with newline")
	}
}

func TestRenderMarkdown_NoTags(t *testing.T) {
	doc := &Document{
		Title: "No Tags",
		Body:  "Body here.\n",
	}
	got := RenderMarkdown(doc)

	if strings.Contains(got, "tags:") {
		t.Errorf("should not contain tags line:\n%s", got)
	}
	if !strings.Contains(got, "title: \"No Tags\"") {
		t.Errorf("missing title:\n%s", got)
	}
}

func TestRenderMarkdown_QuotesInTitle(t *testing.T) {
	doc := &Document{
		Title: `He said "hello"`,
		Body:  "Body.",
	}
	got := RenderMarkdown(doc)

	if !strings.Contains(got, `title: "He said \"hello\""`) {
		t.Errorf("quotes not escaped:\n%s", got)
	}
}

func TestRenderMarkdown_Roundtrip(t *testing.T) {
	doc := &Document{
		Title: "Roundtrip Test",
		Tags:  []string{"alpha", "beta"},
		Body:  "The actual content.\n\nWith paragraphs.\n",
	}
	md := RenderMarkdown(doc)
	parsed := ParseDocument("test.md", md)

	if parsed.Title != doc.Title {
		t.Errorf("title: got %q, want %q", parsed.Title, doc.Title)
	}
	if len(parsed.Tags) != len(doc.Tags) {
		t.Errorf("tags count: got %d, want %d", len(parsed.Tags), len(doc.Tags))
	}
	if strings.TrimRight(parsed.Body, "\n") != strings.TrimRight(doc.Body, "\n") {
		t.Errorf("body: got %q, want %q", parsed.Body, doc.Body)
	}
}
