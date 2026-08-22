package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// writeClaudeSessionFixture creates a fake Claude Code session JSONL file
// under ~/.claude/projects/<encoded-worktree>/<id>.jsonl — mirroring
// internal/claudesession's own test fixture helper (writeSession in
// claudesession_test.go), since that package owns the on-disk layout these
// handlers read through. EncodeProjectDir is exported specifically so
// callers outside the package can build fixtures like this one.
func writeClaudeSessionFixture(t *testing.T, home, worktree, id string, lines []string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	var content string
	for _, l := range lines {
		content += l + "\n"
	}
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(content), 0o644))
}

// TestHandleListClaudeSessions covers GET /api/tasks/{id}/claude-sessions:
// 404 on missing task, 400 on a non-Claude backend, and the success listing
// (newest-activity-first, current_session_id echoed).
func TestHandleListClaudeSessions(t *testing.T) {
	t.Run("404 when task missing", func(t *testing.T) {
		srv, _ := testServer(t)
		mux := srv.routes()
		req := authedReq("GET", "/api/tasks/missing/claude-sessions", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})

	t.Run("400 when backend is not Claude", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		testutil.NoError(t, d.SetBackend("codex-backend", config.Backend{Command: "codex"}))
		task := &model.Task{Name: "codex-task", Backend: "codex-backend", Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))

		req := authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("lists sessions newest first with current_session_id", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		home := t.TempDir()
		t.Setenv("HOME", home)

		// "sh -c 'sleep 30'", not a bare "sleep 30": BuildCmd's resume branch
		// appends "--resume <uuid>" to the END of the command string, and the
		// outer exec.Command("sh", "-c", cmdStr) re-parses the WHOLE string as
		// one shell command line — a bare "sleep 30 --resume 'uuid'" makes the
		// real sleep(1) choke on the unexpected flag and exit 1 immediately.
		// Wrapping in "sh -c '...'" makes the appended flags land as $0/$1
		// inside the inner sh -c, which ignores them (same trick documented in
		// cmd/argus-secrets-smoke's smoke-cat backend and used by
		// internal/tui/sessionpicker_wiring_test.go for this exact scenario).
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
		wt := filepath.Join(t.TempDir(), "proj", "task")
		task := &model.Task{
			Name:      "with-sessions",
			Backend:   "sh-sleep",
			Worktree:  wt,
			SessionID: "22222222-2222-4222-8222-222222222222",
		}
		testutil.NoError(t, d.Add(task))

		writeClaudeSessionFixture(t, home, wt, "11111111-1111-4111-8111-111111111111", []string{
			`{"type":"ai-title","aiTitle":"Older session","sessionId":"11111111-1111-4111-8111-111111111111"}`,
			`{"type":"user","timestamp":"2026-01-01T00:00:00.000Z","gitBranch":"argus/old","sessionId":"11111111-1111-4111-8111-111111111111"}`,
		})
		writeClaudeSessionFixture(t, home, wt, "22222222-2222-4222-8222-222222222222", []string{
			`{"type":"ai-title","aiTitle":"Newer session","sessionId":"22222222-2222-4222-8222-222222222222"}`,
			`{"type":"user","timestamp":"2026-06-01T00:00:00.000Z","gitBranch":"argus/new","sessionId":"22222222-2222-4222-8222-222222222222"}`,
			`{"type":"pr-link","prNumber":42,"prRepository":"drn/argus","timestamp":"2026-06-01T00:00:01.000Z","sessionId":"22222222-2222-4222-8222-222222222222"}`,
		})

		req := authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp struct {
			Sessions         []claudeSessionJSON `json:"sessions"`
			CurrentSessionID string              `json:"current_session_id"`
		}
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp.CurrentSessionID, task.SessionID)
		testutil.Equal(t, len(resp.Sessions), 2)
		testutil.Equal(t, resp.Sessions[0].ID, "22222222-2222-4222-8222-222222222222")
		testutil.Equal(t, resp.Sessions[0].Title, "Newer session")
		testutil.Equal(t, resp.Sessions[0].Branch, "argus/new")
		testutil.Equal(t, resp.Sessions[0].PRRef, "drn/argus#42")
		testutil.Equal(t, resp.Sessions[1].ID, "11111111-1111-4111-8111-111111111111")
		testutil.Equal(t, resp.Sessions[1].Title, "Older session")
	})
}

