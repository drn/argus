package hera

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	heramodel "github.com/drn/argus/internal/hera/model"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// RailStateStore persists the rail's UI state (fold/collapse + selection) across
// argus / daemon restarts. *db.DB satisfies it implicitly (LoadRailState /
// SaveRailState over the config table); remote mode passes nil and persistence
// is simply off. See BUG-002 / openspec/changes/rail-state-persist.
type RailStateStore interface {
	LoadRailState() (string, error)
	SaveRailState(state string) error
}

// railViewState is the JSON shape persisted under the store's single key. Only
// NON-default fold entries are listed (orchestrators default expanded, per-coord
// Archive expandos default closed), so an absent/empty blob restores all
// defaults. Filter state is transient and deliberately absent.
type railViewState struct {
	Collapsed        []int64 `json:"collapsed"`          // orchestrator ids currently collapsed
	CoordArchiveOpen []int64 `json:"coord_archive_open"` // orch ids whose per-coord Archive expando is open
	FreelanceCollap  bool    `json:"freelance_collapsed"`
	ArchiveCollapsed bool    `json:"archive_collapsed"`
	SelectionRef     int64   `json:"selection_ref"` // currentRef() identity: role id, or -orch id for a header
}

// railRowKind enumerates the flattened display-row types. uint8 keeps it small;
// there are only a handful of values.
type railRowKind uint8

const (
	rrRule             railRowKind = iota // non-selectable separator
	rrSectionHeader                       // "Pinned" / "Freelance" label
	rrOrch                                // orchestrator header (selectable, collapsible)
	rrRole                                // role under an orchestrator (selectable)
	rrFreelanceRole                       // freelance-kind role (selectable)
	rrArchiveExpando                      // "Archive (N)" fold (selectable, collapsible)
	rrEmpty                               // empty-state placeholder (non-selectable)
	rrPinnedBreadcrumb                    // line 1 of a pinned non-root entry (selectable; dimmed icon + lineage)
)

// railRow is one flattened display line. orch/role point into the Model (never
// copied) so selection can hand the live projection back to 6b.
type railRow struct {
	kind  railRowKind
	label string
	orch  *heramodel.OrchView
	role  *heramodel.RoleView
	depth int
	dim   bool // archived placement → dimmed style

	// ancestryOnly is true while a filter is active AND this coordinator/
	// orchestrator heading row (rrOrch, or a bridging rrRole/rrPinnedBreadcrumb
	// standing in for a nested coordinator) is on screen ONLY to preserve tree
	// ancestry for a matching descendant — its own name (or its folded-in
	// coordinator's name, for an rrOrch header) does NOT itself match the
	// query. Such a row is never selectable (BUG-028-RAIL): arrow nav and
	// first-match auto-select skip straight past it to a real match, and it
	// renders dimmed so it's visually obvious it can't be selected. Always
	// false when no filter is active.
	ancestryOnly bool

	// Collapse target (only one is set, and only when collapsible).
	collOrchID    int64 // >0 → toggle collapsed[collOrchID]
	collFreelance bool
	collArchive   bool  // bottom Archive section (archived ROOT orchestrators)
	archiveOwner  int64 // >0 → per-coordinator Archive expando for this orch's archived roles

	// Two-line pinned non-root entry (add-hera-pin-nonroot). breadcrumb is the
	// dimmed lineage trail drawn on an rrPinnedBreadcrumb row (line 1).
	// breadcrumbCont marks the rrRole continuation (line 2): the non-selectable
	// name line that pairs with the preceding rrPinnedBreadcrumb row.
	breadcrumb     string
	breadcrumbCont bool

	// kanbanGroup (add-kanban-focus-fold) is set ONLY on the four kanban
	// sub-group header rows (rrSectionHeader, label "Active (N)" / "Backlog
	// (N)" / "Blocked (N)" / "Done (N)") to the group it represents. Zero
	// value "" on every other row (including the "Pinned" and "Freelance"
	// section headers), so kanbanGroupHeader() below never mistakes them for
	// a kanban boundary.
	kanbanGroup db.HeraKanbanStatus
}

// kanbanGroupHeader reports whether row is a kanban sub-group header (Active/
// Backlog/Blocked/Done) and, if so, which group — used by step() to detect a
// boundary crossing. Never true for "Pinned"/"Freelance"/rule/orch/role rows.
func (row railRow) kanbanGroupHeader() (db.HeraKanbanStatus, bool) {
	if row.kind == rrSectionHeader && row.kanbanGroup != "" {
		return row.kanbanGroup, true
	}
	return "", false
}

func (r railRow) selectable() bool {
	// An ancestry-only heading (BUG-028-RAIL) is on screen purely for tree context —
	// its own name never matched the typed query, so it can never be a valid
	// selection target while filtering. Checked first so it overrides every
	// kind below.
	if r.ancestryOnly {
		return false
	}
	switch r.kind {
	case rrOrch, rrFreelanceRole, rrArchiveExpando, rrPinnedBreadcrumb:
		return true // both the bottom Archive section and per-coordinator expandos
	case rrRole:
		// The continuation (line 2) of a two-line pinned entry is non-selectable —
		// the cursor anchors on the preceding rrPinnedBreadcrumb (line 1).
		return !r.breadcrumbCont
	case rrSectionHeader:
		// The Freelance fold header is selectable so the cursor can land on it
		// to collapse/expand; the plain "Pinned" label is not.
		return r.collFreelance
	default:
		return false
	}
}

// heraIconCoord is the coordinator/orchestrator marker (Nerd Font md, same
// codepoint Hera's rail used for iconCoord).
const heraIconCoord = rune(0x0F0E7B) // 󰹻

// Rail is the left navigation widget of the Hera view. It renders the Model
// read-only: sections, cursor, and per-orchestrator collapse. No mutation
// verbs live here (6c).
type Rail struct {
	*tview.Box

	model  heramodel.Model
	rows   []railRow
	cursor int
	offset int

	// Collapse state survives rebuilds. Orchestrators default expanded; the
	// Freelance section defaults expanded; the Archive section defaults
	// collapsed.
	collapsed        map[int64]bool
	freelanceCollap  bool
	archiveCollapsed bool
	// coordArchiveOpen tracks per-coordinator Archive expandos (keyed by orch id).
	// Absent/false → collapsed (the default), matching the bottom Archive section.
	coordArchiveOpen map[int64]bool

	// Filter state (the `/` rail name filter). filterInput is true while the
	// operator is TYPING (drives the `/ <query>` line, the key routing, and the
	// global-handler guard); filterQuery is the active query — non-empty NARROWS
	// the rail (ancestry-preserving). A single Enter selects the current match
	// AND clears the filter in one step (BUG-028-RAIL) — there is no longer an
	// "accepted but still narrowed" resting state, so filterQuery is always ""
	// whenever filterInput is false. Filter state is transient and is NOT
	// persisted. filterVis is the per-build visibility memo (orch id →
	// visible), recomputed each filtered buildRows and nil when no filter is
	// active.
	filterInput bool
	filterQuery string
	filterVis   map[int64]bool

	// pinnedFloat is the set of role ids that float OUT of their parent subtree
	// into the Pinned section as a two-line breadcrumb entry (add-hera-pin-nonroot).
	// Recomputed each buildRows by collectPinnedRoles; appendOrchWorkers consults
	// it to suppress a floated worker row (and its bridged child, already hoisted
	// + placed in the Pinned pass) from the active tree. nil when nothing floats.
	pinnedFloat map[int64]bool

	// canonical is the per-build canonical-parent map (add-hera-kanban-status),
	// recomputed each buildRows and reused by heramodel.Selection() to stamp
	// heramodel.Selection.TopLevelOrch — an orchestrator absent from this map is a true
	// root. nil after the empty-model early return (no orchestrators to have
	// parents at all).
	canonical map[int64]heramodel.CanonParent

	// focusedKanban (add-kanban-focus-fold) is the ONE kanban sub-group
	// (Active/Backlog/Blocked/Done) currently expanded in buildRows; the other
	// three render header+count only. Defaults to db.HeraKanbanActive (NewRail).
	// NOT persisted (unlike collapsed/freelanceCollap/archiveCollapsed) — every
	// code path that moves the selection to a target outside the focused group
	// (SetModel's restore, SelectByTaskID, EnsureAncestorsExpanded, and step()'s
	// boundary-crossing) re-derives it from the target via focusGroupOf BEFORE
	// buildRows runs, since buildRows itself needs to know which group to expand
	// to lay out the target row at all (see design.md decision 1).
	focusedKanban db.HeraKanbanStatus

	focused   bool // drives the border-highlight style
	animFrame int  // spinner frame for in-motion role glyphs (recomputed each Draw)

	// prMeta is the daemon-populated "pr" namespace cache (taskID -> {state,url}),
	// the same best-effort source the Details roster reads. A managed role whose
	// bound task has a non-empty "url" renders a PR indicator. nil → no PR cells.
	prMeta map[string]map[string]string

	// onSelectionChanged fires (with the selected row's ref) whenever the
	// cursor lands on a different selectable row. 6b binds the panes off it;
	// 6a wires it only for the focus-guard smoke test. May be nil.
	onSelectionChanged func()

	// store persists fold/selection state across restarts (BUG-002). nil in
	// remote mode (and until SetStateStore wires it) → persistence off.
	// pendingSelRef holds a persisted selection ref to apply on the FIRST model
	// build (rows don't exist at load time); it is consumed once, after which the
	// live cursor wins.
	store         RailStateStore
	pendingSelRef int64
	// firstRunCollapse is true when no saved rail state exists (first run). On
	// the first non-empty model build, ALL orchestrators are seeded as collapsed
	// so the rail starts fully collapsed instead of fully expanded. One-shot:
	// cleared after the seed, just like pendingSelRef.
	firstRunCollapse bool

	// excursion (add-ctrlg-excursion) holds the fold/selection snapshot taken at
	// the instant the rail's needs-input count last transitioned from fully-at-
	// rest (0) to interrupted (>=1), or nil when no excursion is currently held.
	// See noteExcursionTransition for the full arm/re-arm state machine and
	// RestoreExcursion for the ctrl+g (count==0)/ctrl+b (any time) discharge path.
	excursion *railSnapshot
	// armedNeedsInputIDs is the whole-rail needs-input role-ID SET (Model.
	// NeedsInputRoleIDs) as of the last SetModel call — the baseline the next
	// rebuild's set is compared against to detect (a) the fully-at-rest ->
	// interrupted transition (nil/empty -> non-empty) and (b) a genuinely NEW,
	// distinct entrant while no excursion is held. Updated on EVERY rebuild,
	// including while an excursion is held (frozen) or the model is fully at
	// rest — never gated to only the branches that act on it — so an entrant
	// that merely folds into an already-open excursion is still absorbed into
	// the baseline; otherwise it would look "new" again immediately after a
	// later discharge and cause a spurious re-freeze (see
	// TestRail_ExcursionSnapshot_EntrantAbsorbedWhileFrozenDoesNotReFreezeAfterDischarge).
	// nil (NewRail) matches "no problems yet" before the first model ever loads.
	armedNeedsInputIDs map[int64]bool
}

// railSnapshot captures the rail's fold/expand state and prior selection for
// the ctrl+g/ctrl+b problem-child excursion (add-ctrlg-excursion): enough to
// restore the operator's pre-interruption layout exactly via RestoreExcursion.
type railSnapshot struct {
	collapsed        map[int64]bool
	coordArchiveOpen map[int64]bool
	freelanceCollap  bool
	archiveCollapsed bool
	focusedKanban    db.HeraKanbanStatus
	selRef           int64 // currentRef() identity at capture time
}

