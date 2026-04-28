package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// stubScheduler satisfies ScheduleRunner without spinning up a real scheduler
// goroutine.
type stubScheduler struct {
	lastID string
	task   *model.Task
	err    error
}

func (s *stubScheduler) RunNow(id string) (*model.Task, error) {
	s.lastID = id
	if s.err != nil {
		return nil, s.err
	}
	return s.task, nil
}

func TestScheduleHandlers_CreateListUpdateDelete(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, srv.routes())

	// Create.
	body := `{"name":"Nightly","project":"argus","prompt":"run tests","schedule":"@daily","enabled":true}`
	req := authedReq("POST", "/api/schedules", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	var created scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("expected ID")
	}
	testutil.Equal(t, created.Name, "Nightly")
	testutil.Equal(t, created.Enabled, true)

	// List.
	req = authedReq("GET", "/api/schedules", "")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var list struct {
		Schedules []scheduleJSON `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, len(list.Schedules), 1)

	// Update — toggle enabled off.
	req = authedReq("PUT", "/api/schedules/"+created.ID, `{"enabled":false}`)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var updated scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, updated.Enabled, false)

	// Delete.
	req = authedReq("DELETE", "/api/schedules/"+created.ID, "")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNoContent)

	// Confirm gone.
	req = authedReq("GET", "/api/schedules", "")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, len(list.Schedules), 0)
}

func TestScheduleHandlers_CreateValidates(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, srv.routes())

	cases := map[string]string{
		"missing-name":     `{"project":"p","prompt":"go","schedule":"@daily"}`,
		"missing-project":  `{"name":"x","prompt":"go","schedule":"@daily"}`,
		"missing-prompt":   `{"name":"x","project":"p","schedule":"@daily"}`,
		"missing-schedule": `{"name":"x","project":"p","prompt":"go"}`,
		"bad-schedule":     `{"name":"x","project":"p","prompt":"go","schedule":"foo bar baz"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			req := authedReq("POST", "/api/schedules", body)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, http.StatusBadRequest)
		})
	}
}

// Regression: editing the schedule expression of a never-run schedule must
// not seed a year-0001 NextRunAt that would fire on the very next tick.
// See review-20260428.md BLOCKING #2.
func TestScheduleHandlers_UpdateScheduleAnchorOnNow(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, srv.db, srv.routes())

	sched := &model.ScheduledTask{
		Name:     "fresh",
		Project:  "p",
		Prompt:   "go",
		Schedule: "@daily",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	req := authedReq("PUT", "/api/schedules/"+sched.ID, `{"schedule":"@hourly"}`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	got, _ := d.GetSchedule(sched.ID)
	if got.NextRunAt.Year() < 2020 {
		t.Fatalf("NextRunAt was anchored on zero time: got %v", got.NextRunAt)
	}
	if got.NextRunAt.Before(time.Now().Add(-time.Hour)) {
		t.Fatalf("NextRunAt anchored in the past: got %v (now=%v)", got.NextRunAt, time.Now())
	}
}

func TestScheduleHandlers_RunNow(t *testing.T) {
	srv, d := testServer(t)
	stub := &stubScheduler{task: &model.Task{ID: "task-123", Name: "fired"}}
	srv.SetScheduler(stub)
	handler := authMiddleware(srv.token, srv.db, srv.routes())

	// Add directly to DB so RunNow can find it.
	sched := &model.ScheduledTask{
		Name:     "Manual",
		Project:  "argus",
		Prompt:   "go",
		Schedule: "@every 1h",
		Enabled:  true,
	}
	if err := d.AddSchedule(sched); err != nil {
		t.Fatal(err)
	}

	req := authedReq("POST", "/api/schedules/"+sched.ID+"/run", "")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusAccepted)
	testutil.Equal(t, stub.lastID, sched.ID)
	if !strings.Contains(w.Body.String(), "task-123") {
		t.Fatalf("expected task_id in response, got %s", w.Body.String())
	}
}

func TestScheduleHandlers_MasterOnly(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, srv.routes())
	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)
	device := func(method, url, body string) *http.Request {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, url, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, url, nil)
		}
		req.Header.Set("Authorization", "Bearer "+plain)
		return req
	}

	cases := []struct {
		name   string
		method string
		url    string
		body   string
	}{
		{"list", "GET", "/api/schedules", ""},
		{"create", "POST", "/api/schedules", `{"name":"x","project":"p","prompt":"go","schedule":"@daily"}`},
		{"update", "PUT", "/api/schedules/anything", `{"enabled":false}`},
		{"delete", "DELETE", "/api/schedules/anything", ""},
		{"run", "POST", "/api/schedules/anything/run", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, device(tc.method, tc.url, tc.body))
			testutil.Equal(t, w.Code, http.StatusForbidden)
		})
	}
}
