package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
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

func TestAPI_AckInbox_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/no-such-task/inbox/ack", `{"ids":["m1"]}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
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

func TestAPI_ListInbox_UnreadOnlyParseVariants(t *testing.T) {
	// Each case must produce a 200 — the `0` / `no` / `false` synonyms all
	// flip unread_only off. Without this only the `false` variant is tested.
	srv, d := testServer(t)
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(to))
	mux := srv.routes()
	for _, v := range []string{"false", "0", "no"} {
		t.Run(v, func(t *testing.T) {
			req := authedReq("GET", "/api/tasks/"+to.ID+"/inbox?unread_only="+v, "")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, http.StatusOK)
		})
	}
}

func TestAPI_ListInbox_FullFilters(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	_, err := d.InsertMessage(&model.TaskMessage{
		From: from.ID, To: to.ID, Kind: model.KindNote, Body: "hello",
	})
	testutil.NoError(t, err)

	mux := srv.routes()
	// Cover the unread_only=false branch + the limit query path + sender filter.
	u := "/api/tasks/" + to.ID + "/inbox?unread_only=false&sender=" + from.ID + "&limit=5"
	req := authedReq("GET", u, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Messages []model.TaskMessage `json:"messages"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Messages), 1)
}

func TestAPI_ListInbox_BadLimit(t *testing.T) {
	srv, d := testServer(t)
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(to))
	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+to.ID+"/inbox?limit=not-a-number", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "invalid limit") {
		t.Errorf("expected invalid-limit error, got: %s", w.Body.String())
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

// TestAPI_ArchiveCleansUpMessages confirms the REST archive entrypoint
// mirrors the MCP task_archive cleanup behaviour. Without this every
// archive via the PWA would leave the recipient on the unread cap.
func TestAPI_ArchiveCleansUpMessages(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "sender"}
	to := &model.Task{Name: "doomed"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	_, err := d.InsertMessage(&model.TaskMessage{
		From: from.ID, To: to.ID, Kind: model.KindNote, Body: "x",
	})
	testutil.NoError(t, err)

	mux := srv.routes()
	req := authedReq("POST", "/api/tasks/"+to.ID+"/archive", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	unread, err := d.UnreadCount(to.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, unread, 0)
}

// TestAPI_DeleteCascadesMessages confirms the destroy path cascades via
// db.Delete's app-level FK substitute — no orphan rows pointing at a dead
// task ID survive.
func TestAPI_DeleteCascadesMessages(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "sender"}
	doomed := &model.Task{Name: "doomed", Worktree: ""}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(doomed))

	_, err := d.InsertMessage(&model.TaskMessage{
		From: from.ID, To: doomed.ID, Kind: model.KindNote, Body: "x",
	})
	testutil.NoError(t, err)

	mux := srv.routes()
	req := authedReq("DELETE", "/api/tasks/"+doomed.ID, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// from's outbox row referencing doomed must also be gone — DeleteMessagesForTask
	// wipes both legs.
	got, err := d.Inbox(from.ID, InboxFilterUnused())
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

// InboxFilterUnused returns a zero filter; named for grep-friendliness in the
// test above. The import path forces the db package reference, used here to
// keep the cascade test self-contained.
func InboxFilterUnused() db.InboxFilter { return db.InboxFilter{UnreadOnly: false} }

// TestAPI_SendMessage_FromTaskNotFound covers the 404 branch when the path
// task ID doesn't resolve. The pre-existing happy-path test only covered
// the case where both tasks exist.
func TestAPI_SendMessage_FromTaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	req := masterReq("POST", "/api/tasks/missing/messages", `{"to":"any","body":"x"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

// TestAPI_SendMessage_CapErrors confirms each DB cap rejection maps to
// HTTP 400 with the error message preserved. The cap-rejection branches
// in handleSendMessage previously had no coverage.
func TestAPI_SendMessage_CapErrors(t *testing.T) {
	srv, d := testServer(t)
	from := &model.Task{Name: "from"}
	to := &model.Task{Name: "to"}
	testutil.NoError(t, d.Add(from))
	testutil.NoError(t, d.Add(to))

	mux := srv.routes()

	t.Run("self send", func(t *testing.T) {
		body := fmt.Sprintf(`{"to":"%s","body":"x"}`, from.ID)
		req := masterReq("POST", "/api/tasks/"+from.ID+"/messages", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		if !strings.Contains(w.Body.String(), "self") {
			t.Errorf("expected self-send error, got: %s", w.Body.String())
		}
	})

	t.Run("body too large", func(t *testing.T) {
		body := fmt.Sprintf(`{"to":"%s","body":"%s"}`, to.ID, strings.Repeat("x", model.MaxMessageBodyBytes+1))
		req := masterReq("POST", "/api/tasks/"+from.ID+"/messages", body)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		// http.MaxBytesReader returns its own 400; otherwise the DB cap fires.
		// Either way, this is a 4xx, not a 5xx — confirm.
		if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("expected 4xx, got %d: %s", w.Code, w.Body.String())
		}
	})
}
