package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// regDB returns an in-memory db.DB suitable as a RegistryStore.
func regDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// withNow swaps Registry.now for the duration of a test so heartbeat
// assertions are deterministic.
func withNow(r *Registry, now time.Time) {
	r.now = func() time.Time { return now }
}

func TestRegistry_Register_NamespaceEnforcement(t *testing.T) {
	r := NewRegistry(regDB(t))

	t.Run("rejects name without scope prefix", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "decision_add",
			Description: "x",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "scope prefix")
	})

	t.Run("rejects bare scope_ name", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
		})
		testutil.Error(t, err)
	})

	t.Run("rejects empty name", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
		})
		testutil.Error(t, err)
	})

	t.Run("rejects name with invalid chars", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_bad name",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "invalid character")
	})

	t.Run("rejects empty scope", func(t *testing.T) {
		err := r.Register("", ToolRegistration{
			Name:        "ludwig_decision_add",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
		})
		testutil.Error(t, err)
	})

	t.Run("accepts scoped name", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_decision_add",
			Description: "Record a decision.",
			InputSchema: json.RawMessage(`{"type":"object"}`),
			CallbackURL: "http://127.0.0.1:9000/cb",
			AuthHeader:  "Bearer s",
		})
		testutil.NoError(t, err)

		got, err := r.Get("ludwig_decision_add")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil tool")
		}
		testutil.Equal(t, got.Scope, "ludwig")
	})

	t.Run("rejects callback_url not http(s)", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_decision_add",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "file:///etc/passwd",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "callback_url")
	})

	t.Run("rejects invalid JSON in input schema", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_bad_json",
			InputSchema: json.RawMessage(`{"unclosed":`),
			CallbackURL: "http://x",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "input_schema must be valid JSON")
	})

	t.Run("defaults missing input schema to {}", func(t *testing.T) {
		testutil.NoError(t, r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_no_schema",
			CallbackURL: "http://127.0.0.1/cb",
		}))
		got, _ := r.Get("ludwig_no_schema")
		testutil.Equal(t, string(got.InputSchema), "{}")
	})

	t.Run("rejects oversized description", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_too_chatty",
			Description: strings.Repeat("a", MaxToolDescriptionBytes+1),
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://x",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "description")
	})

	t.Run("rejects oversized callback_url", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_long_url",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://" + strings.Repeat("a", MaxCallbackURLBytes),
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "callback_url")
	})

	t.Run("rejects oversized auth_header", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_huge_auth",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://x",
			AuthHeader:  strings.Repeat("b", MaxAuthHeaderBytes+1),
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "auth_header")
	})

	t.Run("rejects oversized name", func(t *testing.T) {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_" + strings.Repeat("x", MaxToolNameBytes),
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://x",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "exceeds")
	})

	t.Run("rejects oversized input schema", func(t *testing.T) {
		big := strings.Repeat("a", MaxInputSchemaBytes+10)
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_big",
			InputSchema: json.RawMessage(`"` + big + `"`),
			CallbackURL: "http://127.0.0.1/cb",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "input_schema")
	})

	t.Run("rejects cross-scope name collision when prefixes overlap", func(t *testing.T) {
		// validateScopedName rules out the easy mismatch case (scope=ludwig
		// trying to claim "alpha_one"). The cross-scope guard is the
		// belt-and-suspenders for the awkward case where two scopes share an
		// underscore-overlapping prefix: scope "abc" registers "abc_x_one",
		// then scope "abc_x" tries to claim the same name (both pass the
		// name-starts-with-scope_ check). The guard catches it.
		testutil.NoError(t, r.Register("abc", ToolRegistration{
			Name:        "abc_x_one",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://x",
		}))
		err := r.Register("abc_x", ToolRegistration{
			Name:        "abc_x_one",
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://x",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "already registered by scope")
	})
}

func TestRegistry_Register_HeartbeatRefreshesLastSeen(t *testing.T) {
	r := NewRegistry(regDB(t))
	t0 := time.Unix(1700000000, 0).UTC()
	t1 := time.Unix(1700000300, 0).UTC()

	withNow(r, t0)
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name:        "ludwig_one",
		Description: "v1",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb",
	}))

	withNow(r, t1)
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name:        "ludwig_one",
		Description: "v2",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb",
	}))

	got, err := r.Get("ludwig_one")
	testutil.NoError(t, err)
	// RegisteredAt should be sticky.
	testutil.Equal(t, got.RegisteredAt.Unix(), t0.Unix())
	// LastSeenAt should refresh.
	testutil.Equal(t, got.LastSeenAt.Unix(), t1.Unix())
	// Description updated.
	testutil.Equal(t, got.Description, "v2")
}

