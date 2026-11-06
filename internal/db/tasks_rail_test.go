package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestTasksRailPreference(t *testing.T) {
	d := testDB(t)
	testutil.Equal(t, config.DefaultConfig().UI.TasksRail, false)
	testutil.Equal(t, d.Config().UI.TasksRail, false)
	for _, value := range []string{"true", "false"} {
		t.Run(value, func(t *testing.T) {
			testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", value))
			testutil.Equal(t, d.Config().UI.TasksRail, value == "true")
		})
	}
}

func TestTasksRailTOMLAndRelaunch(t *testing.T) {
	dir := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte("[ui]\ntasks_rail = true\n"), 0o600))
	path := filepath.Join(dir, "data.sql")
	d, err := Open(path)
	testutil.NoError(t, err)
	testutil.Equal(t, d.Config().UI.TasksRail, true)
	testutil.NoError(t, d.Close())
	testutil.NoError(t, os.Remove(filepath.Join(dir, config.FileName)))
	d, err = Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	testutil.NoError(t, d.Close())
	d, err = Open(path)
	testutil.NoError(t, err)
	testutil.Equal(t, d.Config().UI.TasksRail, true)
}
