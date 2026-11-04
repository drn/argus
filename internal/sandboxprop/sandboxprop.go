// Package sandboxprop holds the propose/approve/reject logic for agent-
// submitted sandbox write-path proposals. Propose only records a request;
// Approve is the sole function that writes extra_write config, and it is
// reachable only from master-token REST routes and human-driven UIs.
package sandboxprop

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

const (
	ScopeProject = "project"
	ScopeGlobal  = "global"

	MaxPathsPerProposal = 20
	MaxReasonRunes      = 500

	TakesEffectNewSessions = "new_sessions"
)

// Item is one requested path with the agent's justification.
type Item struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ProposeResult is returned to the proposing agent.
type ProposeResult struct {
	Proposal *db.SandboxProposal `json:"proposal"`
	Merged   bool                `json:"merged"`
	Warnings []string            `json:"warnings,omitempty"`
}

// ApproveResult describes what an approval changed.
type ApproveResult struct {
	Proposal       *db.SandboxProposal `json:"proposal"`
	Scope          string              `json:"scope"`
	Applied        []string            `json:"applied"`
	AlreadyGranted []string            `json:"already_granted"`
	Warnings       []string            `json:"warnings,omitempty"`
	TakesEffect    string              `json:"takes_effect"`
}

// validPath trims, drops a trailing slash so ~/x and ~/x/ dedupe, and rejects
// malformed paths including invisible/bidi runes that ValidateWritePath's
// control-character check lets through.
func validPath(raw string) (string, string) {
	p := strings.TrimRight(strings.TrimSpace(raw), "/")
	if p == "" {
		p = strings.TrimSpace(raw)
	}
	if reason := agent.ValidateWritePath(p); reason != "" {
		return p, reason
	}
	for _, r := range p {
		if !unicode.IsPrint(r) {
			return p, "non-printable characters are not allowed"
		}
	}
	return p, ""
}

var applyMu sync.Mutex

