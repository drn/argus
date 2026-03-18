package kb

import (
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
