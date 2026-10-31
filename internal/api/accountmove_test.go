package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type moveTestSession struct {
	agent.SessionHandle
	idle bool
}

func (*moveTestSession) Alive() bool         { return true }
func (s *moveTestSession) IsIdle() bool      { return s.idle }
func (*moveTestSession) PID() int            { return 4242 }
func (*moveTestSession) PTYSize() (int, int) { return 120, 40 }

type moveTestRunner struct {
	agent.SessionRunner
	live     *moveTestSession
	kicked   *model.Task
	started  *model.Task
	startErr error
}

func (r *moveTestRunner) Get(string) agent.SessionHandle {
	if r.live == nil {
		return nil
	}
	return r.live
}

func (r *moveTestRunner) KickRerender(task *model.Task, _ config.Config, _, _ uint16) error {
	cp := *task
	r.kicked = &cp
	return nil
}

func (r *moveTestRunner) Revive(task *model.Task, cfg config.Config, rows, cols uint16) error {
	return r.KickRerender(task, cfg, rows, cols)
}

func (r *moveTestRunner) StartOrReattach(task *model.Task, _ config.Config, _, _ uint16, _ bool) (agent.SessionHandle, bool, error) {
	if r.startErr != nil {
		return nil, false, r.startErr
	}
	cp := *task
	r.started = &cp
	return &moveTestSession{idle: true}, false, nil
}

func moveServer(t *testing.T, r *moveTestRunner) (*Server, *model.Task) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	s, d := testServer(t)
	r.SessionRunner = agent.NewRunner(nil) // background watchers call the rest
	s.runner = r
	testutil.NoError(t, d.SetBackend("cl", config.Backend{Command: "claude"}))
	testutil.NoError(t, d.SetAccount("work", config.Account{ClaudeConfigDir: filepath.Join(t.TempDir(), "work")}))
	task := &model.Task{Name: "mover", Backend: "cl", Worktree: t.TempDir(), SessionID: "sid", Status: model.StatusInReview}
	testutil.NoError(t, d.Add(task))
	return s, task
}

func postMove(s *Server, id, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("POST", "/api/tasks/"+id+"/account", body))
	return w
}

func TestHandleMoveTaskAccount(t *testing.T) {
	t.Run("live idle session is restarted in place", func(t *testing.T) {
		r := &moveTestRunner{live: &moveTestSession{idle: true}}
		s, task := moveServer(t, r)
		w := postMove(s, task.ID, `{"account":"work"}`)
		testutil.Equal(t, w.Code, http.StatusOK)
		var res agent.AccountMoveResult
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		testutil.Equal(t, res.From, "default")
		testutil.Equal(t, res.To, "work")
		testutil.True(t, res.Started)
		testutil.Equal(t, r.kicked.Account, "work")
		testutil.Nil(t, r.started)
	})

	t.Run("exited session is resumed through the restart path", func(t *testing.T) {
		r := &moveTestRunner{}
		s, task := moveServer(t, r)
		w := postMove(s, task.ID, `{"account":"work"}`)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.NotNil(t, r.started)
		testutil.Equal(t, r.started.Account, "work")
		got, err := s.db.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Status, model.StatusInProgress)
		testutil.Equal(t, got.Account, "work")
	})

	t.Run("busy session is 409 without force", func(t *testing.T) {
		r := &moveTestRunner{live: &moveTestSession{idle: false}}
		s, task := moveServer(t, r)
		testutil.Equal(t, postMove(s, task.ID, `{"account":"work"}`).Code, http.StatusConflict)
		got, _ := s.db.Get(task.ID)
		testutil.Equal(t, got.Account, "")
		testutil.Equal(t, postMove(s, task.ID, `{"account":"work","force":true}`).Code, http.StatusOK)
	})

	t.Run("bad target is 400", func(t *testing.T) {
		s, task := moveServer(t, &moveTestRunner{})
		w := postMove(s, task.ID, `{"account":"default"}`)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		testutil.Contains(t, w.Body.String(), "already on account")
		testutil.Equal(t, postMove(s, task.ID, `{"account":"ghost"}`).Code, http.StatusBadRequest)
		testutil.Equal(t, postMove(s, task.ID, `not json`).Code, http.StatusBadRequest)
	})

	t.Run("unknown task is 404", func(t *testing.T) {
		s, _ := moveServer(t, &moveTestRunner{})
		testutil.Equal(t, postMove(s, "missing", `{"account":"work"}`).Code, http.StatusNotFound)
	})

	t.Run("restart failure after the move is 500 with the task on the target", func(t *testing.T) {
		r := &moveTestRunner{startErr: errors.New("spawn failed")}
		s, task := moveServer(t, r)
		w := postMove(s, task.ID, `{"account":"work"}`)
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
		testutil.Contains(t, w.Body.String(), "spawn failed")
		got, _ := s.db.Get(task.ID)
		testutil.Equal(t, got.Account, "work")
	})
}
