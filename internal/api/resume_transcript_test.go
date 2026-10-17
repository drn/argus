package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestResumeMissingTranscript(t *testing.T) {
	for _, action := range []string{"resume", "restart"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			srv, d := testServer(t)
			bin := filepath.Join(t.TempDir(), "claude")
			testutil.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\nexec sleep 30\n"), 0o700))
			testutil.NoError(t, d.SetBackend("fixture", config.Backend{Command: bin}))
			const old = "11111111-1111-7111-9111-111111111111"
			task := &model.Task{Name: "missing", Backend: "fixture", Worktree: t.TempDir(), SessionID: old, Prompt: "original mission"}
			testutil.NoError(t, d.Add(task))
			w := httptest.NewRecorder()
			srv.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/"+action, ""))
			testutil.Equal(t, w.Code, http.StatusOK)
			got, err := d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.False(t, got.SessionID == old || got.SessionID == "")
			testutil.Contains(t, w.Body.String(), `"session_id":"`+got.SessionID+`"`)
			sess := srv.runner.Get(task.ID)
			testutil.NotNil(t, sess)
			t.Cleanup(func() { _ = srv.runner.Stop(task.ID); <-sess.Done() })
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) && !strings.Contains(string(sess.RecentOutput()), task.Prompt) {
				time.Sleep(10 * time.Millisecond)
			}
			out := string(sess.RecentOutput())
			testutil.Contains(t, out, "--session-id\r\n"+got.SessionID)
			testutil.Contains(t, out, task.Prompt)
			testutil.False(t, strings.Contains(out, "--resume"))
		})
	}
}

type transcriptKickSession struct{ agent.SessionHandle }

func (transcriptKickSession) Alive() bool                 { return true }
func (transcriptKickSession) IsIdle() bool                { return true }
func (transcriptKickSession) InitialPTYSize() (int, int)  { return 80, 24 }
func (transcriptKickSession) RecentOutputTail(int) []byte { return nil }

type transcriptKickRunner struct {
	*agent.Runner
	kicks int
}

func (*transcriptKickRunner) Get(string) agent.SessionHandle { return transcriptKickSession{} }
func (r *transcriptKickRunner) KickRerender(*model.Task, config.Config, uint16, uint16) error {
	r.kicks++
	return nil
}

func TestResizeMissingTranscriptRetries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv, d := testServer(t)
	r := &transcriptKickRunner{Runner: agent.NewRunner(nil)}
	srv.runner = r
	task := &model.Task{Name: "quiet", Backend: "claude", Worktree: t.TempDir(), SessionID: "11111111-1111-7111-9111-111111111111"}
	testutil.NoError(t, d.Add(task))
	testutil.False(t, srv.maybeKickRerender(task.ID, 24, 98))
	testutil.Equal(t, r.kicks, 0)
	task.Account = "removed"
	testutil.NoError(t, d.Update(task))
	testutil.False(t, srv.maybeKickRerender(task.ID, 24, 98))
	testutil.Equal(t, r.kicks, 0)
	testutil.False(t, r.HasPendingRestart(task.ID))
	task.Account = ""
	testutil.NoError(t, d.Update(task))
	seedAPITranscript(t, home, task.Worktree, task.SessionID, time.Time{})
	testutil.True(t, srv.maybeKickRerender(task.ID, 24, 98))
	testutil.Equal(t, r.kicks, 1)
}

func TestExactTranscriptProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	srv, d := testServer(t)
	physical, err := filepath.EvalSymlinks(t.TempDir())
	testutil.NoError(t, err)
	link := filepath.Join(t.TempDir(), "link")
	testutil.NoError(t, os.Symlink(physical, link))
	task := &model.Task{Name: "probe", Backend: "claude", Worktree: link, SessionID: "different"}
	testutil.NoError(t, d.Add(task))
	seedAPITranscript(t, home, physical, "11111111-1111-7111-9111-111111111111", time.Time{})
	for _, tc := range []struct {
		id, want string
		status   int
	}{
		{"11111111-1111-7111-9111-111111111111", `"available":true`, 200},
		{"absent", `"available":false`, 200},
	} {
		t.Run(tc.id, func(t *testing.T) {
			w := httptest.NewRecorder()
			srv.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions?check_session_id="+tc.id, ""))
			testutil.Equal(t, w.Code, tc.status)
			testutil.Contains(t, w.Body.String(), tc.want)
		})
	}
	if os.Geteuid() != 0 {
		project, err := claudesession.ProjectDirIn("", physical)
		testutil.NoError(t, err)
		testutil.NoError(t, os.Chmod(project, 0))
		t.Cleanup(func() {
			//nolint:gosec // G302: owner traversal is required for TempDir directory cleanup.
			testutil.NoError(t, os.Chmod(project, 0o700))
		})
		w := httptest.NewRecorder()
		srv.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions?check_session_id=saved", ""))
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	}
	task.Account = "removed"
	testutil.NoError(t, d.Update(task))
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions?check_session_id=saved", ""))
	testutil.Equal(t, w.Code, http.StatusConflict)
}
