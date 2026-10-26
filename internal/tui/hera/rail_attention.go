package hera

import (
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/uxlog"
)

// SetBoardAuto applies ui.coordinator_board to attention scans and mailbox
// rendering. Call before SetModel so reveal paths are built with this policy.
func (r *Rail) SetBoardAuto(enabled bool) { r.boardAuto = enabled }

// unreadBoardOrch resolves the board represented by this exact row. A nested
// coordinator is rendered by its parent's bridging worker, not a header.
func (r *Rail) unreadBoardOrch(row railRow) *heramodel.OrchView {
	if !r.boardAuto || !row.selectable() {
		return nil
	}
	o := row.orch
	if row.collOrchID != 0 {
		o = r.model.OrchByID(row.collOrchID)
	}
	if o != nil {
		if c := o.CoordRole(); c != nil && c.BoardUnread > 0 {
			return o
		}
	}
	return nil
}

// attentionTaskID is shared by the built-row jump and the popup's all-groups
// scan. Board qualification never depends on a descendant's rolled-up signal.
func (r *Rail) attentionTaskID(row railRow) (string, bool) {
	if o := r.unreadBoardOrch(row); o != nil {
		return o.CoordRole().TaskID, true
	}
	return row.needsInputTaskID()
}

// attentionRoleIDs preserves BUG-069's identity baseline and BUG-070's cold
// start rules. More unread items on the same coordinator aren't a new entrant.
func (r *Rail) attentionRoleIDs(m heramodel.Model) map[int64]bool {
	ids := m.NeedsInputRoleIDs()
	r.eachUnreadBoard(m, func(o *heramodel.OrchView) {
		if ids == nil {
			ids = make(map[int64]bool)
		}
		ids[o.CoordRole().RoleID] = true
	})
	return ids
}

func (r *Rail) eachUnreadBoard(m heramodel.Model, visit func(*heramodel.OrchView)) {
	if !r.boardAuto {
		return
	}
	for _, section := range [][]heramodel.OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range section {
			o := &section[i]
			if c := o.CoordRole(); c != nil && c.BoardUnread > 0 {
				visit(o)
			}
		}
	}
}

func (r *Rail) nextNeedsInputRow() (railRow, bool) {
	n := len(r.rows)
	if n == 0 {
		return railRow{}, false
	}
	cursor := r.cursor
	if cursor < 0 || cursor >= n {
		cursor = 0
	}
	for step := 1; step <= n; step++ {
		row := r.rows[(cursor+step)%n]
		if _, ok := r.attentionTaskID(row); ok {
			r.logBoardCandidate(row)
			return row, true
		}
	}
	return railRow{}, false
}

func (r *Rail) logBoardCandidate(row railRow) {
	if o := r.unreadBoardOrch(row); o != nil {
		uxlog.Log("[hera-view] jump to next needs-input: board candidate orch=%d role=%d unread=%d", o.ID, o.CoordRole().RoleID, o.CoordRole().BoardUnread)
	}
}
