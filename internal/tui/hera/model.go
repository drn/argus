package hera

import (
	"errors"
	"sort"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// RoleView is the read-only render projection of one hera role plus the live
// state the rail needs: its status (idle/working/blocked/done), the argus task
// its live binding points at (empty when unbound), and whether that task is
// flagged meta:hera.ready_to_close (M4 — a finished worker awaiting close-out).
type RoleView struct {
	RoleID       int64
	OrchID       int64
	Name         string
	Kind         db.HeraRoleKind
	Status       db.HeraRoleStatusValue // "" when no status row exists
	HasStatus    bool
	TaskID       string // live binding's argus task, or "" when unbound
	Live         bool   // has a live binding
	ReadyToClose bool   // bound task carries meta:hera.ready_to_close=true
	Archived     bool   // role archived_at set
	// Pinned is true when the role's hera_roles.pinned_at is set. A pinned
	// non-root role FLOATS out of its parent subtree into the rail's Pinned
	// section as a two-line breadcrumb entry (rail.collectPinnedRoles). Pin and
	// archive are mutually exclusive (db.PinHeraRole clears archived_at). Without
	// this projection the existing P→Ops.PinToggle→db.PinHeraRole path stamped
	// pinned_at yet rendered nothing — a silent no-op (add-hera-pin-nonroot).
	Pinned bool
	// NeedsInput is the role's OWN needs-input signal: its live binding's argus
	// task is currently blocked on a user prompt per the SAME authoritative set
	// the task list consumes (App.needsInputIDs — the idle-gated, sticky
	// agent.DetectNeedsInput PTY-tail scan), threaded into BuildModel. Only a live
	// role can be set (the set is keyed by live task IDs). Native Hera previously
	// had NO per-role needs-input flag, so a worker genuinely blocked on a prompt
	// did not even show "(?)" on itself; this restores that and feeds the rollup
	// below (BUG-018).
	NeedsInput bool
	// SubtreeNeedsInput is the needs-input ROLLUP computed by BuildModel's
	// post-pass: true when this role itself OR any descendant role in its
	// orchestration subtree (transitively across BRIDGED sub-orchestrators) needs
	// input. For a coordinator role it covers the whole orchestrator subtree; for
	// a bridging worker row it covers the bridged child's subtree; for a leaf /
	// freelance role it equals the role's own needs-input signal. statusIcon reads
	// it (via ShowsNeedsInput) so it stays a pure projection (BUG-018).
	SubtreeNeedsInput bool
	// BridgeTaskID is the role's LATEST binding argus task regardless of liveness
	// (== TaskID when Live). It is the STRUCTURAL nesting key: a worker bridges a
	// child orchestrator when its BridgeTaskID equals that child's coordinator's
	// BridgeTaskID, even after the binding ended. "" when the role never bound.
	BridgeTaskID string
	// LinkEndReason carries the latest binding's end_reason when the role is NOT
	// live ("" when live). A bridge is honoured unless this is an operator
	// teardown (reparented / user_deleted) — see db.HeraEndReasonIsTeardown.
	LinkEndReason string
	// TaskStatus / TaskResult are the bound argus task's workflow status
	// ("in_progress"/"complete"/…) and opaque result JSON. They feed the
	// orchestration-tree DAG's node colour + failed glyph (the rail's own status
	// icons use the hera role Status above, not these). Empty when unbound.
	TaskStatus string
	TaskResult string

	// Planned discriminates a PLAN node that has never been materialized: a
	// worker-kind role with no live binding AND no binding ever (BridgeTaskID==""),
	// i.e. the substrate's planned-vs-materialized split (HeraRoleHasBinding)
	// projected to the UI. The plan view colours a planned node violet ○ (D7); a
	// bound-but-finished role (binding ended) keeps its task-status colour and is
	// NOT planned. Stage 2/3 populates this in buildRoleView.
	Planned bool

	// Prompt is the role's verbatim delivery prompt (persisted on the hera role at
	// plan/spawn time). The plan view's master-detail header shows its first line
	// as the node Description (D9). Empty when the role carries no prompt.
	Prompt string

	// The following fields feed the coordinator Details metadata block
	// (deriveCoordMeta). They are additive projection inputs read straight from
	// the hera store at BuildModel time, so the Details pane stays a pure
	// projection renderer (no Draw-time I/O). The rail itself ignores them.
	CreatedAt        time.Time // role creation time
	ArgusProject     string    // role's argus project (repos-in-scope input)
	WorktreePath     string    // live binding's worktree path ("" when unbound)
	BindingStartedAt time.Time // live binding's start time (zero when unbound)
	StatusUpdatedAt  time.Time // role-status row's last update (zero when none)
	TaskName         string    // bound argus task's name ("" when unbound)
}

// IsActive reports whether the role is genuinely producing output right now: it
// holds a live binding AND its bound argus task is in_progress. This is the
// honest "working" signal the rail spinner animates on — NOT the hera role
// Status field, which is a manual/MCP-set ladder value that goes stale (it stays
// "working" after the session idles, stops, or dies, because nothing reconciles
// it down). Mirrors the plugin's stateGlyph, which animates the spinner only on
// a KNOWN in_progress + running argus state. Native has no per-task idle /
// needs-input flag (the plugin sourced those from the API), so a live in_progress
// task that is sitting idle still counts as active; the stale-status cases the
// bug reported (stopped / dead / days-old sessions) are excluded because they
// are either not Live or no longer in_progress. See BUG-003.
func (r *RoleView) IsActive() bool {
	return r.Live && r.TaskStatus == model.StatusInProgress.String()
}

// needsInputOwn reports the role's OWN needs-input signal — what makes a LEAF
// row render the "(?)" attention glyph: the authoritative per-task needs-input
// flag (NeedsInput) OR the role's self-asserted hera `blocked` status. This is
// the unit the BuildModel rollup aggregates over a subtree.
func (r *RoleView) needsInputOwn() bool {
	return r.NeedsInput || (r.HasStatus && r.Status == db.HeraStatusBlocked)
}

// ShowsNeedsInput reports whether this row renders the needs-input "(?)" glyph:
// the BuildModel subtree rollup (SubtreeNeedsInput, which already folds in this
// role's own signal) OR — as a safety net for hand-built RoleViews that never
// ran BuildModel's post-pass — the role's own needs-input signal directly.
// statusIcon reads this so it stays a pure projection (BUG-018).
func (r *RoleView) ShowsNeedsInput() bool {
	return r.SubtreeNeedsInput || r.needsInputOwn()
}

// OrchView is the render projection of one orchestrator and its non-freelance
// roles (coordinator + worker). Freelance-kind roles are hoisted into the
// Model's Freelance section rather than nested here.
type OrchView struct {
	ID        int64
	Name      string
	Pinned    bool
	Archived  bool
	CreatedAt time.Time // orchestrator creation time (coordinator Details "Created")
	Roles     []RoleView
	// Blocks are the orchestrator's hera_blocks blocking edges (one bulk read per
	// BuildModel via HeraReader.ListHeraBlocks). The plan-view projection
	// (heraPlanNodes) turns these into planview.Edge dependency edges. Stage 2
	// populates this in BuildModel.
	Blocks []db.HeraBlock
}

// Model is the full read-only snapshot the rail renders. Orchestrators are
// partitioned into the rail's sections; Freelance aggregates freelance-kind
// roles across all active orchestrators.
//
// Multi-binding fan-out is structural and automatic: a single argus task bound
// under two orchestrators is reached through two DISTINCT roles (one per
// orchestrator), so it surfaces once under EACH orchestrator. No special case
// is needed — the per-orchestrator role walk produces the fan-out the locked
// design (Q2/Q3) and the smoke test require.
type Model struct {
	Pinned    []OrchView // pinned orchestrators (pinned_at set)
	Active    []OrchView // active, non-pinned orchestrators
	Archived  []OrchView // archived orchestrators (archived_at set)
	Freelance []RoleView // freelance-kind roles across active orchestrators
}

// IsEmpty reports whether the snapshot has no content at all (used to render
// the empty-state placeholder).
func (m Model) IsEmpty() bool {
	return len(m.Pinned) == 0 && len(m.Active) == 0 &&
		len(m.Archived) == 0 && len(m.Freelance) == 0
}

// managedTaskIDs returns the set of argus task ids the Hera model knows: every
// role's live binding (TaskID) and latest-binding structural key (BridgeTaskID),
// across the Pinned/Active/Archived orchestrator sections AND the Freelance
// section. Collecting BridgeTaskID as well as TaskID keeps the set correct when a
// role's binding has ended while its task is still running. Empty ids are skipped.
func (m Model) managedTaskIDs() map[string]bool {
	ids := make(map[string]bool)
	add := func(roles []RoleView) {
		for i := range roles {
			if id := roles[i].TaskID; id != "" {
				ids[id] = true
			}
			if id := roles[i].BridgeTaskID; id != "" {
				ids[id] = true
			}
		}
	}
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			add(sec[i].Roles)
		}
	}
	add(m.Freelance)
	return ids
}

