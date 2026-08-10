package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/mergesafety"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// Namespace + keys the merge-safety review's global Cleanup action caches its
// per-task verdict under in the task_meta sidecar (openspec
// add-merge-safety-review). A cached safe=true verdict is terminal — never
// re-classified by a later compute pass; safe=false is re-checked every time
// since a later merge could change the outcome.
const (
	cleanupMetaNamespace = "cleanup"
	cleanupMetaSafe      = "safe"
	cleanupMetaTier      = "tier"
	cleanupMetaReason    = "reason"

	cleanupScopeSafe = "safe"
	cleanupScopeAll  = "all"
)

// cleanupComputeState tracks whether a background classification pass is
// currently running. A sync.Mutex + bool is sufficient here — there is only
// ever one pass at a time, and the guard's only job is "don't start a
// second one".
type cleanupComputeState struct {
	mu        sync.Mutex
	computing bool
}

// tryStart flips computing to true and reports success, unless a pass is
// already running (in which case it reports false and leaves the state
// untouched, so the caller does not start a competing pass — and does not
// spend a second, wasted GitHub GraphQL batch).
func (c *cleanupComputeState) tryStart() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.computing {
		return false
	}
	c.computing = true
	return true
}

func (c *cleanupComputeState) finish() {
	c.mu.Lock()
	c.computing = false
	c.mu.Unlock()
}

func (c *cleanupComputeState) isComputing() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.computing
}

// handleCleanupCandidatesCompute starts (or no-ops onto) a background
// classification pass over the current stuck-task backlog. Open to any
// authenticated token — this is a read/trigger-tier endpoint, not a bulk
// mutation (see the rest-api spec delta's Master-only gating requirement).
func (s *Server) handleCleanupCandidatesCompute(w http.ResponseWriter, r *http.Request) {
	s.startCleanupCompute()
	writeJSON(w, http.StatusOK, map[string]any{"computing": s.cleanup.isComputing()})
}

// startCleanupCompute launches the background classification pass if one
// isn't already running. The goroutine is deliberately detached from the
// triggering request's context — it must keep going after the HTTP response
// is written, per the "returns immediately without waiting" scenario.
func (s *Server) startCleanupCompute() {
	if !s.cleanup.tryStart() {
		return
	}
	fn := s.cleanupComputeFn
	if fn == nil {
		fn = s.runCleanupCompute
	}
	go func() {
		defer s.cleanup.finish()
		fn(context.Background())
	}()
}