func cloneInt64BoolMap(m map[int64]bool) map[int64]bool {
	c := make(map[int64]bool, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// NewRail builds an empty rail. Archive starts collapsed (matches the task
// list's archive default).
func NewRail() *Rail {
	return &Rail{
		Box:              tview.NewBox(),
		collapsed:        make(map[int64]bool),
		archiveCollapsed: true,
		coordArchiveOpen: make(map[int64]bool),
		focusedKanban:    db.HeraKanbanActive,
	}
}

// SetOnSelectionChanged registers the selection callback.
func (r *Rail) SetOnSelectionChanged(fn func()) { r.onSelectionChanged = fn }

// SetFocused records whether the rail holds keyboard focus, switching its
// border between the focused and unfocused palette.
func (r *Rail) SetFocused(v bool) { r.focused = v }

// SetPRMeta wires the best-effort "pr" namespace cache so managed rail rows can
// render a PR indicator. Pass nil to clear it (the indicator just won't render).
func (r *Rail) SetPRMeta(m map[string]map[string]string) { r.prMeta = m }

// rolePR reports whether the role's bound task has a PR in an actionable
// review state (mirrors theme.PRGlyph / model.PRState.IsActionable) — a
// merged, closed, draft, or unknown-state PR leaves a url in the cache but
// must not flag the rail row.
func (r *Rail) rolePR(role *heramodel.RoleView) bool {
	if role == nil || role.TaskID == "" || r.prMeta == nil {
		return false
	}
	kv := r.prMeta[role.TaskID]
	if kv == nil {
		return false
	}
	s, err := model.ParsePRState(kv["state"])
	return err == nil && s.IsActionable()
}

// SetModel replaces the snapshot and rebuilds rows, preserving the cursor's
// selectable target where possible.
func (r *Rail) SetModel(m heramodel.Model) {
	r.noteExcursionTransition(m)
	prev := r.currentRef()
	r.model = m
	// A persisted selection (BUG-002) takes precedence on the FIRST build after a
	// restore — the rows didn't exist when SetStateStore ran. It is one-shot:
	// once applied here it's zeroed, so later rebuilds keep the live cursor. The
	// cursor is written directly (not via setCursor), so the restore never
	// re-persists. Resolved BEFORE buildRows (add-kanban-focus-fold) since the
	// override changes which ref the focused-kanban-group resolution below uses.
	if r.pendingSelRef != 0 {
		prev = r.pendingSelRef
		r.pendingSelRef = 0
	}
	// Keep the previously-selected row revealed through this rebuild even if
	// its own needs-input signal (the thing that was revealing it) just
	// cleared (BUG-071) — before buildRows runs, so the reveal machinery below
	// sees it like any other still-outstanding needs-input path. No-op when
	// prev was already visible through ordinary expansion.
	r.applyStickyReveal(prev)
	// Re-focus the kanban group containing prev's target BEFORE buildRows —
	// buildRows itself needs r.focusedKanban to decide which group to expand,
	// so this must run first (design.md decision 1). A ref of 0 (nothing
	// resolvable — e.g. the very first build) leaves r.focusedKanban at
	// NewRail's `active` default.
	r.focusGroupFromRef(prev)
	r.buildRows()
	r.restoreCursor(prev)
	r.clampCursor()
}

// applyStickyReveal keeps ref's row peeking through a closed ancestor fold for
// THIS rebuild, even when ref's own needs-input signal (the thing that would
// otherwise earn it a peek via the partial-fold-reveal mechanism) has just
// cleared in the freshly-received r.model — fixing the "yank" where a role
// the operator is actively viewing vanishes, and the cursor with it, the
// instant they resolve its prompt (BUG-071).
//
// It reuses the reveal mechanism verbatim rather than adding parallel gates:
// force SubtreeNeedsInput true on ref's own role (for a role ref) and on
// every ancestor orchestrator up to the root via CanonicalParents — plus, for
// each worker-bridge (non-coordinator-spawn) hop, the PARENT's bridging role,
// since appendOrchWorkers' per-role reveal gate reads the bridging role's own
// SubtreeNeedsInput, not merely the child orchestrator's. ref follows
// currentRef()'s identity: positive is a role id, negative is an
// orchestrator's header (-OrchID), zero (or an id that no longer resolves —
// e.g. the role/orchestrator was deleted) is a no-op.
//
// This has NO effect when ref's row was already visible through ordinary
// (non-collapsed) expansion: the non-revealOnly render path never consults
// SubtreeNeedsInput at all. And because it is re-derived from r.currentRef()
// on every SetModel call, it only lasts as long as the cursor keeps
// resolving to the SAME ref — the moment the operator (or a cursor-restore)
// lands on a different row, the next call computes stickiness from THAT
// identity instead, and the old row is free to fold away normally.
func (r *Rail) applyStickyReveal(ref int64) {
	if ref == 0 {
		return
	}
	var orchID int64
	if ref < 0 {
		orchID = -ref
		if r.model.OrchByID(orchID) == nil {
			return
		}
	} else {
		role := r.model.RoleByID(ref)
		if role == nil {
			return
		}
		role.SubtreeNeedsInput = true
		orchID = role.OrchID
	}
	canonical := r.model.CanonicalParents()
	seen := make(map[int64]bool)
	for id := orchID; ; {
		if seen[id] {
			return // cycle guard (matches EnsureAncestorsExpanded's discipline)
		}
		seen[id] = true
		o := r.model.OrchByID(id)
		if o == nil {
			return
		}
		o.SubtreeNeedsInput = true
		cp, ok := canonical[id]
		if !ok {
			return // reached a top-level root
		}
		if !cp.CoordSpawn {
			if parent := r.model.OrchByID(cp.OrchID); parent != nil {
				if br := parent.BridgingRoleFor(o.CoordBridgeTaskID()); br != nil {
					br.SubtreeNeedsInput = true
				}
			}
		}
		id = cp.OrchID
	}
}

// Model returns the current snapshot (read-only; for tests/inspection).
func (r *Rail) Model() heramodel.Model { return r.model }

// --- persistence (BUG-002) ---

// SetStateStore wires the persistence seam and immediately restores the saved
// state: the fold maps and section booleans (keyed by stable DB ids, valid
// before any model loads) are applied now; the selection ref is stashed and
// applied on the first model build (rows don't exist yet). A nil store, a read
// error, or a malformed/empty blob all leave the rail at its defaults (logged,
// never fatal) — matching the no-legacy-migration policy. Call once at
// construction, before the first Refresh.
func (r *Rail) SetStateStore(s RailStateStore) {
	r.store = s
	if s == nil {
		return
	}
	raw, err := s.LoadRailState()
	if err != nil {
		uxlog.Log("[hera-view] rail state load failed: %v", err)
		return
	}
	if strings.TrimSpace(raw) == "" {
		// Nothing persisted yet (first run) — start fully collapsed on the
		// first model build instead of defaulting to fully expanded.
		r.firstRunCollapse = true
		return
	}
	var st railViewState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		uxlog.Log("[hera-view] rail state parse failed (keeping defaults): %v", err)
		return
	}
	for _, id := range st.Collapsed {
		r.collapsed[id] = true
	}
	for _, id := range st.CoordArchiveOpen {
		r.coordArchiveOpen[id] = true
	}
	r.freelanceCollap = st.FreelanceCollap
	r.archiveCollapsed = st.ArchiveCollapsed
	r.pendingSelRef = st.SelectionRef
}

// persist serializes the live fold maps + current selection and writes them
// through the store. nil store → no-op; a write error is logged, never fatal.
// The rail hands the store a fully-serialized immutable string, so a store impl
// is free to write it asynchronously without racing the UI thread (shipped
// synchronously here — a local SQLite upsert is sub-millisecond).
func (r *Rail) persist() {
	if r.store == nil {
		return
	}
	st := railViewState{
		Collapsed:        trueKeys(r.collapsed),
		CoordArchiveOpen: trueKeys(r.coordArchiveOpen),
		FreelanceCollap:  r.freelanceCollap,
		ArchiveCollapsed: r.archiveCollapsed,
		SelectionRef:     r.currentRef(),
	}
	b, err := json.Marshal(st)
	if err != nil {
		uxlog.Log("[hera-view] rail state marshal failed: %v", err)
		return
	}
	if err := r.store.SaveRailState(string(b)); err != nil {
		uxlog.Log("[hera-view] rail state save failed: %v", err)
	}
}

// trueKeys returns the sorted ids whose value is true (the non-default fold
// entries). Sorted so the serialized blob is stable across saves (no spurious
// rewrites from Go's randomized map iteration).
func trueKeys(m map[int64]bool) []int64 {
	var out []int64
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- problem-child excursion (ctrl+g / ctrl+b, add-ctrlg-excursion) ---

// NeedsInputCount returns the current whole-rail needs-input count
// (Model.NeedsInputTotalCount), fold-independent — the same figure the
// excursion state machine below tracks and the one ctrl+g/ctrl+b consult at
// keypress time to decide whether to cycle, arm, or restore.
func (r *Rail) NeedsInputCount() int { return r.model.NeedsInputTotalCount() }

// HasExcursionSnapshot reports whether a snapshot is currently held (an
// excursion is open). Exposed mainly for tests.
func (r *Rail) HasExcursionSnapshot() bool { return r.excursion != nil }

// noteExcursionTransition is the SOLE place a snapshot is captured — never at
// keypress time. It runs on every SetModel (the rail's one rebuild point,
// wherever the count is recomputed), comparing the fresh whole-rail
// needs-input role-ID SET against the set as of the last rebuild — identity,
// not a bare count, is required (BUG-069, see the re-arm bullet below):
//
//   - A transition from fully-at-rest (empty) to interrupted (>=1 id) captures
//     a FRESH snapshot unconditionally, discarding any stale one still held —
//     this is the operator's true pre-interruption layout, captured before
//     they have had any chance to react and fold/select things themselves.
//   - A second (or third...) needs-input signal appearing while one is
//     already open does NOT retrigger a capture (excursion != nil) — it folds
//     into the SAME excursion.
//   - The other arming path (excursion == nil but the set is non-empty) is
//     reachable ONLY right after an explicit ctrl+b restore fired mid-
//     excursion (some problems still outstanding). BUG-069: this must NOT
//     unconditionally re-arm on the very next rebuild — a still-outstanding,
//     never-resolved role (however stale) is not a new interruption, and
//     freezing immediately would silently discard any fold/selection change
//     the operator makes afterward until the next explicit restore (the live
//     repro: ctrl+b replayed a stale, much-earlier layout instead of the
//     operator's latest position). Instead the tracked baseline
//     (armedNeedsInputIDs) keeps refreshing to the CURRENT set on every
//     rebuild for as long as it stays a subset of (or equal to) that
//     baseline, and a fresh snapshot freezes only the instant a role id
//     appears that was NOT in it — a genuinely new, distinct interruption —
//     so the eventual freeze always reflects the operator's latest organic
//     fold/selection, never a stale one.
//
// BUG-070: neither branch above may fire on the very first SetModel call a
// Rail instance ever sees (r.rows still nil — buildRows has not run once
// yet), which happens right after a fresh TUI launch/relaunch. If a stale,
// already-outstanding needs-input role predates the launch (a permanently
// stuck "?", or simply a coordinator left blocked overnight), that first
// call satisfies the fully-at-rest case unconditionally — but currentRef()
// can only return 0 with no rows to search, and r.collapsed at that instant
// holds whatever SetStateStore loaded from the PREVIOUS session's persisted
// disk state, not anything from the operator's current session. Freezing
// there captures a bogus snapshot the operator can never meaningfully
// restore to (selRef==0 silently no-ops the cursor; the fold state reverts
// to wherever they left off last time, not where they are now) — live
// repro: cursor and both panes went empty on ctrl+b, and unrelated
// orchestrators flipped fold state to a stale prior-session layout. Instead,
// the first-ever call only seeds the baseline (below); the normal machinery
// arms correctly from the second call onward once rows exist, and ctrl+g's
// EnsureExcursionArmed belt-and-suspenders still arms from the operator's
// real live position if nothing has interrupted them by the time they first
// press it — there is no earlier "pre-interruption layout" to capture when
// the problem already existed before the app ever opened.
//
// armedNeedsInputIDs is updated to the current set unconditionally at the end
// of every call, including while an excursion is already held — so an
// entrant that merely folds into an open excursion is absorbed into the
// baseline and does not look "new" again immediately after a later discharge.
//
// No wall-clock or idle-time heuristics — purely a function of the set and
// whether a snapshot is currently held.
func (r *Rail) noteExcursionTransition(m heramodel.Model) {
	current := m.NeedsInputRoleIDs()
	if r.rows == nil {
		// BUG-070: first-ever call, rows don't exist yet — seed only, never
		// capture (see doc above).
		r.armedNeedsInputIDs = current
		return
	}
	switch {
	case len(r.armedNeedsInputIDs) == 0 && len(current) >= 1:
		r.excursion = r.captureExcursionSnapshot()
	case r.excursion == nil && hasNewNeedsInputID(current, r.armedNeedsInputIDs):
		r.excursion = r.captureExcursionSnapshot()
	}
	r.armedNeedsInputIDs = current
}

// hasNewNeedsInputID reports whether current contains a role id absent from
// baseline — a genuinely new, distinct needs-input entrant relative to the
// last capture/refresh, as opposed to the same still-outstanding role(s)
// reappearing across rebuilds.
func hasNewNeedsInputID(current, baseline map[int64]bool) bool {
	for id := range current {
		if !baseline[id] {
			return true
		}
	}
	return false
}

// EnsureExcursionArmed captures a snapshot now if none is held yet — ctrl+g's
// belt-and-suspenders call before it cycles to the next candidate. Under
// normal operation noteExcursionTransition already armed it at the last
// rebuild; this only matters if a caller reaches ctrl+g before any rebuild
// has observed the transition.
func (r *Rail) EnsureExcursionArmed() {
	if r.excursion == nil {
		r.excursion = r.captureExcursionSnapshot()
	}
}

// captureExcursionSnapshot clones the live fold maps + section bools and
// records the current selection identity (currentRef, the same stable
// role-id/-orch-id ref restoreCursor already knows how to re-pin).
func (r *Rail) captureExcursionSnapshot() *railSnapshot {
	return &railSnapshot{
		collapsed:        cloneInt64BoolMap(r.collapsed),
		coordArchiveOpen: cloneInt64BoolMap(r.coordArchiveOpen),
		freelanceCollap:  r.freelanceCollap,
		archiveCollapsed: r.archiveCollapsed,
		focusedKanban:    r.focusedKanban,
		selRef:           r.currentRef(),
	}
}

// RestoreExcursion re-applies a held snapshot's fold/selection state and
// discards it, returning true. Returns false — a no-op — when no snapshot is
// held. Used by ctrl+g when the count has dropped back to 0, and by ctrl+b
// (manual "restore rail") at any time regardless of the remaining count.
func (r *Rail) RestoreExcursion() bool {
	if r.excursion == nil {
		return false
	}
	snap := r.excursion
	r.excursion = nil
	r.collapsed = snap.collapsed
	r.coordArchiveOpen = snap.coordArchiveOpen
	r.freelanceCollap = snap.freelanceCollap
	r.archiveCollapsed = snap.archiveCollapsed
	r.focusedKanban = snap.focusedKanban
	r.buildRows()
	r.restoreCursor(snap.selRef)
	r.clampCursor()
	r.persist() // fold change (BUG-002), like ToggleCollapse/EnsureAncestorsExpanded
	return true
}

// --- filter ---

// Filtering reports whether the rail is in search INPUT mode (the operator is
// typing a query). page.go skips rail mutations while this holds — EXCEPT
// Enter, which is special-cased to select the current match and clear the
// filter in one step (BUG-028-RAIL) — and app.go lets the global rune shortcuts
// (1/2/3/q/?) fall through to the rail as filter input. Enter and Esc both
// fully clear the query (ClearFilter), so there is no longer an
// "accepted-but-still-narrowed" resting state — Filtering() and filterActive()
// now always flip together.
func (r *Rail) Filtering() bool { return r.filterInput }

// filterActive reports whether the rail rows are NARROWED (a non-empty query is
// applied). An empty/all-whitespace query narrows nothing. Since Enter/Esc both
// fully clear filterQuery, this can only be true while Filtering() is also true.
func (r *Rail) filterActive() bool { return strings.TrimSpace(r.filterQuery) != "" }

// filterMatches reports whether name satisfies the current query: every
// whitespace-separated term must be a case-insensitive substring. An empty
// query matches everything (so narrowing only begins once a real term is typed).
func (r *Rail) filterMatches(name string) bool {
	terms := strings.Fields(strings.ToLower(r.filterQuery))
	if len(terms) == 0 {
		return true
	}
	ln := strings.ToLower(name)
	for _, t := range terms {
		if !strings.Contains(ln, t) {
			return false
		}
	}
	return true
}

// orchMatchesOwnQuery reports whether o itself — not merely a visible
// descendant — is a text match against the active query: its own name, OR its
// folded-in coordinator's name. The coordinator has no separate rail row (it IS
// the header — see appendOrch), so a query matching the coordinator's name must
// count as the header's own match, not ancestry (BUG-028-RAIL): otherwise searching
// for a coordinator by name would never let Enter jump straight to it.
func (r *Rail) orchMatchesOwnQuery(o *heramodel.OrchView) bool {
	if r.filterMatches(o.Name) {
		return true
	}
	if coord := o.CoordRole(); coord != nil && r.filterMatches(coord.Name) {
		return true
	}
	return false
}

// computeVisible builds the orchestrator-visibility memo for the active filter:
// an orchestrator is visible when its own name matches, OR any of its roles
// matches, OR any sub-orchestrator it bridges (recursively) is visible —
// ancestry-preserving so a matching nested agent always keeps its parents.
// Cycle-safe via an in-progress set (a cycle edge contributes nothing rather
// than recursing forever), mirroring BridgeSubtree's visited guard.
func (r *Rail) computeVisible(bridge map[string]*heramodel.OrchView) map[int64]bool {
	vis := make(map[int64]bool)
	inProgress := make(map[int64]bool)
	var visit func(o *heramodel.OrchView) bool
	visit = func(o *heramodel.OrchView) bool {
		if v, ok := vis[o.ID]; ok {
			return v
		}
		if inProgress[o.ID] {
			return false // cycle edge: break recursion, contribute nothing
		}
		inProgress[o.ID] = true
		v := r.filterMatches(o.Name)
		for i := range o.Roles {
			if r.filterMatches(o.Roles[i].Name) {
				v = true
			}
		}
		for i := range o.Roles {
			w := &o.Roles[i]
			if w.Kind == db.HeraKindCoordinator || !heramodel.RoleBridges(w) {
				continue
			}
			if c := bridge[heramodel.BridgeTaskID(w)]; c != nil && c.ID != o.ID {
				if visit(c) {
					v = true
				}
			}
		}
		inProgress[o.ID] = false
		vis[o.ID] = v
		return v
	}
	for _, sec := range [][]heramodel.OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			visit(&sec[i])
		}
	}
	return vis
}

