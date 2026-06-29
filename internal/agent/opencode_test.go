package agent

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestIsOpencodeBackend(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"bare opencode", "opencode", true},
		{"opencode with flags", "opencode --prompt", true},
		{"absolute path", "/usr/local/bin/opencode", true},
		{"empty", "", false},
		{"prefix only", "opencode-helper", false},
		{"claude", "claude", false},
		{"codex", "codex --dangerously-bypass-approvals-and-sandbox", false},
		{"pi", "pi", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, IsOpencodeBackend(tc.cmd), tc.want)
		})
	}
}

func TestBuildCmd_OpencodeNewSession(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// Prompt rides the configured --prompt flag; no --session-id (captured post-exit).
	testutil.Equal(t, cmd.Args[2], "opencode --prompt 'fix the bug'")
}

func TestBuildCmd_OpencodeNewSession_IgnoresSessionID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", SessionID: "ses_abc123", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "opencode")
	if got := cmd.Args[2]; got == "opencode --session-id 'ses_abc123' --prompt 'fix the bug'" {
		t.Fatalf("opencode new-session must not emit --session-id, got %q", got)
	}
	testutil.Equal(t, cmd.Args[2], "opencode --prompt 'fix the bug'")
}

func TestBuildCmd_OpencodeResume(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", SessionID: "ses_abc123", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Resume: --session <id>, prompt dropped (conversation is reloaded).
	testutil.Equal(t, cmd.Args[2], "opencode --session 'ses_abc123'")
}

func TestBuildCmd_OpencodeResumeNoSessionID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Resume with no known ID starts fresh — plain base command, no --session.
	testutil.Equal(t, cmd.Args[2], "opencode")
}

func TestBuildCmd_OpencodeModelInjection(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Model: "anthropic/claude-sonnet-4-5", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "opencode --model 'anthropic/claude-sonnet-4-5' --prompt 'fix the bug'")
}

func TestBuildCmd_OpencodeModelInjection_Resume(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Model: "anthropic/claude-opus-4-1", SessionID: "ses_abc123", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Model flag precedes the resume --session; prompt is dropped.
	testutil.Equal(t, cmd.Args[2], "opencode --model 'anthropic/claude-opus-4-1' --session 'ses_abc123'")
}

// seedOpencodeSQLite creates an opencode.db with a `session` table under the
// given data dir and inserts the provided rows (id, directory, timeUpdated).
func seedOpencodeSQLite(t *testing.T, dataDir string, rows [][3]any) {
	t.Helper()
	testutil.NoError(t, os.MkdirAll(dataDir, 0o755))
	conn, err := sql.Open("sqlite", filepath.Join(dataDir, "opencode.db"))
	testutil.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Exec(`CREATE TABLE session (id TEXT, directory TEXT, time_updated INTEGER)`)
	testutil.NoError(t, err)
	for _, r := range rows {
		_, err = conn.Exec(`INSERT INTO session (id, directory, time_updated) VALUES (?, ?, ?)`, r[0], r[1], r[2])
		testutil.NoError(t, err)
	}
}

func TestCaptureOpencodeSessionID_SQLite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()
	other := t.TempDir()

	// Newest row for `wt` wins; rows for `other` are filtered out even when newer.
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"ses_old00000000000000000000", wt, 100},
		{"ses_new11111111111111111111", wt, 200},
		{"ses_other2222222222222222222", other, 999},
	})

	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_new11111111111111111111")
}

func TestCaptureOpencodeSessionID_JSONFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()

	// No opencode.db → JSON walk. Two project buckets; newest matching directory wins.
	sessRoot := filepath.Join(dataRoot, "opencode", "storage", "session")
	projA := filepath.Join(sessRoot, "rootcommithashA")
	testutil.NoError(t, os.MkdirAll(projA, 0o755))
	writeOpencodeJSON(t, filepath.Join(projA, "ses_aaa00000000000000000000.json"), "ses_aaa00000000000000000000", wt, 100)
	writeOpencodeJSON(t, filepath.Join(projA, "ses_bbb11111111111111111111.json"), "ses_bbb11111111111111111111", wt, 300)
	// A session for a different directory must be ignored.
	writeOpencodeJSON(t, filepath.Join(projA, "ses_ccc22222222222222222222.json"), "ses_ccc22222222222222222222", t.TempDir(), 999)

	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_bbb11111111111111111111")
}

func TestCaptureOpencodeSessionID_FailsOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir()) // empty data dir
	_, err := CaptureOpencodeSessionID(t.TempDir())
	if err == nil {
		t.Fatal("expected error (fail-open) when no session matches")
	}
}

func TestCaptureOpencodeSessionID_EmptyWorktree(t *testing.T) {
	_, err := CaptureOpencodeSessionID("")
	if err == nil {
		t.Fatal("expected error for empty worktree path")
	}
}

func TestCaptureOpencodeSessionID_MalformedIDRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"not-a-session-id", wt, 100},
	})
	// Malformed id is not returned; with no other store, capture fails open.
	_, err := CaptureOpencodeSessionID(wt)
	if err == nil {
		t.Fatal("expected error: malformed session id must not be returned")
	}
}

// A malformed NEWEST row must not hide an older valid session for the same
// directory — the scan skips it and returns the valid one.
func TestCaptureOpencodeSessionID_SQLiteSkipsMalformedNewest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"garbage-newest", wt, 300},
		{"ses_valid1111111111111111111", wt, 200},
	})
	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_valid1111111111111111111")
}

// A worktree under a symlinked path must still match a session whose stored
// directory is the resolved-absolute form (the full-scan canonPath fallback).
func TestCaptureOpencodeSessionID_SQLiteSymlinkMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// opencode stores the resolved (real) path; Argus looks up via the symlink.
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"ses_symlink11111111111111111", real, 100},
	})
	got, err := CaptureOpencodeSessionID(link)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_symlink11111111111111111")
}

func TestCaptureSessionID_DispatchesOpencode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()
	seeded := "ses_dispatch0000000000000000"
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{{seeded, wt, 1}})

	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Worktree: wt}
	got, err := CaptureSessionID(task, cfg)
	testutil.NoError(t, err)
	testutil.Equal(t, got, seeded)
}

// writeOpencodeJSON writes a legacy-format opencode session index file.
func writeOpencodeJSON(t *testing.T, path, id, dir string, updated int) {
	t.Helper()
	content := `{"id":"` + id + `","directory":"` + dir + `","time":{"updated":` + strconv.Itoa(updated) + `}}`
	testutil.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
