package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/voicetask"
)

type stubVoiceClassifier struct {
	project string
	prob    float64
	err     error
}

func (c stubVoiceClassifier) Classify(context.Context, string, []string) (string, float64, error) {
	return c.project, c.prob, c.err
}

func stubVoiceJev(t *testing.T, cls voicetask.Classifier) {
	t.Helper()
	prev := newVoiceLauncher
	newVoiceLauncher = func(cfg config.Config, projects func() ([]string, error), create voicetask.CreateFunc, stamp func(string) error, sanitize func(string) string) *voicetask.Launcher {
		l := prev(cfg, projects, create, stamp, sanitize)
		l.Classifier = cls
		return l
	}
	t.Cleanup(func() { newVoiceLauncher = prev })
}

func voiceServer(t *testing.T, defaultProject string) (*Server, *db.DB) {
	t.Helper()
	srv, d := testServer(t)
	testutil.NoError(t, d.SetProject("forge", config.Project{Path: t.TempDir()}))
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: t.TempDir()}))
	if defaultProject != "" {
		testutil.NoError(t, d.SetConfigValue("defaults.voice_project", defaultProject))
	}
	return srv, d
}

func postVoice(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("POST", "/api/voice-tasks", body))
	return w
}

func voiceMeta(t *testing.T, d *db.DB, id string) map[string]string {
	t.Helper()
	entries, err := d.ListMeta(id, db.UIMetaNamespace)
	testutil.NoError(t, err)
	m := map[string]string{}
	for _, e := range entries {
		m[e.Key] = e.Value
	}
	return m
}

func TestHandleCreateVoiceTask(t *testing.T) {
	t.Run("spoken project", func(t *testing.T) {
		srv, d := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"fix the login bug in project argus"}`)
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["project"], "argus")
		testutil.Equal(t, resp["project_source"], "spoken")
		testutil.Equal(t, resp["prompt"], "fix the login bug")
		testutil.Equal(t, resp["status"], "in_progress")
		_, has := resp["spoken_project"]
		testutil.Equal(t, has, false)
		m := voiceMeta(t, d, resp["id"].(string))
		testutil.Equal(t, m[db.UIMetaKeyNew], "true")
		testutil.Equal(t, m[db.UIMetaKeySource], "voice")
	})

	t.Run("default project", func(t *testing.T) {
		srv, d := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"  add a retry to the poller "}`)
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["project"], "forge")
		testutil.Equal(t, resp["project_source"], "default")
		testutil.Equal(t, resp["prompt"], "add a retry to the poller")
		testutil.Equal(t, voiceMeta(t, d, resp["id"].(string))[db.UIMetaKeyNew], "true")
	})

	t.Run("unknown spoken project falls back to default", func(t *testing.T) {
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"do the thing in project zzzzzz"}`)
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["project"], "forge")
		testutil.Equal(t, resp["project_source"], "default")
		testutil.Equal(t, resp["spoken_project"], "zzzzzz")
	})

	t.Run("jev project", func(t *testing.T) {
		stubVoiceJev(t, stubVoiceClassifier{project: "argus", prob: 0.95})
		srv, d := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"tighten the tab bar spacing"}`)
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["project"], "argus")
		testutil.Equal(t, resp["project_source"], "jev")
		testutil.Equal(t, resp["prompt"], "tighten the tab bar spacing")
		testutil.Equal(t, voiceMeta(t, d, resp["id"].(string))[db.UIMetaKeySource], "voice")
	})

	t.Run("jev low confidence falls back to default", func(t *testing.T) {
		stubVoiceJev(t, stubVoiceClassifier{project: "argus", prob: 0.3})
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"tighten the tab bar spacing"}`)
		testutil.Equal(t, w.Code, http.StatusCreated)
		var resp map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		testutil.Equal(t, resp["project"], "forge")
		testutil.Equal(t, resp["project_source"], "default")
	})

	t.Run("jev error with no default", func(t *testing.T) {
		stubVoiceJev(t, stubVoiceClassifier{err: fmt.Errorf("down")})
		srv, _ := voiceServer(t, "")
		w := postVoice(t, srv, `{"transcript":"do the thing"}`)
		testutil.Equal(t, w.Code, http.StatusUnprocessableEntity)
	})

	t.Run("no project", func(t *testing.T) {
		srv, _ := voiceServer(t, "")
		w := postVoice(t, srv, `{"transcript":"do the thing"}`)
		testutil.Equal(t, w.Code, http.StatusUnprocessableEntity)
	})

	t.Run("empty transcript", func(t *testing.T) {
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"   "}`)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("empty prompt after project removal", func(t *testing.T) {
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"in project argus"}`)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("projects load error", func(t *testing.T) {
		srv, d := voiceServer(t, "forge")
		testutil.NoError(t, d.Close())
		w := postVoice(t, srv, `{"transcript":"do it"}`)
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `not json`)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("bad backend", func(t *testing.T) {
		srv, _ := voiceServer(t, "forge")
		w := postVoice(t, srv, `{"transcript":"do it","backend":"nope"}`)
		testutil.Equal(t, w.Code, http.StatusBadRequest)
	})

	t.Run("creator errors", func(t *testing.T) {
		tests := []struct {
			name string
			err  error
			want int
		}{
			{"generic", fmt.Errorf("forced fail"), http.StatusInternalServerError},
			{"account", fmt.Errorf("%w: bad", agent.ErrAccount), http.StatusBadRequest},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				d, err := db.OpenInMemory()
				testutil.NoError(t, err)
				t.Cleanup(func() { _ = d.Close() })
				testutil.NoError(t, d.SetProject("forge", config.Project{Path: t.TempDir()}))
				testutil.NoError(t, d.SetConfigValue("defaults.voice_project", "forge"))
				creator := func(name, prompt, project, backend, taskModel, sandboxOverride, account string, _ bool) (*model.Task, error) {
					return nil, tt.err
				}
				srv := New(d, agent.NewRunner(nil), "test-token", creator, nil)
				w := postVoice(t, srv, `{"transcript":"do it"}`)
				testutil.Equal(t, w.Code, tt.want)
			})
		}
	})
}
