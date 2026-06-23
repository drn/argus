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
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

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

func TestScheduleHandlers_ModelCreateAndPartialUpdate(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

	// Create with a model override — it echoes back.
	body := `{"name":"Watcher","project":"argus","prompt":"watch","schedule":"@daily","backend":"claude","model":"sonnet"}`
	req := authedReq("POST", "/api/schedules", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)
	var created scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, created.Model, "sonnet")

	// Partial update of only `model` preserves the other fields.
	req = authedReq("PUT", "/api/schedules/"+created.ID, `{"model":"opus"}`)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var updated scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, updated.Model, "opus")
	testutil.Equal(t, updated.Name, "Watcher")
	testutil.Equal(t, updated.Backend, "claude")
	testutil.Equal(t, updated.Schedule, "@daily")

	// Clearing the model via empty string drops the override. Decode into a
	// fresh struct: `model` is omitempty, so the cleared response omits it and
	// reusing `updated` would retain the stale value.
	req = authedReq("PUT", "/api/schedules/"+created.ID, `{"model":""}`)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
	var cleared scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &cleared); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, cleared.Model, "")
	testutil.Equal(t, cleared.Name, "Watcher")
}

func TestScheduleHandlers_CreateValidates(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	cases := map[string]string{
		"missing-name":      `{"project":"p","prompt":"go","schedule":"@daily"}`,
		"missing-project":   `{"name":"x","prompt":"go","schedule":"@daily"}`,
		"missing-prompt":    `{"name":"x","project":"p","schedule":"@daily"}`,
		"missing-cadence":   `{"name":"x","project":"p","prompt":"go"}`,
		"bad-schedule":      `{"name":"x","project":"p","prompt":"go","schedule":"foo bar baz"}`,
		"run-once-past":     `{"name":"x","project":"p","prompt":"go","run_once_at":"` + past + `"}`,
		"run-once-bad-fmt":  `{"name":"x","project":"p","prompt":"go","run_once_at":"tomorrow"}`,
		"both-cadences-set": `{"name":"x","project":"p","prompt":"go","schedule":"@daily","run_once_at":"` + future + `"}`,
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

func TestScheduleHandlers_CreateOneShot(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"name":"once","project":"argus","prompt":"go","run_once_at":"` + future + `"}`
	req := authedReq("POST", "/api/schedules", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	var created scheduleJSON
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	testutil.Equal(t, created.Schedule, "")
	if created.RunOnceAt == "" {
		t.Error("expected run_once_at echoed back")
	}
	if created.NextRunAt == "" {
		t.Error("expected next_run_at populated for one-shot")
	}

	// Verify DB row matches.
	stored, _ := srv.db.GetSchedule(created.ID)
	if stored.RunOnceAt.IsZero() {
		t.Fatal("expected RunOnceAt persisted to DB")
	}
	if !stored.IsOneShot() {
		t.Fatal("expected IsOneShot=true after persist")
	}
}

func TestScheduleHandlers_UpdateCadenceSwitch(t *testing.T) {
	srv, _ := testServer(t)
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

	// Seed a recurring schedule.
	body := `{"name":"r","project":"argus","prompt":"go","schedule":"@daily"}`
	req := authedReq("POST", "/api/schedules", body)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var created scheduleJSON
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	t.Run("recurring to one-shot clears schedule", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		req := authedReq("PUT", "/api/schedules/"+created.ID, `{"run_once_at":"`+future+`"}`)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		stored, _ := srv.db.GetSchedule(created.ID)
		testutil.Equal(t, stored.Schedule, "")
		if !stored.IsOneShot() {
			t.Fatal("expected one-shot after switch")
		}
	})

	t.Run("one-shot to recurring clears run_once_at", func(t *testing.T) {
		req := authedReq("PUT", "/api/schedules/"+created.ID, `{"schedule":"@hourly"}`)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		stored, _ := srv.db.GetSchedule(created.ID)
		testutil.Equal(t, stored.Schedule, "@hourly")
		if stored.IsOneShot() {
			t.Fatal("expected one-shot anchor cleared after switch")
		}
	})

	t.Run("update with both cadences rejected", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		body := `{"schedule":"@daily","run_once_at":"` + future + `"}`
		req := authedReq("PUT", "/api/schedules/"+created.ID, body)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
		if !strings.Contains(w.Body.String(), "either") {
			t.Errorf("expected error mentioning 'either', got %s", w.Body.String())
		}
	})
}

// Regression: editing the schedule expression of a never-run schedule must
// not seed a year-0001 NextRunAt that would fire on the very next tick.
// See review-20260428.md BLOCKING #2.
func TestScheduleHandlers_UpdateScheduleAnchorOnNow(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

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
	handler := authMiddleware(srv.token, srv.db, nil, srv.routes())

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

// Single-tier auth: schedule endpoints are open to any authenticated token
// (no RCE/credential risk — not on the master-only denylist). A device token
// must NOT be rejected with 403; the exact success/not-found code varies per
// endpoint, so we only assert "not forbidden".
func TestScheduleHandlers_AnyToken(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())
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
			if w.Code == http.StatusForbidden {
				t.Fatalf("device token forbidden on %s %s; single-tier auth should allow it", tc.method, tc.url)
			}
		})
	}

	// Stronger positives: a device token must get concrete success codes, not
	// merely avoid a 403.
	t.Run("list returns 200 for device token", func(t *testing.T) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, device("GET", "/api/schedules", ""))
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("create persists via device token", func(t *testing.T) {
		body := `{"name":"dev-sched","project":"p","prompt":"go","schedule":"@daily"}`
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, device("POST", "/api/schedules", body))
		testutil.Equal(t, w.Code, http.StatusCreated)
		schedules, err := d.Schedules()
		testutil.NoError(t, err)
		found := false
		for _, sc := range schedules {
			if sc.Name == "dev-sched" {
				found = true
			}
		}
		testutil.Equal(t, found, true)
	})
}
