package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
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
	testutil.Equal(t, cmd.Args[2], "opencode --auto --prompt 'fix the bug'")
}

func TestBuildCmd_DefaultOpencodeSubmitsPrompt(t *testing.T) {
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, config.DefaultConfig(), false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "opencode --auto --prompt 'fix the bug'")
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
	testutil.Equal(t, cmd.Args[2], "opencode --auto --prompt 'fix the bug'")
}

func TestBuildCmd_OpencodeResume(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", SessionID: "ses_abc123", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Resume: --session <id>, prompt dropped (conversation is reloaded).
	testutil.Equal(t, cmd.Args[2], "opencode --auto --session 'ses_abc123'")
}

func TestBuildCmd_OpencodeResumeNoSessionID(t *testing.T) {
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Resume with no known ID starts fresh — auto approval, no --session.
	testutil.Equal(t, cmd.Args[2], "opencode --auto")
}

func TestBuildCmd_OpencodeModelInjection(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "fix the bug", Model: "anthropic/claude-sonnet-4-5", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// OpenCode v2's full TUI rejects a top-level --model; the model travels
	// through the child-only inline configuration instead.
	testutil.Equal(t, cmd.Args[2], "opencode --auto --prompt 'fix the bug'")
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any("anthropic/claude-sonnet-4-5"))
}

func TestBuildCmd_OpencodeModelInjection_Resume(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Model: "anthropic/claude-opus-4-1", SessionID: "ses_abc123", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, true)
	testutil.NoError(t, err)
	// Resume keeps the model in the same child-scoped config; the prompt is
	// dropped and the command carries only the session flag.
	testutil.Equal(t, cmd.Args[2], "opencode --auto --session 'ses_abc123'")
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any("anthropic/claude-opus-4-1"))
}

func TestBuildCmd_OpencodeBackendDefaultModelUsesInlineConfig(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	cfg := testConfig()
	cfg.Backends["opencode"] = config.Backend{Command: "opencode", PromptFlag: "--prompt", Model: "provider/default"}
	task := &model.Task{Backend: "opencode", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any("provider/default"))
}

func TestBuildCmd_OpencodeExplicitCommandModelWins(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	cfg := testConfig()
	cfg.Backends["opencode"] = config.Backend{Command: "opencode --model 'provider/command'", PromptFlag: "--prompt"}
	task := &model.Task{Backend: "opencode", Prompt: "go", Model: "provider/task", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "--model 'provider/command'")
	// The inline config may still carry the skills path; what must be absent is
	// a second, conflicting model.
	if inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT"); ok && inline != "" {
		var got map[string]any
		testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
		if model, present := got["model"]; present {
			t.Fatalf("an explicit command-level model must not receive a second inline override: %v", model)
		}
	}
}

func TestBuildCmd_OpencodeNonProviderModelLogsWarning(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	logs := captureUXLog(t)
	cfg := testConfig()
	task := &model.Task{ID: "bad-model", Backend: "opencode", Prompt: "go", Model: "sonnet", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// The value is still delivered — the operator chose it — but the launch
	// never fails quietly on a model OpenCode will discard.
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any("sonnet"))
	testutil.Contains(t, logs(), "not a provider/model id")
}

func TestBuildCmd_OpencodeProviderModelLogsNoWarning(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	logs := captureUXLog(t)
	cfg := testConfig()
	task := &model.Task{ID: "good-model", Backend: "opencode", Prompt: "go", Model: "provider/model", Worktree: t.TempDir()}

	_, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	if strings.Contains(logs(), "not a provider/model id") {
		t.Fatal("a well-formed provider/model id must not warn")
	}
}

func TestBuildCmd_OpencodeMalformedInlineConfigFailsOpen(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "not json")
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "go", Model: "provider/task", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "opencode --auto --prompt 'go'")
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, inline, "not json")
}

func TestBuildCmd_OpencodeSkillsFailureDoesNotDropModel(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	previous := ensureSessionSkillsFn
	ensureSessionSkillsFn = func() (string, error) { return "", errors.New("skills unavailable") }
	t.Cleanup(func() { ensureSessionSkillsFn = previous })

	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "go", Model: "provider/task", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any("provider/task"))
	if _, ok := got["skills"]; ok {
		t.Fatal("failed skill materialization must not synthesize a skills key")
	}
}

