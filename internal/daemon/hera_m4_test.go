package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// heraInitGitRepo creates a one-commit git repo and redirects HOME so
// CreateAndStart's WorktreeDir() never touches the real ~/.argus.
func heraInitGitRepo(t *testing.T) string {
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
	return repo
}

// seedDaemonProject wires an echo-backed "proj" project on the daemon's DB.
func seedDaemonProject(t *testing.T, d *Daemon, repo string) {
	t.Helper()
	testutil.NoError(t, d.db.SetConfigValue("defaults.backend", "test"))
	testutil.NoError(t, d.db.SetBackend("test", config.Backend{Command: "echo hello", PromptFlag: ""}))
	testutil.NoError(t, d.db.SetProject("proj", config.Project{Path: repo, Branch: "HEAD"}))
}

// bindWorker creates an orchestrator + worker role + live binding for taskID.
func bindWorker(t *testing.T, d *Daemon, taskID string) {
	t.Helper()
	o, err := d.db.CreateHeraOrchestrator("o-" + taskID)
	testutil.NoError(t, err)
	r, err := d.db.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "proj"})
	testutil.NoError(t, err)
	_, err = d.db.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
}

// bindCoordinator creates an orchestrator + coordinator role + binding for taskID.
func bindCoordinator(t *testing.T, d *Daemon, taskID string) {
	t.Helper()
	o, err := d.db.CreateHeraOrchestrator("oc-" + taskID)
	testutil.NoError(t, err)
	r, err := d.db.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj"})
	testutil.NoError(t, err)
	_, err = d.db.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
}

func heraReadyToClose(t *testing.T, d *Daemon, taskID string) bool {
	t.Helper()
	meta, err := d.db.ListMeta(taskID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyReadyToClose && e.Value == "true" {
			return true
		}
	}
	return false
}

// TestTransitionTaskOnExit_HeraFinishPolicy exercises the BUG-050 worker rule
// in the daemon's authoritative flip site.
func TestTransitionTaskOnExit_HeraFinishPolicy(t *testing.T) {
	t.Run("worker-bound clean exit -> in_review + ready_to_close", func(t *testing.T) {
		d, _ := testDaemon(t)
		task := &model.Task{Name: "w", Status: model.StatusInProgress}
		testutil.NoError(t, d.db.Add(task))
		bindWorker(t, d, task.ID)

		d.transitionTaskOnExit(task.ID, true /* cleanExit */)

		got, _ := d.db.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInReview)
		testutil.Equal(t, heraReadyToClose(t, d, task.ID), true)
	})

	t.Run("worker-bound non-clean exit -> in_review + ready_to_close", func(t *testing.T) {
		d, _ := testDaemon(t)
		task := &model.Task{Name: "w", Status: model.StatusInProgress}
		testutil.NoError(t, d.db.Add(task))
		bindWorker(t, d, task.ID)

		d.transitionTaskOnExit(task.ID, false)

		got, _ := d.db.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusInReview)
		testutil.Equal(t, heraReadyToClose(t, d, task.ID), true)
	})

	t.Run("non-hera task clean exit -> complete (unchanged #707)", func(t *testing.T) {
		d, _ := testDaemon(t)
		task := &model.Task{Name: "n", Status: model.StatusInProgress}
		testutil.NoError(t, d.db.Add(task))

		d.transitionTaskOnExit(task.ID, true)

		got, _ := d.db.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusComplete)
		testutil.Equal(t, heraReadyToClose(t, d, task.ID), false)
	})

	t.Run("coordinator-bound clean exit -> complete (not auto-rolled)", func(t *testing.T) {
		d, _ := testDaemon(t)
		task := &model.Task{Name: "c", Status: model.StatusInProgress}
		testutil.NoError(t, d.db.Add(task))
		bindCoordinator(t, d, task.ID)

		d.transitionTaskOnExit(task.ID, true)

		got, _ := d.db.Get(task.ID)
		testutil.Equal(t, got.Status, model.StatusComplete)
		testutil.Equal(t, heraReadyToClose(t, d, task.ID), false)
	})
}

// TestHeraSpawnWorker_HappyPath drives the real transactional spawner: it
// creates the task (worktree + echo session), stamps meta, and creates the
// role+binding.
func TestHeraSpawnWorker_HappyPath(t *testing.T) {
	repo := heraInitGitRepo(t)
	d, _ := testDaemon(t)
	seedDaemonProject(t, d, repo)
	orch, err := d.db.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)

	res, err := d.heraSpawnWorker(mcp.HeraSpawnInput{
		Project:        "proj",
		BaseName:       "do-thing",
		TaskPrompt:     "oriented body",
		RolePrompt:     "verbatim user prompt",
		OrchestratorID: orch.ID,
	})
	testutil.NoError(t, err)
	t.Cleanup(func() { d.runner.StopAll() })

	testutil.Equal(t, res.Role.Name, "do-thing")
	testutil.Equal(t, res.Role.Kind, db.HeraKindWorker)
	testutil.Equal(t, res.Role.Prompt, "verbatim user prompt")
	testutil.Equal(t, res.Binding.ArgusTaskID, res.Task.ID)

	// Task persisted with the oriented prompt body.
	got, err := d.db.Get(res.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Prompt, "oriented body")

	// Live binding exists under the orchestrator.
	bnd, err := d.db.HeraLiveBindingByTaskAndOrchestrator(res.Task.ID, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, bnd.ID, res.Binding.ID)

	// meta:hera.role=worker stamped before the session started.
	testutil.Equal(t, true, func() bool {
		meta, _ := d.db.ListMeta(res.Task.ID, db.HeraMetaNamespace)
		for _, e := range meta {
			if e.Key == db.HeraMetaKeyRole && e.Value == string(db.HeraKindWorker) {
				return true
			}
		}
		return false
	}())
}

// TestHeraSpawnWorker_RoleBindingFailureUnwindsTask forces the role+binding
// insert to fail (invalid orchestrator FK) and asserts no orphan task, no
// orphan worktree, and no orphan role/binding remain.
func TestHeraSpawnWorker_RoleBindingFailureUnwindsTask(t *testing.T) {
	repo := heraInitGitRepo(t)
	d, _ := testDaemon(t)
	seedDaemonProject(t, d, repo)

	before, _ := d.db.Tasks()

	_, err := d.heraSpawnWorker(mcp.HeraSpawnInput{
		Project:        "proj",
		BaseName:       "orphan",
		TaskPrompt:     "body",
		RolePrompt:     "p",
		OrchestratorID: 999999, // no such orchestrator → role insert FK violation
	})
	if err == nil {
		t.Fatal("expected spawn to fail on invalid orchestrator")
	}

	// No orphan task row.
	after, _ := d.db.Tasks()
	testutil.Equal(t, len(after), len(before))

	// No orphan worktree.
	if dirExists(agent.WorktreeDir("proj", "orphan")) {
		t.Error("worktree should have been removed after role+binding failure")
	}

	// No orphan live bindings.
	live, err := d.db.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