// UnmanagedNeedsInputCount returns how many needs-input tasks have NO presence in
// the Hera model: |needsInput − managedTaskIDs|. These are the tasks invisible
// from the Hera tab (plain Tasks-tab tasks never bound to any orchestrator) — the
// only ones the Hera-view attention summary box reports, since managed roles
// (coordinators, workers, freelance-roles), including those whose subtree row is
// folded, already carry their own needs-input cue in the rail.
func (m Model) UnmanagedNeedsInputCount(needsInput map[string]bool) int {
	if len(needsInput) == 0 {
		return 0
	}
	managed := m.managedTaskIDs()
	n := 0
	for id := range needsInput {
		if !managed[id] {
			n++
		}
	}
	return n
}

// OrchByID finds the OrchView with the given id across every non-freelance
// section, returning a pointer into the model's backing array (so callers read
// the live projection, never a copy), or nil when not found. Used to resolve a
// selected role's containing orchestrator — the disambiguator that makes a
// multi-binding task's two roles feed two different pane contexts.
func (m *Model) OrchByID(id int64) *OrchView {
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if sec[i].ID == id {
				return &sec[i]
			}
		}
	}
	return nil
}

// CoordTaskID returns the live argus task of this orchestrator's coordinator
// role, or "" when no coordinator role holds a live binding. The HERA pane
// feeds from this so it always shows the orchestrator's coordinator session,
// regardless of which role under the orchestrator is selected (the coord-vs-
// agent session rule — see panes.go).
func (o *OrchView) CoordTaskID() string {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator && o.Roles[i].Live {
			return o.Roles[i].TaskID
		}
	}
	return ""
}

