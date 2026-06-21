package tui

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
)

// TestUpdateAttentionBar_GatesFinishedStickyTask proves the agent-view attention
// bar excludes a finished (non in_progress) sticky needs-input task and keeps a
// genuinely in_progress one (BUG-006) — the twin of the Hera-box BUG-005 gate.
func TestUpdateAttentionBar_GatesFinishedStickyTask(t *testing.T) {
	a := &App{
		attentionBar: widget.NewAttentionBar(),
		tasks: []*model.Task{
			{ID: "live", Name: "live-task", Status: model.StatusInProgress},
			{ID: "done", Name: "done-task", Status: model.StatusComplete},
		},
		// Sticky set still carries the finished task plus the live one.
		needsInputIDs: []string{"done", "live"},
	}
	a.updateAttentionBar()

	entries := a.attentionBar.Entries()
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].TaskName, "live-task")
}
