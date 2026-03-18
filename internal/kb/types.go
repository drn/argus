package kb

import "time"

// Document represents a markdown document stored in the knowledge base.
type Document struct {
	Path       string    // vault-relative file path, e.g. "projects/thanx.md"
	Title      string
	Body       string
	Tags       []string
	Tier       string    // hot | warm | cold
	ModifiedAt time.Time
	IngestedAt time.Time
	WordCount  int
}

// SearchResult wraps a Document with FTS5 match metadata.
type SearchResult struct {
	Document
	Snippet string  // FTS5 highlighted snippet
	Rank    float64 // BM25 score (lower is better in FTS5)
}

// KBStore is the interface the kb package uses to persist documents.
// Implemented by *db.DB in internal/db/kb.go.
type KBStore interface {
	KBUpsert(doc *Document) error
	KBDelete(path string) error
}