// CoordBridgeTaskID returns this orchestrator's coordinator role's STRUCTURAL
// bridge task (its latest binding regardless of liveness), or "" when no
// coordinator role ever bound. Unlike CoordTaskID (live-only, gated so the COORD
// pane never binds a tombstone), this survives a dormant/finished coordinator so
// a sub-orchestrator still nests under its parent after the coordinator's task
// completed (the bridging-breadth rule). First coordinator role wins.
func (o *OrchView) CoordBridgeTaskID() string {
	t, _ := o.coordBridge()
	return t
}

// coordBridge returns the structural bridge task AND the role id of the SAME
// coordinator role that supplies it — the first coordinator with a non-empty
// bridge task. Both coordinator-resolution callers (CoordBridgeTaskID and
// coordBridgeParentOf) go through this so the bridge task and the cycle-break
// role id never come from different coordinator roles in the (defensive)
// multi-coordinator case. Returns ("", 0) when no coordinator ever bound.
func (o *OrchView) coordBridge() (taskID string, roleID int64) {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator {
			if k := bridgeTaskID(&o.Roles[i]); k != "" {
				return k, o.Roles[i].RoleID
			}
		}
	}
	return "", 0
}

// bridgeIndex maps each orchestrator's coordinator bridge task to the
// orchestrator it coordinates (first wins — a coord task is unique to one
// orchestrator in practice). A worker whose bridge task matches a key IS that
// orchestrator's coordinator, so the keyed orchestrator nests under the worker.
// Pointers index into the receiver's backing arrays (shared with the caller's
// model), so the result is stable for synchronous use on the UI thread.
func (m Model) bridgeIndex() map[string]*OrchView {
	idx := make(map[string]*OrchView)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if k := sec[i].CoordBridgeTaskID(); k != "" {
				if _, dup := idx[k]; !dup {
					idx[k] = &sec[i]
				}
			}
		}
	}
	return idx
}

// consumedSet marks every orchestrator that is bridged as a child by some OTHER
// orchestrator, so the rail's top-level passes skip it (it renders nested
// instead). Two bridge shapes consume a child:
//   - worker bridge: a parent WORKER role whose (non-teardown) latest binding
//     task equals the child's coordinator bridge task (a spawned worker that
//     became a sub-coordinator);
//   - coordinator-spawned sub-team: parent and child share the SAME coordinator
//     bridge task (one coordinator agent runs both — what hera_new_orchestrator
//     creates), with the earlier coordinator-role-id orchestrator the parent.
//
// The coordinator path is the fix for the rail under-nesting bug: db.SubtreeOrchIDs
// matches ANY non-teardown parent-side binding (coordinator OR worker), but the
// in-memory bridge previously only honoured worker rows, so coordinator-spawned
// sub-teams rendered flat as extra top-level roots.
func (m Model) consumedSet(bridge map[string]*OrchView) map[int64]bool {
	consumed := make(map[int64]bool)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			p := &sec[i]
			for j := range p.Roles {
				w := &p.Roles[j]
				if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
					continue
				}
				if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != p.ID {
					consumed[c.ID] = true
				}
			}
			for _, c := range m.coordBridgeChildren(p) {
				consumed[c.ID] = true
			}
		}
	}
	return consumed
}

