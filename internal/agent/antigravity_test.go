package agent

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// antigravitySchema is conversation_summaries as created by agy 1.3.3.
const antigravitySchema = "CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\",`preview` text NOT NULL DEFAULT \"\",`step_count` integer NOT NULL DEFAULT 0,`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT \"\",`source` text NOT NULL DEFAULT \"\",`project_id` text NOT NULL DEFAULT \"\",`agent_name` text NOT NULL DEFAULT \"\",`parent_conversation_id` text NOT NULL DEFAULT \"\",`nesting_depth` integer NOT NULL DEFAULT 0,`battle_id` text NOT NULL DEFAULT \"\",`winning_conversation_id` text NOT NULL DEFAULT \"\",`not_fully_idle` numeric NOT NULL DEFAULT false,`killed` numeric NOT NULL DEFAULT false,`last_user_input_time` datetime NOT NULL,`last_user_input_step_index` integer NOT NULL DEFAULT -1,`app_data_dir` text NOT NULL DEFAULT \"\",`raw_summary` blob,`group_id` text NOT NULL DEFAULT \"\",PRIMARY KEY (`conversation_id`))"

type agyRow struct{ id, modified, uris, parent string }

func seedAntigravityDB(t *testing.T, rows ...agyRow) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	db, err := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
	testutil.NoError(t, err)
	defer func() { _ = db.Close() }()
	_, err = db.Exec(antigravitySchema)
	testutil.NoError(t, err)
	for _, r := range rows {
		_, err = db.Exec(`INSERT INTO conversation_summaries (conversation_id, last_modified_time, workspace_uris, last_user_input_time, parent_conversation_id) VALUES (?, ?, ?, ?, ?)`, r.id, r.modified, r.uris, r.modified, r.parent)
		testutil.NoError(t, err)
	}
}

func TestCaptureAntigravitySessionID(t *testing.T) {
	wt := t.TempDir()
	other := t.TempDir()
	t.Run("newest matching conversation wins", func(t *testing.T) {
		seedAntigravityDB(t,
			agyRow{"old", "2026-10-01 10:00:00", `["file://` + wt + `"]`, ""},
			agyRow{"new", "2026-10-02 10:00:00", `["file://` + wt + `"]`, ""},
			agyRow{"newest-other", "2026-10-03 10:00:00", `["file://` + other + `"]`, ""},
		)
		id, err := CaptureAntigravitySessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "new")
	})
	t.Run("newer sub-agent conversation ignored", func(t *testing.T) {
		seedAntigravityDB(t,
			agyRow{id: "main", modified: "2026-10-02 10:00:00", uris: `["file://` + wt + `"]`},
			agyRow{id: "sub", modified: "2026-10-03 10:00:00", uris: `["file://` + wt + `"]`, parent: "main"},
		)
		id, err := CaptureAntigravitySessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "main")
	})
	t.Run("NULL parent and NULL uris tolerated", func(t *testing.T) {
		// agy 1.3.3 declares these NOT NULL; a looser future schema must not break capture.
		seedAntigravityDB(t)
		db, err := sql.Open("sqlite", filepath.Join(os.Getenv("HOME"), ".gemini", "antigravity-cli", "conversation_summaries.db"))
		testutil.NoError(t, err)
		_, err = db.Exec(`DROP TABLE conversation_summaries; CREATE TABLE conversation_summaries (conversation_id text, last_modified_time datetime, workspace_uris text, parent_conversation_id text)`)
		testutil.NoError(t, err)
		_, err = db.Exec(`INSERT INTO conversation_summaries VALUES ('top', '2026-10-02 10:00:00', ?, NULL), ('nouris', '2026-10-03 10:00:00', NULL, NULL)`, `["file://`+wt+`"]`)
		testutil.NoError(t, err)
		_ = db.Close()
		id, err := CaptureAntigravitySessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "top")
	})
	t.Run("multi-workspace conversation matches", func(t *testing.T) {
		seedAntigravityDB(t, agyRow{"multi", "2026-10-02 10:00:00", `["file://` + other + `","file://` + wt + `"]`, ""})
		id, err := CaptureAntigravitySessionID(wt)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "multi")
	})
	t.Run("symlinked worktree matches", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link")
		testutil.NoError(t, os.Symlink(wt, link))
		seedAntigravityDB(t, agyRow{"via-real", "2026-10-02 10:00:00", `["file://` + canonPath(wt) + `"]`, ""})
		id, err := CaptureAntigravitySessionID(link)
		testutil.NoError(t, err)
		testutil.Equal(t, id, "via-real")
	})
	t.Run("no match", func(t *testing.T) {
		seedAntigravityDB(t, agyRow{"x", "2026-10-02 10:00:00", `["file://` + other + `"]`, ""})
		_, err := CaptureAntigravitySessionID(wt)
		testutil.Contains(t, err.Error(), "no conversation")
	})
	t.Run("missing db", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, err := CaptureAntigravitySessionID(wt)
		testutil.Contains(t, err.Error(), "CaptureAntigravitySessionID")
	})
	t.Run("empty worktree", func(t *testing.T) {
		_, err := CaptureAntigravitySessionID("")
		testutil.Contains(t, err.Error(), "empty")
	})
	t.Run("wrong schema", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		dir := filepath.Join(home, ".gemini", "antigravity-cli")
		testutil.NoError(t, os.MkdirAll(dir, 0o755))
		db, _ := sql.Open("sqlite", filepath.Join(dir, "conversation_summaries.db"))
		_, err := db.Exec(`CREATE TABLE other (x text)`)
		testutil.NoError(t, err)
		_ = db.Close()
		_, err = CaptureAntigravitySessionID(wt)
		testutil.Contains(t, err.Error(), "query")
	})
}

func TestAntigravityWorkspacePaths(t *testing.T) {
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"json file uris", `["file:///a/b","file:///c%20d/"]`, []string{"/a/b", "/c d"}},
		{"plain paths list", "/a/b, /c", []string{"/a/b", "/c"}},
		{"empty", "  ", nil},
		{"empty array", "[]", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, antigravityWorkspacePaths(tc.raw), tc.want)
		})
	}
}
