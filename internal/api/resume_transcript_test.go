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
	seedAPITranscript(t, home, task.Worktree, task.SessionID, time.Time{})
	testutil.True(t, srv.maybeKickRerender(task.ID, 24, 98))
	testutil.Equal(t, r.kicks, 1)
}