// coordBridgeParentOf reports whether parent nests child via the
// coordinator-spawned sub-team shape: both orchestrators' coordinator roles
// bridge the SAME argus task (one coordinator agent runs both — the multi-orch
// coordinator that hera_new_orchestrator creates), and parent's coordinator role
// was created first (lower role id = the parent; the later one is the spawned
// sub-team). This breaks the A↔B symmetry of the shared-task bridge — db.SubtreeOrchIDs
// would include each from the other, so the rail picks the earliest coordinator
// role id as the single root. A non-teardown latest binding is implied by
// CoordBridgeTaskID using bridgeTaskID; a torn-down coordinator yields an empty
// bridge task and so cannot match.
func coordBridgeParentOf(parent, child *OrchView) bool {
	pt, pid := parent.coordBridge()
	ct, cid := child.coordBridge()
	if pt == "" || pt != ct {
		return false
	}
	return pid < cid
}

// coordBridgeChildren returns the orchestrators that nest directly under o
// because o is the earliest-coordinator-role-id member of a set sharing o's
// coordinator bridge task. Archived children are excluded (mirroring
// db.SubtreeOrchIDs' `child_orch.archived_at IS NULL` prune — they surface in
// the bottom Archive section instead, distinct from worker-bridged archived
// children which nest dimmed in place for backward compatibility).
func (m Model) coordBridgeChildren(o *OrchView) []*OrchView {
	var out []*OrchView
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			c := &sec[i]
			if c.ID == o.ID || c.Archived {
				continue
			}
			if coordBridgeParentOf(o, c) {
				out = append(out, c)
			}
		}
	}
	return out
}

// canonParent identifies a child orchestrator's SINGLE canonical parent for rail
// nesting: the orchestrator id that renders it, plus whether the relationship is
// the coordinator-spawn shape (child nests as its own header via
// coordBridgeChildren) or the worker-bridge shape (child nests under a worker row).
type canonParent struct {
	orchID     int64
	coordSpawn bool
}

// canonicalParents assigns every child orchestrator ONE deterministic parent,
// independent of rail collapse state and sibling render order. A child reachable
// from more than one bridge-parent (the multi-binding case where a coordinator
// session is BOTH a worker under one orchestrator and the coordinator-spawn of
// another) would otherwise render under whichever parent the build reached first,
// so folding that parent relocated the whole subtree to the other parent. Fixing
// the parent up front makes placement fold-independent — folding a non-canonical
// parent never migrates the child, and folding the canonical parent just hides it.
//
// The rule (stable, order-free): prefer a coordinator-spawn parent over a
// worker-bridge parent; among coordinator-spawn candidates the EARLIEST
// coordinator role id wins (the root of the shared-coordinator clique, matching
// coordBridgeParentOf's direction); among worker-bridge candidates the lowest
// orchestrator id wins. The result is a function (one parent per child), so the
// render can never put a child under two parents or migrate it between them.
//
// This is purely a RENDER-placement decision: it does not change which
// orchestrators are reachable in a subtree (consumedSet / BridgeSubtree still walk
// every bridge for root selection and the Ctrl+D cascade), only which single
// parent row hosts a multi-parent child.
func (m Model) canonicalParents() map[int64]canonParent {
	var all []*OrchView
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			all = append(all, &sec[i])
		}
	}
	out := make(map[int64]canonParent)
	for _, c := range all {
		// 1. Coordinator-spawn parent (preferred): the earliest-coordinator-role-id
		// orchestrator sharing c's coordinator bridge task.
		var coordParent *OrchView
		var coordParentRole int64
		for _, p := range all {
			if p.ID == c.ID || !coordBridgeParentOf(p, c) {
				continue
			}
			if _, pid := p.coordBridge(); coordParent == nil || pid < coordParentRole {
				coordParent, coordParentRole = p, pid
			}
		}
		if coordParent != nil {
			out[c.ID] = canonParent{orchID: coordParent.ID, coordSpawn: true}
			continue
		}
		// 2. Worker-bridge parent: the lowest-orchestrator-id orchestrator with a
		// worker role bridging c's coordinator bridge task.
		ck := c.CoordBridgeTaskID()
		if ck == "" {
			continue
		}
		var workerParent *OrchView
		for _, p := range all {
			if p.ID == c.ID || !p.hasWorkerBridging(ck) {
				continue
			}
			if workerParent == nil || p.ID < workerParent.ID {
				workerParent = p
			}
		}
		if workerParent != nil {
			out[c.ID] = canonParent{orchID: workerParent.ID, coordSpawn: false}
		}
	}
	return out
}

