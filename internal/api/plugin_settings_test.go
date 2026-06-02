package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/settings"
)

// readAll reads the request body in one shot for table-driven tests.
func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close() //nolint:errcheck
	return io.ReadAll(r.Body)
}

const validSectionBody = `{
	"title": "Hello",
	"type": "form",
	"callback_url": "http://127.0.0.1:9001/save",
	"fields": [{"key":"k","label":"l","type":"bool","default":false}]
}`

// pluginReq builds a request tagged with X-Argus-Auth=scope:<scope> so the
// handler picks it up as a plugin-scoped registration.
func pluginReq(method, url, scope, body string) *http.Request {
	req := authedReq(method, url, body)
	req.Header.Set("X-Argus-Auth", "scope:"+scope)
	return req
}

func TestAPI_RegisterPluginSection_Created(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", validSectionBody))
	testutil.Equal(t, w.Code, http.StatusCreated)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, resp["scope"].(string), "test-plugin")
	testutil.Equal(t, resp["title"].(string), "Hello")

	// DB row exists.
	rows, err := d.ListPluginSections()
	testutil.NoError(t, err)
	testutil.Equal(t, len(rows), 1)
	testutil.Equal(t, rows[0].Scope, "test-plugin")

	// In-memory mirror reflects it too.
	if got, ok := srv.pluginSections.Get("test-plugin", "Hello"); !ok || got.Title != "Hello" {
		t.Fatalf("registry mirror missing section, got=%+v ok=%v", got, ok)
	}
}

func TestAPI_RegisterPluginSection_ReplacementReturns200(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", validSectionBody))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", validSectionBody))
	testutil.Equal(t, w.Code, http.StatusOK)
}

func TestAPI_RegisterPluginSection_RequiresPluginScope(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections", validSectionBody))
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestAPI_RegisterPluginSection_BadBody(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	// Bad JSON.
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", "{not json"))
	testutil.Equal(t, w.Code, http.StatusBadRequest)

	// Missing required fields.
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", `{"title":"x"}`))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestAPI_RegisterPluginSection_AcceptsStreamType(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	w := httptest.NewRecorder()
	body := `{"title":"Live orchestrators","type":"stream","callback_url":"ws://127.0.0.1:9991/live"}`
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "ludwig", body))
	testutil.Equal(t, w.Code, http.StatusCreated)
}

func TestAPI_RegisterPluginSection_RejectsStreamWithFields(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	w := httptest.NewRecorder()
	body := `{"title":"x","type":"stream","callback_url":"ws://x","fields":[{"key":"k","label":"l","type":"bool"}]}`
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "test-plugin", body))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestAPI_ListPluginSections(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "alpha", validSectionBody))
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "bravo", validSectionBody))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("GET", "/api/plugins/settings/sections", ""))
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Sections []pluginSectionJSON `json:"sections"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Sections), 2)
	for _, sec := range resp.Sections {
		testutil.Equal(t, sec.Title, "Hello")
		testutil.Equal(t, len(sec.Fields), 1)
	}
}

func TestAPI_UnregisterPluginSection_OwnerCanRemove(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("DELETE", "/api/plugins/settings/sections/owner/Hello", "owner", ""))
	testutil.Equal(t, w.Code, http.StatusOK)

	if _, ok := srv.pluginSections.Get("owner", "Hello"); ok {
		t.Fatal("expected section removed from mirror")
	}
}

func TestAPI_UnregisterPluginSection_OtherPluginForbidden(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("DELETE", "/api/plugins/settings/sections/owner/Hello", "intruder", ""))
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestAPI_UnregisterPluginSection_MasterCanRemove(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	req := authedReq("DELETE", "/api/plugins/settings/sections/owner/Hello", "")
	req.Header.Set("X-Argus-Auth", "master")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)
}

func TestAPI_UnregisterPluginSection_NotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("DELETE", "/api/plugins/settings/sections/missing/x", "missing", ""))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestAPI_SubmitPluginSectionValues_ProxiesToCallback(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	var seenURL string
	var seenBody string
	srv.pluginSubmitFn = func(_ context.Context, url, _ string, body []byte) (int, []byte, error) {
		seenURL = url
		seenBody = string(body)
		return http.StatusOK, []byte(`{"ok":true}`), nil
	}

	body := `{"k": true}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", body))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, seenURL, "http://127.0.0.1:9001/save")
	testutil.Equal(t, seenBody, body)
}

