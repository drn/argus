package db

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestWithTx_Commit(t *testing.T) {
	d := testDB(t)

	task := &model.Task{ID: "tx-1", Name: "before"}
	testutil.NoError(t, d.Add(task))

	err := d.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "after", "tx-1")
		return err
	})
	testutil.NoError(t, err)

	got, err := d.Get("tx-1")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "after")
}

func TestWithTx_Rollback(t *testing.T) {
	d := testDB(t)

	task := &model.Task{ID: "tx-2", Name: "original"}
	testutil.NoError(t, d.Add(task))

	err := d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "changed", "tx-2"); err != nil {
			return err
		}
		return fmt.Errorf("deliberate rollback")
	})
	testutil.Contains(t, err.Error(), "deliberate rollback")

	got, err := d.Get("tx-2")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Name, "original")
}

func TestWithTx_MultipleOps(t *testing.T) {
	d := testDB(t)

	testutil.NoError(t, d.Add(&model.Task{ID: "tx-a", Name: "a"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "tx-b", Name: "b"}))

	err := d.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "A", "tx-a"); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "B", "tx-b"); err != nil {
			return err
		}
		return nil
	})
	testutil.NoError(t, err)

	a, _ := d.Get("tx-a")
	b, _ := d.Get("tx-b")
	testutil.Equal(t, a.Name, "A")
	testutil.Equal(t, b.Name, "B")
}

func TestWithTx_RollbackMultipleOps(t *testing.T) {
	d := testDB(t)

	testutil.NoError(t, d.Add(&model.Task{ID: "tx-c", Name: "c"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "tx-d", Name: "d"}))

	_ = d.WithTx(func(tx *sql.Tx) error {
		tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "C", "tx-c") //nolint:errcheck
		tx.Exec(`UPDATE tasks SET name=? WHERE id=?`, "D", "tx-d") //nolint:errcheck
		return fmt.Errorf("abort")
	})

	// Both should be unchanged
	c, _ := d.Get("tx-c")
	dd, _ := d.Get("tx-d")
	testutil.Equal(t, c.Name, "c")
	testutil.Equal(t, dd.Name, "d")
}