// runCleanupCompute classifies every currently-eligible (stuck-task-predicate
// matching) task that does not already have a terminal (confirmed-safe)
// cached verdict, via mergesafety.ClassifyBatchFunc, caching EACH verdict to
// task_meta the moment it's produced rather than waiting for the whole batch.
//
// This incremental caching is the fix for a real observability bug
// (fix-hera-reclaim-status): ClassifyBatchFunc's Tier B network call is
// grouped one-per-repo and can legitimately take a while (each `gh api
// graphql` invocation carries its own 10s timeout — gitutil.runAliasedRepoQuery
// — and a backlog spanning N repos runs those N calls SEQUENTIALLY here), so
// on a large backlog where most branches have long since had their local
// copies cleaned up (Tier A misses, falling through to Tier B), the previous
// all-at-once mergesafety.ClassifyBatch call left every candidate showing as
// uncached until the ENTIRE multi-repo pass finished — up to ~10s per repo
// group with zero visible progress in between. Caching incrementally means a
// concurrent GET (the poll driving the cleanup popup) can observe real
// progress after every repo group lands, not just at the very end.
func (s *Server) runCleanupCompute(ctx context.Context) {
	tasks, err := s.db.StuckTaskCandidates()
	if err != nil {
		uxlog.Log("[cleanup] compute: query stuck-task candidates failed: %v", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	cached, err := s.db.ListMetaByNamespace(cleanupMetaNamespace)
	if err != nil {
		uxlog.Log("[cleanup] compute: read cache failed: %v", err)
		return
	}

	cfg := s.db.Config()
	var toClassify []mergesafety.Candidate
	for _, t := range tasks {
		if verdictIsSafe(cached[t.ID]) {
			continue // terminal — never re-classified
		}
		toClassify = append(toClassify, cleanupCandidateFor(ctx, cfg, t))
	}
	if len(toClassify) == 0 {
		return
	}

	uxlog.Log("[cleanup] compute: starting classification of %d of %d eligible task(s)", len(toClassify), len(tasks))
	classified := 0
	mergesafety.ClassifyBatchFunc(ctx, toClassify, func(id string, v mergesafety.Verdict) {
		entries := map[string]string{
			cleanupMetaSafe:   boolStr(v.Safe),
			cleanupMetaTier:   v.Tier,
			cleanupMetaReason: v.Reason,
		}
		if err := s.db.SetMetaBatch(id, cleanupMetaNamespace, entries); err != nil {
			uxlog.Log("[cleanup] compute: cache write failed for task=%s: %v", id, err)
			return
		}
		classified++
	})
	uxlog.Log("[cleanup] compute: classified %d of %d eligible task(s)", classified, len(tasks))
}

// cleanupCandidateFor resolves one task's mergesafety.Candidate via its
// project's configured repo path/branch. A task whose project no longer
// exists in config (or has an empty path) is left with an empty
// RepoDir/RepoSlug — deliberately NOT special-cased here; ClassifyBatch's own
// fail-closed "no repo resolvable for a merged-PR lookup" handling covers it.
func cleanupCandidateFor(ctx context.Context, cfg config.Config, t *model.Task) mergesafety.Candidate {
	cand := mergesafety.Candidate{
		ID:            t.ID,
		Branch:        t.Branch,
		TaskCreatedAt: t.CreatedAt,
	}
	proj := cfg.Projects[t.Project]
	if proj.Path == "" {
		return cand
	}
	cand.RepoDir = proj.Path
	if slug, ok := gitutil.ResolveDefaultRepo(ctx, proj.Path); ok {
		cand.RepoSlug = slug
	}
	if short, ref, err := gitutil.ResolveDefaultBranch(proj.Path, proj.Branch); err == nil {
		cand.DefaultShort = short
		cand.DefaultRef = ref
	}
	return cand
}

func verdictIsSafe(m map[string]string) bool {
	return m != nil && m[cleanupMetaSafe] == "true"
}

// cleanupCandidateJSON is one row of GET /api/maintenance/cleanup-candidates.
// Pending is true when this task has no cached verdict yet — the caller MUST
// treat that as "not yet checked," never as a confirmed-unsafe result, even
// though Safe is also false in that case (there is nothing safe to report
// yet). See fix-hera-reclaim-status: the popup used to render every
// not-yet-classified task under its NOT-SAFE header from the moment it
// opened, implying 737 confirmed verdicts when zero classification had
// actually happened.
type cleanupCandidateJSON struct {
	TaskID  string `json:"task_id"`
	Name    string `json:"name"`
	Project string `json:"project"`
	Branch  string `json:"branch"`
	Safe    bool   `json:"safe"`
	Tier    string `json:"tier,omitempty"`
	Reason  string `json:"reason"`
	Pending bool   `json:"pending"`
}

// handleCleanupCandidatesList returns the current cached classification for
// every currently-eligible task, plus whether a compute pass is in flight.
// Open to any authenticated token, like compute above.
func (s *Server) handleCleanupCandidatesList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.db.StuckTaskCandidates()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	cached, err := s.db.ListMetaByNamespace(cleanupMetaNamespace)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	candidates := make([]cleanupCandidateJSON, 0, len(tasks))
	for _, t := range tasks {
		c := cleanupCandidateJSON{TaskID: t.ID, Name: t.Name, Project: t.Project, Branch: t.Branch}
		if m := cached[t.ID]; m != nil {
			c.Safe = verdictIsSafe(m)
			c.Tier = m[cleanupMetaTier]
			c.Reason = m[cleanupMetaReason]
		} else {
			c.Pending = true
			c.Reason = "not yet classified"
		}
		candidates = append(candidates, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": candidates,
		"computing":  s.cleanup.isComputing(),
	})
}

// cleanupCleanRequest is the POST .../clean body.
type cleanupCleanRequest struct {
	Scope string `json:"scope"`
}

// handleCleanupCandidatesClean deletes the requested scope of the cached
// candidate snapshot via the same guarded PruneTasks/PrunePrepare primitive
// the Ctrl+R prune-completed flow uses — no separate deletion path.
// Master-only: an across-all-tasks bulk mutation.
//
// It acts on the classification already cached in task_meta (never re-runs
// ClassifyBatch here — see design.md's "acts on the last-computed snapshot"
// decision) but re-verifies the cheap, purely-local stuck-task predicate by
// re-querying it fresh: a cached task that has since stopped matching (or
// gained a live Hera binding, re-checked again inside PruneTasks itself) is
// silently excluded rather than erroring the batch.
func (s *Server) handleCleanupCandidatesClean(w http.ResponseWriter, r *http.Request) {
	if requireMaster(w, r) {
		return
	}
	var req cleanupCleanRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", err)
		return
	}
	if req.Scope != cleanupScopeSafe && req.Scope != cleanupScopeAll {
		writeErr(w, http.StatusBadRequest, `scope must be "safe" or "all"`, nil)
		return
	}

	tasks, err := s.db.StuckTaskCandidates()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	cached, err := s.db.ListMetaByNamespace(cleanupMetaNamespace)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}

	var ids []string
	for _, t := range tasks {
		if req.Scope == cleanupScopeSafe && !verdictIsSafe(cached[t.ID]) {
			continue
		}
		ids = append(ids, t.ID)
	}

	// agent.PrunePrepare treats an EMPTY TaskIDs slice as "not set" and falls
	// back to its default all-status=complete sweep — a much broader,
	// unrelated deletion this endpoint must never trigger. Short-circuit
	// before ever calling it when nothing in this scope qualifies.
	if len(ids) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"cleaned": 0, "skipped": 0})
		return
	}

	cfg := s.db.Config()
	projects := make(map[string]string, len(cfg.Projects))
	for name, p := range cfg.Projects {
		projects[name] = p.Path
	}

	plan, err := agent.PrunePrepare(s.db, agent.PruneOptions{
		TaskIDs:  ids,
		WtRoot:   filepath.Join(db.DataDir(), "worktrees"),
		Projects: projects,
		ResolveRepoDir: func(t *model.Task) string {
			return agent.ResolveDir(t, cfg)
		},
		Runner: s.runner,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "", err)
		return
	}
	plan.Run(nil)

	for _, t := range plan.Pruned {
		events.Emit(model.EventTypeTaskDeleted, t.ID, nil)
	}
	uxlog.Log("[api] cleanup-candidates clean: scope=%s requested=%d cleaned=%d skippedHeraBound=%d",
		req.Scope, len(ids), len(plan.Pruned), plan.SkippedHeraBound)

	writeJSON(w, http.StatusOK, map[string]any{
		"cleaned": len(plan.Pruned),
		"skipped": plan.SkippedHeraBound,
	})
}
