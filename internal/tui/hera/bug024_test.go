package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
)

// BUG-024: a worker that reached `done` is ready_to_close (rail glyph = review
// ✓). Stepping it OUT of done (S / revert) must clear the ready_to_close mark so
// the new status glyph is visible — without this the mark wins the glyph
// precedence and the row stays pinned to ✓, making the status step invisible.
func TestOps_StepStatus_RevertOutOfDoneClearsReadyToClose(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")

	// Worker reached done: status done, task rolled to in_review, ready_to_close.
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusDone))
	testutil.NoError(t, d.SetStatus("t1", model.StatusInReview))
	testutil.NoError(t, d.SetMeta("t1", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

	// Sanity: before the step the glyph is the review ✓ (ready_to_close wins).
	pre := roleViewByID(t, d, o, role.ID)
	testutil.Equal(t, pre.ReadyToClose, true)
	preGlyph, _ := statusIcon(pre, false, 0)
	testutil.Equal(t, preGlyph, theme.IconReview)

	ops := NewOps(d)
	sel := roleSel(t, d, role, "t1")
	sel.Role.ReadyToClose = true

	// S (revert): done → blocked.
	testutil.NoError(t, ops.StepStatus(sel, -1))
	st, _ := d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusBlocked)

	// ready_to_close is cleared, so the glyph now reflects the blocked status.
	post := roleViewByID(t, d, o, role.ID)
	testutil.Equal(t, post.ReadyToClose, false)
	postGlyph, _ := statusIcon(post, false, 0)
	testutil.Equal(t, postGlyph, theme.IconNeedsInput)
}

// Stepping a worker to `done` still rolls to in_review AND keeps the mark (the
// clear only fires when stepping AWAY from done) — guards the new branch's
// direction so the close-out behaviour is unchanged.
func TestOps_StepStatus_AdvanceToDoneKeepsReadyToClose(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "o")
	role := seedBoundRole(t, d, o, "w", db.HeraKindWorker, "t1")
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusBlocked))
	ops := NewOps(d)

	testutil.NoError(t, ops.StepStatus(roleSel(t, d, role, "t1"), +1)) // blocked → done
	st, _ := d.HeraRoleStatusFor(role.ID)
	testutil.Equal(t, st.Status, db.HeraStatusDone)

	got, _ := d.Get("t1")
	testutil.Equal(t, got.Status, model.StatusInReview)
	post := roleViewByID(t, d, o, role.ID)
	testutil.Equal(t, post.ReadyToClose, true) // set by the done-roll, not cleared
}

// End-to-end through the rail: stepping the SELECTED worker out of review must
// (a) keep the cursor anchored on that worker (no drift to the next sibling)
// and (b) visibly change its glyph. Mirrors the heraRefresh path (mutate →
// BuildModel → SetModel).
func TestRail_StatusStepAnchorsCursorAndUpdatesGlyph(t *testing.T) {
	d := memDB(t)
	o := seedOrch(t, d, "plan-view-dogfood")
	seedBoundRole(t, d, o, "coord", db.HeraKindCoordinator, "tc")
	seed := seedBoundRole(t, d, o, "1a-seed", db.HeraKindWorker, "t1a")
	seedBoundRole(t, d, o, "2a-alpha", db.HeraKindWorker, "t2a")

	// 1a-seed reached done (review ✓); its task is finished so IsActive is false
	// and the glyph follows the hera status / ready_to_close mark.
	testutil.NoError(t, d.UpsertHeraRoleStatus(seed.ID, db.HeraStatusDone))
	testutil.NoError(t, d.SetStatus("t1a", model.StatusInReview))
	testutil.NoError(t, d.SetMeta("t1a", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))

	r := NewRail()
	rebuild := func() {
		m, err := BuildModel(d, nil, nil, nil)
		testutil.NoError(t, err)
		r.SetModel(m)
	}
	rebuild()

	// Land the cursor on 1a-seed.
	for i := 0; i < 12; i++ {
		if s := r.Selected(); s != nil && s.Name == "1a-seed" {
			break
		}
		r.CursorDown()
	}
	sel := r.Selection()
	testutil.Equal(t, sel.Role.Name, "1a-seed")
	preGlyph, _ := statusIcon(sel.Role, false, 0)
	testutil.Equal(t, preGlyph, theme.IconReview)

	// Step out of review (revert), then refresh exactly like heraRefresh.
	ops := NewOps(d)
	testutil.NoError(t, ops.StepStatus(sel, -1))
	rebuild()

	// Cursor stays anchored on 1a-seed (no drift to 2a-alpha).
	after := r.Selected()
	testutil.Equal(t, after != nil, true)
	testutil.Equal(t, after.Name, "1a-seed")

	// The glyph visibly changed away from the review ✓ (now blocked).
	postGlyph, _ := statusIcon(after, false, 0)
	testutil.Equal(t, postGlyph, theme.IconNeedsInput)

	// The coordinator's agent-count badge stays numeric (never "(?)").
	testutil.Equal(t, r.model.SubtreeAgentCount(o), 2)
}

// roleViewByID builds the model and returns the RoleView for roleID under orch.
func roleViewByID(t *testing.T, d *db.DB, orchID, roleID int64) *RoleView {
	t.Helper()
	m, err := BuildModel(d, nil, nil, nil)
	testutil.NoError(t, err)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if sec[i].ID != orchID {
				continue
			}
			for j := range sec[i].Roles {
				if sec[i].Roles[j].RoleID == roleID {
					return &sec[i].Roles[j]
				}
			}
		}
	}
	t.Fatalf("role %d not found under orch %d", roleID, orchID)
	return nil
}
