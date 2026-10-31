package voicetask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

const testJevKey = "test-placeholder-key"

type jevCapture struct {
	auth        string
	contentType string
	body        map[string]any
}

func jevServer(t *testing.T, status int, resp string) (*httptest.Server, *jevCapture) {
	t.Helper()
	c := &jevCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.auth = r.Header.Get("Authorization")
		c.contentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &c.body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func TestJevClassifier_Request(t *testing.T) {
	t.Run("body shape and auth", func(t *testing.T) {
		srv, c := jevServer(t, 200, `{"answers":{"project":{"type":"choice","choice":"argus","probabilities":{"argus":0.93,"forge":0.05,"none":0.02},"confidence":0.9}}}`)
		cl := NewJevClassifier(config.JevConfig{Endpoint: srv.URL, Model: "typesafe/jev-1.13"}, testJevKey, srv.Client())

		project, prob, err := cl.Classify(context.Background(), "fix the tab bar", []string{"argus", "forge"})
		testutil.NoError(t, err)
		testutil.Equal(t, project, "argus")
		testutil.Equal(t, prob, 0.93)

		testutil.Equal(t, c.auth, "Bearer "+testJevKey)
		testutil.Equal(t, c.contentType, "application/json")
		testutil.Equal(t, c.body["model"], any("typesafe/jev-1.13"))
		testutil.Equal(t, c.body["state"], any("fix the tab bar"))
		q := c.body["questions"].(map[string]any)["project"].(map[string]any)
		testutil.Equal(t, q["type"], any("choice"))
		testutil.True(t, q["instructions"] != "")
		testutil.DeepEqual(t, q["criteria"], any(map[string]any{
			"argus": `Software project "argus"`,
			"forge": `Software project "forge"`,
			"none":  "The request does not clearly name or describe any listed project",
		}))
	})

	t.Run("model omitted when empty", func(t *testing.T) {
		srv, c := jevServer(t, 200, `{"answers":{"project":{"choice":"none"}}}`)
		cl := NewJevClassifier(config.JevConfig{Endpoint: srv.URL}, testJevKey, srv.Client())
		_, _, err := cl.Classify(context.Background(), "x", []string{"argus"})
		testutil.NoError(t, err)
		_, has := c.body["model"]
		testutil.False(t, has)
	})

	t.Run("defaults endpoint and client", func(t *testing.T) {
		cl := NewJevClassifier(config.JevConfig{}, testJevKey, nil).(*jevClassifier)
		testutil.Equal(t, cl.endpoint, config.DefaultJevEndpoint)
		testutil.NotNil(t, cl.hc)
	})
}

func TestJevClassifier_Responses(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		resp     string
		wantProj string
		wantProb float64
		wantErr  string
	}{
		{name: "none choice", status: 200, resp: `{"answers":{"project":{"choice":"none","probabilities":{"none":0.9}}}}`},
		{name: "missing probability", status: 200, resp: `{"answers":{"project":{"choice":"forge"}}}`, wantProj: "forge"},
		{name: "non-2xx", status: 401, resp: `{"error":"bad key"}`, wantErr: "unexpected status 401"},
		{name: "server error", status: 500, resp: ``, wantErr: "unexpected status 500"},
		{name: "malformed", status: 200, resp: `{not json`, wantErr: "malformed response"},
		{name: "missing answer", status: 200, resp: `{"answers":{}}`, wantErr: "missing project answer"},
		{name: "empty choice", status: 200, resp: `{"answers":{"project":{"choice":""}}}`, wantErr: "missing project answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := jevServer(t, tt.status, tt.resp)
			cl := NewJevClassifier(config.JevConfig{Endpoint: srv.URL}, testJevKey, srv.Client())
			project, prob, err := cl.Classify(context.Background(), "x", []string{"argus", "forge"})
			if tt.wantErr != "" {
				testutil.Contains(t, err.Error(), tt.wantErr)
				testutil.False(t, strings.Contains(err.Error(), testJevKey))
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, project, tt.wantProj)
			testutil.Equal(t, prob, tt.wantProb)
		})
	}
}

func TestJevClassifier_TransportErrors(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		cl := NewJevClassifier(config.JevConfig{Endpoint: url}, testJevKey, nil)
		_, _, err := cl.Classify(context.Background(), "x", []string{"argus"})
		testutil.Contains(t, err.Error(), "request failed")
		testutil.False(t, strings.Contains(err.Error(), testJevKey))
	})

	t.Run("bad endpoint", func(t *testing.T) {
		cl := NewJevClassifier(config.JevConfig{Endpoint: "://bad"}, testJevKey, nil)
		_, _, err := cl.Classify(context.Background(), "x", []string{"argus"})
		testutil.Contains(t, err.Error(), "build request")
	})

	t.Run("cancelled context", func(t *testing.T) {
		srv, _ := jevServer(t, 200, `{}`)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cl := NewJevClassifier(config.JevConfig{Endpoint: srv.URL}, testJevKey, srv.Client())
		_, _, err := cl.Classify(ctx, "x", []string{"argus"})
		testutil.Contains(t, err.Error(), "request failed")
	})
}