func TestRegistry_Register_MaxToolsPerScope(t *testing.T) {
	r := NewRegistry(regDB(t))
	for i := 0; i < MaxToolsPerScope; i++ {
		err := r.Register("ludwig", ToolRegistration{
			Name:        "ludwig_t" + intToStr(i),
			InputSchema: json.RawMessage(`{}`),
			CallbackURL: "http://127.0.0.1/cb",
		})
		testutil.NoError(t, err)
	}

	// The +1th distinct name should be rejected.
	err := r.Register("ludwig", ToolRegistration{
		Name:        "ludwig_overflow",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb",
	})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "tool limit")

	// But a re-registration of an existing name (heartbeat) MUST still work.
	err = r.Register("ludwig", ToolRegistration{
		Name:        "ludwig_t0",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb-new",
	})
	testutil.NoError(t, err)
}

func intToStr(i int) string {
	// Tiny helper used only in TestRegistry_Register_MaxToolsPerScope.
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}

func TestRegistry_Unregister(t *testing.T) {
	t.Run("scope-owned removal", func(t *testing.T) {
		r := NewRegistry(regDB(t))
		testutil.NoError(t, r.Register("ludwig", ToolRegistration{
			Name: "ludwig_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
		}))
		err := r.Unregister("ludwig", "ludwig_one")
		testutil.NoError(t, err)
		got, _ := r.Get("ludwig_one")
		if got != nil {
			t.Fatal("expected tool to be gone")
		}
	})

	t.Run("foreign scope rejected", func(t *testing.T) {
		r := NewRegistry(regDB(t))
		testutil.NoError(t, r.Register("ludwig", ToolRegistration{
			Name: "ludwig_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
		}))
		err := r.Unregister("alpha", "ludwig_one")
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "not owned")
		// Tool still present.
		got, _ := r.Get("ludwig_one")
		if got == nil {
			t.Fatal("expected tool to remain")
		}
	})

	t.Run("empty scope is master and may remove anything", func(t *testing.T) {
		r := NewRegistry(regDB(t))
		testutil.NoError(t, r.Register("ludwig", ToolRegistration{
			Name: "ludwig_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
		}))
		err := r.Unregister("", "ludwig_one")
		testutil.NoError(t, err)
	})

	t.Run("idempotent on missing name", func(t *testing.T) {
		r := NewRegistry(regDB(t))
		testutil.NoError(t, r.Unregister("ludwig", "ludwig_missing"))
	})
}

func TestRegistry_UnregisterScope(t *testing.T) {
	r := NewRegistry(regDB(t))
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{Name: "ludwig_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x"}))
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{Name: "ludwig_two", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x"}))
	testutil.NoError(t, r.Register("alpha", ToolRegistration{Name: "alpha_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x"}))

	n, err := r.UnregisterScope("ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	tools, err := r.List()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tools), 1)
	testutil.Equal(t, tools[0].Name, "alpha_one")
}

func TestRegistry_SweepIdle(t *testing.T) {
	r := NewRegistry(regDB(t))
	t0 := time.Unix(1700000000, 0).UTC()
	tFresh := time.Unix(1700001000, 0).UTC()

	withNow(r, t0)
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name: "ludwig_stale", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
	}))
	withNow(r, tFresh)
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name: "ludwig_fresh", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
	}))

	// Cutoff "now" at tFresh; idle window 500s. Anything older than tFresh-500
	// = t-500 should fall. ludwig_stale's LastSeenAt = t0 (older), so it falls.
	withNow(r, tFresh)
	removed, err := r.SweepIdle(500 * time.Second)
	testutil.NoError(t, err)
	testutil.Equal(t, len(removed), 1)
	testutil.Equal(t, removed[0].Name, "ludwig_stale")

	remaining, _ := r.List()
	testutil.Equal(t, len(remaining), 1)
}

