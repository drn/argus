package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/uxlog"
)

func namespaceGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	testutil.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func TestHeraNamespaceProbeFailsOpen(t *testing.T) {
	for _, tc := range []struct{ name, project, path, log string }{
		{"missing project", "missing", "", "unavailable"},
		{"empty path", "proj", "", "unavailable"},
		{"not a repository", "proj", "invalid", "probe failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initGitRepo(t)
			d := createTestDB(t, repo)
			if tc.project == "proj" {
				path := tc.path
				if path != "" {
					path = t.TempDir()
				}
				testutil.NoError(t, d.SetProject("proj", config.Project{Path: path}))
			}
			orch, err := d.CreateHeraOrchestrator("ordinary", "")
			testutil.NoError(t, err)
			logPath := filepath.Join(t.TempDir(), "ux.log")
			testutil.NoError(t, uxlog.Init(logPath))
			t.Cleanup(uxlog.Close)
			testutil.Equal(t, resolveOrchestratorBranchNamespace(d, orch.ID, tc.project), "ordinary")
			logged, err := os.ReadFile(logPath)
			testutil.NoError(t, err)
			testutil.Contains(t, string(logged), tc.log)
		})
	}
}

func TestHeraNamespaceTargetProjectAndLifetime(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	orch, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	// A second git repository without resetting HOME underneath existing tasks.
	other := t.TempDir()
	namespaceGit(t, other, "init", "-q")
	testutil.NoError(t, d.SetProject("other", config.Project{Path: other}))
	namespaceGit(t, repo, "branch", "argus/orch")
	logPath := filepath.Join(t.TempDir(), "ux.log")
	testutil.NoError(t, uxlog.Init(logPath))
	t.Cleanup(uxlog.Close)
	testutil.Equal(t, resolveOrchestratorBranchNamespace(d, orch.ID, "other"), "orch")
	testutil.Equal(t, resolveOrchestratorBranchNamespace(d, orch.ID, "proj"), "orch-t1-1")
	logged, err := os.ReadFile(logPath)
	testutil.NoError(t, err)
	testutil.Contains(t, string(logged), "collision resolved: argus/orch -> argus/orch-t1-1")
	// Removing the blocker restores the ordinary namespace; this is per-spawn.
	namespaceGit(t, repo, "branch", "-D", "argus/orch")
	testutil.Equal(t, resolveOrchestratorBranchNamespace(d, orch.ID, "proj"), "orch")
}

func TestSpawnHeraWorker_PromotedWorkerNamespace(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	parent, err := d.CreateHeraOrchestrator("parent", "")
	testutil.NoError(t, err)
	worker, err := SpawnHeraWorker(d, &fakeRunner{}, HeraWorkerSpawnInput{
		OrchestratorID: parent.ID, BaseName: "stage", Project: "proj", TaskPrompt: "work",
	})
	testutil.NoError(t, err)
	if err != nil {
		return
	}
	testutil.Equal(t, worker.Task.Branch, "argus/parent/stage")
	child, err := d.CreateHeraOrchestrator("stage", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: child.ID, Name: "coord", Kind: db.HeraKindCoordinator,
	}, worker.Task.ID, worker.Task.Worktree)
	testutil.NoError(t, err)
	// An unrelated flat task branch can block the promoted worker's team too.
	namespaceGit(t, repo, "branch", "argus/stage")
	res, err := SpawnHeraWorker(d, &fakeRunner{}, HeraWorkerSpawnInput{
		OrchestratorID: child.ID, BaseName: "impl", Project: "proj", TaskPrompt: "work",
	})
	testutil.NoError(t, err)
	if err != nil {
		return
	}
	testutil.Equal(t, res.Task.Branch, "argus/stage-t2-1/impl")
	stored, err := d.Get(worker.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, stored.Branch, "argus/parent/stage")
	bindings, err := d.ListHeraLiveBindingsByTask(worker.Task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(bindings), 2)
}

