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

func TestHeraCoordReparentTarget(t *testing.T) {
	t.Run("coordinator role row qualifies", func(t *testing.T) {
		sel := hera.Selection{
			Role: &hera.RoleView{Kind: db.HeraKindCoordinator, Name: "c", TaskID: "ct"},
			Orch: &hera.OrchView{ID: 7, Name: "orch-c"},
		}
		id, name, task, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, int64(7))
		testutil.Equal(t, name, "c") // the coordinator role's name
		testutil.Equal(t, task, "ct")
	})
	t.Run("orchestrator header with a coordinator role qualifies", func(t *testing.T) {
		sel := hera.Selection{Orch: &hera.OrchView{
			ID: 8, Name: "o",
			Roles: []hera.RoleView{{Kind: db.HeraKindCoordinator, TaskID: "ct", Live: true}},
		}}
		id, name, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, int64(8))
		testutil.Equal(t, name, "o")
	})
	t.Run("worker role does not qualify", func(t *testing.T) {
		sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}, Orch: &hera.OrchView{ID: 1}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("archived coordinator does not qualify", func(t *testing.T) {
		sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindCoordinator, Archived: true}, Orch: &hera.OrchView{ID: 1}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("orchestrator header without a coordinator role does not qualify", func(t *testing.T) {
		sel := hera.Selection{Orch: &hera.OrchView{ID: 1, Roles: []hera.RoleView{{Kind: db.HeraKindWorker}}}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("orchestrator header whose only coordinator role is archived does not qualify", func(t *testing.T) {
		// Symmetry with the role-row branch (which guards !Archived).
		sel := hera.Selection{Orch: &hera.OrchView{ID: 1, Roles: []hera.RoleView{
			{Kind: db.HeraKindCoordinator, Archived: true},
		}}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("empty selection does not qualify", func(t *testing.T) {
		_, _, _, ok := heraCoordReparentTarget(hera.Selection{})
		testutil.Equal(t, ok, false)
	})
}

func TestHeraActions_OpenAdoptNoOpAndFeedbackBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Remote mode (heraAdoptOps nil) → fully inert.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "x"}})
	testutil.Equal(t, app.mode, modeTaskList)

	app.heraAdoptOps = hera.NewAdoptOps(d)

	// Empty selection → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)

	// Managed worker role → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}, Orch: &hera.OrchView{ID: 1}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Freelancer with no task id → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: ""}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Freelancer with a task but NO active orchestrators → feedback, no picker.
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "tf", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "tf"}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Coordinator with NO other orchestrator to nest under → feedback, no picker.
	orch := seedHeraOrch(t, d, "only")
	seedHeraBoundRole(t, d, orch, "only", db.HeraKindCoordinator, "tc")
	app.heraOpenAdopt(hera.Selection{
		Role: &hera.RoleView{Kind: db.HeraKindCoordinator, TaskID: "tc", Name: "only"},
		Orch: &hera.OrchView{ID: orch, Name: "only"},
	})
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestSmoke_HeraAdoptFreelancerThroughPicker(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	target := seedHeraOrch(t, d, "target")
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "freelancer", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/tf", CreatedAt: time.Now()}))

	sim, stop := wireApp(t, app)
	defer stop()

	// Open the adopt picker for a freelancer on the tview thread.
	sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "freelancer"}}
	readUI(t, app.tapp, func() { app.heraOpenAdopt(sel) })
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	// Enter selects the (single) orchestrator → adopt runs.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	live, err := d.HeraLiveBindingByTaskAndOrchestrator("tf", target)
	testutil.NoError(t, err)
	testutil.Equal(t, live.WorktreePath, "/wt/tf")
}

func TestSmoke_HeraReparentCoordinatorThroughPicker(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	child := seedHeraOrch(t, d, "child")
	seedHeraBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
	parent := seedHeraOrch(t, d, "parent")
	seedHeraBoundRole(t, d, parent, "parent", db.HeraKindCoordinator, "parent-coord")

	sim, stop := wireApp(t, app)
	defer stop()

	sel := hera.Selection{
		Role: &hera.RoleView{Kind: db.HeraKindCoordinator, TaskID: "child-coord", Name: "child"},
		Orch: &hera.OrchView{ID: child, Name: "child"},
	}
	readUI(t, app.tapp, func() { app.heraOpenAdopt(sel) })
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	// The picker excludes the child itself, so only "parent" remains; Enter picks it.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)

	link, err := d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
	testutil.NoError(t, err)
	testutil.Equal(t, link.OrchestratorID, parent)
}

func TestSmoke_HeraAdoptPickerEscCancels(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	seedHeraOrch(t, d, "target")
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "tf", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	sim, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "tf"}})
	})
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	// Esc made no change — no binding created.
	_, err := d.HeraLiveBindingByTask("tf")
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
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