// hasWorkerBridging reports whether the orchestrator has a non-coordinator role
// whose structurally-intact bridge task equals ck (the worker-bridge nesting key).
func (o *OrchView) hasWorkerBridging(ck string) bool {
	for i := range o.Roles {
		w := &o.Roles[i]
		if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
			continue
		}
		if bridgeTaskID(w) == ck {
			return true
		}
	}
	return false
}

// BridgeSubtree returns the orchestrator with id rootID and every orchestrator
// nested beneath it through the worker→coordinator bridge, inclusive, in
// pre-order. Cycle-safe (visited set). Used by the rail's Ctrl+D cascade to tear
// down a whole nested sub-team. Empty when rootID is unknown.
func (m Model) BridgeSubtree(rootID int64) []*OrchView {
	start := m.OrchByID(rootID)
	if start == nil {
		return nil
	}
	bridge := m.bridgeIndex()
	var out []*OrchView
	visited := make(map[int64]bool)
	var walk func(o *OrchView)
	walk = func(o *OrchView) {
		if o == nil || visited[o.ID] {
			return
		}
		visited[o.ID] = true
		out = append(out, o)
		for i := range o.Roles {
			w := &o.Roles[i]
			if w.Kind == db.HeraKindCoordinator || !roleBridges(w) {
				continue
			}
			if c := bridge[bridgeTaskID(w)]; c != nil && c.ID != o.ID {
				walk(c)
			}
		}
		for _, c := range m.coordBridgeChildren(o) {
			walk(c)
		}
	}
	walk(start)
	return out
}

// bridgeTaskID returns a role's structural bridge key: its latest-binding task
// (BridgeTaskID), falling back to the live TaskID when the model did not
// populate the bridge field (older callers / hand-built test fixtures). In
// production BuildModel always sets BridgeTaskID, so the fallback only matters
// for fixtures that set TaskID alone.
func bridgeTaskID(r *RoleView) string {
	if r.BridgeTaskID != "" {
		return r.BridgeTaskID
	}
	return r.TaskID
}

// roleBridges reports whether a role's parent link is structurally intact for
// nesting: it bridges when live, or when its latest binding ended for a
// non-teardown reason. An operator-teardown link (reparented / user_deleted) is
// stale and must not nest its child.
func roleBridges(r *RoleView) bool {
	return r.Live || !db.HeraEndReasonIsTeardown(r.LinkEndReason)
}

// CoordRole returns this orchestrator's coordinator role, or nil. Used by the
// rail header (which folds the coordinator into itself) to read its status glyph.
func (o *OrchView) CoordRole() *RoleView {
	for i := range o.Roles {
		if o.Roles[i].Kind == db.HeraKindCoordinator {
			return &o.Roles[i]
		}
	}
	return nil
}

// Selection is the (role, orchestrator, task) context resolved from the rail
// cursor. It is the single value threaded to the pane feeds (6b) and — via
// HeraPage.SelectionContext — to the future mutation extension point (6c). The
// orchestrator is ALWAYS the disambiguator: a multi-binding task reached
// through two different roles yields two different Selections (different Role,
// different Orch), so every downstream op acts on the right binding's task.
type Selection struct {
	Role *RoleView // selected role; nil when the cursor is on an orch header
	Orch *OrchView // selected/containing orchestrator; nil when the rail is empty
	// BridgeChildOrchID is the id of the sub-orchestrator nested under this row
	// when the selected worker bridges one (0 otherwise). Pane binding and most
	// mutations ignore it (they act on the worker Role), but Ctrl+D uses it to
	// cascade-tear-down the whole nested sub-team rooted at this child.
	BridgeChildOrchID int64
}

// TaskID returns the selected role's bound argus task, or "" when none.
func (s Selection) TaskID() string {
	if s.Role == nil {
		return ""
	}
	return s.Role.TaskID
}

// FocusTaskID returns the argus task the selection's pane/reattach acts on: the
// selected role's bound task, or — for a coordinator header with no role row
// (the folded coordinator) — the orchestrator's coordinator task. "" when
// neither resolves.
func (s Selection) FocusTaskID() string {
	if t := s.TaskID(); t != "" {
		return t
	}
	if s.IsCoordinator() {
		return s.CoordTaskID()
	}
	return ""
}

