package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// metaEntry mirrors the JSON shape handleGetMeta emits per row.
type metaEntry struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt string `json:"updated_at"`
}

type metaListResp struct {
	Entries []metaEntry `json:"entries"`
}

func TestAPI_GetMeta_Empty(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+task.ID+"/meta?namespace=ns-a", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp metaListResp
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Entries), 0)
}

func TestAPI_GetMeta_FiltersByNamespace(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "role", "coordinator"))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "label", "alpha"))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-b", "role", "worker"))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+task.ID+"/meta?namespace=ns-a", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp metaListResp
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Entries), 2)
	for _, e := range resp.Entries {
		testutil.Equal(t, e.Namespace, "ns-a")
	}
}

func TestAPI_GetMeta_NoNamespaceReturnsAll(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "k", "v1"))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-b", "k", "v2"))

	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/"+task.ID+"/meta", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp metaListResp
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Entries), 2)
}

func TestAPI_GetMeta_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	req := authedReq("GET", "/api/tasks/missing/meta", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestAPI_PutMeta_Single_Master(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	body := `{"namespace":"ns-a","key":"role","value":"coordinator"}`
	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// Single write reports written=1.
	if got, _ := resp["written"].(float64); got != 1 {
		t.Fatalf("expected written=1, got %v", resp["written"])
	}

	entries, err := d.ListMeta(task.ID, "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Value, "coordinator")
}

func TestAPI_PutMeta_Batch_Master(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	body := `{"namespace":"ns-a","entries":{"role":"worker","status":"active","label":"alpha"}}`
	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp map[string]any
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	if got, _ := resp["written"].(float64); got != 3 {
		t.Fatalf("expected written=3, got %v", resp["written"])
	}

	entries, err := d.ListMeta(task.ID, "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 3)
}

func TestAPI_PutMeta_RequiresMaster(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	body := `{"namespace":"ns-a","key":"k","value":"v"}`
	req := deviceReq("PUT", "/api/tasks/"+task.ID+"/meta", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

// scopeReq stamps X-Argus-Auth: scope:<name> to simulate a request that
// the auth middleware would have tagged when authenticated by a plugin-scoped
// token. Mirrors masterReq/deviceReq from messages_test.go.
//
// PR 1 of the substrate plan ships the actual middleware-side tagging. Until
// it lands, the handler-side enforcement is exercised directly with a
// hand-stamped header — same pattern as masterReq.
func scopeReq(method, url, body, scope string) *http.Request {
	req := authedReq(method, url, body)
	req.Header.Set("X-Argus-Auth", "scope:"+scope)
	return req
}

func TestAPI_PutMeta_Scope_OmittedNamespace_AutoDerives(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	// Body has no namespace field — handler must auto-derive from the
	// scope tag.
	body := `{"key":"role","value":"coordinator"}`
	req := scopeReq("PUT", "/api/tasks/"+task.ID+"/meta", body, "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	entries, err := d.ListMeta(task.ID, "ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Namespace, "ludwig")
	testutil.Equal(t, entries[0].Key, "role")
	testutil.Equal(t, entries[0].Value, "coordinator")
}

func TestAPI_PutMeta_Scope_MatchingNamespace_OK(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	body := `{"namespace":"ludwig","key":"role","value":"worker"}`
	req := scopeReq("PUT", "/api/tasks/"+task.ID+"/meta", body, "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	entries, err := d.ListMeta(task.ID, "ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Value, "worker")
}

func TestAPI_PutMeta_Scope_CrossNamespace_Forbidden(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	// A scope:ludwig token tries to write into namespace=other.
	body := `{"namespace":"other","key":"role","value":"worker"}`
	req := scopeReq("PUT", "/api/tasks/"+task.ID+"/meta", body, "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)

	// And no row was written to either namespace.
	entries, err := d.ListMeta(task.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 0)
}

// TestAPI_PutMeta_Scope_EmptySuffix_Rejected covers the scopeFromAuth
// empty-suffix branch — a malformed `X-Argus-Auth: scope:` header must NOT
// be treated as a valid scope token (would otherwise allow a write into the
// empty-string namespace).
func TestAPI_PutMeta_Scope_EmptySuffix_Rejected(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	req := scopeReq("PUT", "/api/tasks/"+task.ID+"/meta",
		`{"namespace":"x","key":"k","value":"v"}`, "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestAPI_PutMeta_Master_ExplicitNamespace_Unchanged(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	// Master tier — body's explicit namespace is taken as-is, even one
	// that "looks like" a scope it doesn't own.
	body := `{"namespace":"ludwig","key":"role","value":"coordinator"}`
	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	entries, err := d.ListMeta(task.ID, "ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Value, "coordinator")
}

func TestAPI_PutMeta_TaskNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()
	req := masterReq("PUT", "/api/tasks/missing/meta", `{"namespace":"ns","key":"k","value":"v"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestAPI_PutMeta_BadRequest(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()

	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed JSON", `not-json`, http.StatusBadRequest},
		{"missing namespace", `{"key":"k","value":"v"}`, http.StatusBadRequest},
		{"single missing key", `{"namespace":"ns","value":"v"}`, http.StatusBadRequest},
		{"batch with empty key", `{"namespace":"ns","entries":{"":"v"}}`, http.StatusBadRequest},
		{"neither single nor batch", `{"namespace":"ns"}`, http.StatusBadRequest},
		{"both single and batch", `{"namespace":"ns","key":"k","value":"v","entries":{"k2":"v2"}}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta", tc.body)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			testutil.Equal(t, w.Code, tc.want)
		})
	}
}

func TestAPI_PutMeta_Roundtrip(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()

	// Write
	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta",
		`{"namespace":"ludwig","key":"role","value":"coordinator"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	// Read back
	req = authedReq("GET", "/api/tasks/"+task.ID+"/meta?namespace=ludwig", "")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp metaListResp
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Entries), 1)
	testutil.Equal(t, resp.Entries[0].Namespace, "ludwig")
	testutil.Equal(t, resp.Entries[0].Key, "role")
	testutil.Equal(t, resp.Entries[0].Value, "coordinator")
	if resp.Entries[0].UpdatedAt == "" {
		t.Fatal("expected UpdatedAt to be populated")
	}
}

// TestAPI_GetMeta_DBErrorReturns500 covers the 500-branch of both the
// s.db.Get and s.db.ListMeta failure paths by closing the underlying DB
// before the handler runs. Without these the error branches sit at 0%.
func TestAPI_GetMeta_DBErrorReturns500(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()

	t.Run("Get returns generic error", func(t *testing.T) {
		// Use a non-empty task ID so the Get fails on the closed connection,
		// not on the ErrTaskNotFound branch (which already has coverage).
		testutil.NoError(t, d.Close())
		req := authedReq("GET", "/api/tasks/"+task.ID+"/meta", "")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		testutil.Equal(t, w.Code, http.StatusInternalServerError)
	})
}

// TestAPI_PutMeta_DBErrorReturns500 covers the 500-branch of the s.db.Get
// failure path during a master-tier PUT.
func TestAPI_PutMeta_DBErrorReturns500(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))
	mux := srv.routes()
	testutil.NoError(t, d.Close())

	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta",
		`{"namespace":"ns","key":"k","value":"v"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusInternalServerError)
}

func TestAPI_PutMeta_BodyTooLarge(t *testing.T) {
	srv, d := testServer(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))

	mux := srv.routes()
	// Exceed taskMetaMaxBodyBytes by sending a 2 MiB value.
	big := strings.Repeat("x", 2*1024*1024)
	body := `{"namespace":"ns","key":"k","value":"` + big + `"}`
	req := masterReq("PUT", "/api/tasks/"+task.ID+"/meta", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}
