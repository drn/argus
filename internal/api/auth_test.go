package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
}