// StatusRole resolves the role whose hera status the `s`/`S` keys step. It is
// the selected role when one is selected; otherwise — for a coordinator HEADER
// selection (Role nil, Orch set), since the coordinator is folded into the
// header and has no child row — the orchestrator's coordinator role. Returns
// nil when neither resolves (empty rail, or a header over a coordinator-less
// orchestrator), in which case the status step is a silent no-op. This is why a
// coordinator's `✓` can be cycled with `s`/`S` from the header (BUG-014).
func (s Selection) StatusRole() *RoleView {
	if s.Role != nil {
		return s.Role
	}
	if s.Orch != nil {
		return s.Orch.CoordRole()
	}
	return nil
}

// IsCoordinator reports whether the selection represents a coordinator. The
// right region renders the coordinator details summary (not a terminal) for a
// coordinator selection. Since the coordinator role is folded into the
// orchestrator HEADER (no separate child row), a header selection (Role nil,
// Orch set) IS a coordinator selection; an explicit coordinator-kind role still
// counts too (defensive — coordinators no longer render as their own rows).
func (s Selection) IsCoordinator() bool {
	if s.Role != nil {
		return s.Role.Kind == db.HeraKindCoordinator
	}
	return s.Orch != nil
}

// CoordTaskID returns the live coordinator task of the selected orchestrator,
// or "" when none. The HERA (middle) pane feeds from this.
func (s Selection) CoordTaskID() string {
	if s.Orch == nil {
		return ""
	}
	return s.Orch.CoordTaskID()
}

// BuildModel reads the hera store and assembles the read-only rail snapshot.
// It is pure-read: every call goes through HeraReader's List/Status/Meta
// methods, all of which are mutex-guarded and fast on *db.DB, so it is safe to
// call on the tview thread.
//
// Soft-fail discipline: a per-role status lookup that returns ErrHeraNotFound
// is normal (no status row yet) and leaves Status zero. Any other read error
// aborts and is returned so the caller can log it and keep the prior model.
// needsInput is the authoritative per-task needs-input set (the App's
// needsInputIDs — the SAME idle-gated agent.DetectNeedsInput scan the task list
// consumes), keyed by live argus task ID. nil/empty is fine (no role shows a
// needs-input flag from this source; the self-asserted hera `blocked` status
// still drives "(?)"). It feeds RoleView.NeedsInput and the subtree rollup.
func BuildModel(r HeraReader, needsInput map[string]bool) (Model, error) {
	var m Model
	if r == nil {
		return m, nil
	}

	orchs, err := r.ListHeraOrchestrators(true) // include archived
	if err != nil {
		return Model{}, err
	}

	bindings, err := r.ListHeraLiveBindings()
	if err != nil {
		return Model{}, err
	}
	// Keyed by role, carrying the whole live binding so buildRoleView can read
	// the bound task ID, the worktree path, and the binding start time (the
	// coordinator Details "Worktree" + "Last activity" inputs).
	roleToBinding := make(map[int64]*db.HeraBinding, len(bindings))
	for _, b := range bindings {
		roleToBinding[b.RoleID] = b
	}

	// Latest binding per role (live OR ended) drives the structural rail bridge:
	// a role's BridgeTaskID/LinkEndReason come from here so an ended-but-not-
	// torn-down link still nests its child. A read error is non-fatal — bridging
	// just falls back to live-binding behaviour (BridgeTaskID == live TaskID).
	roleToLatest := make(map[int64]*db.HeraBinding)
	if latest, lerr := r.ListHeraLatestBindings(); lerr == nil {
		for _, b := range latest {
			roleToLatest[b.RoleID] = b
		}
	} else {
		// This read failing silently reverts the headline latest-binding nesting to
		// live-only, so log it (CLAUDE.md: log guards that silently skip work).
		uxlog.Log("[hera-view] latest-binding read failed, falling back to live bridge: %v", lerr)
	}

	// meta:hera.ready_to_close lives in the task-addressed task_meta sidecar.
	// One batch read covers every flagged task; a read error is non-fatal
	// (the flag just won't render).
	heraMeta, _ := r.ListMetaByNamespace(db.HeraMetaNamespace)

	// Task snapshot keyed by ID so each bound role can carry its argus task's
	// status + result (the orchestration-tree DAG colours nodes by task
	// progress). A read error is non-fatal — nodes just render uncoloured.
	taskByID := make(map[string]*model.Task)
	if tasks, terr := r.Tasks(); terr == nil {
		for _, t := range tasks {
			taskByID[t.ID] = t
		}
	}

	for _, o := range orchs {
		// Defense-in-depth: a NUKED orchestrator (Tier-2 EOL, BUG-022) is invisible
		// to the rail. The DB list query already excludes nuked rows, but the model
		// skips them too so it stays self-contained for any reader that doesn't.
		if o.NukedAt != nil {
			continue
		}
		ov := OrchView{
			ID:        o.ID,
			Name:      o.Name,
			Pinned:    o.PinnedAt != nil,
			Archived:  o.ArchivedAt != nil,
			CreatedAt: o.CreatedAt,
		}
		roles, err := r.ListHeraRoles(o.ID, true) // include archived roles
		if err != nil {
			return Model{}, err
		}
		// Plan-DAG edges: one bulk read per orchestrator (D8). A read error is
		// non-fatal — the plan view just renders nodes with no dependency edges
		// (the degenerate flat-stage shape), so log and continue rather than abort
		// the whole rebuild for a missing edge table or transient error.
		if blocks, berr := r.ListHeraBlocks(o.ID); berr == nil {
			ov.Blocks = blocks
		} else {
			uxlog.Log("[hera-view] list hera blocks failed for orch %d, rendering edgeless: %v", o.ID, berr)
		}
		for _, role := range roles {
			// Same Tier-2 guard for roles — a nuked role never renders.
			if role.NukedAt != nil {
				continue
			}
			rv := buildRoleView(r, role, roleToBinding, roleToLatest, heraMeta, taskByID, needsInput)
			if role.Kind == db.HeraKindFreelance && role.ArchivedAt == nil && o.ArchivedAt == nil {
				// Active freelance roles live in their own top-level section.
				m.Freelance = append(m.Freelance, rv)
				continue
			}
			ov.Roles = append(ov.Roles, rv)
		}
		switch {
		case ov.Archived:
			m.Archived = append(m.Archived, ov)
		case ov.Pinned:
			m.Pinned = append(m.Pinned, ov)
		default:
			m.Active = append(m.Active, ov)
		}
	}

	sort.SliceStable(m.Freelance, func(i, j int) bool {
		return m.Freelance[i].Name < m.Freelance[j].Name
	})

	// Needs-input rollup: now that every OrchView/RoleView is assembled, stamp
	// each role's SubtreeNeedsInput so the rail can project "(?)" up the tree.
	m.rollupNeedsInput()
	return m, nil
}

