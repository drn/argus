package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestHandleListClaudeSessions_RemovedAccountConflicts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, d := testServer(t)
	mux := srv.routes()
	testutil.NoError(t, d.SetBackend("sh-sleep", config.Backend{Command: "sh -c 'sleep 30'"}))
	task := &model.Task{Name: "acct-task", Backend: "sh-sleep", Worktree: t.TempDir(), Account: "gone"}
	testutil.NoError(t, d.Add(task))

	req := authedReq("GET", "/api/tasks/"+task.ID+"/claude-sessions", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusConflict)
}
