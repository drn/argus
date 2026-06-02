package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/views"
)

// TestPluginViews_PostCreates pins the POST /api/plugins/views shape. Body
// is {title, hotkey, callback_url}; response is 201 + the persisted row.
func TestPluginViews_PostCreates(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	req := masterReq("POST", "/api/plugins/views",
		`{"title":"Ludwig","hotkey":"ctrl+l","callback_url":"ws://127.0.0.1:5111/view"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	var got pluginViewJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.Equal(t, got.Title, "Ludwig")
	testutil.Equal(t, got.Hotkey, "ctrl+l")
	testutil.Equal(t, got.CallbackURL, "ws://127.0.0.1:5111/view")
	if got.ID <= 0 {
		t.Fatalf("expected positive ID, got %d", got.ID)
	}

	// Round-trip through the DB to confirm persistence.
	all, _ := d.PluginViews()
	testutil.Equal(t, len(all), 1)
}

func TestPluginViews_PostRejectsNonMaster(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := authedReq("POST", "/api/plugins/views", `{"title":"X","callback_url":"ws://x"}`)
	req.Header.Set("X-Argus-Auth", "device")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestPluginViews_PostInvalidJSON(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("POST", "/api/plugins/views", `{ broken`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestPluginViews_PostMissingTitle(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("POST", "/api/plugins/views", `{"title":"","callback_url":"ws://x"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestPluginViews_PostMissingCallbackURL(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("POST", "/api/plugins/views", `{"title":"X"}`)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestPluginViews_PostDuplicateConflict(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	body := `{"title":"Ludwig","callback_url":"ws://a"}`
	req := masterReq("POST", "/api/plugins/views", body)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	req = masterReq("POST", "/api/plugins/views", body)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusConflict)
}

func TestPluginViews_GetLists(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	r := views.New(d)
	_, _ = r.Register("", "Alpha", "ctrl+a", "ws://a")
	_, _ = r.Register("", "Beta", "ctrl+b", "ws://b")

	req := masterReq("GET", "/api/plugins/views", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Views []pluginViewJSON `json:"views"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Views), 2)
	testutil.Equal(t, resp.Views[0].Title, "Alpha")
	testutil.Equal(t, resp.Views[1].Title, "Beta")
}

func TestPluginViews_GetRejectsNonMaster(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := authedReq("GET", "/api/plugins/views", "")
	req.Header.Set("X-Argus-Auth", "device")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

func TestPluginViews_DeleteRemoves(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	r := views.New(d)
	v, err := r.Register("", "Doomed", "", "ws://doom")
	testutil.NoError(t, err)

	req := masterReq("DELETE", "/api/plugins/views/"+itoa(v.ID), "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	all, _ := d.PluginViews()
	testutil.Equal(t, len(all), 0)
}

func TestPluginViews_DeleteNotFound(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("DELETE", "/api/plugins/views/99999", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusNotFound)
}

func TestPluginViews_DeleteBadID(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := masterReq("DELETE", "/api/plugins/views/not-an-int", "")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusBadRequest)
}

func TestPluginViews_DeleteRejectsNonMaster(t *testing.T) {
	srv, _ := testServer(t)
	mux := srv.routes()

	req := authedReq("DELETE", "/api/plugins/views/1", "")
	req.Header.Set("X-Argus-Auth", "device")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)
}

// Scope-token tests follow. The handler accepts master OR scope:<name>;
// scope tokens may only see and mutate rows whose scope matches their tag.

func TestPluginViews_PostScopeTokenStoresOwnScope(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	req := scopeReq("POST", "/api/plugins/views",
		`{"title":"Coord","hotkey":"ctrl+c","callback_url":"ws://127.0.0.1:9/view"}`,
		"ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusCreated)

	var got pluginViewJSON
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	testutil.Equal(t, got.Scope, "ludwig")
	testutil.Equal(t, got.Title, "Coord")

	all, _ := d.PluginViews()
	testutil.Equal(t, len(all), 1)
	testutil.Equal(t, all[0].Scope, "ludwig")
}

func TestPluginViews_GetScopeTokenFiltersToOwnScope(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	r := views.New(d)
	_, _ = r.Register("ludwig", "Ludwig view", "", "ws://l")
	_, _ = r.Register("plannotator", "Plannotator view", "", "ws://p")
	_, _ = r.Register("", "Master view", "", "ws://m") // master-owned

	// Scope token sees only its own row.
	req := scopeReq("GET", "/api/plugins/views", "", "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	var resp struct {
		Views []pluginViewJSON `json:"views"`
	}
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	testutil.Equal(t, len(resp.Views), 1)
	testutil.Equal(t, resp.Views[0].Scope, "ludwig")

	// Master still sees all three.
	mreq := masterReq("GET", "/api/plugins/views", "")
	mw := httptest.NewRecorder()
	mux.ServeHTTP(mw, mreq)
	testutil.Equal(t, mw.Code, http.StatusOK)
	var mresp struct {
		Views []pluginViewJSON `json:"views"`
	}
	testutil.NoError(t, json.Unmarshal(mw.Body.Bytes(), &mresp))
	testutil.Equal(t, len(mresp.Views), 3)
}

func TestPluginViews_DeleteScopeTokenAllowsOwnRow(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	r := views.New(d)
	v, err := r.Register("ludwig", "Doomed", "", "ws://d")
	testutil.NoError(t, err)

	req := scopeReq("DELETE", "/api/plugins/views/"+strconv.FormatInt(v.ID, 10), "", "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusOK)

	all, _ := d.PluginViews()
	testutil.Equal(t, len(all), 0)
}

func TestPluginViews_DeleteScopeTokenForbiddenOnCrossScope(t *testing.T) {
	srv, d := testServer(t)
	mux := srv.routes()

	r := views.New(d)
	v, err := r.Register("plannotator", "Theirs", "", "ws://t")
	testutil.NoError(t, err)

	// "ludwig" token tries to delete a "plannotator" row.
	req := scopeReq("DELETE", "/api/plugins/views/"+strconv.FormatInt(v.ID, 10), "", "ludwig")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	testutil.Equal(t, w.Code, http.StatusForbidden)

	// Row still exists.
	all, _ := d.PluginViews()
	testutil.Equal(t, len(all), 1)
}
