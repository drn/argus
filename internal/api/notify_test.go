package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

// notifyNilRunner implements notify.RunnerIface — always returns nil (no sessions).
type notifyNilRunner struct{}

func (notifyNilRunner) Get(string) notify.SessionHandleIface { return nil }

// notifyNoFocus is a notify.FocusReader — always unfocused.
type notifyNoFocus struct{}

func (notifyNoFocus) IsFocused(string) bool { return false }

// newNotifierForTest builds a Notifier and wires it to the server.
func newNotifierForTest(srv *Server) {
	n := notify.New(notifyNilRunner{}, notifyNoFocus{})
	srv.SetNotifier(n)
}

// deviceAuthedReq builds an HTTP request with a device Bearer token.
func deviceAuthedReq(method, url, body, token string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, url, nil)
	} else {
		r = httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestHandleNotify_MissingNotifier_Returns503(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/any/notify",
		`{"text":"hi","submit":true,"delivery_id":"d1"}`))
	testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
}

func TestHandleNotify_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	newNotifierForTest(srv)
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/nonexistent/notify",
		`{"text":"hi","submit":true,"delivery_id":"d1"}`))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestHandleNotify_MissingText_Returns400(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"submit":true,"delivery_id":"d1"}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHandleNotify_SubmitNotTrue_Returns400(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":false,"delivery_id":"d1"}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHandleNotify_MissingDeliveryID_Returns400(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":true}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHandleNotify_DeadlineMS_BelowMinimum_Returns400(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":true,"delivery_id":"d1","deadline_ms":500}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHandleNotify_DeadlineMS_Zero_UsesDefault(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":true,"delivery_id":"d1","deadline_ms":0}`))
	// 0 means "use default" — should succeed.
	testutil.Equal(t, w.Code, http.StatusAccepted)
}

func TestHandleNotify_BadDeliveryIDFormat_Returns400(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":true,"delivery_id":"bad id!"}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestHandleNotify_ValidRequest_Returns202Pending(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hello","submit":true,"delivery_id":"d1"}`))
	// No live session → delivery is pending.
	testutil.Equal(t, w.Code, http.StatusAccepted)
	testutil.Contains(t, w.Body.String(), `"pending"`)
}

// notifyIdleRunner returns an idle session for any task ID — used to test
// the inline-submit path where handleNotify calls Reconcile immediately.
type notifyIdleRunner struct{}

func (notifyIdleRunner) Get(string) notify.SessionHandleIface { return &notifyIdleSession{} }

type notifyIdleSession struct{ writes [][]byte }

func (s *notifyIdleSession) IsIdle() bool { return true }
func (s *notifyIdleSession) WriteInputSystem(p []byte) (int, error) {
	s.writes = append(s.writes, append([]byte(nil), p...))
	return len(p), nil
}

func TestHandleNotify_SessionIdle_ReturnsSubmitted(t *testing.T) {
	srv, d := testServer(t)
	n := notify.New(notifyIdleRunner{}, notifyNoFocus{})
	srv.SetNotifier(n)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hello","submit":true,"delivery_id":"d1"}`))
	// Session is idle+unfocused — inline Reconcile should submit immediately.
	testutil.Equal(t, w.Code, http.StatusAccepted)
	testutil.Contains(t, w.Body.String(), `"submitted"`)
}

func TestHandleNotify_RepostSubmitted_Returns200(t *testing.T) {
	srv, d := testServer(t)
	// Use idle runner so first POST submits inline.
	n := notify.New(notifyIdleRunner{}, notifyNoFocus{})
	srv.SetNotifier(n)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	// First POST — submits inline because session is idle.
	w1 := httptest.NewRecorder()
	mux.ServeHTTP(w1, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hello","submit":true,"delivery_id":"d1"}`))
	testutil.Equal(t, w1.Code, http.StatusAccepted)
	testutil.Contains(t, w1.Body.String(), `"submitted"`)

	// Second POST with same delivery_id — idempotent 200.
	w2 := httptest.NewRecorder()
	mux.ServeHTTP(w2, authedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hello","submit":true,"delivery_id":"d1"}`))
	testutil.Equal(t, w2.Code, http.StatusOK)
	testutil.Contains(t, w2.Body.String(), `"submitted"`)
}

func TestHandleNotify_DeviceTokenAccepted(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	plain, _, err := MintToken(d, "test-device")
	testutil.NoError(t, err)

	mux := srv.routes()
	handler := authMiddleware(srv.token, srv.db, nil, mux,
		"/", "/share", "/vendor/", "/manifest.webmanifest", "/sw.js",
		"/icon-192.png", "/icon-512.png", "/apple-touch-icon.png")

	r := deviceAuthedReq("POST", "/api/tasks/"+task.ID+"/notify",
		`{"text":"hi","submit":true,"delivery_id":"d2"}`, plain)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	testutil.Equal(t, w.Code, http.StatusAccepted)
}

func TestHandleCancelNotify_MissingNotifier_Returns503(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("DELETE", "/api/tasks/any/notify/d1", ""))
	testutil.Equal(t, w.Code, http.StatusServiceUnavailable)
}

func TestHandleCancelNotify_PendingDelivery_ReturnsCancelledTrue(t *testing.T) {
	srv, d := testServer(t)
	n := notify.New(notifyNilRunner{}, notifyNoFocus{})
	srv.SetNotifier(n)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	// Register a delivery.
	n.ReliableNotify(task.ID, "hello", "d1", notify.NotifyOpts{})

	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("DELETE", "/api/tasks/"+task.ID+"/notify/d1", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), `"cancelled":true`)
}

func TestHandleCancelNotify_UnknownDeliveryID_ReturnsCancelledFalse(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("DELETE", "/api/tasks/"+task.ID+"/notify/nonexistent", ""))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), `"cancelled":false`)
}

func TestHandleCancelNotify_DeviceTokenAccepted(t *testing.T) {
	srv, d := testServer(t)
	newNotifierForTest(srv)
	task := &model.Task{Name: "n1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	plain, _, err := MintToken(d, "test-device")
	testutil.NoError(t, err)

	mux := srv.routes()
	handler := authMiddleware(srv.token, srv.db, nil, mux,
		"/", "/share", "/vendor/", "/manifest.webmanifest", "/sw.js",
		"/icon-192.png", "/icon-512.png", "/apple-touch-icon.png")

	r := deviceAuthedReq("DELETE", "/api/tasks/"+task.ID+"/notify/d1", "", plain)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	testutil.Equal(t, w.Code, http.StatusOK)
}
