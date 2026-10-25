package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestBridgeUISnapshotReadOnly(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	orch, err := s.db.CreateHeraOrchestrator("ui-team", "")
	testutil.NoError(t, err)
	role, binding, err := s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator}, task.ID, task.Worktree)
	testutil.NoError(t, err)
	sender, err := s.db.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "sender", Kind: db.HeraKindWorker})
	testutil.NoError(t, err)
	mail, err := s.db.SendHeraMessage(sender.ID, role.ID, "body", "Ready to review", nil)
	testutil.NoError(t, err)
	_, err = s.db.InsertSystemMessage(&model.TaskMessage{From: model.SystemTaskID, To: task.ID, Kind: model.KindNote, Body: "Task notice"})
	testutil.NoError(t, err)
	testutil.NoError(t, s.db.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))
	percent := 25.0
	tokens := int64(250000)
	s.bridgeHub.Ingest(task.ID, []bridge.Event{{Type: bridge.TypeMeasure, Generation: "launch", SessionID: "session", Boot: "b", Seq: 1, Context: &bridge.Context{Tokens: &tokens, Percent: &percent, Window: 1000000}}}, time.Now())
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
		testutil.Equal(t, w.Code, 200)
		var got bridgeUISnapshot
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		testutil.True(t, got.Live)
		testutil.True(t, got.Settings.Band)
		testutil.True(t, got.Settings.Toasts)
		testutil.Equal(t, got.Unread, 2)
		testutil.Equal(t, len(got.Messages), 2)
		testutil.Equal(t, got.Bindings[0].BindingID, binding.ID)
		testutil.Equal(t, got.Bindings[0].Role, "coord")
		testutil.Equal(t, got.Bindings[0].Status, "working")
		testutil.Equal(t, got.Bindings[0].Budget, 300000)
		testutil.Equal(t, *got.Context.Percent, 25.0)
	}
	inbox, err := s.db.HeraInbox(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(inbox), 1)
	testutil.Equal(t, inbox[0].ID, mail.ID)
	testutil.Nil(t, inbox[0].ReadAt)
	testutil.Equal(t, inbox[0].DeliveryMode, db.HeraDeliveryPending)
	taskInbox, err := s.db.Inbox(task.ID, db.InboxFilter{UnreadOnly: true})
	testutil.NoError(t, err)
	testutil.Equal(t, len(taskInbox), 1)
	testutil.NoError(t, s.db.SetConfigValue("bridge.enabled", "false"))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
	var got bridgeUISnapshot
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.False(t, got.Live)
	testutil.False(t, got.Settings.Enabled)
}

func TestBridgeUISnapshotUnboundAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auth    bool
		missing bool
		closed  bool
		want    int
	}{
		{"unbound", true, false, false, 200}, {"missing", true, true, false, 404}, {"unauthorized", false, false, false, 401}, {"database", true, false, true, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, task := bridgeAuthorityServer(t)
			id := task.ID
			if tc.missing {
				id = "missing"
			}
			if tc.closed {
				testutil.NoError(t, s.db.Close())
			}
			req := httptest.NewRequest("GET", "/api/tasks/"+id+"/bridge/ui", nil)
			if tc.auth {
				req.Header.Set("Authorization", "Bearer test-token")
			}
			w := httptest.NewRecorder()
			authMiddleware("test-token", s.db, nil, s.routes()).ServeHTTP(w, req)
			testutil.Equal(t, w.Code, tc.want)
			if tc.want == http.StatusOK {
				var got bridgeUISnapshot
				testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
				testutil.Equal(t, len(got.Bindings), 0)
			}
		})
	}
}

func TestBridgeUISnapshotLiveBindingsAndTaskSummary(t *testing.T) {
	s, _, task := bridgeAuthorityServer(t)
	var ended int64
	for _, name := range []string{"first", "second", "ended"} {
		orch, err := s.db.CreateHeraOrchestrator(name, "")
		testutil.NoError(t, err)
		role, binding, err := s.db.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: name, Kind: db.HeraKindWorker}, task.ID, task.Worktree)
		testutil.NoError(t, err)
		sender, err := s.db.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "sender", Kind: db.HeraKindCoordinator})
		testutil.NoError(t, err)
		_, err = s.db.SendHeraMessage(sender.ID, role.ID, "body", name+" summary", nil)
		testutil.NoError(t, err)
		if name == "ended" {
			ended = binding.ID
		}
	}
	testutil.NoError(t, s.db.EndHeraBinding(ended, "test"))
	from := &model.Task{Name: "Named sender", Status: model.StatusInProgress}
	testutil.NoError(t, s.db.Add(from))
	_, err := s.db.InsertMessage(&model.TaskMessage{From: from.ID, To: task.ID, Kind: model.KindNote, Body: strings.Repeat("界", 130) + "\nmore"})
	testutil.NoError(t, err)
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, authedReq("GET", "/api/tasks/"+task.ID+"/bridge/ui", ""))
	testutil.Equal(t, w.Code, 200)
	var got bridgeUISnapshot
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.Equal(t, len(got.Bindings), 2)
	testutil.Equal(t, got.Unread, 3)
	for _, binding := range got.Bindings {
		testutil.Equal(t, binding.Kind, "worker")
		testutil.Equal(t, binding.Budget, 0)
		testutil.Equal(t, binding.Unread, 1)
	}
	summaries := map[string]string{}
	for _, message := range got.Messages {
		summaries[message.Sender] = message.Tldr
	}
	testutil.Equal(t, summaries["Named sender"], strings.Repeat("界", 117)+"...")
}