// rollupNeedsInput populates every role's SubtreeNeedsInput from the per-role
// own needs-input signals already stamped on the assembled model. It runs as a
// post-pass (after all orchestrators/roles are built) because the rollup crosses
// BRIDGED sub-orchestrators, which only exist once the whole model is assembled.
//
// Two phases, no read-during-mutate hazard: phase 1 reads only the OWN signals
// (needsInputOwn) to compute a per-orchestrator subtree rollup; phase 2 writes
// only SubtreeNeedsInput. The traversal reuses BridgeSubtree (cycle-safe) so it
// matches rail nesting and the Ctrl+D cascade exactly. See BUG-018.
func (m *Model) rollupNeedsInput() {
	// Phase 1: per-orchestrator subtree rollup (transitive across bridges).
	subtree := make(map[int64]bool)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			subtree[sec[i].ID] = m.orchSubtreeNeedsInput(sec[i].ID)
		}
	}
	// Phase 2: stamp each role. A coordinator carries its orchestrator's whole
	// subtree (it is itself in that subtree, so its own signal is included). A
	// bridging worker row (a nested sub-coordinator) carries its bridged child's
	// subtree OR'd with its own signal. Every other role (leaf worker) carries
	// only its own signal.
	bridge := m.bridgeIndex()
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			o := &sec[i]
			for j := range o.Roles {
				rv := &o.Roles[j]
				switch {
				case rv.Kind == db.HeraKindCoordinator:
					rv.SubtreeNeedsInput = subtree[o.ID]
				case roleBridges(rv):
					if c := bridge[bridgeTaskID(rv)]; c != nil && c.ID != o.ID {
						rv.SubtreeNeedsInput = rv.needsInputOwn() || subtree[c.ID]
						continue
					}
					rv.SubtreeNeedsInput = rv.needsInputOwn()
				default:
					rv.SubtreeNeedsInput = rv.needsInputOwn()
				}
			}
		}
	}
	for i := range m.Freelance {
		m.Freelance[i].SubtreeNeedsInput = m.Freelance[i].needsInputOwn()
	}
}