// TestHandleSwitchClaudeSession covers POST /api/tasks/{id}/claude-session:
// 404/400 input validation, the "unchanged" no-op, and the two "switched"
// paths (no live session vs. a live session that must be stopped and
// restarted via Runner.KickRerender).
func TestHandleSwitchClaudeSession(t *testing.T) {
	t.Run("404 when task missing", func(t *testing.T) {
		srv, _ := testServer(t)
		mux := srv.routes()
		req := authedReq("POST", "/api/tasks/missing/claude-session", `{"session_id":"x"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusNotFound)
	})

	t.Run("400 when backend is not Claude", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		testutil.NoError(t, d.SetBackend("pi-backend", config.Backend{Command: "pi"}))
		task := &model.Task{Name: "pi-task", Backend: "pi-backend", Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))

		req := authedReq("POST", "/api/tasks/"+task.ID+"/claude-session", `{"session_id":"x"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("400 when session_id is missing", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		// "sh -c 'sleep 30'", not a bare "sleep 30": BuildCmd's resume branch
		// appends "--resume <uuid>" to the END of the command string, and the
		// outer exec.Command("sh", "-c", cmdStr) re-parses the WHOLE string as
		// one shell command line — a bare "sleep 30 --resume 'uuid'" makes the
		// real sleep(1) choke on the unexpected flag and exit 1 immediately.
		// Wrapping in "sh -c '...'" makes the appended flags land as $0/$1
		// inside the inner sh -c, which ignores them (same trick documented in
		// cmd/argus-secrets-smoke's smoke-cat backend and used by
		// internal/tui/sessionpicker_wiring_test.go for this exact scenario).
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
		task := &model.Task{Name: "claude-task", Backend: "sh-sleep", Worktree: t.TempDir()}
		testutil.NoError(t, d.Add(task))

		req := authedReq("POST", "/api/tasks/"+task.ID+"/claude-session", `{}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("unchanged when session_id matches current", func(t *testing.T) {
		srv, d := testServer(t)
		mux := srv.routes()
		// "sh -c 'sleep 30'", not a bare "sleep 30": BuildCmd's resume branch
		// appends "--resume <uuid>" to the END of the command string, and the
		// outer exec.Command("sh", "-c", cmdStr) re-parses the WHOLE string as
		// one shell command line — a bare "sleep 30 --resume 'uuid'" makes the
		// real sleep(1) choke on the unexpected flag and exit 1 immediately.
		// Wrapping in "sh -c '...'" makes the appended flags land as $0/$1
		// inside the inner sh -c, which ignores them (same trick documented in
		// cmd/argus-secrets-smoke's smoke-cat backend and used by
		// internal/tui/sessionpicker_wiring_test.go for this exact scenario).
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
		task := &model.Task{
			Name:      "same-session",
			Backend:   "sh-sleep",
			Worktree:  t.TempDir(),
			SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		}
		testutil.NoError(t, d.Add(task))

		req := authedReq("POST", "/api/tasks/"+task.ID+"/claude-session", `{"session_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["status"], "unchanged")

		// No session was ever started, so nothing to stop; runner must have
		// no live session for this task.
		testutil.Nil(t, srv.runner.Get(task.ID))
	})

	t.Run("switches with no live session (starts fresh)", func(t *testing.T) {
		if testing.Short() {
			t.Skip("starts a real PTY-backed sleep; skipped in -short")
		}
		srv, d := testServer(t)
		mux := srv.routes()
		// "sh -c 'sleep 30'", not a bare "sleep 30": BuildCmd's resume branch
		// appends "--resume <uuid>" to the END of the command string, and the
		// outer exec.Command("sh", "-c", cmdStr) re-parses the WHOLE string as
		// one shell command line — a bare "sleep 30 --resume 'uuid'" makes the
		// real sleep(1) choke on the unexpected flag and exit 1 immediately.
		// Wrapping in "sh -c '...'" makes the appended flags land as $0/$1
		// inside the inner sh -c, which ignores them (same trick documented in
		// cmd/argus-secrets-smoke's smoke-cat backend and used by
		// internal/tui/sessionpicker_wiring_test.go for this exact scenario).
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
		task := &model.Task{
			Name:     "dead-session",
			Status:   model.StatusInReview,
			Backend:  "sh-sleep",
			Worktree: t.TempDir(),
		}
		testutil.NoError(t, d.Add(task))

		req := authedReq("POST", "/api/tasks/"+task.ID+"/claude-session", `{"session_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		t.Cleanup(func() {
			if sess := srv.runner.Get(task.ID); sess != nil {
				_ = srv.runner.Stop(task.ID)
				<-sess.Done()
			}
		})

		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["status"], "switched")
		pid, ok := resp["pid"].(float64)
		if !ok || pid <= 0 {
			t.Fatalf("expected a positive pid in response, got %v", resp["pid"])
		}

		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.SessionID, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
		testutil.Equal(t, got.Status, model.StatusInProgress)
	})

	t.Run("switches a live session via stop+restart", func(t *testing.T) {
		if testing.Short() {
			t.Skip("starts a real PTY-backed sleep; skipped in -short")
		}
		srv, d := testServer(t)
		mux := srv.routes()
		// "sh -c 'sleep 30'", not a bare "sleep 30": BuildCmd's resume branch
		// appends "--resume <uuid>" to the END of the command string, and the
		// outer exec.Command("sh", "-c", cmdStr) re-parses the WHOLE string as
		// one shell command line — a bare "sleep 30 --resume 'uuid'" makes the
		// real sleep(1) choke on the unexpected flag and exit 1 immediately.
		// Wrapping in "sh -c '...'" makes the appended flags land as $0/$1
		// inside the inner sh -c, which ignores them (same trick documented in
		// cmd/argus-secrets-smoke's smoke-cat backend and used by
		// internal/tui/sessionpicker_wiring_test.go for this exact scenario).
		testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
		task := &model.Task{
			Name:      "live-session",
			Status:    model.StatusInProgress,
			Backend:   "sh-sleep",
			Worktree:  t.TempDir(),
			SessionID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		}
		testutil.NoError(t, d.Add(task))

		oldSess, err := srv.runner.Start(task, d.Config(), 24, 80, false)
		testutil.NoError(t, err)
		oldPID := oldSess.PID()

		req := authedReq("POST", "/api/tasks/"+task.ID+"/claude-session", `{"session_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd"}`)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		t.Cleanup(func() {
			if sess := srv.runner.Get(task.ID); sess != nil {
				_ = srv.runner.Stop(task.ID)
				<-sess.Done()
			}
		})

		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["status"], "switched")
		pid, ok := resp["pid"].(float64)
		if !ok || pid <= 0 {
			t.Fatalf("expected a positive pid in response, got %v", resp["pid"])
		}
		if int(pid) == oldPID {
			t.Fatalf("expected a new pid distinct from the stopped session's pid %d", oldPID)
		}

		// The runner must show a live session for the task again (the
		// restarted one), not the original.
		newSess := srv.runner.Get(task.ID)
		if newSess == nil {
			t.Fatal("expected a live session after switch")
		}
		testutil.Equal(t, newSess.PID(), int(pid))

		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.SessionID, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	})
}
