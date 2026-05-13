package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// masterReq decorates an authedReq with the X-Argus-Auth=master header so
// requireMaster handlers pass when invoked through the raw mux (bypassing
// the middleware that would otherwise set the tag).
func masterReq(method, url, body string) *http.Request {
	req := authedReq(method, url, body)
	req.Header.Set("X-Argus-Auth", "master")
	return req
}

func deviceReq(method, url, body string) *http.Request {
	req := authedReq(method, url, body)
	req.Header.Set("X-Argus-Auth", "device")
	return req
}

func TestAPI_ListInbox_Empty(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+task.ID+"/inbox", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Messages    []model.TaskMessage `json:"messages"`
		UnreadCount int                 `json:"unread_count"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Messages), 0)
	testutil.Equal(t, resp.UnreadCount, 0)
}

func TestAPI_SendMessage_Happy(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	mux := srv.routes()
	body := fmt.Sprintf(`{"to":"%s","body":"hello"}`, to.ID)
	req := masterReq("POST", "/api/tasks/"+from.ID+"/messages", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	// Inbox endpoint shows it.
	req = authedReq("GET", "/api/tasks/"+to.ID+"/inbox", "")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var inbox struct {
		Messages    []model.TaskMessage `json:"messages"`
		UnreadCount int                 `json:"unread_count"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &inbox))
	testutil.Equal(t, len(inbox.Messages), 1)
	testutil.Equal(t, inbox.Messages[0].Body, "hello")
	testutil.Equal(t, inbox.UnreadCount, 1)
}

func TestAPI_SendMessage_DeviceRejected(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	mux := srv.routes()
	body := fmt.Sprintf(`{"to":"%s","body":"x"}`, to.ID)
	req := deviceReq("POST", "/api/tasks/"+from.ID+"/messages", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestAPI_SendMessage_BadRequest(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	testutil.NoError(t, d.Add(from))
	mux := srv.routes()

	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{"missing to", `{"body":"x"}`, http.StatusBadRequest},
		{"missing body", `{"to":"someone"}`, http.StatusBadRequest},
		{"recipient not found", `{"to":"does-not-exist","body":"x"}`, http.StatusNotFound},
		{"malformed JSON", `not-json`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := masterReq("POST", "/api/tasks/"+from.ID+"/messages", tc.body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, tc.wantCode)
		})
	}
}

func TestAPI_AckInbox(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	msg, err := d.InsertMessage(&model.TaskMessage{
		From: from.ID, To: to.ID, Kind: model.KindNote, Body: "x",
	})
	testutil.NoError(t, err)

	mux := srv.routes()
	body := fmt.Sprintf(`{"ids":["%s"]}`, msg.ID)
	req := authedReq("POST", "/api/tasks/"+to.ID+"/inbox/ack", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Acked int `json:"acked"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp.Acked, 1)

	// After ack, unread count drops to 0.
	unread, err := d.UnreadCount(to.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 0)
}

func TestAPI_AckInbox_RejectsEmpty(t *testing.T) {
	srv, d := testServer(t)
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(to))

	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+to.ID+"/inbox/ack", `{"ids":[]}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestAPI_ListInbox_BadSince(t *testing.T) {
	srv, d := testServer(t)
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(to))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+to.ID+"/inbox?since=bogus", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "invalid since") {
		t.Errorf("expected error about since, got: %s", w.Body.String())
	}
}

func TestAPI_ListInbox_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/no-such-task/inbox", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}
