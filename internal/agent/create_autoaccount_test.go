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
	// The backend tier must be fed the Claude usage of the account auto would
	// pick, from the ledger: max of the two windows, a named account with no
	// reading counting as 0%, and only an unmeasured default using the probe.
	for _, tc := range []struct {
		name      string
		usage     map[string]accountusage.Usage
		wantProbe []string // accounts handed to the probe-backed resolver
		wantFixed []float64
	}{
		{
			name:      "unmeasured named account counts as 0%",
			usage:     map[string]accountusage.Usage{"default": {FiveHour: pct(100)}},
			wantFixed: []float64{0},
		},
		{
			name: "measured account uses the higher window",
			usage: map[string]accountusage.Usage{
				"default": {FiveHour: pct(100)},
				"work":    {FiveHour: pct(20), Weekly: pct(70)},
			},
			wantFixed: []float64{70},
		},
		{
			name:      "unmeasured default keeps the probe",
			usage:     map[string]accountusage.Usage{"work": {FiveHour: pct(100)}},
			wantProbe: []string{"default"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubAccountUsage(t, tc.usage)
			var probe []string
			var fixed []float64
			prevTier, prevFixed := resolveBackendTier, resolveBackendTierForClaudeUsage
			resolveBackendTier = func(_ config.Config, accounts ...string) string {
				probe = append(probe, accounts...)
				return ""
			}
			resolveBackendTierForClaudeUsage = func(_ config.Config, pct float64) string {
				fixed = append(fixed, pct)
				return ""
			}
			t.Cleanup(func() { resolveBackendTier, resolveBackendTierForClaudeUsage = prevTier, prevFixed })

			cfg := config.Config{
				DefaultAccount: config.AutoAccountName,
				Accounts:       map[string]config.Account{"work": {ClaudeConfigDir: "/opt/work"}},
				Backends:       map[string]config.Backend{"claude": {Command: "claude"}},
				Defaults:       config.Defaults{Backend: "claude"},
			}
			testutil.Equal(t, resolveDefaultBackendName("", "", cfg), "claude")
			testutil.DeepEqual(t, probe, tc.wantProbe)
			testutil.DeepEqual(t, fixed, tc.wantFixed)
		})
	}
}

func TestResolveDefaultBackendName_AutoUnmeasuredPersonalKeepsClaude(t *testing.T) {
	// Dogfood regression: default at 83% weekly, personal unmeasured, headroom
	// routing with Codex at 39% must still route Claude (to personal).
	stubAccountUsage(t, map[string]accountusage.Usage{"default": {FiveHour: pct(47), Weekly: pct(83)}})
	cfg := config.Config{
		DefaultAccount: config.AutoAccountName,
		Accounts:       map[string]config.Account{"personal": {ClaudeConfigDir: "/opt/personal"}},
		Backends:       map[string]config.Backend{"claude": {Command: "claude"}, "codex": {Command: "codex"}},
		Defaults:       config.Defaults{Backend: "claude"},
		BackendRouting: config.BackendRoutingConfig{
			Strategy: config.StrategyHeadroom,
			Tiers: []config.BackendTier{
				// Codex first and unprobed: an unknown Claude reading would tie
				// on "unread" and lose to it on list order.
				{Backend: "codex", Probe: config.ProbeNone},
				{Backend: "claude", Probe: config.ProbeClaudeUsage, ThresholdPct: 90},
			},
		},
	}
	testutil.Equal(t, resolveDefaultBackendName("", "", cfg), "claude")
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
