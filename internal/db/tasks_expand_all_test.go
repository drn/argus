package db

import (
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestTasksExpandAllRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sql")
	d, err := Open(path)
	testutil.NoError(t, err)
	value, err := d.LoadTasksExpandAll()
	testutil.NoError(t, err)
	testutil.Equal(t, value, false)
	testutil.NoError(t, d.SaveTasksExpandAll(true))
	testutil.NoError(t, d.Close())
	d, err = Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	value, err = d.LoadTasksExpandAll()
	testutil.NoError(t, err)
	testutil.Equal(t, value, true)
	testutil.NoError(t, d.SaveTasksExpandAll(false))
	value, err = d.LoadTasksExpandAll()
	testutil.NoError(t, err)
	testutil.Equal(t, value, false)
}