// isCollapsed / isCoordArchiveOpen apply the auto-expand rule: while a filter is
// active every node renders EXPANDED so matching rows are never hidden behind a
// fold. The persisted fold maps are read (not mutated), so clearing the filter
// restores the operator's folds untouched.
func (r *Rail) isCollapsed(id int64) bool {
	if r.filterActive() {
		return false
	}
	return r.collapsed[id]
}

func (r *Rail) isCoordArchiveOpen(id int64) bool {
	if r.filterActive() {
		return true
	}
	return r.coordArchiveOpen[id]
}

// orchVisible reports whether the orchestrator should render under the active
// filter (always true when no filter is active).
func (r *Rail) orchVisible(id int64) bool {
	return !r.filterActive() || r.filterVis[id]
}

// anyOrchVisible reports whether any orchestrator in the section is visible
// under the active filter (used to prune an empty section header). True for any
// non-empty section when no filter is active.
func (r *Rail) anyOrchVisible(secs []heramodel.OrchView) bool {
	if !r.filterActive() {
		return len(secs) > 0
	}
	for i := range secs {
		if r.filterVis[secs[i].ID] {
			return true
		}
	}
	return false
}

// anyFreelanceVisible reports whether any freelance role matches the active
// filter (prunes the Freelance section header). True for any non-empty set when
// no filter is active.
func (r *Rail) anyFreelanceVisible() bool {
	if !r.filterActive() {
		return len(r.model.Freelance) > 0
	}
	for i := range r.model.Freelance {
		if r.filterMatches(r.model.Freelance[i].Name) {
			return true
		}
	}
	return false
}

// workerRowVisible reports whether a worker row should render under the active
// filter: its own name matches, OR it bridges a visible sub-orchestrator (so the
// bridge parent is kept for a nested match). Always true when no filter active.
// The bridge child is resolved through the canonical parent (the same primitive
// placement uses) so visibility and nesting agree.
func (r *Rail) workerRowVisible(ownerID int64, w *heramodel.RoleView, canonical map[int64]heramodel.CanonParent, placed map[int64]bool) bool {
	if !r.filterActive() {
		return true
	}
	if r.filterMatches(w.Name) {
		return true
	}
	if c := r.workerBridgeChild(ownerID, w, canonical, placed); c != nil && r.filterVis[c.ID] {
		return true
	}
	return false
}

// currentRef returns a stable identity for the row under the cursor so the
// cursor can be re-pinned after a rebuild. RoleID for roles, OrchID (negated
// to avoid colliding with role ids) for orch headers, 0 otherwise.
//
// Known gap (pre-existing, shared with the BUG-002 persisted-selection
// mechanism this excursion snapshot's selRef also reuses — not introduced or
// fixed by add-ctrlg-excursion/BUG-069): a cursor resting on a selectable
// fold row with no role/orch identity of its own — the bottom Archive
// expando, a per-coordinator Archive expando, or the Freelance fold header —
// returns 0 here, and restoreCursor(0) is then a silent no-op, leaving the
// cursor wherever it already was rather than re-pinning it. Confirmed via
// TestRail_CurrentRef_ZeroOnArchiveExpandoRow; left unfixed here since it is
// narrow (the cursor must sit on one of these specific fold rows, not a role
// or orchestrator header, at the exact moment of capture) and orthogonal to
// BUG-069 — worth a dedicated follow-up if it recurs in practice.
func (r *Rail) currentRef() int64 {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return 0
	}
	row := r.rows[r.cursor]
	switch {
	case row.role != nil:
		return row.role.RoleID
	case row.orch != nil:
		return -row.orch.ID
	default:
		return 0
	}
}

// focusGroupOf resolves orchID's top-level (root) orchestrator by walking
// CanonicalParents() and returns that root's kanban status. Computed fresh
// from r.model each call — never from r.canonical, which is a buildRows
// side-effect that does not exist yet at the point this needs to run (this
// must be callable BEFORE buildRows, to decide which group buildRows should
// expand — design.md decision 1's chicken-and-egg resolution). ok is false
// when orchID does not resolve to a live orchestrator in the model (a stale
// id, or a canonical-parent cycle).
func (r *Rail) focusGroupOf(orchID int64) (db.HeraKanbanStatus, bool) {
	canonical := r.model.CanonicalParents()
	seen := make(map[int64]bool)
	id := orchID
	for {
		if seen[id] {
			return "", false // cycle guard
		}
		seen[id] = true
		cp, ok := canonical[id]
		if !ok {
			break // id is now the top-level root
		}
		id = cp.OrchID
	}
	o := r.model.OrchByID(id)
	if o == nil {
		return "", false
	}
	return kanbanStatusOf(o), true
}

// focusGroupFromRef resolves the kanban group containing ref — a
// currentRef()-style identity: a positive role id, or a negated orchestrator
// id — and sets r.focusedKanban to it. A ref of 0 (nothing previously
// selected), a ref that no longer resolves (stale id, or a Freelance role,
// which sits outside the kanban partition entirely), all leave r.focusedKanban
// untouched — preserving NewRail's `active` default on the very first
// non-empty build (design.md decision 5). Must run BEFORE buildRows.
func (r *Rail) focusGroupFromRef(ref int64) {
	if ref == 0 {
		return
	}
	var orchID int64
	if ref < 0 {
		orchID = -ref
	} else {
		id, ok := r.model.RoleOrchID(ref)
		if !ok {
			return
		}
		orchID = id
	}
	if group, ok := r.focusGroupOf(orchID); ok {
		r.focusedKanban = group
	}
}

func (r *Rail) restoreCursor(ref int64) {
	if ref == 0 {
		return
	}
	for i, row := range r.rows {
		switch {
		case row.role != nil && !row.breadcrumbCont && row.role.RoleID == ref:
			// Anchor on the breadcrumb line (line 1), never the non-selectable
			// continuation (line 2) which carries the same role pointer.
			r.cursor = i
			return
		case row.orch != nil && -row.orch.ID == ref:
			r.cursor = i
			return
		}
	}
}

// --- row building ---

