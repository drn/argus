package hera

import (
	"errors"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestBoardSurvivesRecycle(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	_, _, role := seedRecycleCoordinator(t, d, "board", "/wt/board", "branch", "mission")
	i, err := d.PostHeraBoardItem(role.ID, "decision", "Choose", "body")
	testutil.NoError(t, err)
	prompt, err := BuildRecycleSeedPrompt(d, role.ID)
	testutil.NoError(t, err)
	testutil.Contains(t, prompt, fmt.Sprintf("#%d [decision] Choose", i.ID))
	items, err := d.ListHeraBoardItems(role.ID, false)
	testutil.NoError(t, err)
	testutil.Equal(t, items[0].Read, false)
	testutil.NoError(t, d.ResolveHeraBoardItem(role.ID, i.ID, "agent", "done"))
	testutil.Contains(t, BoardSummary(d, role), "no open items")
}

type failedBoardReader struct{}

func (failedBoardReader) ListHeraBoardItems(int64, bool) ([]*db.HeraBoardItem, error) {
	return nil, errors.New("unavailable")
}

func TestBoardSummaryFallbacks(t *testing.T) {
	coord := &db.HeraRole{Kind: db.HeraKindCoordinator}
	testutil.Equal(t, BoardSummary(struct{}{}, coord), "")
	testutil.Contains(t, BoardSummary(failedBoardReader{}, coord), "hera_board_list")
	testutil.Equal(t, BoardSummary(failedBoardReader{}, &db.HeraRole{Kind: db.HeraKindWorker}), "")
}
