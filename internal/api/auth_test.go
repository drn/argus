package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/push"
	"github.com/drn/argus/internal/testutil"
)

// tlsState is a minimal placeholder used to flip r.TLS != nil in tests.
var tlsState = tls.ConnectionState{}

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
	handler := authMiddleware(token, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		h := authMiddleware(token, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := authMiddleware(master, d, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		h := authMiddleware(master, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+master)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
	})
}

func TestMintTokenWithScope(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	plain, id, err := MintTokenWithScope(d, "ludwig token", "ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, len(plain), tokenBytes*2)
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	got, err := d.FindAPITokenByHash(hashToken(plain))
	testutil.NoError(t, err)
	if got == nil {
		t.Fatal("expected token round-trip")
	}
	testutil.Equal(t, got.Scope, "ludwig")
	testutil.Equal(t, got.Label, "ludwig token")
}

func TestAuthMiddleware_PluginScopeToken(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	master := "master-secret"
	pluginPlain, _, err := MintTokenWithScope(d, "ludwig", "ludwig")
	testutil.NoError(t, err)
	devicePlain, _, err := MintToken(d, "iPhone")
	testutil.NoError(t, err)

	var seenAuth string
	handler := authMiddleware(master, d, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("X-Argus-Auth")
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("plugin token tags X-Argus-Auth=scope:<name>", func(t *testing.T) {
		seenAuth = ""
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+pluginPlain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, seenAuth, "scope:ludwig")
	})

	t.Run("device token still tags X-Argus-Auth=device", func(t *testing.T) {
		seenAuth = ""
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+devicePlain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, seenAuth, "device")
	})

	t.Run("master token still tags X-Argus-Auth=master", func(t *testing.T) {
		seenAuth = ""
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+master)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, seenAuth, "master")
	})

	t.Run("revoked plugin token rejected", func(t *testing.T) {
		burnerPlain, burnerID, err := MintTokenWithScope(d, "burner", "burner-plugin")
		testutil.NoError(t, err)
		testutil.NoError(t, d.RevokeAPIToken(burnerID))

		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+burnerPlain)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("requireMaster rejects plugin scope token", func(t *testing.T) {
		// Plugin tokens must not be able to call master-only endpoints
		// (e.g. minting more tokens, revoking other tokens). The scope
		// tag must not be aliased to master.
		var allowed bool
		h := authMiddleware(master, d, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requireMaster(w, r) {
				return
			}
			allowed = true
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest("POST", "/api/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+pluginPlain)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusForbidden)
		if allowed {
			t.Error("plugin token must not satisfy requireMaster")
		}
	})
}

// TestAuthOrigin pins the contract of authOrigin: it returns the X-Argus-Auth
// header value populated by authMiddleware. Audit log call sites read this to
// stamp the originating principal (master, device, or scope:<plugin>) on
// mutating events. An empty return must mean "unauthenticated" — defensive,
// because routes mounted behind authMiddleware should always have the header
// set, but the helper still needs to behave on raw requests built in tests.
func TestAuthOrigin(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{name: "master", header: "master", want: "master"},
		{name: "device", header: "device", want: "device"},
		{name: "plugin scope", header: "scope:ludwig", want: "scope:ludwig"},
		{name: "scope with dash", header: "scope:my-plugin", want: "scope:my-plugin"},
		{name: "no header", header: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/x", nil)
			if tc.header != "" {
				req.Header.Set("X-Argus-Auth", tc.header)
			}
			testutil.Equal(t, authOrigin(req), tc.want)
		})
	}
}

func TestVAPIDOriginFromRequest(t *testing.T) {
	cases := []struct {
		name       string
		origin     string
		host       string
		xfp        string
		remoteAddr string
		tlsOK      bool
		want       string
	}{
		{name: "https origin header", origin: "https://host.tailnet.ts.net", want: "https://host.tailnet.ts.net"},
		{name: "https origin with path stripped", origin: "https://host.tailnet.ts.net/api/x", want: "https://host.tailnet.ts.net"},
		{name: "https origin with userinfo stripped", origin: "https://user:pass@host.tailnet.ts.net/api", want: "https://host.tailnet.ts.net"},
		{name: "http origin rejected", origin: "http://host.lan", want: ""},
		{name: "xforwarded https from loopback accepted", host: "host.tailnet.ts.net", xfp: "https", remoteAddr: "127.0.0.1:51234", want: "https://host.tailnet.ts.net"},
		{name: "xforwarded https from remote rejected", host: "host.lan", xfp: "https", remoteAddr: "192.168.1.5:51234", want: ""},
		{name: "fallback to TLS", host: "host.tailnet.ts.net", tlsOK: true, want: "https://host.tailnet.ts.net"},
		{name: "plain http rejected", host: "host.lan", want: ""},
		{name: "no host", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/x", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.host != "" {
				req.Host = tc.host
			} else {
				req.Host = ""
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			if tc.remoteAddr != "" {
				req.RemoteAddr = tc.remoteAddr
			}
			if tc.tlsOK {
				req.TLS = &tlsState
			}
			testutil.Equal(t, vapidOriginFromRequest(req), tc.want)
		})
	}
}

func TestRecordVAPIDOrigin_UpdatesManager(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	pm, err := push.New(d)
	testutil.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Origin", "https://host.tailnet.ts.net")
	recordVAPIDOrigin(pm, req)

	testutil.Equal(t, pm.Subject(), "https://host.tailnet.ts.net")
}

func TestRecordVAPIDOrigin_NilManagerSafe(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("Origin", "https://host.tailnet.ts.net")
	recordVAPIDOrigin(nil, req) // must not panic
}

// TestAuthMiddleware_SetsVAPIDSubject pins the wiring that makes the bug
// fix work end-to-end: an authenticated request through the real
// authMiddleware must update the push manager's subject. Without this,
// re-ordering recordVAPIDOrigin out of the auth path (or above the auth
// gate) would silently regress without a single existing test failing.
func TestAuthMiddleware_SetsVAPIDSubject(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	pm, err := push.New(d)
	testutil.NoError(t, err)

	const tok = "test-master-token"
	handler := authMiddleware(tok, d, pm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("authed request sets subject", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Origin", "https://host.tailnet.ts.net")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusOK)
		testutil.Equal(t, pm.Subject(), "https://host.tailnet.ts.net")
	})

	t.Run("unauthed request does not set subject", func(t *testing.T) {
		// Reset by setting a known sentinel via a successful authed call
		// would just confirm the previous subtest's outcome. Easier: open a
		// fresh manager on a fresh DB and confirm the unauthed path leaves
		// it empty.
		d2, err := db.OpenInMemory()
		testutil.NoError(t, err)
		t.Cleanup(func() { _ = d2.Close() })
		pm2, err := push.New(d2)
		testutil.NoError(t, err)
		h := authMiddleware(tok, d2, pm2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Header.Set("Origin", "https://attacker.example") // no Authorization
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusUnauthorized)
		testutil.Equal(t, pm2.Subject(), "")
	})
}