func (r *Rail) buildRows() {
	r.rows = nil

	if r.model.IsEmpty() {
		r.rows = append(r.rows, railRow{kind: rrEmpty, label: "No hera orchestrators"})
		r.canonical = nil
		return
	}

	// On first run (no saved state) collapse ALL orchestrators so the rail
	// opens in a tidy summary view. One-shot: cleared after the seed.
	if r.firstRunCollapse {
		for _, sec := range [][]heramodel.OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
			for _, o := range sec {
				r.collapsed[o.ID] = true
			}
		}
		r.firstRunCollapse = false
	}

	// Nesting machinery: each child orchestrator's SINGLE canonical parent is
	// precomputed once (`canonical`) so placement is deterministic and
	// FOLD-INDEPENDENT — a child with two valid bridge-parents (coordinator-spawn
	// and worker-bridge) renders under exactly one of them regardless of which is
	// collapsed (the multi-parent fold-migration quirk). `consumed` = has a
	// canonical parent, so it nests rather than rendering as a top-level root;
	// `placed` guards single-placement + cycles across the whole build (an
	// orchestrator is rendered at most once, breaking any bridge cycle).
	canonical := r.model.CanonicalParents()
	r.canonical = canonical
	consumed := make(map[int64]bool, len(canonical))
	for id := range canonical {
		consumed[id] = true
	}
	placed := make(map[int64]bool)
	// Structural reachability under FULL expansion (collapse/archive fold
	// IGNORED): an orchestrator whose canonical-parent chain reaches a true root is
	// not a genuine top-level root — it is merely hidden when an ancestor is
	// folded. The safety sweep below consults this so collapse/archive-HIDDEN
	// children stay folded instead of leaking to the top; only true cycle-orphans
	// (a chain that loops without ever reaching a root) get rescued.
	structReach := r.structuralReach(canonical)

	// Filter visibility memo (ancestry-preserving). nil when no filter is active,
	// so every non-filtered path below short-circuits to today's behaviour. The
	// raw bridge index (every bridge, not just canonical parents) drives the memo
	// so a match anywhere in a multi-bridge subtree keeps its ancestors visible.
	if r.filterActive() {
		r.filterVis = r.computeVisible(r.model.BridgeIndex())
	} else {
		r.filterVis = nil
	}

	// 1. Pinned section. Pinned orchestrators are always top-level roots
	// (user intent), even if some worker bridges them. Pinned NON-ROOT roles
	// (add-hera-pin-nonroot) float OUT of their parent subtree into the same
	// section as a two-line breadcrumb entry, after the pinned orchestrators. The
	// header renders when a pinned orchestrator is visible OR any role floats; it
	// is pruned when neither holds under the active filter.
	floated := r.collectPinnedRoles(canonical)
	pinnedRendered := r.anyOrchVisible(r.model.Pinned) || len(floated) > 0
	if pinnedRendered {
		r.rows = append(r.rows, railRow{kind: rrSectionHeader, label: "Pinned"})
		for i := range r.model.Pinned {
			r.appendOrch(&r.model.Pinned[i], 0, r.model.Pinned[i].Archived, canonical, placed)
		}
		for _, pe := range floated {
			r.appendPinnedRole(pe, canonical, placed)
		}
	}

	// 2. Active orchestrators, partitioned into kanban-status sub-groups
	// (add-hera-kanban-status, add-kanban-focus-fold): Active (N) → Backlog
	// (N) → Blocked (N) → Done (N), uniformly — Active is no longer a
	// headerless special case. Each group is scoped to TOP-LEVEL (root — no
	// canonical parent) orchestrators only; a nested/bridged orchestrator's own
	// kanban status is never consulted for placement — it always nests under
	// its canonical parent regardless of these section boundaries. Within each
	// group: render roots first; then a safety sweep rescues only TRUE
	// cycle-orphans carrying that group's status — an orchestrator left
	// unplaced AND whose canonical chain reaches no root. A child that is
	// merely hidden because an ancestor is collapsed/archived is structurally
	// reachable, so it stays folded instead of leaking to the top.
	//
	// Every non-empty group gets its OWN unconditioned leading divider — the
	// SAME convention the Freelance/Archive sections below already use (always
	// lead with a divider when non-empty, regardless of what rendered above) —
	// there is no longer a distinct Pinned→Active divider special case.
	//
	// Fold gating (add-kanban-focus-fold): a group's member subtree renders
	// only when it is the currently FOCUSED group (r.focusedKanban); the other
	// three show their header line only. The member-building pass still runs
	// for every group regardless of focus — via appendOrch/the safety sweep,
	// exactly as before — so `n`, filter visibility, `placed` marking, and
	// nested-child recursion all stay correct even for a collapsed group; only
	// the ACCUMULATED ROWS are conditionally kept. r.rows is temporarily
	// swapped for a scratch buffer during that pass so appendOrch's normal
	// r.rows-append machinery needs no changes.
	kanbanGroups := []struct {
		status db.HeraKanbanStatus
		label  string
	}{
		{db.HeraKanbanActive, "Active"},
		{db.HeraKanbanBacklog, "Backlog"},
		{db.HeraKanbanBlocked, "Blocked"},
		{db.HeraKanbanDone, "Done"},
	}
	for _, g := range kanbanGroups {
		saved := r.rows
		r.rows = nil
		n := 0 // count of top-level orchestrators actually placed in this group
		for i := range r.model.Active {
			ov := &r.model.Active[i]
			if consumed[ov.ID] || placed[ov.ID] || kanbanStatusOf(ov) != g.status {
				continue
			}
			r.appendOrch(ov, 0, false, canonical, placed)
			if placed[ov.ID] {
				n++
			}
		}
		for i := range r.model.Active {
			ov := &r.model.Active[i]
			if placed[ov.ID] || structReach[ov.ID] || kanbanStatusOf(ov) != g.status {
				continue
			}
			r.appendOrch(ov, 0, false, canonical, placed)
			if placed[ov.ID] {
				n++
			}
		}
		members := r.rows
		r.rows = saved
		if n == 0 {
			continue // no header, no divider, no rows — group truly empty
		}
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:        rrSectionHeader,
			label:       fmt.Sprintf("%s (%d)", g.label, n),
			kanbanGroup: g.status,
		})
		if g.status == r.focusedKanban {
			r.rows = append(r.rows, members...)
		}
	}

	// 3. Freelance section. The header + separator are pruned when no freelance
	// role matches the active filter; the section auto-expands while filtering.
	if r.anyFreelanceVisible() {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:          rrSectionHeader,
			label:         fmt.Sprintf("Freelance (%d)", len(r.model.Freelance)),
			collFreelance: true,
		})
		if !r.freelanceCollap || r.filterActive() {
			for i := range r.model.Freelance {
				if r.filterActive() && !r.filterMatches(r.model.Freelance[i].Name) {
					continue
				}
				r.rows = append(r.rows, railRow{kind: rrFreelanceRole, role: &r.model.Freelance[i], depth: 1})
			}
		}
	}

	// 4. Archive section (collapsed by default): archived orchestrators not
	// already nested under a live parent (placed). A consumed-but-unplaced
	// archived orphan (pure cycle) still surfaces here. Under an active filter
	// only visible archived roots are listed, and the section auto-expands.
	var archivedRoots []*heramodel.OrchView
	for i := range r.model.Archived {
		if placed[r.model.Archived[i].ID] {
			continue
		}
		if !r.orchVisible(r.model.Archived[i].ID) {
			continue
		}
		archivedRoots = append(archivedRoots, &r.model.Archived[i])
	}
	if len(archivedRoots) > 0 {
		r.rows = append(r.rows, railRow{kind: rrRule})
		r.rows = append(r.rows, railRow{
			kind:        rrArchiveExpando,
			label:       fmt.Sprintf("Archive (%d)", len(archivedRoots)),
			collArchive: true,
		})
		if !r.archiveCollapsed || r.filterActive() {
			for _, o := range archivedRoots {
				r.appendOrch(o, 1, true, canonical, placed)
			}
		}
	}
}

// structuralReach returns every orchestrator whose CANONICAL-parent chain reaches
// a true root (an orchestrator with no canonical parent) under FULL expansion —
// collapse/archive fold IGNORED. Because canonical placement is exactly what the
// render nests (both the worker-bridge and coordinator-spawn shapes flow through
// CanonicalParents), this can never drift from what is actually drawn. The render
// passes respect fold; structuralReach deliberately does not, so a child merely
// hidden behind a collapsed or archived ancestor is still "reachable" here and is
// therefore NOT re-leaked to the top by the safety sweep — only a true
// cycle-orphan (a chain that loops without ever reaching a root) is.
func (r *Rail) structuralReach(canonical map[int64]heramodel.CanonParent) map[int64]bool {
	var resolves func(id int64, seen map[int64]bool) bool
	resolves = func(id int64, seen map[int64]bool) bool {
		cp, ok := canonical[id]
		if !ok {
			return true // no canonical parent → a top-level root → reachable
		}
		if seen[id] {
			return false // chain cycles without reaching a root
		}
		seen[id] = true
		return resolves(cp.OrchID, seen)
	}
	reach := make(map[int64]bool)
	for _, sec := range [][]heramodel.OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			if resolves(sec[i].ID, map[int64]bool{}) {
				reach[sec[i].ID] = true
			}
		}
	}
	return reach
}

// appendOrch emits an orchestrator HEADER (the folded coordinator) and, when
// expanded, its non-coordinator roles nested via appendOrchWorkers. The header
// is rendered once per build (placed guard). dim propagates an archived
// placement down the whole subtree.
func (r *Rail) appendOrch(o *heramodel.OrchView, depth int, dim bool, canonical map[int64]heramodel.CanonParent, placed map[int64]bool) {
	if placed[o.ID] {
		return
	}
	// Filter: an orchestrator with no match anywhere in its subtree is not placed
	// (left unplaced so the bottom-Archive / safety passes also skip it).
	if !r.orchVisible(o.ID) {
		return
	}
	placed[o.ID] = true
	r.rows = append(r.rows, railRow{
		kind:         rrOrch,
		orch:         o,
		depth:        depth,
		dim:          dim,
		collOrchID:   o.ID,
		ancestryOnly: r.filterActive() && !r.orchMatchesOwnQuery(o),
	})
	if r.isCollapsed(o.ID) {
		// Partial-fold reveal: a closed coordinator whose subtree needs input
		// still peeks through to the specific ancestor chain(s) down to each
		// needs-input leaf, even though the fold stays visually closed —
		// every other sibling at every level stays fully hidden (add-hera-
		// jump-question). Pure rendering: fold state (isCollapsed) is never
		// mutated by this branch.
		if o.SubtreeNeedsInput {
			r.appendOrchWorkers(o, depth+1, dim, canonical, placed, true)
		}
		return
	}
	r.appendOrchWorkers(o, depth+1, dim, canonical, placed, false)
}

// appendOrchWorkers emits o's non-coordinator role rows at `depth`. A worker
// that bridges a not-yet-placed child orchestrator nests that child's workers
// one level deeper, immediately under the worker row (the worker row IS the
// child's coordinator — same multi-binding task — so no separate child header is
// drawn). The bridging row carries collOrchID = child.ID so Space folds the
// nested subtree, and a chevron marks it foldable. The placed guard breaks any
// bridge cycle and guarantees single placement.
//
// The bridging row keeps its PARENT worker selection context (a worker role
// under o): nesting is purely visual, so mutations (notably Ctrl+D) act on the
// worker role, never the child orchestrator — conservative multi-binding safety.
// revealOnly restricts this call to the partial-fold-reveal path: o is a
// closed ancestor whose subtree needs input, so ONLY children whose own
// SubtreeNeedsInput is true are emitted (recursively, at every level below —
// each level's OWN collapse flag is ignored, since the operator can't see or
// toggle a fold inside a subtree that isn't otherwise visible). Archived and
// pinned-floated roles are skipped exactly as in normal mode (they already
// have their own, independent visibility mechanisms — Archive expando /
// Pinned section — so the reveal leaves them alone rather than layering a
// third), and the per-coordinator Archive expando itself is never rendered
// in reveal mode.
func (r *Rail) appendOrchWorkers(o *heramodel.OrchView, depth int, dim bool, canonical map[int64]heramodel.CanonParent, placed map[int64]bool, revealOnly bool) {
	var archived []*heramodel.RoleView
	for i := range o.Roles {
		w := &o.Roles[i]
		if w.Kind == db.HeraKindCoordinator {
			continue // folded into the header / the bridging row above
		}
		// A pinned non-root role floats into the Pinned section (rendered first,
		// already placed) — suppress it (and its hoisted bridged child) here so it
		// renders exactly once (add-hera-pin-nonroot).
		if r.pinnedFloat[w.RoleID] {
			continue
		}
		if w.Archived {
			if !revealOnly {
				// BUG-022 Q3: HIDING a worker (or a bridging sub-coordinator) folds it
				// into the per-coordinator Archive expando — and a bridging sub-coord
				// drags its WHOLE subtree in with it (structure retained INSIDE the
				// expando), NOT rendered dimmed-in-place. ALL archived workers go to
				// the expando here; the expando's appendWorkerRow (below) nests any
				// bridged sub-team beneath the hidden worker when the expando is open.
				// The orphaning hazard the old in-place rule guarded against (an
				// archived bridging worker hoisted while its child is left unplaced →
				// safety-swept flat to the top) is now closed by `structuralReach`: a
				// child whose canonical-parent chain reaches a root is never re-leaked
				// by the safety sweep, so when the expando is COLLAPSED the child stays
				// hidden under its hidden parent instead of leaking. See
				// TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando (both fold states).
				archived = append(archived, w)
			}
			continue
		}
		if revealOnly {
			if !w.SubtreeNeedsInput {
				continue // prune — no needs-input descendant on this path
			}
			r.appendWorkerRow(o.ID, w, depth, dim, canonical, placed, true)
			continue
		}
		if !r.workerRowVisible(o.ID, w, canonical, placed) {
			continue // filtered out (no name match, bridges no visible child)
		}
		r.appendWorkerRow(o.ID, w, depth, dim, canonical, placed, false)
	}

	// Coordinator-spawned sub-teams: a child orchestrator whose coordinator is the
	// SAME agent as o's (the multi-orch coordinator hera_new_orchestrator creates)
	// has no worker row to nest under — the parent's coordinator IS the bridge. It
	// nests as its own sub-orchestrator header directly under o, recursively, at
	// the worker depth, ONLY when o is its canonical coordinator-spawn parent. The
	// canonical guard (plus the placed guard) breaks the shared-task symmetry so a
	// multi-parent child renders under exactly one parent regardless of fold.
	for _, child := range r.model.CoordBridgeChildren(o) {
		if placed[child.ID] {
			continue
		}
		if cp, ok := canonical[child.ID]; !ok || !cp.CoordSpawn || cp.OrchID != o.ID {
			continue
		}
		if revealOnly {
			if !child.SubtreeNeedsInput {
				continue
			}
			r.appendOrchRevealPath(child, depth, dim, canonical, placed)
			continue
		}
		r.appendOrch(child, depth, dim, canonical, placed)
	}

	if revealOnly {
		return // no per-coordinator Archive expando in reveal mode
	}

	// Per-coordinator Archive (N) expando: HIDDEN (archived) roles fold under their
	// coordinator's active agents, collapsed by default. Distinct from the bottom
	// Archive section (archived ROOT orchestrators). Hidden roles render dimmed and
	// — for a bridging sub-coordinator — still nest their WHOLE bridged sub-team
	// beneath them INSIDE the expando (forced-dim down the subtree), so hiding a
	// sub-coord collapses its subtree out of the main view with structure retained
	// (BUG-022 Q3). Under an active filter only visible hidden roles list (the
	// expando is pruned when none match), and the expando auto-expands.
	visibleArchived := archived
	if r.filterActive() {
		visibleArchived = visibleArchived[:0:0]
		for _, w := range archived {
			if r.workerRowVisible(o.ID, w, canonical, placed) {
				visibleArchived = append(visibleArchived, w)
			}
		}
	}
	if len(visibleArchived) > 0 {
		r.rows = append(r.rows, railRow{
			kind:         rrArchiveExpando,
			archiveOwner: o.ID,
			depth:        depth,
			dim:          dim,
			label:        fmt.Sprintf("Archive (%d)", len(archived)),
		})
		if r.isCoordArchiveOpen(o.ID) {
			for _, w := range visibleArchived {
				r.appendWorkerRow(o.ID, w, depth+1, true, canonical, placed, false)
			}
		}
	}
}

