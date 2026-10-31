package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// moveAccountRequest is the POST /api/tasks/{id}/account body.
type moveAccountRequest struct {
	Account string `json:"account"`
	Force   bool   `json:"force"`
}

// handleMoveTaskAccount moves a Claude task to another account and resumes its
// conversation there (agent.MoveTaskAccount). A live session is restarted in
// place; an exited one goes through the same path as /restart.
func (s *Server) handleMoveTaskAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req moveAccountRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body", err)
		return
	}
	res, status, err := s.moveTaskAccount(agent.AccountMoveInput{TaskID: id, Account: req.Account, Force: req.Force})
	if err != nil {
		writeErr(w, status, err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// MoveTaskAccount is moveTaskAccount without the HTTP status, for the MCP
// task_move_account tool.
func (s *Server) MoveTaskAccount(in agent.AccountMoveInput) (agent.AccountMoveResult, error) {
	res, _, err := s.moveTaskAccount(in)
	return res, err
}

// moveTaskAccount runs the move plus the restart fallback and maps failures to
// an HTTP status. Shared with the MCP task_move_account tool.
func (s *Server) moveTaskAccount(in agent.AccountMoveInput) (agent.AccountMoveResult, int, error) {
	task, res, err := agent.MoveTaskAccount(s.db, s.runner, in)
	switch {
	case errors.Is(err, db.ErrTaskNotFound):
		return res, http.StatusNotFound, err
	case errors.Is(err, agent.ErrAccountMoveBusy):
		return res, http.StatusConflict, err
	case errors.Is(err, agent.ErrAccount):
		return res, http.StatusBadRequest, err
	case err != nil:
		return res, http.StatusInternalServerError, err
	}
	if !res.Started {
		if _, err := s.restartTaskSession(task); err != nil {
			uxlog.Log("[account] move task=%s: restart on %s failed: %v", task.ID, res.To, err)
			return res, http.StatusInternalServerError, fmt.Errorf("account moved to %q but restart failed: %w", res.To, err)
		}
		res.Started = true
	}
	return res, http.StatusOK, nil
}