// orchSubtreeNeedsInput reports whether any role in the orchestration subtree
// rooted at orchID (inclusive, transitively across bridged sub-orchestrators)
// has an OWN needs-input signal. Walks BridgeSubtree (cycle-safe visited set).
func (m *Model) orchSubtreeNeedsInput(orchID int64) bool {
	for _, o := range m.BridgeSubtree(orchID) {
		for i := range o.Roles {
			if o.Roles[i].needsInputOwn() {
				return true
			}
		}
	}
	return false
}

// buildRoleView projects one db.HeraRole into a RoleView, resolving its live
// binding's task, status row, and ready_to_close flag.
func buildRoleView(r HeraReader, role *db.HeraRole, roleToBinding map[int64]*db.HeraBinding, roleToLatest map[int64]*db.HeraBinding, heraMeta map[string]map[string]string, taskByID map[string]*model.Task, needsInput map[string]bool) RoleView {
	rv := RoleView{
		RoleID:       role.ID,
		OrchID:       role.OrchestratorID,
		Name:         role.Name,
		Kind:         role.Kind,
		Archived:     role.ArchivedAt != nil,
		Pinned:       role.PinnedAt != nil,
		CreatedAt:    role.CreatedAt,
		ArgusProject: role.ArgusProject,
		Prompt:       role.Prompt,
	}
	if b := roleToBinding[role.ID]; b != nil {
		taskID := b.ArgusTaskID
		rv.TaskID = taskID
		rv.Live = true
		rv.WorktreePath = b.WorktreePath
		rv.BindingStartedAt = b.StartedAt
		if kv := heraMeta[taskID]; kv != nil && kv[db.HeraMetaKeyReadyToClose] == "true" {
			rv.ReadyToClose = true
		}
		taskInProgress := false
		if t := taskByID[taskID]; t != nil {
			rv.TaskStatus = t.Status.String()
			rv.TaskResult = t.Result
			rv.TaskName = t.Name
			taskInProgress = t.Status == model.StatusInProgress
		}
		// Own needs-input from the authoritative App-tick set (keyed by live task)
		// — but ONLY while the bound task is actively in_progress (BUG-023). The
		// App's needsInputIDs scan is STICKY: a finished worker idling at its final
		// prompt keeps the needs-input marker in its log tail indefinitely, so
		// without this gate the task would stay flagged forever and pin "(?)" on
		// every ancestor coordinator permanently — the rollup could never clear.
		// Gating on in_progress is the "agent resolved" clear condition: when the
		// worker finishes (rolls to in_review/complete) the signal drops and the
		// rollup recomputes clear, transitively to the root, on the next refresh.
		// The deliberate hera `blocked` role status stays an INDEPENDENT, ungated
		// needs-input source (needsInputOwn), cleared by stepping off `blocked`
		// (s/S). A task missing from the snapshot (read failure) is treated as not
		// in_progress — a transient flicker that self-heals next tick, never a
		// stuck-forever "(?)".
		if needsInput[taskID] && taskInProgress {
			rv.NeedsInput = true
		}
	}
	// Structural bridge key: the role's LATEST binding regardless of liveness.
	// For a live role this is the same live task (empty end_reason); for a
	// finished role it is the most-recent ended binding's task + its end_reason,
	// so the rail can still nest a child whose link ended for a non-teardown
	// reason. Fall back to the live task when the latest-binding read was empty.
	if b := roleToLatest[role.ID]; b != nil {
		rv.BridgeTaskID = b.ArgusTaskID
		if b.EndedAt != nil {
			rv.LinkEndReason = b.EndReason
		}
	} else {
		rv.BridgeTaskID = rv.TaskID
	}
	if st, err := r.HeraRoleStatusFor(role.ID); err == nil {
		rv.Status = st.Status
		rv.HasStatus = true
		rv.StatusUpdatedAt = st.UpdatedAt
	} else if !errors.Is(err, db.ErrHeraNotFound) {
		// A non-"missing" status error is unusual; leave status zero rather
		// than aborting the whole rebuild for one role.
		rv.HasStatus = false
	}
	// Planned discriminator (D7): a worker-kind role with no live binding and no
	// binding EVER (BridgeTaskID is the latest-binding key, "" when never bound).
	// A bound-but-finished role keeps Live==false but carries a BridgeTaskID, so it
	// is NOT planned — the gater never re-materializes it.
	rv.Planned = role.Kind == db.HeraKindWorker && !rv.Live && rv.BridgeTaskID == ""
	return rv
}