// appendOrchRevealPath emits a coordinator-spawned child orchestrator's own
// header row and recurses into it in reveal-only mode, UNCONDITIONALLY —
// ignoring the child's own collapse flag entirely. It is only ever reached
// from appendOrchWorkers' revealOnly branch, already gated on the child's
// SubtreeNeedsInput being true, for a closed ANCESTOR the operator can't see
// into — so the child's own fold state is not meaningful here (there is
// nothing visible for the operator to have toggled), unlike appendOrch's
// normal collapsed-vs-expanded gate at the top level.
func (r *Rail) appendOrchRevealPath(o *heramodel.OrchView, depth int, dim bool, canonical map[int64]heramodel.CanonParent, placed map[int64]bool) {
	if placed[o.ID] {
		return
	}
	placed[o.ID] = true
	r.rows = append(r.rows, railRow{
		kind:       rrOrch,
		orch:       o,
		depth:      depth,
		dim:        dim,
		collOrchID: o.ID,
	})
	r.appendOrchWorkers(o, depth+1, dim, canonical, placed, true)
}

// workerBridgeChild returns the not-yet-placed child orchestrator that nests under
// worker w — the orchestrator whose CANONICAL parent is ownerID via the
// worker-bridge shape AND whose coordinator bridge task matches w's bridge task —
// or nil. Resolving through the precomputed canonical parent (NOT a first-wins
// bridge-index lookup) is what makes a multi-parent child render under one
// deterministic parent regardless of fold/order: when the same coordinator task
// coordinates two orchestrators, the bridge index would pick whichever appears
// first, but only the canonical assignment is honoured here. Coordinator-kind,
// torn-down, and already-placed links do not bridge. Shared by appendOrchWorkers
// (hoist-vs-nest decision) and appendWorkerRow (the nest itself) so both agree.
func (r *Rail) workerBridgeChild(ownerID int64, w *heramodel.RoleView, canonical map[int64]heramodel.CanonParent, placed map[int64]bool) *heramodel.OrchView {
	if w.Kind == db.HeraKindCoordinator || !heramodel.RoleBridges(w) {
		return nil
	}
	ck := heramodel.BridgeTaskID(w)
	if ck == "" {
		return nil
	}
	for _, sec := range [][]heramodel.OrchView{r.model.Pinned, r.model.Active, r.model.Archived} {
		for i := range sec {
			c := &sec[i]
			cp, ok := canonical[c.ID]
			if !ok || cp.CoordSpawn || cp.OrchID != ownerID || placed[c.ID] {
				continue
			}
			if c.CoordBridgeTaskID() == ck {
				return c
			}
		}
	}
	return nil
}

// appendWorkerRow emits one worker role row at `depth` and, when it bridges a
// not-yet-placed child orchestrator, nests the child's workers one level deeper.
// The worker ROW dims when the worker itself is archived (an honest per-node
// signal); the child subtree dims only from inherited dim or the CHILD's own
// archived state — an active child under an archived bridging worker stays
// normal (it is live work, not archived placement).
//
// revealOnly (partial-fold reveal): when true, the bridged child — a nested
// sub-coordinator whose worker row IS its coordinator, so it gets no separate
// header — recurses in reveal-only mode UNCONDITIONALLY, ignoring the
// child's own collapse flag, mirroring appendOrchRevealPath's rationale (the
// caller already gated this row on w.SubtreeNeedsInput being true before
// calling appendWorkerRow, so the child is known to need the reveal).
func (r *Rail) appendWorkerRow(ownerID int64, w *heramodel.RoleView, depth int, dim bool, canonical map[int64]heramodel.CanonParent, placed map[int64]bool, revealOnly bool) {
	rowDim := dim || w.Archived
	child := r.workerBridgeChild(ownerID, w, canonical, placed)
	// Under an active filter, only bridge a VISIBLE child: a non-matching subtree
	// must not surface (and the bridging row drops its chevron). Filtering and
	// reveal never overlap in practice (the `/` filter force-expands every
	// fold, so appendOrch's revealOnly branch — gated on isCollapsed — never
	// triggers while filtering), so this check is skipped in reveal mode.
	if child != nil && !revealOnly && r.filterActive() && !r.filterVis[child.ID] {
		child = nil
	}
	collID := int64(0)
	if child != nil {
		collID = child.ID
	}
	r.rows = append(r.rows, railRow{
		kind:         rrRole,
		role:         w,
		depth:        depth,
		dim:          rowDim,
		collOrchID:   collID,
		ancestryOnly: !revealOnly && r.filterActive() && !r.filterMatches(w.Name),
	})
	if child != nil {
		placed[child.ID] = true
		if revealOnly {
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, true)
		} else if !r.isCollapsed(child.ID) {
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, false)
		} else if child.SubtreeNeedsInput {
			// Partial-fold reveal (BUG-064): the child is still collapsed even
			// though its ancestor (o) just expanded — reveal-only mirrors
			// appendPinnedRole's identical fallback so re-expanding an outer
			// coordinator doesn't blank out a still-closed nested
			// coordinator's own needs-input leaf.
			r.appendOrchWorkers(child, depth+1, dim || child.Archived, canonical, placed, true)
		}
	}
}

// --- pinned non-root roles (add-hera-pin-nonroot) ---

// pinnedRoleEntry pairs a floated pinned role with its computed lineage trail.
type pinnedRoleEntry struct {
	role       *heramodel.RoleView
	breadcrumb string
}

// collectPinnedRoles walks the Active and Archived orchestrators and returns
// every pinned NON-coordinator role that floats OUT of its parent subtree into
// the Pinned section, paired with its lineage breadcrumb. It also (re)sets
// r.pinnedFloat (the role-id suppression set appendOrchWorkers consults) so the
// floated row renders exactly once. Roles under a PINNED orchestrator are NOT
// collected — that orchestrator already floats as a root and its roles stay
// nested under it (mirrors the plugin's rolePinnedOut). Under an active filter a
// role is kept only when its own name matches OR it bridges a filter-visible
// child. A role whose orchestrator cannot be resolved for the breadcrumb is
// skipped (logged) rather than rendered without lineage.
func (r *Rail) collectPinnedRoles(canonical map[int64]heramodel.CanonParent) []pinnedRoleEntry {
	var out []pinnedRoleEntry
	floatSet := make(map[int64]bool)
	for _, sec := range [][]heramodel.OrchView{r.model.Active, r.model.Archived} {
		for i := range sec {
			o := &sec[i]
			for j := range o.Roles {
				rv := &o.Roles[j]
				if !rv.Pinned || rv.Kind == db.HeraKindCoordinator {
					continue
				}
				if r.filterActive() && !r.filterMatches(rv.Name) {
					// Keep a non-matching pinned row only when it bridges a visible
					// child (so a matching nested subtree is not orphaned).
					c := r.workerBridgeChild(o.ID, rv, canonical, map[int64]bool{})
					if c == nil || !r.filterVis[c.ID] {
						continue
					}
				}
				bc := r.pinnedBreadcrumb(rv, canonical)
				if bc == "" {
					uxlog.Log("[hera-view] pinned role %d (%s): parent orch %d unresolved, not floating", rv.RoleID, rv.Name, rv.OrchID)
					continue
				}
				out = append(out, pinnedRoleEntry{role: rv, breadcrumb: bc})
				floatSet[rv.RoleID] = true
			}
		}
	}
	r.pinnedFloat = floatSet
	return out
}

// pinnedBreadcrumb builds a floated role's lineage trail: the orchestrator-name
// chain from the root down to and including the role's own orchestrator
// (role.OrchID), joined with " › " and trailing " › " (e.g. "root › sub › ").
// The chain is walked via CanonicalParents — the SAME deterministic, fold-
// independent parentage the rail nests by — so the trail matches the rendered
// tree. Returns "" when any orchestrator on the chain cannot be resolved.
func (r *Rail) pinnedBreadcrumb(rv *heramodel.RoleView, canonical map[int64]heramodel.CanonParent) string {
	var names []string
	seen := make(map[int64]bool)
	for id := rv.OrchID; id != 0; {
		o := r.model.OrchByID(id)
		if o == nil {
			return "" // unresolvable parent → caller skips (no context-free render)
		}
		names = append(names, o.Name)
		if seen[id] {
			break // cycle guard (matches BridgeSubtree's visited discipline)
		}
		seen[id] = true
		cp, ok := canonical[id]
		if !ok {
			break // reached a top-level root
		}
		id = cp.OrchID
	}
	var b strings.Builder
	for i := len(names) - 1; i >= 0; i-- {
		b.WriteString(names[i])
		b.WriteString(" › ")
	}
	return b.String()
}

// appendPinnedRole emits a floated pinned role as a two-line entry: line 1 is a
// selectable rrPinnedBreadcrumb (dimmed icon + lineage), line 2 a non-selectable
// rrRole continuation (the name). When the role bridges a child orchestrator (a
// pinned sub-coordinator) the breadcrumb row carries collOrchID = child.ID (so
// Space folds it and Ctrl+D cascades), and the child's subtree is hoisted
// beneath the entry and marked placed so the active passes render it exactly
// once (full parity with the plugin's BUG-021).
func (r *Rail) appendPinnedRole(pe pinnedRoleEntry, canonical map[int64]heramodel.CanonParent, placed map[int64]bool) {
	rv := pe.role
	child := r.workerBridgeChild(rv.OrchID, rv, canonical, placed)
	if child != nil && r.filterActive() && !r.filterVis[child.ID] {
		child = nil // a non-visible child does not hoist (and the chevron drops)
	}
	collID := int64(0)
	if child != nil {
		collID = child.ID
	}
	bcAncestry := r.filterActive() && !r.filterMatches(rv.Name)
	r.rows = append(r.rows, railRow{kind: rrPinnedBreadcrumb, role: rv, depth: 0, breadcrumb: pe.breadcrumb, collOrchID: collID, ancestryOnly: bcAncestry})
	r.rows = append(r.rows, railRow{kind: rrRole, role: rv, depth: 1, breadcrumbCont: true, ancestryOnly: bcAncestry})
	if child != nil {
		placed[child.ID] = true
		if !r.isCollapsed(child.ID) {
			r.appendOrchWorkers(child, 2, child.Archived, canonical, placed, false)
		} else if child.SubtreeNeedsInput {
			// Partial-fold reveal applies here too: a pinned role's own bridged
			// child can be closed with a hidden needs-input descendant.
			r.appendOrchWorkers(child, 2, child.Archived, canonical, placed, true)
		}
	}
}

// --- navigation ---

// CursorDown moves to the next selectable row.
func (r *Rail) CursorDown() { r.step(1) }

// CursorUp moves to the previous selectable row.
func (r *Rail) CursorUp() { r.step(-1) }

func (r *Rail) step(dir int) {
	if len(r.rows) == 0 {
		return
	}
	i := r.cursor
	for {
		i += dir
		if i < 0 || i >= len(r.rows) {
			return // no further selectable row; leave cursor put
		}
		row := r.rows[i]
		// Crossing INTO a differently-focused kanban group's header (BUG-independent,
		// add-kanban-focus-fold, design.md decision 3): re-focus rather than treat the
		// header as just another non-selectable row to skip. A header belonging to the
		// group ALREADY focused (e.g. stepping up into your own group's own header) is
		// not a crossing — group == r.focusedKanban falls through to the ordinary
		// non-selectable skip below, exactly as today.
		if group, ok := row.kanbanGroupHeader(); ok && group != r.focusedKanban {
			r.crossKanbanBoundary(group, dir)
			return
		}
		if row.selectable() {
			r.setCursor(i)
			return
		}
	}
}

