package api

import (
	"net/http"
	"sort"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// heraRoleJSON is the read-only projection of one hera role for the webapp's
// Hera tab. It mirrors the fields the TUI rail's RoleView carries (status,
// bound task, ready_to_close) plus the bound task's name/status so the SPA can
// render a roster row and drill into the task without a second round-trip.
type heraRoleJSON struct {
	RoleID       int64  `json:"role_id"`
	OrchID       int64  `json:"orch_id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`           // coordinator | worker | freelance
	Status       string `json:"status"`         // idle | working | blocked | done; "" when no status row
	TaskID       string `json:"task_id"`        // live binding's argus task, or "" when unbound
	TaskName     string `json:"task_name"`      // bound task's display name, or ""
	TaskStatus   string `json:"task_status"`    // bound task's workflow status, or ""
	Live         bool   `json:"live"`           // has a live binding
	ReadyToClose bool   `json:"ready_to_close"` // bound task carries meta:hera.ready_to_close=true
	Archived     bool   `json:"archived"`       // role archived_at set
	// TokensInput..TokensOutput and CostUSD (add-coordinator-cost-estimate)
	// are sourced directly from persisted, already-priced values
	// (hera_bindings) — this handler never computes or reprices cost. Zero
	// (omitted) means never measured, per design.md Decision 6 — never a
	// fabricated "$0.00".
	TokensInput        int64   `json:"tokens_input,omitempty"`
	TokensCacheWrite1h int64   `json:"tokens_cache_write_1h,omitempty"`
	TokensCacheWrite5m int64   `json:"tokens_cache_write_5m,omitempty"`
	TokensCacheRead    int64   `json:"tokens_cache_read,omitempty"`
	TokensOutput       int64   `json:"tokens_output,omitempty"`
	CostUSD            float64 `json:"cost_usd,omitempty"`
}

// heraOrchJSON is one orchestrator with its non-freelance roles (coordinator +
// workers). Freelance-kind roles are hoisted into the top-level freelance list,
// mirroring the TUI Model partition.
type heraOrchJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Pinned   bool   `json:"pinned"`
	Archived bool   `json:"archived"`
	// KanbanStatus (add-hera-kanban-status) is the independent, operator-set
	// kanban axis (active/backlog/blocked/done, default active) — read-only
	// here; emitted as-is regardless of nesting (this endpoint does not
	// resolve canonical parents). See db.HeraKanbanStatus.
	KanbanStatus string         `json:"kanban_status"`
	Roles        []heraRoleJSON `json:"roles"`
	// SubtreeCostUSD (add-coordinator-cost-estimate) sums this orchestrator's
	// OWN roles' cost (all kinds, including nuked ones) — NOT a recursive
	// walk into nested sub-coordinators reached via the worker→coordinator
	// bridge. The TUI's LOCAL-mode rollup (Model.SubtreeCostUSD) IS the full
	// recursive subtree total; reproducing that walk here would require
	// importing internal/tui/hera, which this handler deliberately avoids
	// (see handleHera's doc comment — "free of TUI deps"). A true
	// cross-orchestrator recursive total for remote-mode/REST consumers is a
	// named follow-up, not shipped in this change. Zero (omitted) means
	// never measured.
	SubtreeCostUSD float64 `json:"subtree_cost_usd,omitempty"`
}

// heraJSON is the full read-only snapshot the webapp Hera tab renders. The SPA
// groups orchestrators into Pinned / Active / Archived sections from the flags.
type heraJSON struct {
	Orchestrators []heraOrchJSON `json:"orchestrators"`
	Freelance     []heraRoleJSON `json:"freelance"`
}

