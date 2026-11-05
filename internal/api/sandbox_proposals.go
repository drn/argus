package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/sandboxprop"
	"github.com/drn/argus/internal/uxlog"
)

// handleSandboxProposalList is readable by any token (agents read status via
// argus_api_get). Deciding is master-only; see handleSandboxProposalApprove.
func (s *Server) handleSandboxProposalList(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.ListSandboxProposals(r.URL.Query().Get("status"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": list})
}

func (s *Server) handleSandboxProposalGet(w http.ResponseWriter, r *http.Request) {
	p, err := s.db.GetSandboxProposal(r.PathValue("id"))
	if err != nil {
		writeSandboxProposalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

type sandboxApproveReq struct {
	Scope string   `json:"scope"`
	Paths []string `json:"paths"`
}

// handleSandboxProposalApprove applies a proposal to extra_write. Master token
// only: this is the single human-gated path that widens an agent's sandbox.
func (s *Server) handleSandboxProposalApprove(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req sandboxApproveReq
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	res, err := sandboxprop.Approve(s.db, r.PathValue("id"), req.Scope, req.Paths, "master")
	if err != nil {
		writeSandboxProposalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSandboxProposalReject(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	p, err := sandboxprop.Reject(s.db, r.PathValue("id"), "master")
	if err != nil {
		writeSandboxProposalErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func writeSandboxProposalErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrSandboxProposalNotFound):
		writeErr(w, http.StatusNotFound, "proposal not found", nil)
	case errors.Is(err, db.ErrSandboxProposalDecided):
		writeErr(w, http.StatusConflict, "proposal already decided", nil)
	default:
		uxlog.Log("[sandbox-proposal] api error: %v", err)
		writeErr(w, http.StatusBadRequest, "", err)
	}
}
