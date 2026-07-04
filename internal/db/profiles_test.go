package db

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestDB_TaskArchetypeRoundTrip pins the add-diligence-profiles
// tasks.archetype column: an archetype set on a task survives Add → Get and a
// later Update → Get.
func TestDB_TaskArchetypeRoundTrip(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "ci-task", Archetype: "ci_loop"}
	testutil.NoError(t, d.Add(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "ci_loop")

	// Full-row Update carries the column too.
	got.Archetype = "review"
	testutil.NoError(t, d.Update(got))

	got2, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got2.Archetype, "review")

	// A task created with no archetype reads empty (no profile is consulted).
	bare := &model.Task{Name: "bare"}
	testutil.NoError(t, d.Add(bare))
	gotBare, err := d.Get(bare.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotBare.Archetype, "")
}

// TestDB_TaskProfileRoundTrip pins the add-diligence-profiles tasks.profile
// column (the per-spawn profile override): a profile name set on a task
// survives Add → Get and a later Update → Get, and a task created with no
// override reads back empty.
func TestDB_TaskProfileRoundTrip(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "override-task", Profile: "custom"}
	testutil.NoError(t, d.Add(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Profile, "custom")

	// Full-row Update carries the column too.
	got.Profile = "customer_grade"
	testutil.NoError(t, d.Update(got))

	got2, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got2.Profile, "customer_grade")

	// A task created with no per-spawn override reads empty (resolution falls
	// through to the project's bound profile).
	bare := &model.Task{Name: "bare-profile"}
	testutil.NoError(t, d.Add(bare))
	gotBare, err := d.Get(bare.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotBare.Profile, "")
}

// TestDB_TaskEffortRoundTrip pins the add-model-menu-selection tasks.effort
// column (the per-spawn effort override): an effort level set on a task
// survives Add → Get and a later Update → Get, and a task created with no
// override reads back empty.
func TestDB_TaskEffortRoundTrip(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "effort-task", Effort: "xhigh"}
	testutil.NoError(t, d.Add(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Effort, "xhigh")

	// Full-row Update carries the column too.
	got.Effort = "low"
	testutil.NoError(t, d.Update(got))

	got2, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got2.Effort, "low")

	// A task created with no per-spawn override reads empty.
	bare := &model.Task{Name: "bare-effort"}
	testutil.NoError(t, d.Add(bare))
	gotBare, err := d.Get(bare.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotBare.Effort, "")
}

// TestDB_ProjectProfileRoundTrip pins the projects.profile column: a profile
// NAME set on a project survives SetProject → Projects.
func TestDB_ProjectProfileRoundTrip(t *testing.T) {
	d := testDB(t)

	testutil.NoError(t, d.SetProject("argus", config.Project{Path: "/tmp/argus", Profile: "lean"}))

	projects, err := d.Projects()
	testutil.NoError(t, err)
	testutil.Equal(t, projects["argus"].Profile, "lean")

	// Re-binding overwrites (INSERT OR REPLACE).
	testutil.NoError(t, d.SetProject("argus", config.Project{Path: "/tmp/argus", Profile: "customer_grade"}))
	projects, err = d.Projects()
	testutil.NoError(t, err)
	testutil.Equal(t, projects["argus"].Profile, "customer_grade")

	// A project written with no profile reads empty.
	testutil.NoError(t, d.SetProject("other", config.Project{Path: "/tmp/other"}))
	projects, err = d.Projects()
	testutil.NoError(t, err)
	testutil.Equal(t, projects["other"].Profile, "")
}

// TestDB_HeraRoleArchetypeRoundTrip pins the hera_roles.archetype column round-trip
// through the role read/write layer. Empty stores NULL and scans back empty.
func TestDB_HeraRoleArchetypeRoundTrip(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "profiles")

	withArch, err := d.CreateHeraRole(CreateHeraRoleInput{
		OrchestratorID: o.ID,
		Name:           "reviewer",
		Kind:           HeraKindWorker,
		ArgusProject:   "proj",
		Archetype:      "review",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, withArch.Archetype, "review")

	gotByID, err := d.HeraRole(withArch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotByID.Archetype, "review")

	// A role created with no archetype scans back empty (NULL column).
	noArch := mkRole(t, d, o.ID, "plain", HeraKindWorker)
	testutil.Equal(t, noArch.Archetype, "")
	gotPlain, err := d.HeraRole(noArch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotPlain.Archetype, "")
}

// TestDB_HeraRoleEffortRoundTrip pins the hera_roles.effort column round-trip
// through the role read/write layer (add-model-menu-selection). Empty stores
// NULL and scans back empty.
func TestDB_HeraRoleEffortRoundTrip(t *testing.T) {
	d := heraTestDB(t)
	o := mkOrch(t, d, "profiles-effort")

	withEffort, err := d.CreateHeraRole(CreateHeraRoleInput{
		OrchestratorID: o.ID,
		Name:           "reviewer",
		Kind:           HeraKindWorker,
		ArgusProject:   "proj",
		Effort:         "high",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, withEffort.Effort, "high")

	gotByID, err := d.HeraRole(withEffort.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotByID.Effort, "high")

	// A role created with no effort override scans back empty (NULL column).
	noEffort := mkRole(t, d, o.ID, "plain-effort", HeraKindWorker)
	testutil.Equal(t, noEffort.Effort, "")
	gotPlain, err := d.HeraRole(noEffort.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotPlain.Effort, "")
}

// TestDB_ProfileColumnsDefaultEmptyAfterAdd pins the migration scenario: when the
// new archetype/effort/profile columns are added to a database that already has
// rows (rows that predate the columns), those rows read empty without error. We
// simulate a legacy DB by dropping the columns, inserting legacy rows, then
// re-running the idempotent createTables migration that re-adds them.
func TestDB_ProfileColumnsDefaultEmptyAfterAdd(t *testing.T) {
	d := testDB(t)

	// Drop the columns to reproduce a pre-add schema, then insert rows that
	// never carried an archetype / effort / profile value.
	_, err := d.conn.Exec(`ALTER TABLE tasks DROP COLUMN archetype`)
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`ALTER TABLE tasks DROP COLUMN effort`)
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`ALTER TABLE projects DROP COLUMN profile`)
	testutil.NoError(t, err)

	_, err = d.conn.Exec(`INSERT INTO tasks (id, name, status, created_at) VALUES ('legacy-1', 'legacy', 'pending', '2026-01-01T00:00:00Z')`)
	testutil.NoError(t, err)
	_, err = d.conn.Exec(`INSERT INTO projects (name, path) VALUES ('legacy-proj', '/tmp/legacy')`)
	testutil.NoError(t, err)

	// The idempotent ADD COLUMN migrations re-add archetype/effort/profile with
	// DEFAULT '' — the regression this guards is reading those legacy rows.
	testutil.NoError(t, d.createTables())

	got, err := d.Get("legacy-1")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Archetype, "")
	testutil.Equal(t, got.Effort, "")

	projects, err := d.Projects()
	testutil.NoError(t, err)
	testutil.Equal(t, projects["legacy-proj"].Profile, "")
}