func TestSpawnHeraWorker_PromotedTaskNamespace(t *testing.T) {
	repo := initGitRepo(t) // Redirects HOME before any task/worktree creation.
	d := createTestDB(t, repo)
	coord, _, err := CreateAndStart(d, &fakeRunner{}, CreateInput{
		Name: "exec-irl-prep", Project: "proj", Prompt: "prepare",
	})
	testutil.NoError(t, err)
	testutil.Equal(t, coord.Branch, "argus/exec-irl-prep")
	priorRef := namespaceGit(t, repo, "rev-parse", coord.Branch)
	orch, err := d.CreateHeraOrchestrator("exec-irl-prep", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator,
	}, coord.ID, coord.Worktree)
	testutil.NoError(t, err)

	for _, role := range []string{"first", "second"} {
		t.Run(role, func(t *testing.T) {
			res, spawnErr := SpawnHeraWorker(d, &fakeRunner{}, HeraWorkerSpawnInput{
				OrchestratorID: orch.ID, BaseName: role, Project: "proj", TaskPrompt: "work",
			})
			testutil.NoError(t, spawnErr)
			if spawnErr != nil {
				return
			}
			testutil.Equal(t, res.Task.Branch, "argus/exec-irl-prep-t1-1/"+role)
		})
	}
	stored, err := d.Get(coord.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, stored.Branch, coord.Branch)
	testutil.Equal(t, namespaceGit(t, repo, "rev-parse", coord.Branch), priorRef)
}

func TestHeraNamespaceCollisionVariants(t *testing.T) {
	for _, tc := range []struct {
		name, orch, root, fallback string
		packed, blockFallback      bool
	}{
		{name: "packed", orch: "packed", root: "packed", fallback: "packed-t1-1", packed: true},
		{name: "sanitized", orch: "team/a", root: "team-a", fallback: "team-a-t1-1"},
		{name: "long", orch: strings.Repeat("x", 40), root: strings.Repeat("x", 30), fallback: strings.Repeat("x", 25) + "-t1-1"},
		{name: "fallback occupied", orch: "orch", root: "orch", fallback: "orch-t1-2", blockFallback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initGitRepo(t)
			d := createTestDB(t, repo)
			orch, err := d.CreateHeraOrchestrator(tc.orch, "")
			testutil.NoError(t, err)
			testutil.Equal(t, orch.ID, int64(1))
			namespaceGit(t, repo, "branch", "argus/"+tc.root)
			if tc.blockFallback {
				namespaceGit(t, repo, "branch", "argus/orch-t1-1")
			}
			if tc.packed {
				namespaceGit(t, repo, "pack-refs", "--all", "--prune")
			}
			res, err := SpawnHeraWorker(d, &fakeRunner{}, HeraWorkerSpawnInput{
				OrchestratorID: orch.ID, BaseName: "worker", Project: "proj", TaskPrompt: "work",
			})
			testutil.NoError(t, err)
			if err != nil {
				return
			}
			testutil.Equal(t, res.Task.Branch, "argus/"+tc.fallback+"/worker")
		})
	}
}

func TestHeraMaterializeNamespaceCollision(t *testing.T) {
	for _, kind := range []db.HeraNodeKind{db.HeraNodeKindWorker, db.HeraNodeKindSubCoord} {
		t.Run(string(kind), func(t *testing.T) {
			repo := initGitRepo(t)
			d := createTestDB(t, repo)
			orch, err := d.CreateHeraOrchestrator("parent", "")
			testutil.NoError(t, err)
			namespaceGit(t, repo, "branch", "argus/parent")
			planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
				OrchestratorID: orch.ID, Name: "stage", NodeKind: kind, ArgusProject: "proj", Prompt: "work",
			})
			testutil.NoError(t, err)
			in := HeraMaterializeInput{Role: planned, Project: "proj", TaskPrompt: "work"}
			var taskID string
			if kind == db.HeraNodeKindSubCoord {
				res, materializeErr := MaterializeHeraSubCoordinator(d, &fakeRunner{}, in)
				testutil.NoError(t, materializeErr)
				if materializeErr != nil {
					return
				}
				taskID = res.Task.ID
			} else {
				res, materializeErr := MaterializeHeraWorker(d, &fakeRunner{}, in)
				testutil.NoError(t, materializeErr)
				if materializeErr != nil {
					return
				}
				taskID = res.Task.ID
			}
			task, err := d.Get(taskID)
			testutil.NoError(t, err)
			testutil.Equal(t, task.Branch, "argus/parent-t1-1/stage")
		})
	}
}
