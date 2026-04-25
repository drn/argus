package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestGenerateToken(t *testing.T) {
	t.Run("generates hex token", func(t *testing.T) {
		tok, err := GenerateToken()
		testutil.NoError(t, err)
		testutil.Equal(t, len(tok), tokenBytes*2) // hex encoding doubles length
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		tok1, _ := GenerateToken()
		tok2, _ := GenerateToken()
		testutil.NotEqual(t, tok1, tok2)
	})
}

func TestLoadOrCreateToken(t *testing.T) {
	t.Run("creates new token file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "api-token")

		tok, err := LoadOrCreateToken(path)
		testutil.NoError(t, err)
		testutil.Equal(t, len(tok), tokenBytes*2)

		// File should exist with the token.
		data, err := os.ReadFile(path)
		testutil.NoError(t, err)
		testutil.Contains(t, string(data), tok)
	})

	t.Run("reads existing token", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "api-token")
		os.WriteFile(path, []byte("existing-token\n"), 0o600)

		tok, err := LoadOrCreateToken(path)
		testutil.NoError(t, err)
		testutil.Equal(t, tok, "existing-token")
	})

	t.Run("regenerates if file is empty", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "api-token")
		os.WriteFile(path, []byte(""), 0o600)

		tok, err := LoadOrCreateToken(path)
		testutil.NoError(t, err)
		testutil.Equal(t, len(tok), tokenBytes*2)
	})
}

func TestAuthMiddleware(t *testing.T) {
	token := "test-secret-token"
	handler := authMiddleware(token, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}), "/public")

	t.Run("accepts valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("rejects missing header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("rejects wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("rejects non-bearer auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("skips auth for skip paths", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/public", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("accepts ?token= query param when no Bearer header", func(t *testing.T) {
		// Required for EventSource which cannot set custom headers.
		req := httptest.NewRequest("GET", "/api/tasks/1/stream?token="+token, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("rejects wrong ?token= query param", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tasks/1/stream?token=wrong", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("Bearer header takes precedence over ?token=", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status?token=wrong", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("master token tags request with X-Argus-Auth=master", func(t *testing.T) {
		var seenAuth string
		h := authMiddleware(token, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seenAuth = r.Header.Get("X-Argus-Auth")
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(httptest.NewRecorder(), req)
		testutil.Equal(t, seenAuth, "master")
	})
}

func TestAuthMiddleware_DeviceToken(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { d.Close() }) //nolint:errcheck

	master := "master-secret"
	plain, _, err := MintToken(d, "iPhone")
	testutil.NoError(t, err)

	var seenAuth string
	handler := authMiddleware(master, d, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("X-Argus-Auth")
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("device token authenticates and is tagged X-Argus-Auth=device", func(t *testing.T) {
		seenAuth = ""
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+plain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, seenAuth, "device")
	})

	t.Run("device token via ?token= also works", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/tasks/1/stream?token="+plain, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})

	t.Run("revoked device token is rejected", func(t *testing.T) {
		_, id, err := MintToken(d, "burner")
		testutil.NoError(t, err)
		// Find the plaintext we just minted... we already returned it from MintToken
		// but didn't capture above. Re-mint and capture.
		burner, burnerID, err := MintToken(d, "burner2")
		testutil.NoError(t, err)
		_ = id

		// First confirm it works.
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+burner)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)

		// Revoke and confirm it stops working.
		testutil.NoError(t, d.RevokeAPIToken(burnerID))
		req2 := httptest.NewRequest("GET", "/api/status", nil)
		req2.Header.Set("Authorization", "Bearer "+burner)
		w2 := httptest.NewRecorder()
		handler.ServeHTTP(w2, req2)
		testutil.Equal(t, w2.Code, http.StatusUnauthorized)
	})

	t.Run("master + nil DB still works", func(t *testing.T) {
		h := authMiddleware(master, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+master)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})
}
