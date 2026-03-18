package kb

import (
	"strings"
	"time"
	"unicode/utf8"
)

// ParseDocument parses a markdown file into a Document.
// It extracts YAML frontmatter for title/tags, falls back to first H1 for title.
// The body stored excludes frontmatter.
func ParseDocument(path, content string) Document {
	title, tags, body := parseYAMLFrontmatter(content)

	// Fall back to first H1 heading if no title from frontmatter.
	if title == "" {
		for _, line := range strings.SplitN(body, "\n", 50) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# ") {
				title = strings.TrimPrefix(line, "# ")
				break
			}
		}
	}

	// Fall back to filename stem as title.
	if title == "" {
		base := path
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		title = strings.TrimSuffix(base, ".md")
	}

	wordCount := countWords(body)

	return Document{
		Path:      path,
		Title:     title,
		Body:      body,
		Tags:      tags,
		Tier:      "hot",
		WordCount: wordCount,
	}
}

// parseYAMLFrontmatter extracts title and tags from YAML frontmatter.
// Returns (title, tags, bodyWithoutFrontmatter).
// Frontmatter is delimited by --- lines at the start of the file.
func parseYAMLFrontmatter(content string) (string, []string, string) {
	content = strings.TrimLeft(content, "\n\r")
	if !strings.HasPrefix(content, "---") {
		return "", nil, content
	}
	// Find closing ---
	rest := content[3:]
	// Skip optional newline after opening ---
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}

	end := strings.Index(rest, "\n---")
	if end == -1 {
		return "", nil, content // malformed frontmatter — treat as no frontmatter
	}

	frontmatter := rest[:end]
	body := rest[end+4:] // skip "\n---"
	// Skip the newline immediately after the closing ---.
	if strings.HasPrefix(body, "\r\n") {
		body = body[2:]
	} else if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}
	body = strings.TrimLeft(body, "\n")

	var title string
	var tags []string

	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "title:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
			val = strings.Trim(val, `"'`)
			title = val
		} else if strings.HasPrefix(line, "tags:") {
			// Inline tags: tags: [a, b, c] or tags: a, b, c
			val := strings.TrimSpace(strings.TrimPrefix(line, "tags:"))
			val = strings.Trim(val, "[]")
			for _, t := range strings.Split(val, ",") {
				t = strings.TrimSpace(t)
				t = strings.Trim(t, `"'`)
				if t != "" {
					tags = append(tags, t)
				}
			}
		} else if strings.HasPrefix(line, "- ") && len(tags) == 0 {
			// List-style tags under a tags: key (simplified — only first pass)
			tag := strings.TrimPrefix(line, "- ")
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}

	return title, tags, body
}

// countWords returns an approximate word count for the given text.
func countWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if isWordChar(r) {
			if !inWord {
				count++
				inWord = true
			}
		} else {
			inWord = false
		}
	}
	return count
}

func isWordChar(r rune) bool {
	return utf8.ValidRune(r) && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r > 127)
}

// IngestFile reads content and upserts it into the store under the given path.
func IngestFile(store KBStore, path, content string) error {
	doc := ParseDocument(path, content)
	doc.IngestedAt = time.Now()
	// ModifiedAt set by caller (from file stat) — default to now if zero.
	if doc.ModifiedAt.IsZero() {
		doc.ModifiedAt = time.Now()
	}
	return store.KBUpsert(&doc)
}
