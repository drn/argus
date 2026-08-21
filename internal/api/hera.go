package api

import (
	"net/http"
	"sort"

	heramodel "github.com/drn/argus/internal/hera/model"
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
	// NeedsInput (add-mac-hera-rail-toggle) mirrors the same daemon-authoritative
	// idle-detection signal that drives GET /api/tasks and the SSE events
	// stream — sourced from heramodel.RoleView.NeedsInput, itself fed by
	// Server.sessionStateMaps' needsInputSet in handleHera below.
	NeedsInput bool `json:"needs_input"`
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
	// recursive subtree total; reproducing that walk here would require a
	// different (recursive) query shape than this endpoint's per-orchestrator
	// scope. A true cross-orchestrator recursive total for remote-mode/REST
	// consumers is a named follow-up, not shipped in this change. Zero
	// (omitted) means never measured.
	SubtreeCostUSD float64 `json:"subtree_cost_usd,omitempty"`
	// BridgeParentOrchID / BridgeParentRoleID (add-mac-hera-rail-toggle) are
	// both null when this orchestrator is top-level, and identify the parent
	// orchestrator/role when it is nested beneath another orchestrator's
	// worker→coordinator bridge (or a coordinator-spawned sub-team sharing one
	// coordinator agent). Computed via heramodel.Model.BridgeParentOf — the
	// same bridging logic the TUI rail nests by — never a REST-local
	// reimplementation.
	BridgeParentOrchID *int64 `json:"bridge_parent_orch_id"`
	BridgeParentRoleID *int64 `json:"bridge_parent_role_id"`
	// SubtreeNeedsInput (add-mac-hera-rail-toggle) is true when any role in
	// this orchestrator's subtree — including nested sub-orchestrators reached
	// via bridges — currently needs input. Sourced directly from
	// heramodel.OrchView.SubtreeNeedsInput (BuildModel's own rollup pass), not
	// recomputed here.
	SubtreeNeedsInput bool `json:"subtree_needs_input"`
}

// heraJSON is the full read-only snapshot the webapp Hera tab renders. The SPA
// groups orchestrators into Pinned / Active / Archived sections from the flags.
type heraJSON struct {
	Orchestrators []heraOrchJSON `json:"orchestrators"`
	Freelance     []heraRoleJSON `json:"freelance"`
}

// handleHera returns the Hera orchestration roster (orchestrators → roles, plus
// freelance roles). Nesting/bridging (BridgeParentOrchID/RoleID,
// SubtreeNeedsInput) and every role's structural/status/task fields are
// computed by heramodel.BuildModel — the SAME shared, tview-free package the
// native TUI rail calls — so this handler never reimplements that walk; only
// the persisted cost/token figures (which BuildModel doesn't carry raw token
// breakdowns for) are read directly from the store here.
//
// Read-only and soft-fail: a missing role-status row leaves Status "" (normal,
// no status yet); ready_to_close and bound-task lookups are best-effort and
// degrade to unset fields rather than failing the request.
func (s *Server) handleHera(w http.ResponseWriter, r *http.Request) {
	runningSet, idleSet, needsInputSet := s.sessionStateMaps()
	// sustainedActive has no daemon-authoritative equivalent outside the TUI's
	// own per-tick agent.ResumeActivityTick debounce (App-level, in-memory) —
	// passing nil is the documented, accepted cosmetic gap (design.md D2): it
	// only affects a rail-dimming nuance in the TUI, never correctness.
	m, err := heramodel.BuildModel(s.db, needsInputSet, idleSet, runningSet, nil)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build hera model", err)
		return
	}

	out := heraJSON{Orchestrators: []heraOrchJSON{}, Freelance: []heraRoleJSON{}}

	// Group hoisted freelance roles by their owning orchestrator so each
	// orchestrator's per-orchestrator cost/token fetch below can also price
	// its own freelance roles (freelance cost still counts toward its
	// orchestrator's subtree_cost_usd, even though the role itself renders in
	// the top-level Freelance list — preserves this endpoint's pre-existing
	// behavior).
	freelanceByOrch := make(map[int64][]heramodel.RoleView)
	for _, rv := range m.Freelance {
		freelanceByOrch[rv.OrchID] = append(freelanceByOrch[rv.OrchID], rv)
	}

	for _, sec := range [][]heramodel.OrchView{m.Pinned, m.Active, m.Archived} {
		for _, o := range sec {
			oj := buildHeraOrchJSON(&m, o)

			// Token-breakdown fields aren't part of heramodel.RoleView (BuildModel
			// only carries the already-priced CostUSDAccrued); one bulk read per
			// orchestrator, mirroring the cost query's own convention. Non-fatal
			// on error — roles just render tokenless.
			tokensByRole, tokErr := s.db.SumHeraRoleRawTokensByOrchestrator(o.ID)
			if tokErr != nil {
				uxlog.Log("[api] sum role raw tokens failed for orch %d, rendering tokenless: %v", o.ID, tokErr)
			}

			for _, rv := range o.Roles {
				rj := heraRoleJSONFrom(rv)
				if tokErr == nil {
					if t, ok := tokensByRole[rv.RoleID]; ok {
						rj.TokensInput = t.TokensInput
						rj.TokensCacheWrite1h = t.TokensCacheWrite1h
						rj.TokensCacheWrite5m = t.TokensCacheWrite5m
						rj.TokensCacheRead = t.TokensCacheRead
						rj.TokensOutput = t.TokensOutput
					}
				}
				oj.Roles = append(oj.Roles, rj)
			}
			for _, rv := range freelanceByOrch[o.ID] {
				rj := heraRoleJSONFrom(rv)
				if tokErr == nil {
					if t, ok := tokensByRole[rv.RoleID]; ok {
						rj.TokensInput = t.TokensInput
						rj.TokensCacheWrite1h = t.TokensCacheWrite1h
						rj.TokensCacheWrite5m = t.TokensCacheWrite5m
						rj.TokensCacheRead = t.TokensCacheRead
						rj.TokensOutput = t.TokensOutput
					}
				}
				out.Freelance = append(out.Freelance, rj)
			}

			out.Orchestrators = append(out.Orchestrators, oj)
		}
	}

	sort.SliceStable(out.Freelance, func(i, j int) bool {
		return out.Freelance[i].Name < out.Freelance[j].Name
	})

	writeJSON(w, http.StatusOK, out)
}