// crossKanbanBoundary re-focuses the rail on `group` — expanding it and
// collapsing whichever group was focused before — and lands the cursor on its
// first (dir>0, stepping down) or last (dir<0, stepping up) member row. The
// header itself is never a landing spot. No restoreCursor/currentRef is
// involved: this is a brand-new landing spot, not preserving an old one
// (design.md decision 3).
func (r *Rail) crossKanbanBoundary(group db.HeraKanbanStatus, dir int) {
	r.focusedKanban = group
	r.buildRows()
	for i, row := range r.rows {
		if g, ok := row.kanbanGroupHeader(); ok && g == group {
			r.landOnGroupMember(i, dir)
			return
		}
	}
}

// landOnGroupMember sets the cursor to the first (dir>0) or last (dir<0)
// selectable row belonging to the group whose header sits at headerIdx — the
// span of rows between the header and the next section boundary (the next
// rrRule, or the end of the rail). Bounding the scan at the next rrRule keeps
// it from wandering into a LATER section (Freelance/Archive/the next kanban
// group), whose rows are selectable too but belong to a different group
// entirely.
func (r *Rail) landOnGroupMember(headerIdx int, dir int) {
	if dir > 0 {
		for i := headerIdx + 1; i < len(r.rows); i++ {
			if r.rows[i].kind == rrRule {
				return
			}
			if r.rows[i].selectable() {
				r.setCursor(i)
				return
			}
		}
		return
	}
	last := -1
	for i := headerIdx + 1; i < len(r.rows); i++ {
		if r.rows[i].kind == rrRule {
			break
		}
		if r.rows[i].selectable() {
			last = i
		}
	}
	if last >= 0 {
		r.setCursor(last)
	}
}

func (r *Rail) setCursor(i int) {
	if i == r.cursor {
		return
	}
	r.cursor = i
	if r.onSelectionChanged != nil {
		r.onSelectionChanged()
	}
	// Persist the new selection (BUG-002). setCursor is the single funnel for
	// user-driven cursor moves (via step); the load-restore and filter rebuilds
	// write r.cursor directly (not through here), so neither persists.
	r.persist()
}

func (r *Rail) clampCursor() {
	if len(r.rows) == 0 {
		r.cursor = 0
		return
	}
	if r.cursor < 0 {
		r.cursor = 0
	}
	if r.cursor >= len(r.rows) {
		r.cursor = len(r.rows) - 1
	}
	// Land on a selectable row if the clamp parked on a rule/header.
	if !r.rows[r.cursor].selectable() {
		for i := r.cursor; i < len(r.rows); i++ {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
		for i := r.cursor; i >= 0; i-- {
			if r.rows[i].selectable() {
				r.cursor = i
				return
			}
		}
	}
}

// ToggleCollapse flips the fold state of the collapsible row under the cursor.
// Roles and rules are no-ops.
func (r *Rail) ToggleCollapse() {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return
	}
	row := r.rows[r.cursor]
	switch {
	case row.collOrchID > 0:
		r.collapsed[row.collOrchID] = !r.collapsed[row.collOrchID]
	case row.archiveOwner > 0:
		r.coordArchiveOpen[row.archiveOwner] = !r.coordArchiveOpen[row.archiveOwner]
	case row.collFreelance:
		r.freelanceCollap = !r.freelanceCollap
	case row.collArchive:
		r.archiveCollapsed = !r.archiveCollapsed
	default:
		return
	}
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
	r.persist() // fold change (BUG-002)
}

// SelectByTaskID moves the cursor to the first selectable role row whose bound
// task is taskID, firing onSelectionChanged so the panes rebind. Falling back
// when no role row matches, it also resolves a coordinator's own task id to
// its orchestrator's rrOrch HEADER row (fix-ctrlg-coordinator-own-need) — a
// coordinator is never its own role-bearing row, so this is the only way its
// own task id can land the cursor at all. Returns true when either scan finds
// a row in the CURRENT (built) row set. Used by the plan view's leaf-Enter to
// jump to a node's role WITHIN the Hera view (BUG-002) — never a Tasks-tab
// switch. A taskID with no visible row (e.g. under a collapsed branch the plan
// never surfaces) is a no-op returning false.
func (r *Rail) SelectByTaskID(taskID string) bool {
	if taskID == "" {
		return false
	}
	// Re-focus the target's kanban group BEFORE scanning rows (add-kanban-focus-
	// fold): a role bound to taskID may live in a group other than the one
	// currently focused, in which case its row does not exist in r.rows at all
	// yet — resolved independently of EnsureAncestorsExpanded (callers may
	// invoke this directly without it) via the model, not the built rows.
	if orchIDs := r.model.OrchIDsForTask(taskID); len(orchIDs) > 0 {
		if group, ok := r.focusGroupOf(orchIDs[0]); ok && group != r.focusedKanban {
			r.focusedKanban = group
			r.buildRows()
		}
	}
	for i, row := range r.rows {
		if row.selectable() && row.role != nil && row.role.TaskID == taskID {
			r.setCursor(i) // funnels through onSelectionChanged + persist
			return true
		}
	}
	// A coordinator role never gets its own role-bearing row — it is folded
	// entirely into its orchestrator's rrOrch HEADER row (appendOrchWorkers
	// skips db.HeraKindCoordinator). Run only when the role-row scan above
	// found nothing, so a header never shadows a genuine role match. When two
	// header rows share the SAME coordinator task (a coordinator-spawned
	// nested sub-team — one coordinator agent driving both a parent and child
	// orchestrator), this resolves to whichever header appears FIRST in row
	// order — the same first-match convention already governing any other
	// multi-binding task (design.md Decision 3, fix-ctrlg-coordinator-own-need).
	for i, row := range r.rows {
		if row.kind == rrOrch && row.orch.CoordRole() != nil && row.orch.CoordRole().TaskID == taskID {
			r.setCursor(i)
			return true
		}
	}
	return false
}

// EnsureAncestorsExpanded uncollapses orchID and every ancestor on its canonical
// parent chain up to the root, so a row nested under folded coordinator(s) becomes
// visible. It walks CanonicalParents() — the SAME fold-independent parentage the
// rail nests by — with a visited guard against cycles, handling deeply nested
// sub-coordinators (the WHOLE chain, not just the immediate parent). When any fold
// actually flips it rebuilds the rows and persists the change, so the expand
// survives like a user Space-toggle. Used by the plan view's leaf-Enter join
// (BUG-007): a folded coordinator must not swallow the join — expand first, then
// SelectByTaskID can see (and select) the now-built row.
func (r *Rail) EnsureAncestorsExpanded(orchID int64) {
	if orchID == 0 {
		return
	}
	canonical := r.model.CanonicalParents()
	seen := make(map[int64]bool)
	changed := false
	for id := orchID; id != 0; {
		if seen[id] {
			break // cycle guard (matches BridgeSubtree's visited discipline)
		}
		seen[id] = true
		if r.collapsed[id] {
			r.collapsed[id] = false
			changed = true
		}
		cp, ok := canonical[id]
		if !ok {
			break // reached a top-level root
		}
		id = cp.OrchID
	}
	// Re-focus orchID's kanban group too (add-kanban-focus-fold): even when no
	// per-orchestrator fold changed above, the target row still won't exist in
	// the built rows unless its top-level group is the focused one.
	if group, ok := r.focusGroupOf(orchID); ok && group != r.focusedKanban {
		r.focusedKanban = group
		changed = true
	}
	if !changed {
		return
	}
	ref := r.currentRef()
	r.buildRows()
	r.restoreCursor(ref)
	r.clampCursor()
	r.persist() // fold change (BUG-002), like ToggleCollapse
}

// needsInputTaskID returns the argus task id this row's OWN needs-input
// signal points at, and whether the row qualifies as a jump target at all
// (add-hera-jump-question, Ctrl+G; extended by fix-ctrlg-coordinator-own-need
// to also reach a coordinator's own need). "Own" — needsInputOwn(), the same
// unit the switcher's needs-input-first sort and the rail's leaf "(?)" glyph
// both key on. For a genuine role-bearing row (rrRole/rrFreelanceRole/
// rrPinnedBreadcrumb), it qualifies via row.role.needsInputOwn(). A top-level
// orchestrator's rrOrch HEADER row — and, identically, a coordinator-spawned
// nested sub-team's own header row (one coordinator agent simultaneously
// driving a second orchestrator) — ALSO qualifies now, via its coordinator's
// OWN signal (row.orch.CoordRole().needsInputOwn()), NEVER the rolled-up
// SubtreeNeedsInput: remove-needs-input-rollup-glyph retired the BUG-028
// fallback that once surfaced the rollup on a coordinator-less header, so the
// header glyph itself (statusIcon, drawOrchRow) now shows only a coordinator's
// own signal too — this jump target and the glyph agree by construction, and
// a descendant that actually needs input still surfaces as its own, separate
// candidate through the partial-fold-reveal mechanism rather than being
// double-counted here. SelectByTaskID gained a matching header-row scan, so a
// coordinator's own need is now reachable rather than a "found but
// unreachable" dead cycle stop. A NESTED sub-coordinator that bridges as an
// ordinary role-bearing worker row in its PARENT orchestrator
// (appendWorkerRow) is unaffected either way — its own need was already a
// reachable candidate here. A non-selectable row (e.g. a pinned entry's
// non-selectable continuation line) never qualifies, mirroring

func (row railRow) needsInputTaskID() (string, bool) {
	if !row.selectable() {
		return "", false
	}
	if row.role != nil {
		if row.role.ShowsNeedsInput() && row.role.TaskID != "" {
			return row.role.TaskID, true
		}
		return "", false
	}
	if row.kind == rrOrch && row.orch != nil {
		if coord := row.orch.CoordRole(); coord != nil && coord.ShowsNeedsInput() && coord.TaskID != "" {
			return coord.TaskID, true
		}
	}
	return "", false
}

// NextNeedsInputTaskID returns the argus task id of the next row — in today's
// built rail order (Pinned → Active depth-first → Freelance → Archive), the
// SAME traversal appendOrchWorkers already reveals a hidden needs-input leaf
// through when its ancestor is folded — whose own signal needs input,
// scanning strictly AFTER the current cursor and wrapping around the whole
// ring back to (and including) the cursor itself. Mirrors SelectByTaskID's
// scan-and-select shape, but position- rather than id-driven: repeated calls
// therefore cycle forward through every candidate in turn (never repeatedly
// returning the row the cursor already sits on) while a single remaining
// candidate keeps re-selecting itself once the ring wraps all the way
// around — the caller (HeraPage.JumpToNextNeedsInput) does the actual
// ancestor-expand + select. Returns ("", false) when no row qualifies or the
// rail is empty.
func (r *Rail) NextNeedsInputTaskID() (string, bool) {
	n := len(r.rows)
	if n == 0 {
		return "", false
	}
	cursor := r.cursor
	if cursor < 0 || cursor >= n {
		cursor = 0
	}
	for step := 1; step <= n; step++ {
		i := (cursor + step) % n
		if id, ok := r.rows[i].needsInputTaskID(); ok {
			return id, true
		}
	}
	return "", false
}

// Selected returns the heramodel.RoleView under the cursor, or nil when the cursor is on
// a header/orchestrator. 6b uses this to bind the panes.
func (r *Rail) Selected() *heramodel.RoleView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].role
}

// SelectedOrch returns the heramodel.OrchView under the cursor, or nil.
func (r *Rail) SelectedOrch() *heramodel.OrchView {
	if r.cursor < 0 || r.cursor >= len(r.rows) {
		return nil
	}
	return r.rows[r.cursor].orch
}

// Selection returns the (role, orchestrator) context under the cursor. The
// orchestrator is resolved from the selected role's OrchID — correct even in
// the multi-binding case where the same task surfaces under two orchestrators
// (each via a distinct role with a distinct OrchID) — or taken directly when
// the cursor rests on an orchestrator header. 6b binds the panes off this; 6c
// reads it for mutations via the orchestrator disambiguator.
func (r *Rail) Selection() heramodel.Selection {
	role := r.Selected()
	orch := r.SelectedOrch()
	if orch == nil && role != nil {
		orch = r.model.OrchByID(role.OrchID)
	}
	sel := heramodel.Selection{Role: role, Orch: orch}
	if orch != nil {
		_, hasParent := r.canonical[orch.ID]
		sel.TopLevelOrch = !hasParent
	}
	// A role row whose collOrchID is set is a bridging sub-coordinator row; carry
	// the child orchestrator id so Ctrl+D can cascade the nested sub-team.
	if r.cursor >= 0 && r.cursor < len(r.rows) {
		if row := r.rows[r.cursor]; row.role != nil && row.collOrchID > 0 {
			sel.BridgeChildOrchID = row.collOrchID
		}
	}
	return sel
}

// Rows returns the flattened row count (test seam).
func (r *Rail) Rows() int { return len(r.rows) }

// OrchCollapsed reports whether the given orchestrator ID is folded (test seam).
func (r *Rail) OrchCollapsed(id int64) bool { return r.collapsed[id] }

// CursorIndex returns the cursor's row index (test seam).
func (r *Rail) CursorIndex() int { return r.cursor }

