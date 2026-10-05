package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// accountFileDB opens a file-backed DB whose sibling config.toml defines a
// Claude-only "work" account, a Codex-only "cx" account and any extra TOML, so
// the config.toml overlay path is exercised. The default backend is Claude.
func accountFileDB(t *testing.T, repo string, extraTOML ...string) *db.DB {
	t.Helper()
	dir := t.TempDir()
	toml := "[accounts.work]\nclaude_config_dir = \"" + filepath.Join(dir, "work") + "\"\n" +
		"[accounts.cx]\ncodex_home = \"" + filepath.Join(dir, "cx") + "\"\n"
	for _, extra := range extraTOML {
		toml = extra + "\n" + toml
	}
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, config.FileName), []byte(toml), 0o644))
	d, err := db.Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "test"))
	testutil.NoError(t, d.SetBackend("test", config.Backend{Command: "claude"}))
	testutil.NoError(t, d.SetBackend("codex", config.Backend{Command: "codex"}))
	testutil.NoError(t, d.SetBackend("pi", config.Backend{Command: "pi"}))
	testutil.NoError(t, d.SetProject("proj", config.Project{Path: repo, Branch: "HEAD"}))
	return d
}

func TestCreateAndStart_RejectsUnknownAccount(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	fr := &fakeRunner{sessionPID: 1}
	_, _, err := CreateAndStart(d, fr, CreateInput{
		Name: "x", Prompt: "p", Project: "proj", Account: "ghost",
	})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "ghost")
	testutil.Equal(t, fr.startCalls, 0)
	testutil.False(t, dirExists(WorktreeDir("proj", "x")))
	tasks, terr := d.Tasks()
	testutil.NoError(t, terr)
	testutil.Equal(t, len(tasks), 0)
}

func TestCreateAndStart_StoresAccount(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"named", "work", "work"},
		{"default stored empty", "default", ""},
		{"unset stored empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initGitRepo(t)
			d := accountFileDB(t, repo)
			task, _, err := CreateAndStart(d, &fakeRunner{sessionPID: 1}, CreateInput{
				Name: "acct", Prompt: "p", Project: "proj", Account: tc.in,
			})
			testutil.NoError(t, err)
			testutil.Equal(t, task.Account, tc.want)
			got, err := d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Account, tc.want)
		})
	}
}

func TestCreateAndStart_AccountBackendSupport(t *testing.T) {
	t.Run("explicit account lacking the backend's tool is rejected before side effects", func(t *testing.T) {
		repo := initGitRepo(t)
		d := accountFileDB(t, repo)
		fr := &fakeRunner{sessionPID: 1}
		_, _, err := CreateAndStart(d, fr, CreateInput{
			Name: "rej", Prompt: "p", Project: "proj", Backend: "codex", Account: "work",
		})
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), `account "work" does not support backend "codex"`)
		testutil.Equal(t, fr.startCalls, 0)
		testutil.False(t, dirExists(WorktreeDir("proj", "rej")))
		tasks, terr := d.Tasks()
		testutil.NoError(t, terr)
		testutil.Equal(t, len(tasks), 0)
	})

	cases := []struct {
		name, extra, backend, account string
		inherited                     bool
		want                          string
		wantErr                       string
	}{
		{"codex account on codex backend", "", "codex", "cx", false, "cx", ""},
		{"unsupported global default falls back", `default_account = "work"`, "codex", "", false, "", ""},
		{"unsupported project default falls back", "[project_accounts]\nproj = \"cx\"", "test", "", false, "", ""},
		{"supported global default applies", `default_account = "cx"`, "codex", "", false, "cx", ""},
		{"inherited unsupported rejected", "", "pi", "work", true, "", "inherited account \"work\" does not support backend"},
		{"inherited unknown rejected", "", "test", "ghost", true, "", "unknown account"},
		{"inherited supported kept", "", "test", "work", true, "work", ""},
		{"inherited default pins over global default", `default_account = "work"`, "test", "default", true, "", ""},
		{"inherited default pins over project default", "[project_accounts]\nproj = \"work\"", "test", "default", true, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initGitRepo(t)
			var d *db.DB
			if tc.extra != "" {
				d = accountFileDB(t, repo, tc.extra)
			} else {
				d = accountFileDB(t, repo)
			}
			task, _, err := CreateAndStart(d, &fakeRunner{sessionPID: 1}, CreateInput{
				Name: "sup", Prompt: "p", Project: "proj", Backend: tc.backend,
				Account: tc.account, InheritedAccount: tc.inherited,
			})
			if tc.wantErr != "" {
				testutil.Error(t, err)
				testutil.Contains(t, err.Error(), tc.wantErr)
				return
			}
			testutil.NoError(t, err)
			testutil.Equal(t, task.Account, tc.want)
		})
	}
}

func TestResolveOrchestratorAccount(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)

	t.Run("no coordinator to inherit from", func(t *testing.T) {
		got, err := resolveOrchestratorAccount(d, 999999)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "")
	})

	t.Run("coordinator account inherited", func(t *testing.T) {
		orch, err := d.CreateHeraOrchestrator("acct-orch", "")
		testutil.NoError(t, err)
		task := &model.Task{ID: "coord-task", Name: "coord", Project: "proj", Account: "work", Status: model.StatusInProgress}
		testutil.NoError(t, d.Add(task))
		_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID, Name: "coord", Kind: db.HeraKindCoordinator,
			ArgusProject: "proj", Prompt: "x",
		}, task.ID, "")
		testutil.NoError(t, err)
		got, err := resolveOrchestratorAccount(d, orch.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "work")

		binding, err := d.HeraLiveBindingByTask(task.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(binding.ID, "test"))
		got, err = resolveOrchestratorAccount(d, orch.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "work")
	})

	t.Run("unreadable coordinator task fails instead of falling back", func(t *testing.T) {
		orch, err := d.CreateHeraOrchestrator("ghost-orch", "")
		testutil.NoError(t, err)
		_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID, Name: "gcoord", Kind: db.HeraKindCoordinator,
			ArgusProject: "proj", Prompt: "x",
		}, "no-such-task", "")
		testutil.NoError(t, err)
		_, err = resolveOrchestratorAccount(d, orch.ID)
		testutil.Error(t, err)
		testutil.Contains(t, err.Error(), "no readable coordinator task")
	})

	t.Run("default-account coordinator pins default", func(t *testing.T) {
		orch, err := d.CreateHeraOrchestrator("dflt-orch", "")
		testutil.NoError(t, err)
		task := &model.Task{ID: "dflt-coord", Name: "dcoord", Project: "proj", Status: model.StatusInProgress}
		testutil.NoError(t, d.Add(task))
		_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID, Name: "dcoord", Kind: db.HeraKindCoordinator,
			ArgusProject: "proj", Prompt: "x",
		}, task.ID, "")
		testutil.NoError(t, err)
		got, err := resolveOrchestratorAccount(d, orch.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got, "default")
	})
}

func TestPinnedAccount(t *testing.T) {
	testutil.Equal(t, PinnedAccount(""), "default")
	testutil.Equal(t, PinnedAccount("default"), "default")
	testutil.Equal(t, PinnedAccount("work"), "work")
}
