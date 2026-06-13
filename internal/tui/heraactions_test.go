package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/gdamore/tcell/v2"
)

// --- fast direct-call branch coverage (no event loop) -----------------------

func TestHeraActions_SpawnWorkerValidationBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	// No orchestrator / no coordinator → guarded, no modal.
	app.heraSpawnWorker(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)

	// Orchestrator with a coordinator role but the coord task has no project.
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc-noproj")
	// Blank the project on the coord task so resolution fails.
	tc, _ := d.Get("tc-noproj")
	tc.Project = ""
	testutil.NoError(t, d.Update(tc))
	sel := hera.Selection{Orch: &hera.OrchView{
		ID: orch, Name: "o",
		Roles: []hera.RoleView{{RoleID: 1, OrchID: orch, Name: "coord", Kind: db.HeraKindCoordinator, TaskID: "tc-noproj", Live: true}},
	}}
	app.heraSpawnWorker(sel)
	testutil.Equal(t, app.mode, modeTaskList) // still no modal (project missing)
}

func TestHeraActions_ReattachNoopBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	app.heraReattach(hera.Selection{})                                      // empty taskID → no-op
	app.heraReattach(hera.Selection{Role: &hera.RoleView{TaskID: "ghost"}}) // missing task → no-op
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_ArchivingLive(t *testing.T) {
	app := New(testDB(t), agent.NewRunner(nil), false)
	// Live, unarchived role → archiving is "live".
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{Role: &hera.RoleView{Live: true}}), true)
	// Archived role → not archiving (it'll unarchive).
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{Role: &hera.RoleView{Archived: true, Live: true}}), false)
	// Orchestrator with a live role → live.
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{Orch: &hera.OrchView{Roles: []hera.RoleView{{Live: true}}}}), true)
	// Orchestrator with no live role → not live.
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{Orch: &hera.OrchView{Roles: []hera.RoleView{{Live: false}}}}), false)
	// Archived orchestrator → not live.
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{Orch: &hera.OrchView{Archived: true, Roles: []hera.RoleView{{Live: true}}}}), false)
	// Empty selection.
	testutil.Equal(t, app.heraArchivingLive(hera.Selection{}), false)
}

func TestHeraActions_StatusStepNilRoleNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	app.heraStatusStep(hera.Selection{Orch: &hera.OrchView{ID: 1}}, +1) // orch header → no-op
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_OpenRenameEmptySelectionNoModal(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	app.heraOpenRename(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_SelName(t *testing.T) {
	testutil.Equal(t, heraSelName(hera.Selection{Role: &hera.RoleView{Name: "w"}}), "role w")
	testutil.Equal(t, heraSelName(hera.Selection{Orch: &hera.OrchView{Name: "o"}}), "orchestrator o")
	testutil.Equal(t, heraSelName(hera.Selection{}), "selection")
}

func TestHeraActions_CoordRoleName(t *testing.T) {
	o := &hera.OrchView{Roles: []hera.RoleView{
		{Name: "w", Kind: db.HeraKindWorker},
		{Name: "boss", Kind: db.HeraKindCoordinator},
	}}
	testutil.Equal(t, heraCoordRoleName(o), "boss")
	// Fallback when no coordinator role.
	testutil.Equal(t, heraCoordRoleName(&hera.OrchView{Roles: []hera.RoleView{{Name: "w", Kind: db.HeraKindWorker}}}), "coordinator")
}

// --- real-repo integration (spawn + reattach through the event loop) --------

// heraRepoApp sets HOME to a temp dir, builds a one-commit git repo, and seeds
// project "p" + an echo backend so CreateAndStart / startSession succeed.
func heraRepoApp(t *testing.T) (*App, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "T")
	testutil.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")

	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "test"))
	testutil.NoError(t, d.SetBackend("test", config.Backend{Command: "echo hello", PromptFlag: ""}))
	testutil.NoError(t, d.SetProject("p", config.Project{Path: repo, Branch: "HEAD"}))
	app := New(d, agent.NewRunner(nil), false)
	return app, d
}

func TestSmoke_HeraSpawnWorkerCreatesRoleAndTask(t *testing.T) {
	app, d := heraRepoApp(t)
	orch := seedHeraOrch(t, d, "orch")
	// Coordinator bound to a real task in project "p".
	testutil.NoError(t, d.Add(&model.Task{ID: "tc", Name: "tc", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/wt/tc"})
	testutil.NoError(t, err)

	sim, stop := wireApp(t, app)
	defer stop()
	t.Cleanup(func() { app.runner.StopAll() })

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp) // → Hera tab, cursor on orch header

	// `w` opens the spawn prompt modal.
	sim.InjectKey(tcell.KeyRune, 'w', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraInput) })

	// Type a prompt and submit.
	for _, r := range "do work" {
		sim.InjectKey(tcell.KeyRune, r, 0)
	}
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0)

	// The spawn runs in a goroutine; poll for the new worker role.
	deadline := time.Now().Add(3 * time.Second)
	var workerCount int
	for time.Now().Before(deadline) {
		syncUI(t, app.tapp)
		roles, _ := d.ListHeraRolesByKind(orch, db.HeraKindWorker)
		workerCount = len(roles)
		if workerCount >= 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	testutil.Equal(t, workerCount, 1)
}

func TestSmoke_HeraReattachRestartsSession(t *testing.T) {
	app, d := heraRepoApp(t)
	orch := seedHeraOrch(t, d, "orch")
	// A coordinator task with a real worktree (the repo) but no live session.
	repo := d.Config().Projects["p"].Path
	testutil.NoError(t, d.Add(&model.Task{ID: "tc", Name: "tc", Status: model.StatusInReview, Project: "p", Worktree: repo, CreatedAt: time.Now()}))
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: repo})
	testutil.NoError(t, err)

	sim, stop := wireApp(t, app)
	defer stop()
	t.Cleanup(func() { app.runner.StopAll() })

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'j', 0) // → coord role
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0) // Enter → reattach (no live session)

	// startSession mints a session ID for the (Claude-style) echo backend and
	// persists it before runner.Start — a durable signal that reattach ran,
	// even though the echo session exits instantly.
	deadline := time.Now().Add(3 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		syncUI(t, app.tapp)
		if got, _ := d.Get("tc"); got != nil && got.SessionID != "" {
			started = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	testutil.Equal(t, started, true)
}