func TestBuildCmd_OpencodeEnvVarsConfigTargetWins(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	t.Setenv("ARGUS_TEST_OPENCODE_CONFIG", "user-config")
	cfg := testConfig()
	cfg.Backends["opencode"] = config.Backend{
		Command:    "opencode",
		PromptFlag: "--prompt",
		Model:      "provider/task",
		EnvVars:    map[string]string{"OPENCODE_CONFIG_CONTENT": "ARGUS_TEST_OPENCODE_CONFIG"},
	}
	task := &model.Task{Backend: "opencode", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, inline, "user-config")
}

func TestBuildCmd_OpencodeModelIsNotShellInterpolated(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	const hostile = "provider/'; rm -rf /"
	cfg := testConfig()
	task := &model.Task{Backend: "opencode", Prompt: "go", Model: hostile, Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	// The model must never touch the shell: an argv regression is the exact
	// failure this change fixes, so assert the flag is absent, not just that
	// the hostile substring is missing (shellQuote would have hidden it).
	if strings.Contains(cmd.Args[2], "--model") {
		t.Fatalf("OpenCode must not receive a top-level --model flag: %q", cmd.Args[2])
	}
	if strings.Contains(cmd.Args[2], hostile) {
		t.Fatalf("model value must not reach the shell command: %q", cmd.Args[2])
	}
	inline, ok := envValue(cmd.Env, "OPENCODE_CONFIG_CONTENT")
	testutil.Equal(t, ok, true)
	var got map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &got))
	testutil.Equal(t, got["model"], any(hostile))
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

func TestCaptureOpencodeSessionID_SQLiteV2(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()
	dataDir := filepath.Join(dataRoot, "opencode")
	testutil.NoError(t, os.MkdirAll(dataDir, 0o755))
	conn, err := sql.Open("sqlite", filepath.Join(dataDir, "opencode.db"))
	testutil.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = conn.Exec(`CREATE TABLE session_v2 (id TEXT, directory TEXT, time_updated INTEGER)`)
	testutil.NoError(t, err)
	_, err = conn.Exec(`INSERT INTO session_v2 VALUES (?, ?, ?)`, "ses_v2old", wt, 100)
	testutil.NoError(t, err)
	_, err = conn.Exec(`INSERT INTO session_v2 VALUES (?, ?, ?)`, "ses_v2new", wt, 200)
	testutil.NoError(t, err)

	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_v2new")
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

// A session whose stored directory is NON-canonical (a symlink path) must still
// match — the indexed exact-match misses (it binds the canonicalized want), and
// the full-scan fallback's canonPath(dir) resolves it. This exercises the scan
// fallback, NOT the fast path.
func TestCaptureOpencodeSessionID_SQLiteScanCanonFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// DB stores the non-canonical symlink path; lookup uses real. want =
	// canonPath(real); fast path `WHERE directory = want` misses (DB holds the
	// raw link string); scan canonPath(link) == want catches it.
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"ses_scanfallback111111111111", link, 100},
	})
	got, err := CaptureOpencodeSessionID(real)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_scanfallback111111111111")
}

// SQLite present but holding only a DIFFERENT directory → capture falls through
// to the legacy JSON store, which has a matching session.
func TestCaptureOpencodeSessionID_SQLiteMissJSONHit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dataRoot := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataRoot)
	wt := t.TempDir()

	// SQLite has only an unrelated directory.
	seedOpencodeSQLite(t, filepath.Join(dataRoot, "opencode"), [][3]any{
		{"ses_sqlonly00000000000000000", t.TempDir(), 999},
	})
	// JSON store has the worktree's session.
	projDir := filepath.Join(dataRoot, "opencode", "storage", "session", "proj")
	testutil.NoError(t, os.MkdirAll(projDir, 0o755))
	writeOpencodeJSON(t, filepath.Join(projDir, "ses_jsonhit1111111111111111.json"), "ses_jsonhit1111111111111111", wt, 100)

	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_jsonhit1111111111111111")
}

// With XDG_DATA_HOME unset, the data dir falls back to ~/.local/share/opencode.
func TestCaptureOpencodeSessionID_HomeFallback(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "") // force the HOME fallback
	home := t.TempDir()
	t.Setenv("HOME", home)
	wt := t.TempDir()
	seedOpencodeSQLite(t, filepath.Join(home, ".local", "share", "opencode"), [][3]any{
		{"ses_homefallback11111111111", wt, 100},
	})
	got, err := CaptureOpencodeSessionID(wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, "ses_homefallback11111111111")
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
