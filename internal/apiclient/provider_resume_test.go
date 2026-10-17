package apiclient

import (
	"net/http"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestProvider_TranscriptAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		status        int
		want, wantErr bool
	}{
		{"missing", `{"sessions":[{"id":"other"}]}`, 200, false, false},
		{"saved", `{"sessions":[{"id":"saved"}]}`, 200, true, false},
		{"lookup error", `{"error":"unavailable"}`, 500, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := newFixture(t)
			f.mux.HandleFunc("GET /api/tasks/task/claude-sessions", requireAuth(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, err := w.Write([]byte(tc.body))
				testutil.NoError(t, err)
			}))
			p := NewProvider(f.c)
			t.Cleanup(p.Close)
			got, err := p.ResumableSessionAvailable(&model.Task{ID: "task", Backend: "claude", SessionID: "saved"}, config.DefaultConfig())
			testutil.Equal(t, err != nil, tc.wantErr)
			testutil.Equal(t, got, tc.want)
		})
	}
	t.Run("families without probe do not query", func(t *testing.T) {
		f := newFixture(t) // no matching route: any request would error
		p := NewProvider(f.c)
		t.Cleanup(p.Close)
		got, err := p.ResumableSessionAvailable(&model.Task{Backend: "codex", SessionID: "saved"}, config.DefaultConfig())
		testutil.NoError(t, err)
		testutil.True(t, got)
	})
}

func TestProvider_StartKeepsRecoveredID(t *testing.T) {
	f := newFixture(t)
	f.mux.HandleFunc("POST /api/tasks/task/resume", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"status":"resumed","pid":123,"session_id":"recovered"}`))
		testutil.NoError(t, err)
	})
	p := NewProvider(f.c)
	t.Cleanup(p.Close)
	task := &model.Task{ID: "task", SessionID: "stale"}
	_, err := p.Start(task, config.Config{}, 24, 80, true)
	testutil.NoError(t, err)
	testutil.Equal(t, task.SessionID, "recovered")
}
