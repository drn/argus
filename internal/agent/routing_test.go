package agent

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
)

func TestCreateRoutesAccountWithExplicitBackendAndPins(t *testing.T) {
	for _, tc := range []struct{ name, explicit, project, want string }{
		{"route", "", "", "work"},
		{"explicit default", "default", "", "default"},
		{"project pin", "", "default", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := initGitRepo(t)
			extra := "[backend_routing]\nstrategy=\"headroom\"\n[[backend_routing.tier]]\nbackend=\"test\"\naccount=\"work\"\nprobe=\"claude_usage\"\nthreshold_pct=90\n"
			if tc.project != "" {
				extra += "[project_accounts]\nproj=\"" + tc.project + "\"\n"
			}
			d := accountFileDB(t, repo, extra)
			restore := usagebudget.SetBridgeReader(func(account string) (usagebudget.Reading, bool) {
				return usagebudget.Reading{Percentage: 0}, account == "work"
			})
			defer restore()
			task, _, err := CreateAndStart(d, &fakeRunner{sessionPID: 1}, CreateInput{Name: "routed", Project: "proj", Backend: "test", Account: tc.explicit})
			testutil.NoError(t, err)
			testutil.Equal(t, task.Account, tc.want)
		})
	}
}

func TestHeraBlankParentAllowsAccountRouting(t *testing.T) {
	repo := initGitRepo(t)
	d := accountFileDB(t, repo, "[[backend_routing.tier]]\nbackend=\"test\"\naccount=\"work\"\nprobe=\"none\"\n")
	parent := &model.Task{ID: "parent", Name: "parent", Project: "proj", Backend: "test"}
	testutil.NoError(t, d.Add(parent))
	testutil.Equal(t, parent.Account, "")
	orch, err := d.CreateHeraOrchestrator("route", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj"}, parent.ID, repo)
	testutil.NoError(t, err)
	result, err := SpawnHeraWorker(d, &fakeRunner{sessionPID: 1}, HeraWorkerSpawnInput{OrchestratorID: orch.ID, BaseName: "worker", Project: "proj", Backend: "test", TaskPrompt: "p"})
	testutil.NoError(t, err)
	testutil.Equal(t, result.Task.Account, "work")
	// An explicit default is a nonblank stored identity and remains inherited.
	parent.Account = "default"
	testutil.NoError(t, d.Update(parent))
	account, err := resolveOrchestratorAccount(d, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, account, "default")
}

func TestCoordinatorFiltersToClaudeAccountTiers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Backends["other-claude"] = config.Backend{Command: "claude"}
	cfg.BackendRouting.Tiers = []config.BackendTier{{Backend: "codex", Probe: config.ProbeNone}, {Backend: "other-claude", Probe: config.ProbeNone}}
	testutil.Equal(t, resolveCoordinatorBackend("", "", cfg), "other-claude")
	cfg.Projects = map[string]config.Project{"proj": {Backend: "codex"}}
	testutil.Equal(t, resolveCoordinatorBackend("", "proj", cfg), "other-claude")
}

func TestCoordinatorSpawnRoutesAccount(t *testing.T) {
	for _, backend := range []string{"", "test"} {
		t.Run(backend, func(t *testing.T) {
			repo := initGitRepo(t)
			d := accountFileDB(t, repo, "[backend_routing]\nstrategy=\"headroom\"\n[[backend_routing.tier]]\nbackend=\"test\"\naccount=\"work\"\nprobe=\"claude_usage\"\nthreshold_pct=90\n")
			restore := usagebudget.SetBridgeReader(func(account string) (usagebudget.Reading, bool) {
				return usagebudget.Reading{Percentage: 0}, account == "work"
			})
			defer restore()
			result, err := SpawnHeraCoordinator(d, &fakeRunner{sessionPID: 1}, HeraCoordinatorSpawnInput{OrchestratorBaseName: "quota", TaskPrompt: "p", Project: "proj", Backend: backend})
			testutil.NoError(t, err)
			testutil.Equal(t, result.Task.Backend, "test")
			testutil.Equal(t, result.Task.Account, "work")
		})
	}
}

func TestSubCoordinatorRoutesAccountFromBlankParent(t *testing.T) {
	repo := initGitRepo(t)
	d := accountFileDB(t, repo, "[[backend_routing.tier]]\nbackend=\"test\"\naccount=\"work\"\nprobe=\"none\"\n")
	parent := &model.Task{ID: "blank-parent", Name: "parent", Project: "proj", Backend: "test"}
	testutil.NoError(t, d.Add(parent))
	orch, err := d.CreateHeraOrchestrator("parent", "")
	testutil.NoError(t, err)
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj"}, parent.ID, repo)
	testutil.NoError(t, err)
	role, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{OrchestratorID: orch.ID, Name: "sub", ArgusProject: "proj", NodeKind: db.HeraNodeKindSubCoord, Prompt: "p"})
	testutil.NoError(t, err)
	result, err := MaterializeHeraSubCoordinator(d, &fakeRunner{sessionPID: 1}, HeraMaterializeInput{Role: role, TaskPrompt: "p", Project: "proj"})
	testutil.NoError(t, err)
	testutil.Equal(t, result.Task.Backend, "test")
	testutil.Equal(t, result.Task.Account, "work")
}
