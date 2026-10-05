package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/drn/argus/internal/claudeaccount"
	"github.com/drn/argus/internal/testutil"
)

func stubCodexLogin(t *testing.T, fn func(ctx context.Context, source string) (bool, error)) {
	t.Helper()
	old := codexLoginStatus
	codexLoginStatus = fn
	t.Cleanup(func() { codexLoginStatus = old })
}

func TestHandleListAccounts_CodexLoggedIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stubAuth(t, func(context.Context, string) (claudeaccount.Status, error) {
		return claudeaccount.Status{LoggedIn: true}, nil
	})
	var mu sync.Mutex
	var sources []string
	stubCodexLogin(t, func(_ context.Context, source string) (bool, error) {
		mu.Lock()
		sources = append(sources, source)
		mu.Unlock()
		switch source {
		case "":
			return true, nil
		case "/tmp/argus-test-codexonly":
			return false, nil
		}
		return false, errors.New("boom")
	})
	srv, _ := accountsServer(t, twoAccountsTOML)
	w := httptest.NewRecorder()
	srv.routes().ServeHTTP(w, authedReq("GET", "/api/accounts", ""))
	testutil.Equal(t, w.Code, http.StatusOK)

	var got []accountJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	byName := map[string]accountJSON{}
	for _, a := range got {
		byName[a.Name] = a
	}

	t.Run("default probes the default codex home", func(t *testing.T) {
		v := byName["default"].CodexLoggedIn
		testutil.True(t, v != nil && *v)
	})
	t.Run("codex-only account reports logged out", func(t *testing.T) {
		v := byName["codexonly"].CodexLoggedIn
		testutil.True(t, v != nil && !*v)
		testutil.False(t, byName["codexonly"].LoggedIn)
	})
	t.Run("failed lookup stays unknown", func(t *testing.T) {
		testutil.Nil(t, byName["personal"].CodexLoggedIn)
	})
	t.Run("claude-only account is never probed", func(t *testing.T) {
		testutil.Nil(t, byName["work"].CodexLoggedIn)
		for _, s := range sources {
			testutil.False(t, s == "/tmp/argus-test-work")
		}
	})
	t.Run("unknown is omitted from the wire", func(t *testing.T) {
		var raw []map[string]any
		testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		for _, a := range raw {
			_, has := a["codex_logged_in"]
			testutil.Equal(t, has, a["name"] == "default" || a["name"] == "codexonly")
		}
		testutil.False(t, strings.Contains(w.Body.String(), "auth.json"))
	})
}
