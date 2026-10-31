package agent

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// stubAccountUsage installs a fixed usage snapshot for the test.
func stubAccountUsage(t *testing.T, snap map[string]accountusage.Usage) {
	t.Helper()
	t.Cleanup(SetAccountUsageSource(func() map[string]accountusage.Usage { return snap }))
}

func pct(p float64) *accountusage.Reading {
	return &accountusage.Reading{Pct: p, ResetsAt: time.Now().Add(time.Hour)}
}

func TestCreateAndStart_AutoAccount(t *testing.T) {
	capped := map[string]accountusage.Usage{"default": {FiveHour: pct(100)}}
	cases := []struct {
		name, extra, backend, account string
		inherited                     bool
		snap                          map[string]accountusage.Usage
		want                          string
		wantAuto                      bool
	}{
		{"global auto skips capped default", `default_account = "auto"`, "", "", false, capped, "work", true},
		{"project auto", "[project_accounts]\nproj = \"auto\"", "", "", false, capped, "work", true},
		{"explicit auto", "", "", "auto", false, capped, "work", true},
		{"inherited auto", "", "", "auto", true, capped, "work", true},
		{"all unknown picks default", `default_account = "auto"`, "", "", false, nil, "", true},
		{"codex backend only considers codex accounts", `default_account = "auto"`, "codex", "", false, capped, "cx", true},
		{"project pin beats global auto", "default_account = \"auto\"\n[project_accounts]\nproj = \"work\"", "", "", false, capped, "work", false},
		{"explicit named account not auto", `default_account = "auto"`, "", "work", false, nil, "work", false},
		{"explicit default not auto", `default_account = "auto"`, "", "default", false, capped, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubAccountUsage(t, tc.snap)
			repo := initGitRepo(t)
			var d *db.DB
			if tc.extra != "" {
				d = accountFileDB(t, repo, tc.extra)
			} else {
				d = accountFileDB(t, repo)
			}
			task, _, err := CreateAndStart(d, &fakeRunner{sessionPID: 1}, CreateInput{
				Name: "auto", Prompt: "p", Project: "proj", Backend: tc.backend,
				Account: tc.account, InheritedAccount: tc.inherited,
			})
			testutil.NoError(t, err)
			testutil.Equal(t, task.Account, tc.want)
			testutil.Equal(t, task.AccountAuto, tc.wantAuto)
			got, err := d.Get(task.ID)
			testutil.NoError(t, err)
			testutil.Equal(t, got.Account, tc.want)
			testutil.Equal(t, got.AccountAuto, tc.wantAuto)
		})
	}
}

func TestPickAutoAccount_NilSourceIsAllUnknown(t *testing.T) {
	t.Cleanup(SetAccountUsageSource(nil))
	cfg := config.Config{Accounts: map[string]config.Account{"work": {ClaudeConfigDir: "/opt/work"}}}
	testutil.Equal(t, pickAutoAccount(cfg, "claude"), "default")
}

func TestResolveDefaultBackendName_AutoReadsPickedClaudeAccount(t *testing.T) {
	// The backend tier must be fed the account auto would pick for Claude:
	// default is capped, so that is "work", never the literal "auto".
	stubAccountUsage(t, map[string]accountusage.Usage{"default": {FiveHour: pct(100)}})
	var seen []string
	prev := resolveBackendTier
	resolveBackendTier = func(_ config.Config, accounts ...string) string {
		seen = append(seen, accounts...)
		return ""
	}
	t.Cleanup(func() { resolveBackendTier = prev })

	cfg := config.Config{
		DefaultAccount: config.AutoAccountName,
		Accounts:       map[string]config.Account{"work": {ClaudeConfigDir: "/opt/work"}},
		Backends:       map[string]config.Backend{"claude": {Command: "claude"}},
		Defaults:       config.Defaults{Backend: "claude"},
	}
	testutil.Equal(t, resolveDefaultBackendName("", "", cfg), "claude")
	testutil.DeepEqual(t, seen, []string{"work"})
}

func TestInheritAccount(t *testing.T) {
	testutil.Equal(t, InheritAccount(&model.Task{}), "default")
	testutil.Equal(t, InheritAccount(&model.Task{Account: "work"}), "work")
	testutil.Equal(t, InheritAccount(&model.Task{Account: "work", AccountAuto: true}), "auto")
	testutil.Equal(t, InheritAccount(&model.Task{AccountAuto: true}), "auto")
}

func TestResolveOrchestratorAccount_AutoCoordinator(t *testing.T) {
	repo := initGitRepo(t)
	d := createTestDB(t, repo)
	orch, err := d.CreateHeraOrchestrator("auto-orch", "")
	testutil.NoError(t, err)
	task := &model.Task{ID: "auto-coord", Name: "acoord", Project: "proj", Account: "work", AccountAuto: true, Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch.ID, Name: "acoord", Kind: db.HeraKindCoordinator,
		ArgusProject: "proj", Prompt: "x",
	}, task.ID, "")
	testutil.NoError(t, err)
	got, err := resolveOrchestratorAccount(d, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got, config.AutoAccountName)
}