func TestAPI_SubmitPluginSectionValues_BadJSON(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", "{not json"))
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestAPI_SubmitPluginSectionValues_UnknownSection(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/none/none/submit", "{}"))
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestAPI_SubmitPluginSectionValues_PluginError(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", validSectionBody))

	srv.pluginSubmitFn = func(_ context.Context, _, _ string, _ []byte) (int, []byte, error) {
		return 0, nil, errors.New("connection refused")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", `{}`))
	testutil.Equal(t, w.Code, http.StatusBadGateway)
}

func TestDefaultPluginSubmit_RoundTrip(t *testing.T) {
	// httptest.NewServer to exercise the real http.DefaultClient code path.
	var seenBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = readAll(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"got":"yes"}`))
	}))
	defer upstream.Close()

	status, body, err := defaultPluginSubmit(t.Context(), upstream.URL, "", []byte(`{"k":true}`))
	testutil.NoError(t, err)
	testutil.Equal(t, status, http.StatusAccepted)
	testutil.Equal(t, string(body), `{"got":"yes"}`)
	testutil.Equal(t, string(seenBody), `{"k":true}`)
}

func TestDefaultPluginSubmit_BadURL(t *testing.T) {
	_, _, err := defaultPluginSubmit(t.Context(), "::not-a-url::", "", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestDefaultPluginSubmit_TransportError(t *testing.T) {
	// Use an unroutable port to force a fast transport error.
	_, _, err := defaultPluginSubmit(t.Context(), "http://127.0.0.1:1/", "", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unreachable URL")
	}
}

func TestAPI_SubmitPluginSectionValues_DefaultSubmitterReachesPlugin(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	var seenBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	body := `{"title":"Hello","callback_url":"` + upstream.URL + `","fields":[{"key":"k","label":"l","type":"bool","default":false}]}`
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", body))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", `{"k":true}`))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, string(seenBody), `{"k":true}`)
}

// TestAPI_SubmitPluginSectionValues_ForwardsAuthHeader pins the contract that
// when a section is registered with an `auth_header`, the daemon's callback
// proxy MUST set `Authorization: <auth_header>` on the POST to the plugin's
// callback URL. hera's MCP listener (and any plugin gating its callback on a
// shared secret) relies on this; before the fix the proxy dropped the header
// and the plugin saw an unauthenticated POST → 401.
func TestAPI_SubmitPluginSectionValues_ForwardsAuthHeader(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	// Real upstream so the production code path through defaultPluginSubmit
	// runs — the test seam is intentionally NOT used here, because the bug
	// was in defaultPluginSubmit dropping the header before egress.
	var seenAuth string
	var seenBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenBody, _ = readAll(r)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	const wantAuth = "Bearer plugin-secret-xyz"
	body := `{"title":"Hello","callback_url":"` + upstream.URL +
		`","auth_header":"` + wantAuth +
		`","fields":[{"key":"k","label":"l","type":"bool","default":false}]}`
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, pluginReq("POST", "/api/plugins/settings/sections", "owner", body))
	testutil.Equal(t, w.Code, http.StatusCreated)

	w = httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", `{"k":true}`))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, string(seenBody), `{"k":true}`)
	testutil.Equal(t, seenAuth, wantAuth)
}

// TestAPI_SubmitPluginSectionValues_OmitsEmptyAuthHeader covers the negative
// half: sections registered without an `auth_header` MUST NOT have the
// proxy set Authorization at all — otherwise plugins that reject unknown
// auth headers would 401 on every save.
func TestAPI_SubmitPluginSectionValues_OmitsEmptyAuthHeader(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	var hasAuth bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasAuth = r.Header["Authorization"]
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	body := `{"title":"Hello","callback_url":"` + upstream.URL + `","fields":[{"key":"k","label":"l","type":"bool","default":false}]}`
	mux.ServeHTTP(httptest.NewRecorder(), pluginReq("POST", "/api/plugins/settings/sections", "owner", body))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, authedReq("POST", "/api/plugins/settings/sections/owner/Hello/submit", `{"k":true}`))
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Equal(t, hasAuth, false)
}

// TestDefaultPluginSubmit_SetsAuthorizationWhenNonEmpty is the unit-level
// pin for defaultPluginSubmit itself. Belt-and-braces with the integration
// test above so a refactor moving header logic out of defaultPluginSubmit
// can't silently regress the contract.
func TestDefaultPluginSubmit_SetsAuthorizationWhenNonEmpty(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	_, _, err := defaultPluginSubmit(t.Context(), upstream.URL, "Bearer xyz", []byte(`{}`))
	testutil.NoError(t, err)
	testutil.Equal(t, seen, "Bearer xyz")
}

func TestAuthScope(t *testing.T) {
	cases := []struct {
		header string
		want   string
	}{
		{"scope:foo", "foo"},
		{"scope:", ""},
		{"master", ""},
		{"device", ""},
		{"", ""},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/", nil)
		if c.header != "" {
			req.Header.Set("X-Argus-Auth", c.header)
		}
		got := authScope(req)
		testutil.Equal(t, got, c.want)
	}
}

func TestAPI_RehydratePluginSections_LoadsExisting(t *testing.T) {
	srv, d := testServer(t)
	// Persist a section directly to the DB before any handler runs.
	sec, err := settings.ParseSection("seed", []byte(validSectionBody))
	testutil.NoError(t, err)
	row, err := db.FromSection(sec)
	testutil.NoError(t, err)
	_, err = d.UpsertPluginSection(row)
	testutil.NoError(t, err)

	// Replace registry to ensure rehydrate sees a fresh state.
	srv.pluginSections = settings.NewRegistry()
	srv.rehydratePluginSections()

	if _, ok := srv.pluginSections.Get("seed", "Hello"); !ok {
		t.Fatal("expected rehydrated section to be in registry")
	}
}

func TestAPI_RehydratePluginSections_NilSafe(t *testing.T) {
	srv, _ := testServer(t)
	srv.pluginSections = nil
	srv.rehydratePluginSections() // must not panic
}

func TestAPI_RehydratePluginSections_SkipsCorruptRow(t *testing.T) {
	srv, d := testServer(t)
	// Insert a corrupt JSON spec_json directly, bypassing UpsertPluginSection's
	// validation by reaching for the conn (this is a regression-style probe —
	// a real "corruption" today would be a manual SQLite edit).
	_, err := d.UpsertPluginSection(db.PluginSection{
		Scope: "good", Title: "Good", Type: "form",
		SpecJSON:    `{"fields":[{"key":"k","label":"l","type":"bool","default":false}]}`,
		CallbackURL: "http://127.0.0.1/save",
	})
	testutil.NoError(t, err)
	// Now corrupt the spec_json column for that row using a raw exec via
	// another upsert with broken JSON — UpsertPluginSection persists whatever
	// SpecJSON it's handed (validation is upstream in ParseSection).
	_, err = d.UpsertPluginSection(db.PluginSection{
		Scope: "broken", Title: "Broken", Type: "form",
		SpecJSON: "{not json", CallbackURL: "http://127.0.0.1/save",
	})
	testutil.NoError(t, err)

	srv.pluginSections = settings.NewRegistry()
	srv.rehydratePluginSections()
	if _, ok := srv.pluginSections.Get("good", "Good"); !ok {
		t.Fatal("good row should rehydrate")
	}
	if _, ok := srv.pluginSections.Get("broken", "Broken"); ok {
		t.Fatal("broken row must be skipped")
	}
}
