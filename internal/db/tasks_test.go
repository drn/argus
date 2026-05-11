package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestUpdate_AllBoolFields exercises the Sandboxed / Archived boolean branches
// in Update.
func TestDB_Update_AllBoolFields(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "x"}
	testutil.NoError(t, d.Add(task))

	task.Sandboxed = true
	task.Archived = true
	testutil.NoError(t, d.Update(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Sandboxed, true)
	testutil.Equal(t, got.Archived, true)
}

// TestRenameIfName_RowGoneAfterUpdate covers the row-disappeared error branch.
func TestDB_RenameIfName_RowGone(t *testing.T) {
	d := testDB(t)
	// CAS against an id that doesn't exist; the row-gone branch fires the
	// QueryRow which finds no row and returns the not-found error.
	_, err := d.RenameIfName("ghost-id", "expected", "new")
	testutil.Error(t, err)
}