// handleHera returns the Hera orchestration roster (orchestrators → roles, plus
// freelance roles). It mirrors hera.BuildModel's read logic but emits JSON
// directly from the db methods so the API package stays free of TUI deps.
//
// Read-only and soft-fail: a missing role-status row leaves Status "" (normal,
// no status yet); ready_to_close and bound-task lookups are best-effort and
// degrade to unset fields rather than failing the request.
func (s *Server) handleHera(w http.ResponseWriter, r *http.Request) {
	orchs, err := s.db.ListHeraOrchestrators(true) // include archived
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load orchestrators", err)
		return
	}

	bindings, err := s.db.ListHeraLiveBindings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load bindings", err)
		return
	}
	roleToTask := make(map[int64]string, len(bindings))
	for _, b := range bindings {
		roleToTask[b.RoleID] = b.ArgusTaskID
	}

	// meta:hera.ready_to_close lives in the task-addressed task_meta sidecar;
	// one batch read covers every flagged task. Non-fatal — the flag just
	// won't render on a read error.
	heraMeta, _ := s.db.ListMetaByNamespace(db.HeraMetaNamespace)

	// Task snapshot keyed by ID so each bound role can carry its task's name +
	// status. Non-fatal — bound rows just render without those fields.
	taskByID := make(map[string]*model.Task)
	if tasks, terr := s.db.Tasks(); terr == nil {
		for _, t := range tasks {
			taskByID[t.ID] = t
		}
	}

	out := heraJSON{Orchestrators: []heraOrchJSON{}, Freelance: []heraRoleJSON{}}
	for _, o := range orchs {
		oj := heraOrchJSON{
			ID:           o.ID,
			Name:         o.Name,
			Pinned:       o.PinnedAt != nil,
			Archived:     o.ArchivedAt != nil,
			KanbanStatus: string(o.KanbanStatus),
			Roles:        []heraRoleJSON{},
		}
		roles, rerr := s.db.ListHeraRoles(o.ID, true) // include archived roles
		if rerr != nil {
			writeErr(w, http.StatusInternalServerError, "failed to load roles", rerr)
			return
		}
		// Token-cost accrual (add-coordinator-cost-estimate): one bulk read per
		// orchestrator, mirroring hera.BuildModel's own convention for this same
		// data. Non-fatal on error — roles just render costless.
		costByRole, cerr := s.db.SumHeraRoleCostAccruedByOrchestrator(o.ID)
		if cerr != nil {
			uxlog.Log("[api] sum role cost accrued failed for orch %d, rendering costless: %v", o.ID, cerr)
		}
		tokensByRole, tokErr := s.db.SumHeraRoleRawTokensByOrchestrator(o.ID)
		if tokErr != nil {
			uxlog.Log("[api] sum role raw tokens failed for orch %d, rendering tokenless: %v", o.ID, tokErr)
		}
		nukedCost, nerr := s.db.SumNukedHeraRolesCostByOrchestrator(o.ID)
		if nerr == nil {
			oj.SubtreeCostUSD = nukedCost
		} else {
			uxlog.Log("[api] sum nuked role cost failed for orch %d: %v", o.ID, nerr)
		}
		for _, role := range roles {
			rj := s.buildHeraRoleJSON(role, roleToTask, heraMeta, taskByID)
			if cerr == nil {
				if cost := costByRole[role.ID]; cost != 0 {
					rj.CostUSD = cost
					oj.SubtreeCostUSD += cost
				}
			}
			if tokErr == nil {
				if t, ok := tokensByRole[role.ID]; ok {
					rj.TokensInput = t.TokensInput
					rj.TokensCacheWrite1h = t.TokensCacheWrite1h
					rj.TokensCacheWrite5m = t.TokensCacheWrite5m
					rj.TokensCacheRead = t.TokensCacheRead
					rj.TokensOutput = t.TokensOutput
				}
			}
			// Active freelance roles live in their own top-level section,
			// mirroring the TUI Model partition.
			if role.Kind == db.HeraKindFreelance && role.ArchivedAt == nil && o.ArchivedAt == nil {
				out.Freelance = append(out.Freelance, rj)
				continue
			}
			oj.Roles = append(oj.Roles, rj)
		}
		out.Orchestrators = append(out.Orchestrators, oj)
	}

	sort.SliceStable(out.Freelance, func(i, j int) bool {
		return out.Freelance[i].Name < out.Freelance[j].Name
	})

	writeJSON(w, http.StatusOK, out)
}

// buildHeraRoleJSON projects one db.HeraRole into a heraRoleJSON, resolving its
// live binding's task, status row, and ready_to_close flag.
func (s *Server) buildHeraRoleJSON(role *db.HeraRole, roleToTask map[int64]string, heraMeta map[string]map[string]string, taskByID map[string]*model.Task) heraRoleJSON {
	rj := heraRoleJSON{
		RoleID:   role.ID,
		OrchID:   role.OrchestratorID,
		Name:     role.Name,
		Kind:     string(role.Kind),
		Archived: role.ArchivedAt != nil,
	}
	if st, serr := s.db.HeraRoleStatusFor(role.ID); serr == nil && st != nil {
		rj.Status = string(st.Status)
	}
	if taskID, ok := roleToTask[role.ID]; ok {
		rj.TaskID = taskID
		rj.Live = true
		if kv := heraMeta[taskID]; kv != nil && kv[db.HeraMetaKeyReadyToClose] == "true" {
			rj.ReadyToClose = true
		}
		if t := taskByID[taskID]; t != nil {
			rj.TaskName = t.Name
			rj.TaskStatus = t.Status.String()
		}
	}
	return rj
}