// buildHeraOrchJSON projects one heramodel.OrchView into a heraOrchJSON,
// including its already-priced subtree_cost_usd (own roles + nuked siblings,
// no recursion — see the field's doc comment) and its bridge-parent/
// needs-input rollup resolved against the whole model m.
func buildHeraOrchJSON(m *heramodel.Model, o heramodel.OrchView) heraOrchJSON {
	oj := heraOrchJSON{
		ID:                o.ID,
		Name:              o.Name,
		Pinned:            o.Pinned,
		Archived:          o.Archived,
		KanbanStatus:      string(o.KanbanStatus),
		Roles:             []heraRoleJSON{},
		SubtreeCostUSD:    o.NukedRolesCostUSD,
		SubtreeNeedsInput: o.SubtreeNeedsInput,
	}
	for _, rv := range o.Roles {
		oj.SubtreeCostUSD += rv.CostUSDAccrued
	}
	for _, rv := range m.Freelance {
		if rv.OrchID == o.ID {
			oj.SubtreeCostUSD += rv.CostUSDAccrued
		}
	}
	if parentOrchID, parentRoleID, ok := m.BridgeParentOf(o.ID); ok {
		oj.BridgeParentOrchID = &parentOrchID
		oj.BridgeParentRoleID = &parentRoleID
	}
	return oj
}

// heraRoleJSONFrom projects one heramodel.RoleView (already resolved by
// BuildModel — status, bound task, ready_to_close, needs_input) into a
// heraRoleJSON. Token fields are populated by the caller, which has the
// per-orchestrator token map this function doesn't have access to.
func heraRoleJSONFrom(rv heramodel.RoleView) heraRoleJSON {
	rj := heraRoleJSON{
		RoleID:       rv.RoleID,
		OrchID:       rv.OrchID,
		Name:         rv.Name,
		Kind:         string(rv.Kind),
		Status:       string(rv.Status),
		TaskID:       rv.TaskID,
		TaskName:     rv.TaskName,
		TaskStatus:   rv.TaskStatus,
		Live:         rv.Live,
		ReadyToClose: rv.ReadyToClose,
		Archived:     rv.Archived,
		NeedsInput:   rv.NeedsInput,
	}
	if rv.CostUSDAccrued != 0 {
		rj.CostUSD = rv.CostUSDAccrued
	}
	return rj
}
