package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/bridge"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestRefreshTasksBridgeAuthority(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { testutil.NoError(t, d.Close()) })
	task := &model.Task{ID: "bridge", Name: "bridge", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	a := New(d, agent.NewRunner(nil), false)
	a.bridgeStates = map[string]bridge.State{task.ID: {Busy: true, NeedsInput: true}}
	a.refreshTasksWithIDs([]string{task.ID}, nil, false)
	testutil.DeepEqual(t, a.needsInputIDs, []string{task.ID})
	testutil.True(t, a.tasklist.IdleSet()[task.ID])
	a.bridgeStates[task.ID] = bridge.State{Busy: true}
	a.refreshTasksWithIDs([]string{task.ID}, []string{task.ID}, false)
	testutil.Equal(t, len(a.needsInputIDs), 0)
	testutil.False(t, a.tasklist.IdleSet()[task.ID])
	testutil.DeepEqual(t, a.sustainedActiveIDs, []string{task.ID})
	// Disabling the existing master switch must discard even a cached dialog
	// report and restore scraper verdicts, without sticky bridge contamination.
	a.bridgeStates[task.ID] = bridge.State{Busy: true, NeedsInput: true}
	a.refreshTasksWithIDs([]string{task.ID}, nil, false)
	testutil.DeepEqual(t, a.needsInputIDs, []string{task.ID})
	testutil.NoError(t, d.SetConfigValue("bridge.enabled", "false"))
	a.refreshTasksWithIDs([]string{task.ID}, []string{task.ID}, false)
	testutil.Equal(t, len(a.needsInputIDs), 0)
	testutil.True(t, a.tasklist.IdleSet()[task.ID])
}