// Propose validates items and records a pending proposal. It never changes
// sandbox config. Malformed paths reject the whole call.
func Propose(d *db.DB, task *model.Task, items []Item) (*ProposeResult, error) {
	if len(items) == 0 {
		return nil, errors.New("paths is required")
	}
	if len(items) > MaxPathsPerProposal {
		return nil, fmt.Errorf("at most %d paths per proposal", MaxPathsPerProposal)
	}
	var bad []string
	seen := map[string]bool{}
	var entries []db.SandboxProposalEntry
	var warnings []string
	for _, it := range items {
		p, reason := validPath(it.Path)
		if reason != "" {
			bad = append(bad, fmt.Sprintf("%s: %s", p, reason))
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		r := []rune(strings.TrimSpace(it.Reason))
		if len(r) > MaxReasonRunes {
			r = r[:MaxReasonRunes]
		}
		e := db.SandboxProposalEntry{Path: p, Reason: string(r), Warning: agent.SandboxPathWarning(p)}
		if e.Warning != "" {
			warnings = append(warnings, p+": "+e.Warning)
		}
		entries = append(entries, e)
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("invalid path(s): %s", strings.Join(bad, "; "))
	}
	prop, merged, err := d.AddSandboxProposal(task.ID, task.Project, entries)
	if err != nil {
		return nil, err
	}
	uxlog.Log("[sandbox-proposal] proposed: id=%s task=%s project=%q paths=%d merged=%v warnings=%d", prop.ID, task.ID, task.Project, len(entries), merged, len(warnings))
	return &ProposeResult{Proposal: prop, Merged: merged, Warnings: warnings}, nil
}

// Approve applies a pending proposal's (optionally edited) paths to the chosen
// scope's extra_write list. Human-only: callers must be master-authenticated or
// a local human UI. by is recorded as the decider.
func Approve(d *db.DB, id, scope string, edited []string, by string) (*ApproveResult, error) {
	prop, err := d.GetSandboxProposal(id)
	if err != nil {
		return nil, err
	}
	if prop.Status != db.SandboxProposalPending {
		return nil, db.ErrSandboxProposalDecided
	}
	if scope == "" {
		scope = ScopeProject
		if prop.Project == "" {
			scope = ScopeGlobal
		}
	}
	if scope != ScopeProject && scope != ScopeGlobal {
		return nil, fmt.Errorf("scope must be %q or %q", ScopeProject, ScopeGlobal)
	}
	if scope == ScopeProject && prop.Project == "" {
		return nil, errors.New("proposal has no project; approve with scope global")
	}

	var paths []string
	if edited != nil {
		paths = edited
	} else {
		for _, e := range prop.Entries {
			paths = append(paths, e.Path)
		}
	}
	var clean, warnings []string
	seen := map[string]bool{}
	for _, p := range paths {
		p, reason := validPath(p)
		if reason != "" {
			return nil, fmt.Errorf("invalid path %s: %s", p, reason)
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		clean = append(clean, p)
		if w := agent.SandboxPathWarning(p); w != "" {
			warnings = append(warnings, p+": "+w)
		}
	}
	if len(clean) == 0 {
		return nil, errors.New("no paths to approve")
	}

	// Serialize read-modify-write of the list within this process; the list is
	// re-read after the status flip so a concurrent approval is not clobbered.
	applyMu.Lock()
	defer applyMu.Unlock()
	if _, err := currentList(d, scope, prop.Project); err != nil {
		return nil, err
	}
	if err := d.DecideSandboxProposal(id, db.SandboxProposalApproved, scope, clean, by); err != nil {
		return nil, err
	}
	existing, err := currentList(d, scope, prop.Project)
	if err != nil {
		_ = d.RevertSandboxProposal(id)
		return nil, err
	}
	have := map[string]bool{}
	for _, p := range existing {
		have[p] = true
	}
	var applied, already []string
	for _, p := range clean {
		if have[p] {
			already = append(already, p)
		} else {
			applied = append(applied, p)
		}
	}
	if len(applied) > 0 {
		if err := writeList(d, scope, prop.Project, append(existing, applied...)); err != nil {
			if rerr := d.RevertSandboxProposal(id); rerr != nil {
				uxlog.Log("[sandbox-proposal] revert after failed apply failed: id=%s err=%v", id, rerr)
			}
			return nil, fmt.Errorf("applying paths: %w", err)
		}
	}
	prop, _ = d.GetSandboxProposal(id)
	uxlog.Log("[sandbox-proposal] approved: id=%s scope=%s applied=%d already=%d by=%s", id, scope, len(applied), len(already), by)
	return &ApproveResult{Proposal: prop, Scope: scope, Applied: applied, AlreadyGranted: already, Warnings: warnings, TakesEffect: TakesEffectNewSessions}, nil
}

// Reject marks a pending proposal rejected without touching config.
func Reject(d *db.DB, id, by string) (*db.SandboxProposal, error) {
	if err := d.DecideSandboxProposal(id, db.SandboxProposalRejected, "", nil, by); err != nil {
		return nil, err
	}
	uxlog.Log("[sandbox-proposal] rejected: id=%s by=%s", id, by)
	return d.GetSandboxProposal(id)
}

func currentList(d *db.DB, scope, project string) ([]string, error) {
	if scope == ScopeGlobal {
		v, err := d.GetConfigValue("sandbox.extra_write")
		if err != nil {
			return nil, nil // unset
		}
		return splitCSV(v), nil
	}
	projects, err := d.Projects()
	if err != nil {
		return nil, err
	}
	p, ok := projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q not found", project)
	}
	return append([]string(nil), p.Sandbox.ExtraWrite...), nil
}

func writeList(d *db.DB, scope, project string, list []string) error {
	if scope == ScopeGlobal {
		return d.SetConfigValue("sandbox.extra_write", strings.Join(list, ","))
	}
	projects, err := d.Projects()
	if err != nil {
		return err
	}
	p, ok := projects[project]
	if !ok {
		return fmt.Errorf("project %q not found", project)
	}
	p.Sandbox.ExtraWrite = list
	return d.SetProject(project, p)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Proposer adapts Propose to the MCP server's narrow interface. It exposes
// propose only, by construction: there is no approve/reject on this type.
type Proposer struct{ DB *db.DB }

// Propose records a pending proposal for task.
func (p Proposer) Propose(task *model.Task, items []Item) (*ProposeResult, error) {
	return Propose(p.DB, task, items)
}
