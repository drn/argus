package mcp

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/review"
)

// ConfigStore returns the live daemon config. *db.DB satisfies this (same
// call shape used elsewhere, e.g. SetScheduleManager(d.db, ...)). Consulted
// fresh on every profile_resolve call — never cached — so the tool reflects
// live config.toml / project-binding edits without a daemon restart, matching
// the read-fresh pattern at spawn time (internal/agent.resolveProfile).
type ConfigStore interface {
	Config() config.Config
}

// profileToolDefs are exposed only when SetProfileResolver has been called.
var profileToolDefs = []Tool{
	{
		Name: "profile_resolve",
		Description: `Resolve the diligence profile in effect and return its full body (archetype entries, [rigor], [panel]) as structured JSON.

Resolution precedence: an explicit ` + "`profile`" + ` argument bypasses cwd resolution entirely; otherwise the calling task's per-spawn profile override wins, then the project bound to ` + "`cwd`" + `'s task, then "default".

Fails open: a missing or invalid profile (including a malformed [panel]) returns ` + "`{\"resolved\": false, \"errors\": [...]}`" + ` rather than a hard tool error — callers should fall back to a built-in default when resolved is false.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cwd":     map[string]interface{}{"type": "string", "description": "Caller's worktree path (use $PWD). Used to resolve the bound task/project when profile is omitted."},
				"profile": map[string]interface{}{"type": "string", "description": "Explicit profile name. When set, resolves this profile directly and skips cwd→project resolution (useful for testing a profile in isolation)."},
			},
		},
	},
}

// profileResolveEnabled reports whether SetProfileResolver has been called.
// cwd resolution reuses s.resolveTask, so task management must also be wired.
func (s *Server) profileResolveEnabled() bool {
	return s.profileCfg != nil && s.taskMgmtEnabled()
}

// SetProfileResolver wires the mcp__argus__profile_resolve tool. cfg supplies
// the live daemon config (project→profile binding, and the model/backend
// allow-list consulted by validation). Requires SetTaskManager to already be
// wired (cwd resolution reuses resolveTask). Resolution reads
// ~/.argus/profiles and the worktree's .argus/profiles, both of which EPERM
// inside the sandbox — this tool is the sandboxed agent's only path to a
// resolved profile body, and it works because the MCP server itself only
// ever runs inside the daemon process (D6: "runs daemon-side").
func (s *Server) SetProfileResolver(cfg ConfigStore) {
	s.profileCfg = cfg
}

func (s *Server) toolProfileResolve(id interface{}, args json.RawMessage) *Response {
	if !s.profileResolveEnabled() {
		return toolError(id, "profile resolution not configured")
	}

	var p struct {
		Cwd     string `json:"cwd"`
		Profile string `json:"profile"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	cwd := strings.TrimSpace(p.Cwd)
	explicit := strings.TrimSpace(p.Profile)
	if cwd == "" && explicit == "" {
		return toolError(id, "cwd or profile is required")
	}

	cfg := s.profileCfg.Config()

	var task *model.Task
	if cwd != "" {
		task, _ = s.resolveTask("", cwd) // best-effort: nil on no match
	}

	name := explicit
	if name == "" {
		// Mirrors internal/agent.resolveProfile's precedence: per-spawn
		// override > project binding > "default".
		name = "default"
		if task != nil {
			if override := strings.TrimSpace(task.Profile); override != "" {
				name = override
			} else if task.Project != "" {
				if proj, ok := cfg.Projects[task.Project]; ok {
					name = proj.ResolveProfileName()
				}
			}
		}
	}

	loader := &profiles.Loader{LibraryDir: filepath.Join(db.DataDir(), "profiles")}
	if task != nil && task.Worktree != "" {
		loader.RepoDir = filepath.Join(task.Worktree, ".argus", "profiles")
	}

	prof, errs := loader.ValidateName(name, cfg, agent.KnownModels, review.NewValidator(cfg))
	return toolResult(id, marshalProfileResolveResult(name, prof, errs))
}

// marshalProfileResolveResult renders the fail-open JSON body: resolved profiles
// carry their full body (archetype entries passed through verbatim, never
// collapsed to scalars — D6 forward-compat), unresolved ones carry the name
// attempted and the errors found.
func marshalProfileResolveResult(name string, p *profiles.Profile, errs []error) string {
	type result struct {
		Resolved  bool                          `json:"resolved"`
		Name      string                        `json:"name,omitempty"`
		Source    string                        `json:"source,omitempty"`
		Archetype map[string]profiles.Archetype `json:"archetype,omitempty"`
		Rigor     *profiles.Rigor               `json:"rigor,omitempty"`
		Panel     map[string]any                `json:"panel,omitempty"`
		Errors    []string                      `json:"errors,omitempty"`
	}

	r := result{Name: name}
	if p == nil || len(errs) > 0 {
		for _, e := range errs {
			r.Errors = append(r.Errors, e.Error())
		}
		out, _ := json.Marshal(r)
		return string(out)
	}

	r.Resolved = true
	r.Source = string(p.Source)
	r.Archetype = p.Archetype
	r.Rigor = &p.Rigor
	r.Panel = p.Panel
	out, _ := json.Marshal(r)
	return string(out)
}
