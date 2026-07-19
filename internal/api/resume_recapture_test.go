package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// startFailRunner is a SessionRunner whose StartOrReattach always errors without
// spawning a process, so a resume/restart handler test can exercise the
// resume-time recapture (which runs BEFORE StartOrReattach) without touching a
// real PTY. All other SessionRunner methods come from the embedded no-op runner.
type startFailRunner struct{ *agent.Runner }

func (startFailRunner) StartOrReattach(*model.Task, config.Config, uint16, uint16, bool) (agent.SessionHandle, bool, error) {
	return nil, false, fmt.Errorf("start refused in test")
}

func seedAPITranscript(t *testing.T, home, worktree, id string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudesession.EncodeProjectDir(worktree))
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte("{}\n"), 0o644))
	if !mod.IsZero() {
		testutil.NoError(t, os.Chtimes(filepath.Join(dir, id+".jsonl"), mod, mod))
	}
}

// TestHandleResume_RefreshesClaudeSessionID pins the REST resume/restart seams:
// before StartOrReattach, a Claude task's recorded session ID is refreshed to
// the newest worktree transcript so the resume targets the most recent in-place
// session. StartOrReattach then errors (fake runner) — a 500 documents that no
// process is spawned; the refresh has already persisted regardless.
func TestHandleResume_RefreshesClaudeSessionID(t *testing.T) {
	setup := func(t *testing.T) (*Server, *db.DB, *model.Task, string) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		d, err := db.OpenInMemory()
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d.Close() })

		srv := New(d, startFailRunner{agent.NewRunner(nil)}, "test-token", nil, nil)

		wt := filepath.Join(home, "wt")
		original := "11111111-1111-7111-9111-111111111111"
		newest := "22222222-2222-7222-9222-222222222222"
		seedAPITranscript(t, home, wt, original, time.Now().Add(-1*time.Hour))
		seedAPITranscript(t, home, wt, newest, time.Now())

		task := &model.Task{
			Name:      "resume-me",
			Status:    model.StatusInProgress,
			Backend:   "claude",
			Worktree:  wt,
			SessionID: original,
		}
		testutil.NoError(t, d.Add(task))
		return srv, d, task, newest
	}

	t.Run("resume refreshes then fails start", func(t *testing.T) {
		srv, d, task, newest := setup(t)
		mux := srv.routes()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/resume", ""))

		testutil.Equal(t, w.Code, http.StatusInternalServerError) // fake StartOrReattach; no spawn
		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.SessionID, newest) // refreshed before the failed start
	})

	t.Run("restart refreshes then fails start", func(t *testing.T) {
		srv, d, task, newest := setup(t)
		mux := srv.routes()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/restart", ""))

		testutil.Equal(t, w.Code, http.StatusInternalServerError)
		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.SessionID, newest)
	})
}