// FocusedKanban returns the currently-expanded kanban sub-group (test seam,
// add-kanban-focus-fold).
func (r *Rail) FocusedKanban() db.HeraKanbanStatus { return r.focusedKanban }

// --- rendering ---

// Draw paints the rail inside a bordered panel, covering its full bounding
// rect (DrawBorderedPanel blanks the interior) so no stale cells survive — per
// the CLAUDE.md UX-rendering rules (no Sync; full-rect coverage instead).
func (r *Rail) Draw(screen tcell.Screen) {
	r.DrawForSubclass(screen, r)
	r.animFrame = spinnerFrame()
	x, y, w, h := r.GetRect()
	if w <= 0 || h <= 0 {
		return
	}
	borderStyle := theme.StyleBorder
	if r.focused {
		borderStyle = theme.StyleFocusedBorder
	}
	// Reflect the query in the border title while typing (BUG-028-RAIL: Enter/Esc
	// both fully clear the query, so it's never non-empty once input mode ends).
	title := " Hera "
	if r.filterActive() {
		title = " Hera /" + r.filterQuery + " "
	}
	inner := widget.DrawBorderedPanel(screen, x, y, w, h, title, borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	// While typing, a `/ <query>` input line takes the top row; the rows render
	// below it over the reduced viewport. DrawBorderedPanel already blanked the
	// interior, so no stale cells survive (no Sync — CLAUDE.md UX rules).
	rowY := inner.Y
	rowH := inner.H
	if r.filterInput {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "/ "+r.filterQuery+"▌", theme.StyleSelected)
		rowY = inner.Y + 1
		rowH = inner.H - 1
		if rowH <= 0 {
			return
		}
	}
	r.adjustOffset(rowH)
	for vis := 0; vis < rowH; vis++ {
		idx := r.offset + vis
		if idx >= len(r.rows) {
			break
		}
		// A two-line pinned entry highlights its continuation (line 2) when the
		// preceding breadcrumb (line 1, the cursor anchor) is selected.
		selected := idx == r.cursor
		if r.rows[idx].breadcrumbCont && idx-1 == r.cursor {
			selected = true
		}
		r.drawRow(screen, inner.X, rowY+vis, inner.W, r.rows[idx], selected)
	}
}

// adjustOffset keeps the cursor visible within the inner viewport height.
func (r *Rail) adjustOffset(viewH int) {
	if viewH <= 0 {
		return
	}
	if r.cursor < r.offset {
		r.offset = r.cursor
	}
	if r.cursor >= r.offset+viewH {
		r.offset = r.cursor - viewH + 1
	}
	if r.offset < 0 {
		r.offset = 0
	}
}

func (r *Rail) drawRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	const marker = '›'
	indent := row.depth * 2

	// heramodel.Selection marker gutter. The continuation (line 2) of a two-line pinned
	// entry never draws the '›' marker — the cursor anchors on line 1 — but its
	// text still renders selected-style when line 1 is selected.
	gutterStyle := theme.StyleDimmed
	if selected {
		if !row.breadcrumbCont {
			screen.SetContent(x, y, marker, nil, theme.StyleSelected)
		}
		gutterStyle = theme.StyleSelected
	}
	textX := x + 2 + indent
	textW := w - 2 - indent
	if textW <= 0 {
		return
	}

	switch row.kind {
	case rrRule:
		for c := x; c < x+w; c++ {
			screen.SetContent(c, y, '─', nil, theme.StyleBorder)
		}
	case rrEmpty:
		widget.DrawText(screen, textX, y, textW, row.label, theme.StyleDimmed)
	case rrSectionHeader:
		style := theme.StyleTitle
		if selected {
			style = theme.StyleSelected
		}
		label := row.label
		if row.collFreelance {
			label = chevron(r.freelanceCollap && !r.filterActive()) + " " + label
		}
		widget.DrawText(screen, textX, y, textW, label, style)
	case rrArchiveExpando:
		style := theme.StyleDimmed
		if selected {
			style = theme.StyleSelected
		}
		// Per-coordinator expandos fold via coordArchiveOpen; the bottom Archive
		// section folds via archiveCollapsed. While filtering both auto-expand, so
		// the chevron reads expanded to match the rendered rows.
		collapsed := r.archiveCollapsed && !r.filterActive()
		if row.archiveOwner > 0 {
			collapsed = !r.isCoordArchiveOpen(row.archiveOwner)
		}
		widget.DrawText(screen, textX, y, textW, chevron(collapsed)+" "+row.label, style)
	case rrOrch:
		r.drawOrchRow(screen, textX, y, textW, row, selected)
	case rrPinnedBreadcrumb:
		r.drawPinnedBreadcrumbRow(screen, textX, y, textW, row)
	case rrRole, rrFreelanceRole:
		if row.breadcrumbCont {
			r.drawBreadcrumbNameRow(screen, textX, y, textW, row.role, row.ancestryOnly, selected)
			return
		}
		r.drawRoleRow(screen, textX, y, textW, row, selected, gutterStyle)
	}
}

func (r *Rail) drawOrchRow(screen tcell.Screen, x, y, w int, row railRow, selected bool) {
	o := row.orch
	// greyed folds in ancestryOnly (BUG-028-RAIL) alongside the existing archived-
	// placement dim, so a heading kept only for tree context reads visually
	// non-selectable too.
	greyed := row.dim || row.ancestryOnly
	nameStyle := theme.StyleProject
	if greyed {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	// Coordinator status glyph first (the header IS the coordinator — folded
	// from a redundant child row). It reads with the same vocabulary as worker
	// rows; the glyph keeps its own status style even when the row is selected
	// (the glyph never lies). Worker-less / coordinator-less orchestrators skip it.
	if coord := o.CoordRole(); coord != nil {
		glyph, gstyle := statusIcon(coord, greyed, r.animFrame)
		screen.SetContent(col, y, glyph, nil, gstyle)
		col += 2
	}
	// A coordinator-less orchestrator (e.g. its coordinator role was nuked) has
	// no "own" signal and renders no needs-input glyph on its header at all —
	// the BUG-028 fallback that once surfaced heramodel.OrchView.SubtreeNeedsInput directly
	// here was retired by remove-needs-input-rollup-glyph. A blocked descendant
	// stays visible via its own row, peeked through the closed fold by the
	// partial-fold-reveal mechanism below.
	// chevron
	if col < x+w {
		screen.SetContent(col, y, []rune(chevron(r.isCollapsed(o.ID)))[0], nil, nameStyle)
		col += 2
	}
	// coordinator marker
	if col < x+w {
		screen.SetContent(col, y, heraIconCoord, nil, nameStyle)
		col += 2
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}
	agents := r.model.SubtreeAgentCount(o.ID)
	label := o.Name
	count := fmt.Sprintf(" %d", agents)
	// Subtree cost figure (add-coordinator-cost-estimate, design.md Decision
	// 5/7): appended alongside the existing agent-count badge, omitted
	// entirely when nothing in the subtree has ever accrued anything — never
	// a misleading "$0.00" (Decision 6).
	if cost, any := r.model.SubtreeCostUSD(o.ID); any {
		count += " · " + heramodel.FormatCostUSD(cost)
	}
	if len(label)+len(count) > remaining {
		widget.DrawText(screen, col, y, remaining, label, nameStyle)
		return
	}
	widget.DrawText(screen, col, y, remaining, label, nameStyle)
	widget.DrawText(screen, x+w-len(count), y, len(count), count, theme.StyleDimmed)
}

func (r *Rail) drawRoleRow(screen tcell.Screen, x, y, w int, row railRow, selected bool, _ tcell.Style) {
	role := row.role
	// greyed folds in ancestryOnly (BUG-028-RAIL) alongside the existing archived-
	// placement dim, so a bridging heading kept only for tree context reads
	// visually non-selectable too.
	greyed := row.dim || row.ancestryOnly
	icon, iconStyle := statusIcon(role, greyed, r.animFrame)
	nameStyle := theme.StyleNormal
	if greyed {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	col := x
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2
	// A bridging sub-coordinator row (collOrchID set) reads like a nested
	// orchestrator header: a fold chevron + the coordinator marker before the
	// name, so the operator can tell it carries (and folds) a child subtree.
	if row.collOrchID > 0 {
		if col < x+w {
			markerStyle := nameStyle
			if !selected {
				markerStyle = theme.StyleProject
				if greyed {
					markerStyle = theme.StyleDimmed
				}
			}
			screen.SetContent(col, y, []rune(chevron(r.isCollapsed(row.collOrchID)))[0], nil, markerStyle)
			col += 2
			if col < x+w {
				screen.SetContent(col, y, heraIconCoord, nil, markerStyle)
				col += 2
			}
		}
	}
	remaining := w - (col - x)
	if remaining <= 0 {
		return
	}

	// Context-pressure indicator (add-worker-context-indicator): a worker or
	// freelance role reserves a trailing 2-column slot (a blank separator
	// column + the glyph column) regardless of its current percentage, so the
	// name never reflows the instant a row crosses a threshold. Coordinators
	// never reserve it — they keep the live-count badge in that position
	// (drawOrchRow) instead. Reserved BEFORE the PR tag below so both
	// trailing marks compose rather than overwrite each other.
	reserveInd, indGlyph, indStyle := contextIndicator(role)
	indW := 0
	if reserveInd {
		indW = 2
	}
	drawInd := func() {
		if indGlyph != 0 {
			screen.SetContent(x+w-1, y, indGlyph, nil, indStyle)
		}
	}
	rightW := w - indW
	remaining = rightW - (col - x)
	if remaining <= 0 {
		drawInd()
		return
	}

	// PR indicator: a managed row whose bound task has an open PR renders a
	// right-aligned "PR" tag, reserving space so the name truncates instead of
	// overwriting it. Mirrors the Details roster's PR mark, on the rail row.
	const prTag = "PR"
	if r.rolePR(role) && remaining > len(prTag)+1 {
		widget.DrawText(screen, col, y, remaining-len(prTag)-1, role.Name, nameStyle)
		prStyle := theme.StyleInReview
		if row.dim {
			prStyle = theme.StyleDimmed
		}
		widget.DrawText(screen, x+rightW-len(prTag), y, len(prTag), prTag, prStyle)
		drawInd()
		return
	}
	widget.DrawText(screen, col, y, remaining, role.Name, nameStyle)
	drawInd()
}

// contextIndicator reports the trailing context-pressure mark for a role row
// (add-worker-context-indicator): whether the row's kind reserves the
// 2-column slot at all (workers and freelance — coordinators are excluded,
// they already have the coord-hook's budget/nudge/recycle guard,
// cmd/argus/coord_hook.go), and if so, what glyph and style to draw there
// right now. reserve is independent of the current percentage so a name
// never reflows the instant a row crosses the 40% threshold; glyph is 0
// (draw nothing) when the role is not live, is archived, or is under 40%.
func contextIndicator(role *heramodel.RoleView) (reserve bool, glyph rune, style tcell.Style) {
	if role == nil || role.Kind == db.HeraKindCoordinator {
		return false, 0, tcell.StyleDefault
	}
	reserve = true
	if !role.Live || role.Archived {
		return reserve, 0, tcell.StyleDefault
	}
	switch {
	case role.ContextPercent >= 90:
		return reserve, '!', theme.StyleContextCritical
	case role.ContextPercent >= 65:
		return reserve, '•', theme.StyleContextHot
	case role.ContextPercent >= 40:
		return reserve, '•', theme.StyleContextWarm
	default:
		return reserve, 0, tcell.StyleDefault
	}
}

// drawPinnedBreadcrumbRow renders line 1 of a two-line pinned non-root entry:
// the role's status glyph (dimmed — context only, the selection cue is the
// gutter marker drawn by drawRow) followed by the dimmed lineage trail. An
// over-wide trail is left-truncated with a leading "…" so the NEAREST parent
// (rightmost text) stays visible (add-hera-pin-nonroot).
func (r *Rail) drawPinnedBreadcrumbRow(screen tcell.Screen, x, y, w int, row railRow) {
	role := row.role
	if role == nil {
		return
	}
	icon, _ := statusIcon(role, true, r.animFrame) // force dimmed below
	screen.SetContent(x, y, icon, nil, theme.StyleDimmed)
	col := x + 2
	availW := w - 2
	if availW <= 0 {
		return
	}
	widget.DrawText(screen, col, y, availW, truncRunesLeft(row.breadcrumb, availW), theme.StyleDimmed)
}

// drawBreadcrumbNameRow renders line 2 of a two-line pinned non-root entry: the
// role name (indented under line 1). It renders selected-style when the
// preceding breadcrumb line (the cursor anchor) is selected, dimmed when the
// role is archived OR the entry is an ancestry-only filter heading (BUG-028-RAIL),
// normal otherwise (add-hera-pin-nonroot).
func (r *Rail) drawBreadcrumbNameRow(screen tcell.Screen, x, y, w int, role *heramodel.RoleView, ancestryOnly bool, selected bool) {
	if role == nil {
		return
	}
	nameStyle := theme.StyleNormal
	if role.Archived || ancestryOnly {
		nameStyle = theme.StyleDimmed
	}
	if selected {
		nameStyle = theme.StyleSelected
	}
	widget.DrawText(screen, x, y, w, role.Name, nameStyle)
}

// truncRunesLeft left-truncates s to at most max runes, prepending "…" when
// truncation occurs, so the rightmost (nearest-ancestor) text of an overflowing
// breadcrumb trail stays visible. Rune-aware (not byte-aware) per the rail's
// truncation rules (add-hera-pin-nonroot).
func truncRunesLeft(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(max-1):])
}

