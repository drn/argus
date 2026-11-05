package apistore

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestStore_SandboxProposals(t *testing.T) {
	var approveBody string
	rejected := false
	s, _ := newStore(t, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) })
		mux.HandleFunc("GET /api/sandbox/proposals", func(w http.ResponseWriter, r *http.Request) {
			testutil.Equal(t, r.URL.Query().Get("status"), "pending")
			_ = json.NewEncoder(w).Encode(map[string]any{"proposals": []map[string]any{{"id": "p1", "task_id": "t", "entries": []map[string]any{{"path": "~/.gem"}}}}})
		})
		mux.HandleFunc("POST /api/sandbox/proposals/p1/approve", func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			approveBody = string(b)
			_, _ = w.Write([]byte(`{"scope":"project","applied":["~/.gem"],"takes_effect":"new_sessions"}`))
		})
		mux.HandleFunc("POST /api/sandbox/proposals/p1/reject", func(w http.ResponseWriter, r *http.Request) {
			rejected = true
			_, _ = w.Write([]byte(`{}`))
		})
	})

	l, _ := s.ListSandboxProposals("")
	testutil.Equal(t, len(l), 0)
	_, err := s.RefreshConfig(context.Background())
	testutil.NoError(t, err)
	l, _ = s.ListSandboxProposals("")
	testutil.Equal(t, len(l), 1)

	res, err := s.ApproveSandboxProposal("p1", "project", []string{"~/.gem"})
	testutil.NoError(t, err)
	testutil.Equal(t, res.TakesEffect, "new_sessions")
	testutil.Contains(t, approveBody, `"paths":["~/.gem"]`)

	testutil.NoError(t, s.RejectSandboxProposal("p1"))
	testutil.Equal(t, rejected, true)

	// Without edited paths, the body omits "paths".
	_, err = s.ApproveSandboxProposal("p1", "", nil)
	testutil.NoError(t, err)
	if approveBody != `{"scope":""}`+"\n" && approveBody != `{"scope":""}` {
		t.Fatalf("unexpected body %q", approveBody)
	}

	// Errors propagate and leave the cache alone.
	_, err = s.ApproveSandboxProposal("nope", "", nil)
	testutil.Equal(t, err != nil, true)
	testutil.Equal(t, s.RejectSandboxProposal("nope") != nil, true)
}
