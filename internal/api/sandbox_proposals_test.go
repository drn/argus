package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sandboxprop"
	"github.com/drn/argus/internal/testutil"
)

func TestSandboxProposals_API(t *testing.T) {
	srv, d := testServer(t)
	handler := authMiddleware(srv.token, d, nil, srv.routes())
	testutil.NoError(t, d.SetProject("op", config.Project{Path: "/tmp/op"}))
	plain, _, err := MintToken(d, "phone")
	testutil.NoError(t, err)

	do := func(token, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	res, err := sandboxprop.Propose(d, &model.Task{ID: "t1", Project: "op"}, []sandboxprop.Item{{Path: "~/.gem", Reason: "r"}})
	testutil.NoError(t, err)
	id := res.Proposal.ID
	base := "/api/sandbox/proposals/" + id

	// Any token can read.
	w := do(plain, "GET", "/api/sandbox/proposals?status=pending", "")
	testutil.Equal(t, w.Code, http.StatusOK)
	var list struct{ Proposals []db.SandboxProposal }
	testutil.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	testutil.Equal(t, len(list.Proposals), 1)
	testutil.Equal(t, do(plain, "GET", base, "").Code, http.StatusOK)
	testutil.Equal(t, do(plain, "GET", "/api/sandbox/proposals/nope", "").Code, http.StatusNotFound)

	// Device token cannot decide.
	testutil.Equal(t, do(plain, "POST", base+"/approve", "").Code, http.StatusForbidden)
	testutil.Equal(t, do(plain, "POST", base+"/reject", "").Code, http.StatusForbidden)
	p, _ := d.GetSandboxProposal(id)
	testutil.Equal(t, p.Status, "pending")
	ps, _ := d.Projects()
	testutil.Equal(t, len(ps["op"].Sandbox.ExtraWrite), 0)

	// Master approves (empty body = as proposed), second approve conflicts.
	w = do(srv.token, "POST", base+"/approve", "")
	testutil.Equal(t, w.Code, http.StatusOK)
	testutil.Contains(t, w.Body.String(), "new_sessions")
	ps, _ = d.Projects()
	testutil.DeepEqual(t, ps["op"].Sandbox.ExtraWrite, []string{"~/.gem"})
	testutil.Equal(t, do(srv.token, "POST", base+"/approve", "").Code, http.StatusConflict)
	testutil.Equal(t, do(srv.token, "POST", base+"/reject", "").Code, http.StatusConflict)

	// Reject path + bad requests.
	res2, _ := sandboxprop.Propose(d, &model.Task{ID: "t1", Project: "op"}, []sandboxprop.Item{{Path: "~/.expo"}})
	testutil.Equal(t, do(srv.token, "POST", "/api/sandbox/proposals/"+res2.Proposal.ID+"/reject", "").Code, http.StatusOK)
	res3, _ := sandboxprop.Propose(d, &model.Task{ID: "t1", Project: "op"}, []sandboxprop.Item{{Path: "~/.cocoapods"}})
	b3 := "/api/sandbox/proposals/" + res3.Proposal.ID + "/approve"
	testutil.Equal(t, do(srv.token, "POST", b3, "{bad").Code, http.StatusBadRequest)
	testutil.Equal(t, do(srv.token, "POST", b3, `{"paths":["relative"]}`).Code, http.StatusBadRequest)
	testutil.Equal(t, do(srv.token, "POST", b3, `{"scope":"global","paths":["~/.cocoapods"]}`).Code, http.StatusOK)
	testutil.Equal(t, do(srv.token, "POST", "/api/sandbox/proposals/nope/approve", "").Code, http.StatusNotFound)
}

func TestSandboxProposals_NotDecidableViaReadOnlyGet(t *testing.T) {
	srv, d := testServer(t)
	testutil.NoError(t, d.SetProject("op", config.Project{Path: "/tmp/op"}))
	res, err := sandboxprop.Propose(d, &model.Task{ID: "t1", Project: "op"}, []sandboxprop.Item{{Path: "~/.gem"}})
	testutil.NoError(t, err)
	for _, action := range []string{"approve", "reject"} {
		out, err := srv.ReadOnlyGet(context.Background(), "/api/sandbox/proposals/"+res.Proposal.ID+"/"+action)
		testutil.NoError(t, err)
		testutil.Equal(t, out.Status, http.StatusNotFound)
	}
	p, _ := d.GetSandboxProposal(res.Proposal.ID)
	testutil.Equal(t, p.Status, "pending")
	ps, _ := d.Projects()
	testutil.Equal(t, len(ps["op"].Sandbox.ExtraWrite), 0)

	out, err := srv.ReadOnlyGet(context.Background(), "/api/sandbox/proposals")
	testutil.NoError(t, err)
	testutil.Equal(t, out.Status, http.StatusOK)
}