// spinnerFrame computes the current spinner animation frame from wall-clock
// time, mirroring the task list's updateSpinnerFrame. Recomputed on each Draw so
// a WORKING role's glyph advances as long as the spinner loop keeps redrawing
// (it does while any session is actively running).
func spinnerFrame() int {
	interval := widget.SpinnerTickInterval()
	if interval <= 0 {
		return 0
	}
	return int(time.Now().UnixMilli()/interval.Milliseconds()) % widget.SpinnerFrameCount()
}

// chevron returns the fold glyph for a collapsed/expanded state.
func chevron(collapsed bool) string {
	if collapsed {
		return "▸"
	}
	return "▾"
}

// kanbanStatusOf returns o's kanban status, defaulting an unset/empty value to
// active (add-hera-kanban-status). heramodel.BuildModel always populates KanbanStatus
// from the DB's NOT NULL DEFAULT 'active' column, so this default only matters
// for hand-built test fixtures (and any other heramodel.OrchView constructed outside
// heramodel.BuildModel) that never set the field — treating them as active preserves
// their historical headerless-active-group placement.
func kanbanStatusOf(o *heramodel.OrchView) db.HeraKanbanStatus {
	if o.KanbanStatus == "" {
		return db.HeraKanbanActive
	}
	return o.KanbanStatus
}

// statusIcon picks the glyph + style for a role row by delegating to the shared
// classifier widget.RoleStatusIcon (widget/rolestatusicon.go), whose precedence
// is needs-input → active → ready_to_close → failed → done → idle → live →
// default. needs-input "(?)" outranks everything (BUG-A): it is the role's OWN
// signal ONLY (authoritative needs-input flag or a self-asserted blocked
// status) — a descendant's rollup does NOT bubble up to an ancestor's own icon
// (remove-needs-input-rollup-glyph retired that; a needs-input descendant stays
// visible via its own row, peeked through a closed ancestor fold by the
// partial-fold-reveal mechanism instead). GENUINE activity (role.IsActive —
// Live && SessionRunning && !SessionIdle, NOT
// a task-status term) ranks next, animates the spinner, and OUTRANKS the
// stale-able resting stamps ready_to_close/failed/done (BUG-F); when no higher
// signal applies, ready_to_close/failed/done/idle/live each map to their own
// static glyph. dim forces the dimmed style for archived placement (the glyph
// never lies — only the style dims).
//
// frame is the current spinner animation frame: only a genuinely-active role
// renders the active spinner's frame so it animates. The animated "working"
// glyph is sourced from REAL session activity (role.IsActive), NOT the hera role
// Status "working" field — that field is a manual/MCP-set ladder value that goes
// stale (it stays "working" after a session idles, stops, or dies), so binding
// the spinner to it made idle/stopped/dead roles animate. See BUG-003.
func statusIcon(role *heramodel.RoleView, dim bool, frame int) (rune, tcell.Style) {
	// Single source of truth shared with the plan-view node projection
	// (widget.RoleStatusIcon) so the two surfaces render 1:1 (BUG-007). The
	// precedence + vocabulary live in widget; this only maps heramodel.RoleView → inputs.
	// ShowsNeedsInput is the role's OWN needs-input signal only
	// (remove-needs-input-rollup-glyph); IsActive is the honest
	// live-running-and-not-idle "working" signal (Live && SessionRunning &&
	// !SessionIdle), never the stale hera status (BUG-003).
	return widget.RoleStatusIcon(roleStatusInputs(role), dim, frame)
}

// roleStatusInputs maps a heramodel.RoleView to the primitive signals the shared
// classifier switches on. Kept next to statusIcon so the rail's contract is
// visible; the plan-view projection builds the same inputs from its own heramodel.RoleView.
func roleStatusInputs(role *heramodel.RoleView) widget.RoleStatusInputs {
	return widget.RoleStatusInputs{
		// Accepted is a worker/freelance-only signal (add-hera-accept-lifecycle):
		// hera_accept and the gater's auto-accept both act on a WORKER's bound
		// task, never a coordinator's own. A coordinator's own task can
		// independently reach StatusComplete (e.g. via task_complete on itself)
		// with no accept semantics at all, so the header row must not borrow the
		// worker-ladder's "accepted" glyph — same coordinator exclusion pattern
		// as contextIndicator above.
		Accepted:     role.Kind != db.HeraKindCoordinator && role.TaskStatus == model.StatusComplete.String(),
		ReadyToClose: role.ReadyToClose,
		NeedsInput:   role.ShowsNeedsInput(),
		Failed:       role.HasStatus && role.Status == db.HeraStatusFailed,
		Done:         role.HasStatus && role.Status == db.HeraStatusDone,
		Active:       role.IsActive(),
		Idle:         role.HasStatus && role.Status == db.HeraStatusIdle,
		Live:         role.Live,
	}
}

// CursorToParent moves the cursor to the nearest parent-coordinator row — the
// closest rrOrch header or bridging rrRole (collOrchID > 0) with a strictly
// smaller depth than the current row. No-op when the cursor is at the root
// (depth 0 with no qualifying ancestor) or on a section that has no coordinator
// parent (e.g. a freelance role). Called by the page's Left-arrow handler when
// the rail is focused (BUG-016).
func (r *Rail) CursorToParent() {
	if r.cursor <= 0 || r.cursor >= len(r.rows) {
		return
	}
	cur := r.rows[r.cursor]
	for i := r.cursor - 1; i >= 0; i-- {
		row := r.rows[i]
		if row.depth >= cur.depth {
			continue
		}
		// Only land on rows that represent a coordinator: an orchestrator header
		// (rrOrch) or a bridging worker row whose collOrchID marks it as the
		// coordinator of a nested sub-team (rrRole with collOrchID > 0).
		if row.kind == rrOrch || (row.kind == rrRole && row.collOrchID > 0) {
			r.setCursor(i)
			return
		}
	}
}

// InputHandler routes navigation keys. Left/Right are currently unused here
// (Up/Down/j/k drive the cursor, Space toggles collapse); they are free for
// future horizontal navigation now that the global handler no longer consumes
// them for tab switching (tab nav is 1/2/3 only).
func (r *Rail) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return r.WrapInputHandler(func(event *tcell.EventKey, _ func(p tview.Primitive)) {
		// While typing a filter every keystroke is filter input — nav and mutation
		// keys are suppressed (page.handleRailMutation already bailed; app.go let
		// the global rune shortcuts through). See handleFilterKey.
		if r.filterInput {
			r.handleFilterKey(event)
			return
		}
		switch event.Key() {
		case tcell.KeyDown:
			r.CursorDown()
		case tcell.KeyUp:
			r.CursorUp()
		case tcell.KeyRune:
			switch event.Rune() {
			case 'j':
				r.CursorDown()
			case 'k':
				r.CursorUp()
			case ' ':
				r.ToggleCollapse()
			case '/':
				r.enterFilter()
			}
		}
	})
}

// MouseHandler handles mouse events over the rail: left-click focuses it
// (matching tview's Box default), and wheel scroll moves the cursor up/down
// one selectable row per notch via the same step() the keyboard bindings use
// (mirrors gitpanel.FilePanel and taskview.TaskListView). page.go's own
// MouseHandler already gates dispatch here on the click column falling in the
// rail's region; the InRect check below additionally protects direct callers
// (tests, or a future non-region-gated caller) from acting on out-of-rect events.
//
// The scroll-to-cursor mapping is inverted relative to a plain content pan:
// this widget moves the CURSOR, not an independent viewport, so a scroll
// gesture reads as "drag the cursor," not "drag the pane" — the cursor
// should move in the same direction as the fingers (trackpad "natural"
// scrolling), the opposite of FilePanel's pane-scroll convention.
func (r *Rail) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return r.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if !r.InRect(event.Position()) {
			return false, nil
		}
		switch action {
		case tview.MouseLeftDown:
			setFocus(r)
			consumed = true
		case tview.MouseScrollUp:
			r.CursorDown()
			consumed = true
		case tview.MouseScrollDown:
			r.CursorUp()
			consumed = true
		}
		return
	})
}

// enterFilter switches the rail into search INPUT mode. filterQuery is always
// "" here — Enter and Esc both fully clear it on exit (BUG-028-RAIL), so there is no
// "accepted but still narrowed" state to resume; every `/` press starts a fresh
// query.
func (r *Rail) enterFilter() {
	r.filterInput = true
	uxlog.Log("[hera-view] rail filter: input mode")
}

// handleFilterKey routes a keystroke while in search INPUT mode. Esc and Enter
// both fully clear the filter via ClearFilter — Enter, at the bare-Rail level,
// can't itself perform the "jump into the agent" half (that needs a HeraPage
// wrapper's heramodel.Selection/OnReattach wiring — see page.go's handleRailMutation,
// which intercepts Enter FIRST and resolves the jump before calling
// ClearFilter). Up/Down move the cursor WITHIN the filtered rows without
// leaving input mode (so the operator can select a row while still
// typing/editing the query — matching the Tasks-tab `/` filter, though unlike
// it the Hera rail resolves Enter in ONE step, not two — BUG-028-RAIL); Backspace
// trims a rune; any other rune extends the query. Every query change rebuilds,
// auto-selecting the FIRST real match (never an ancestry-only heading) so the
// operator sees the top candidate live as they type.
func (r *Rail) handleFilterKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEscape:
		r.ClearFilter()
		uxlog.Log("[hera-view] rail filter: cleared")
	case tcell.KeyEnter:
		r.ClearFilter()
		uxlog.Log("[hera-view] rail filter: selected + cleared")
	case tcell.KeyDown:
		// Navigate the filtered set while typing — step() only lands on selectable
		// rows (ancestry-only headings excluded), and a narrowed buildRows contains
		// only visible rows, so the cursor can never reach a filtered-out or
		// ancestry-only row.
		r.CursorDown()
	case tcell.KeyUp:
		r.CursorUp()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r.filterQuery != "" {
			runes := []rune(r.filterQuery)
			r.filterQuery = string(runes[:len(runes)-1])
			r.rebuildAfterFilter()
		}
	case tcell.KeyRune:
		r.filterQuery += string(event.Rune())
		r.rebuildAfterFilter()
	}
}

// ClearFilter exits search input mode and fully resets the query, rebuilding
// the full unfiltered rail. Shared by Esc (discard) and Enter (BUG-028-RAIL: select
// then clear) — both restore the SAME full-rail state; a HeraPage-level Enter
// just resolves the selection BEFORE calling this. Rebuilds via
// rebuildAfterFilter, which writes the cursor directly (never via setCursor),
// so clearing the filter itself never fires the selection callback nor
// persists rail state.
func (r *Rail) ClearFilter() {
	r.filterInput = false
	r.filterQuery = ""
	r.rebuildAfterFilter()
}

// rebuildAfterFilter rebuilds the rows for the current query. While the query
// still narrows the rail it auto-selects the FIRST real match (BUG-028-RAIL) so the
// operator sees the live top candidate as they type or backspace; once the
// query is empty (cleared) it re-pins the cursor by stable identity onto
// whatever row it held before, matching the pre-filter selection. Either way
// the cursor is written directly (never via setCursor), so a filter change
// neither fires the selection callback nor (per BUG-002) persists rail state —
// the filter is transient.
func (r *Rail) rebuildAfterFilter() {
	ref := r.currentRef()
	r.buildRows()
	if r.filterActive() {
		r.jumpToFirstMatch()
	} else {
		r.restoreCursor(ref)
	}
	r.clampCursor()
}

// jumpToFirstMatch pins the cursor onto the FIRST real query match in the
// narrowed rows — a coordinator/orchestrator/pinned/freelance row whose own
// name (or, for an orchestrator header, its folded-in coordinator's name)
// itself matched the query. It never lands on an ancestry-only heading
// (selectable() already excludes those) nor a structural fold header
// (Freelance/Archive expando — always selectable but never itself a text
// match). Live auto-select while typing (BUG-028-RAIL): Enter can jump straight
// into the top candidate with no separate "lock" step. A no-op (leaving the
// cursor for clampCursor to resolve) when the narrowed rows contain no real
// match.
func (r *Rail) jumpToFirstMatch() {
	for i, row := range r.rows {
		if !row.selectable() {
			continue
		}
		isMatch := false
		switch row.kind {
		case rrOrch, rrPinnedBreadcrumb, rrFreelanceRole:
			isMatch = true
		case rrRole:
			isMatch = !row.breadcrumbCont
		}
		if isMatch {
			r.cursor = i
			return
		}
	}
}
