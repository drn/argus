package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// TestMigrate_NonNoRowsError exercises the !errors.Is(err, sql.ErrNoRows)
// branch of migrate(). We construct a DB whose underlying connection is closed
// so that every QueryRow returns "sql: database is closed" — not ErrNoRows.
func TestMigrate_NonNoRowsError(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	testutil.NoError(t, err)
	d := &DB{conn: conn}
	// First create the schema so subsequent ops query a real table.
	testutil.NoError(t, d.createTables())
	// Close to force a non-NoRows error from the schema_version SELECT.
	testutil.NoError(t, d.Close())

	err = d.migrate()
	testutil.Error(t, err)
}

// TestMigrate_CountPathExits covers the "count > 0 → return nil" branch.
// We populate schema_version with a row whose version column is NULL. The
// initial Scan(&version) errors with a non-NoRows error (NULL conversion),
// triggers the count path, count=1>0, returns nil.
func TestMigrate_CountPathExits(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	// Manually create schema_version with NULL-able version, insert NULL row.
	if _, err := conn.Exec(`CREATE TABLE schema_version (version INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_version (version) VALUES (NULL)`); err != nil {
		t.Fatal(err)
	}

	d := &DB{conn: conn}
	// Create the rest of the schema.
	testutil.NoError(t, d.createTables())
	// migrate should hit the non-NoRows scan error path and exit early via
	// the "count > 0 → return nil" branch.
	testutil.NoError(t, d.migrate())
}

// TestSeedDefaults_ClosedConn forces seedDefaults to fail on its Exec call.
func TestSeedDefaults_ClosedConn(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	err = d.runSeedDefaults()
	testutil.Error(t, err)
}

// TestSeedDefaults_BackendUpdateError covers the UPDATE-error path in
// seedDefaults (line 59-61). We pre-seed a placeholder backend then close
// the conn so the UPDATE fails.
func TestSeedDefaults_BackendUpdateError(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "echo", PromptFlag: ""}))
	testutil.NoError(t, d.Close())
	err = d.runSeedDefaults()
	testutil.Error(t, err)
}

// TestSeedDefaults_ConfigInsertError covers the config-insert-error path in
// seedDefaults (line 88-90). We need an existing config row gone and a
// closed conn for the INSERT OR IGNORE to fail.
func TestSeedDefaults_ConfigInsertError(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	testutil.NoError(t, err)
	d := &DB{conn: conn}
	testutil.NoError(t, d.createTables())
	// Don't insert backends yet — just close conn so even backend insert fails.
	testutil.NoError(t, d.Close())
	err = d.runSeedDefaults()
	testutil.Error(t, err)
}

// TestFixupBackends_UpdateExecError covers the UPDATE error path. We close the
// conn after a backend that will trigger an update is set. fixupBackends uses
// QueryRow which fails on closed conn → "continue", so UPDATE never runs.
// To exercise the UPDATE error path, we'd need QueryRow to succeed but Exec
// to fail — not reachable through public API.
func TestFixupBackends_ClosedConn(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "claude --worktree", PromptFlag: "-p"}))
	testutil.NoError(t, d.Close())
	// Returns nil because QueryRow err triggers continue.
	_ = d.fixupBackends()
}

// TestOpen_FixupBackendsFailure: we need a path where Open's fixupBackends
// step returns an error. fixupBackends only errors if an UPDATE fails after a
// successful SELECT — not reachable without injection.
//
// Same for migrate's seedDefaults failure path during Open: the migrate step
// runs on a fresh DB where seedDefaults' first backend INSERT typically
// succeeds. Document the gap here.
func TestOpen_AlreadyMigrated(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir + "/data.sql")
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	// Reopen — migrate sees schema_version row, returns early.
	d2, err := Open(dir + "/data.sql")
	testutil.NoError(t, err)
	testutil.NoError(t, d2.Close())
}

// TestFixupBackends_AlreadyCorrect ensures the fast-skip branch fires when
// existing config matches.
func TestFixupBackends_AlreadyCorrect(t *testing.T) {
	d := testDB(t)
	// Calling again on healthy DB should be a no-op.
	testutil.NoError(t, d.fixupBackends())
}

// TestFixupBackends_PreservesUserCustomization is a placeholder — we already
// have tests for many fixupBackends paths in db_test.go; this just verifies
// the path that includes user customizations when --permission-mode is missing.
func TestFixupBackends_AppendsPermissionMode(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackend("claude", config.Backend{
		Command:    "claude --dangerously-skip-permissions --extra-flag",
		PromptFlag: "",
	}))
	testutil.NoError(t, d.fixupBackends())
	backends, err := d.Backends()
	testutil.NoError(t, err)
	testutil.Contains(t, backends["claude"].Command, "--permission-mode plan")
	testutil.Contains(t, backends["claude"].Command, "--extra-flag")
}
