package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
	"github.com/drn/argus/internal/voicetask"
)

var newVoiceLauncher = voicetask.NewLauncherFromConfig

type voiceTaskReq struct {
	Transcript string `json:"transcript"`
	Backend    string `json:"backend"`
}

func (s *Server) handleCreateVoiceTask(w http.ResponseWriter, r *http.Request) {
	var req voiceTaskReq
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		uxlog.Log("[voice] rejected: invalid JSON")
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if strings.TrimSpace(req.Transcript) == "" {
		uxlog.Log("[voice] rejected: empty transcript")
		writeErr(w, http.StatusBadRequest, "transcript is required", nil)
		return
	}
	if err := s.validateBackend(req.Backend); err != nil {
		uxlog.Log("[voice] rejected: bad backend")
		writeErr(w, http.StatusBadRequest, "", err)
		return
	}

	launcher := newVoiceLauncher(s.db.Config(), s.voiceProjects, s.voiceCreate, s.voiceStamp, sanitizeName)
	res, err := launcher.Launch(r.Context(), req.Transcript, req.Backend)
	if err != nil {
		writeErr(w, voiceErrStatus(err), "", err)
		return
	}

	resp := map[string]any{
		"id":             res.Task.ID,
		"name":           res.Task.Name,
		"status":         res.Task.Status.String(),
		"project":        res.Project,
		"prompt":         res.Prompt,
		"project_source": res.Source,
	}
	if res.SpokenProject != "" {
		resp["spoken_project"] = res.SpokenProject
	}
	writeJSON(w, http.StatusCreated, resp)
}

func voiceErrStatus(err error) int {
	switch {
	case errors.Is(err, voicetask.ErrNoProject):
		return http.StatusUnprocessableEntity
	case errors.Is(err, voicetask.ErrEmptyTranscript), errors.Is(err, agent.ErrAccount):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (s *Server) voiceProjects() ([]string, error) {
	projects, err := s.db.Projects()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	return names, nil
}

func (s *Server) voiceCreate(name, prompt, project, backend string) (*model.Task, error) {
	return s.createTask(name, prompt, project, backend, "", "", "", true)
}

func (s *Server) voiceStamp(taskID string) error {
	return s.db.SetMetaBatch(taskID, db.UIMetaNamespace, map[string]string{
		db.UIMetaKeyNew:    "true",
		db.UIMetaKeySource: "voice",
	})
}
