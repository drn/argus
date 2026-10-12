package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// antigravityConversationDB returns agy's conversation index,
// ~/.gemini/antigravity-cli/conversation_summaries.db.
func antigravityConversationDB() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("antigravity conversation db: home dir: %w", err)
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db"), nil
}

// CaptureAntigravitySessionID returns the conversation ID of the most
// recently modified top-level agy conversation whose workspace is the given
// worktree.
// agy indexes conversations in SQLite (table conversation_summaries) with a
// workspace_uris column listing file:// URIs; the database is opened
// read-only (mode=ro, not immutable, so the WAL's newest rows are seen).
func CaptureAntigravitySessionID(worktreePath string) (string, error) {
	if worktreePath == "" {
		return "", fmt.Errorf("CaptureAntigravitySessionID: worktree path is empty")
	}
	dbPath, err := antigravityConversationDB()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("CaptureAntigravitySessionID: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", fmt.Errorf("CaptureAntigravitySessionID: open: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Sub-agent conversations carry their parent's ID and share the
	// workspace; only a top-level conversation is resumable as the task.
	rows, err := db.Query(`SELECT conversation_id, COALESCE(workspace_uris, '') FROM conversation_summaries
		WHERE COALESCE(conversation_id, '') <> '' AND COALESCE(parent_conversation_id, '') = ''
		ORDER BY last_modified_time DESC`)
	if err != nil {
		return "", fmt.Errorf("CaptureAntigravitySessionID: query: %w", err)
	}
	defer rows.Close()

	want := canonPath(worktreePath)
	for rows.Next() {
		var id, uris string
		if rows.Scan(&id, &uris) != nil {
			continue
		}
		for _, p := range antigravityWorkspacePaths(uris) {
			if p == want || canonPath(p) == want {
				return id, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("CaptureAntigravitySessionID: scan: %w", err)
	}
	return "", fmt.Errorf("CaptureAntigravitySessionID: no conversation for worktree %s", worktreePath)
}

// antigravityWorkspacePaths decodes a workspace_uris value into filesystem
// paths. agy stores a JSON array of file:// URIs; a bare comma/space
// separated list or plain paths are tolerated so a storage tweak degrades to
// a miss rather than a crash.
func antigravityWorkspacePaths(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var items []string
	if json.Unmarshal([]byte(raw), &items) != nil {
		items = strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' })
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if u, err := url.Parse(item); err == nil && u.Scheme == "file" {
			item = u.Path
		}
		if item != "" {
			out = append(out, filepath.Clean(item))
		}
	}
	return out
}
