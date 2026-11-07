package db

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// explainPlan returns the concatenated EXPLAIN QUERY PLAN detail text.
func explainPlan(t *testing.T, d *DB, query string, args ...any) string {
	t.Helper()
	rows, err := d.conn.Query("EXPLAIN QUERY PLAN "+query, args...)
	testutil.NoError(t, err)
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		testutil.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		b.WriteString(detail + "\n")
	}
	testutil.NoError(t, rows.Err())
	return b.String()
}

// The per-orchestrator cost sums run once per orchestrator on every rail
// rebuild. hera_bindings only had PARTIAL role_id indexes (ended_at IS NULL),
// which cannot serve ended bindings, so each call scanned the whole table
// (measured: 88ms BuildModel on 412 orchestrators / 2837 bindings).
func TestHeraBindingsRoleIndex_CostSumsDoNotScanBindings(t *testing.T) {
	d := heraTestDB(t)

	cases := []struct {
		name  string
		query string
	}{
		{"accrued sum", `SELECT hr.id, COALESCE(SUM(hb.cost_usd_accrued), 0)
			FROM hera_roles hr LEFT JOIN hera_bindings hb ON hb.role_id = hr.id
			WHERE hr.orchestrator_id = ? GROUP BY hr.id`},
		{"nuked sum", `SELECT COALESCE(SUM(hb.cost_usd_accrued), 0)
			FROM hera_bindings hb JOIN hera_roles hr ON hb.role_id = hr.id
			WHERE hr.orchestrator_id = ? AND hr.nuked_at IS NOT NULL`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainPlan(t, d, tc.query, 1)
			if strings.Contains(plan, "SCAN hb") || strings.Contains(plan, "SCAN hera_bindings") {
				t.Fatalf("query scans hera_bindings:\n%s", plan)
			}
			testutil.Contains(t, plan, "idx_hera_bindings_role")
		})
	}
}

// The index must be a plain (non-partial) index on role_id: ended bindings
// are exactly what the cost sums need to reach.
func TestHeraBindingsRoleIndex_IsNotPartial(t *testing.T) {
	d := heraTestDB(t)
	var sqlText string
	testutil.NoError(t, d.conn.QueryRow(
		`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_hera_bindings_role'`).Scan(&sqlText))
	testutil.Contains(t, sqlText, "hera_bindings(role_id)")
	if strings.Contains(strings.ToUpper(sqlText), "WHERE") {
		t.Fatalf("index must not be partial: %s", sqlText)
	}
}

// A database created before this change has no such index; opening it must add
// it (CREATE INDEX IF NOT EXISTS) without disturbing existing rows.
func TestHeraBindingsRoleIndex_AddedToExistingDatabase(t *testing.T) {
	path := t.TempDir() + "/data.sql"
	d, err := Open(path)
	testutil.NoError(t, err)
	o := mkOrch(t, d, "orch")
	r := mkRole(t, d, o.ID, "w", HeraKindWorker)
	mkBinding(t, d, r.ID, "task-1", "/tmp/task-1")
	_, err = d.conn.Exec(`DROP INDEX idx_hera_bindings_role`) // simulate the pre-change database
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())

	d2, err := Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d2.Close() })

	var n int
	testutil.NoError(t, d2.conn.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_hera_bindings_role'`).Scan(&n))
	testutil.Equal(t, n, 1)
	var rows int
	testutil.NoError(t, d2.conn.QueryRow(`SELECT count(*) FROM hera_bindings`).Scan(&rows))
	testutil.Equal(t, rows, 1)
}