func TestRegistry_Invoke(t *testing.T) {
	r := NewRegistry(regDB(t))
	t0 := time.Unix(1700000000, 0).UTC()
	withNow(r, t0)

	var lastBody string
	var lastAuth string
	var calls int32
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		atomic.AddInt32(&calls, 1)
		body, _ := io.ReadAll(req.Body)
		lastBody = string(body)
		lastAuth = req.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"isError":false}`))
	}))
	defer plugin.Close()

	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name:        "ludwig_decision_add",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: plugin.URL,
		AuthHeader:  "Bearer plugin-secret",
	}))

	tHeartbeat := time.Unix(1700000300, 0).UTC()
	withNow(r, tHeartbeat)
	result, err := r.Invoke(context.Background(), "ludwig_decision_add", json.RawMessage(`{"x":1}`), CallerContext{TaskID: "T", SessionID: "S"})
	testutil.NoError(t, err)
	testutil.Equal(t, result.IsError, false)
	testutil.Equal(t, len(result.Content), 1)
	testutil.Equal(t, result.Content[0].Text, "ok")
	testutil.Equal(t, atomic.LoadInt32(&calls), int32(1))

	testutil.Contains(t, lastBody, `"tool":"ludwig_decision_add"`)
	testutil.Contains(t, lastBody, `"input":{"x":1}`)
	testutil.Contains(t, lastBody, `"task_id":"T"`)
	testutil.Equal(t, lastAuth, "Bearer plugin-secret")

	// Heartbeat: invoke refreshed last_seen_at to tHeartbeat.
	got, err := r.Get("ludwig_decision_add")
	testutil.NoError(t, err)
	testutil.Equal(t, got.LastSeenAt.Unix(), tHeartbeat.Unix())
}

func TestRegistry_Invoke_Unknown(t *testing.T) {
	r := NewRegistry(regDB(t))
	_, err := r.Invoke(context.Background(), "ludwig_missing", json.RawMessage(`{}`), CallerContext{})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "not registered")
}

func TestRegistry_Invoke_HTTPError(t *testing.T) {
	r := NewRegistry(regDB(t))
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer plugin.Close()
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name: "ludwig_boom", InputSchema: json.RawMessage(`{}`), CallbackURL: plugin.URL,
	}))
	_, err := r.Invoke(context.Background(), "ludwig_boom", json.RawMessage(`{}`), CallerContext{})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "plugin returned 500")
}

func TestRegistry_Invoke_TransportError(t *testing.T) {
	r := NewRegistry(regDB(t))
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		// Pick a TCP port nothing should be listening on so Dial fails fast.
		Name: "ludwig_dead", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://127.0.0.1:1/cb",
	}))
	_, err := r.Invoke(context.Background(), "ludwig_dead", json.RawMessage(`{}`), CallerContext{})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "plugin invoke")
}

func TestRegistry_Invoke_BadResponse(t *testing.T) {
	r := NewRegistry(regDB(t))
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer plugin.Close()
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name: "ludwig_badjson", InputSchema: json.RawMessage(`{}`), CallbackURL: plugin.URL,
	}))
	_, err := r.Invoke(context.Background(), "ludwig_badjson", json.RawMessage(`{}`), CallerContext{})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "decode plugin response")
}

func TestRegistry_Invoke_DefaultsEmptyInput(t *testing.T) {
	r := NewRegistry(regDB(t))
	var captured []byte
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		captured, _ = io.ReadAll(req.Body)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer plugin.Close()
	testutil.NoError(t, r.Register("ludwig", ToolRegistration{
		Name: "ludwig_empty", InputSchema: json.RawMessage(`{}`), CallbackURL: plugin.URL,
	}))
	_, err := r.Invoke(context.Background(), "ludwig_empty", nil, CallerContext{})
	testutil.NoError(t, err)
	// Empty caller input must be normalized to "{}" so plugins always see
	// a well-formed object on the wire.
	testutil.Contains(t, string(captured), `"input":{}`)
}

func TestRegistry_List_Empty(t *testing.T) {
	r := NewRegistry(regDB(t))
	tools, err := r.List()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tools), 0)
}

// stubStore exercises error paths the *db.DB happy-path won't trigger.
type stubStore struct {
	upsertErr       error
	upsertCalls     int
	listErr         error
	listTools       []*db.PluginMCPTool
	getErr          error
	getTool         *db.PluginMCPTool
	delErr          error
	delIdleErr      error
	delScopeErr     error
	delScopeRemoved int
}

func (s *stubStore) UpsertPluginMCPTool(t *db.PluginMCPTool) error {
	s.upsertCalls++
	return s.upsertErr
}
func (s *stubStore) DeletePluginMCPTool(name string) (bool, error) {
	if s.delErr != nil {
		return false, s.delErr
	}
	return true, nil
}
func (s *stubStore) DeletePluginMCPToolsByScope(scope string) (int, error) {
	return s.delScopeRemoved, s.delScopeErr
}
func (s *stubStore) PluginMCPTools() ([]*db.PluginMCPTool, error) {
	return s.listTools, s.listErr
}
func (s *stubStore) GetPluginMCPTool(name string) (*db.PluginMCPTool, error) {
	return s.getTool, s.getErr
}
func (s *stubStore) DeletePluginMCPToolsIdle(cutoff time.Time) ([]*db.PluginMCPTool, error) {
	return nil, s.delIdleErr
}

func TestRegistry_Errors_StorePropagation(t *testing.T) {
	t.Run("list error surfaces from Register", func(t *testing.T) {
		s := &stubStore{listErr: errors.New("boom")}
		r := NewRegistry(s)
		err := r.Register("ludwig", ToolRegistration{
			Name: "ludwig_one", InputSchema: json.RawMessage(`{}`), CallbackURL: "http://x",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "boom")
	})

	t.Run("get error surfaces from Unregister", func(t *testing.T) {
		s := &stubStore{getErr: errors.New("get-boom")}
		r := NewRegistry(s)
		err := r.Unregister("ludwig", "ludwig_one")
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "get-boom")
	})

	t.Run("idle sweep error surfaces", func(t *testing.T) {
		s := &stubStore{delIdleErr: errors.New("idle-boom")}
		r := NewRegistry(s)
		_, err := r.SweepIdle(time.Minute)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "idle-boom")
	})

	t.Run("scope sweep error surfaces", func(t *testing.T) {
		s := &stubStore{delScopeErr: errors.New("scope-boom")}
		r := NewRegistry(s)
		_, err := r.UnregisterScope("ludwig")
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "scope-boom")
	})
}
