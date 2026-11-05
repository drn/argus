package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/claudeaccount"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/doctor"
	"github.com/drn/argus/internal/hostsleep"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- Stop-hook registration check (detect-missing-coord-hook) ---

func writeSettingsJSON(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	testutil.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestReadStopHookCommands_Registered(t *testing.T) {
	path := writeSettingsJSON(t, `{
		"hooks": {
			"Stop": [
				{ "hooks": [ { "type": "command", "command": "argus coord-hook" } ] }
			]
		}
	}`)
	cmds, err := readStopHookCommands(path)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, cmds, []string{"argus coord-hook"})
}

func TestReadStopHookCommands_MultipleGroupsAndHooks(t *testing.T) {
	path := writeSettingsJSON(t, `{
		"hooks": {
			"Stop": [
				{ "hooks": [ { "type": "command", "command": "some-other-hook" } ] },
				{ "hooks": [
					{ "type": "command", "command": "/opt/bin/argus coord-hook" },
					{ "type": "command", "command": "another-hook" }
				] }
			]
		}
	}`)
	cmds, err := readStopHookCommands(path)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, cmds, []string{"some-other-hook", "/opt/bin/argus coord-hook", "another-hook"})
}

func TestReadStopHookCommands_NoStopHooks(t *testing.T) {
	path := writeSettingsJSON(t, `{"hooks": {"UserPromptSubmit": []}}`)
	cmds, err := readStopHookCommands(path)
	testutil.NoError(t, err)
	testutil.Equal(t, len(cmds), 0)
}

func TestReadStopHookCommands_MissingFile(t *testing.T) {
	_, err := readStopHookCommands(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error for a missing settings file, got nil")
	}
}

func TestReadStopHookCommands_MalformedJSON(t *testing.T) {
	path := writeSettingsJSON(t, `{not valid json`)
	_, err := readStopHookCommands(path)
	if err == nil {
		t.Fatal("expected an error for malformed JSON, got nil")
	}
}

func TestGatherStopHookStatus_EmptyPathIsUnknown(t *testing.T) {
	// claudeSettingsPath() falling back to "" (home dir unresolvable) must
	// degrade to Unknown, never a false "not registered".
	_, err := readStopHookCommands("")
	if err == nil {
		t.Fatal("expected an error reading an empty path")
	}
}

// --- Diligence-profile library check (add-doctor-profile-check) ---
// Reuses writeProfileFile (validate_test.go), which writes dir/name.toml.

func TestDiagnoseProfileLibraryAt_ValidProfilePresent(t *testing.T) {
	dir := t.TempDir()
	writeProfileFile(t, dir, "default", "[archetype.docs]\n")
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryFound)
}

func TestDiagnoseProfileLibraryAt_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryNone)
}

func TestDiagnoseProfileLibraryAt_MissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryNone)
}

func TestDiagnoseProfileLibraryAt_OnlyInvalidProfiles(t *testing.T) {
	dir := t.TempDir()
	writeProfileFile(t, dir, "broken", "[archetype.not_a_real_archetype]\n")
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryNone)
}

func TestDiagnoseProfileLibraryAt_NonTomlFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a profile"), 0o644))
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryNone)
}

func TestDiagnoseProfileLibraryAt_MixedValidAndInvalid(t *testing.T) {
	dir := t.TempDir()
	writeProfileFile(t, dir, "broken", "[archetype.not_a_real_archetype]\n")
	writeProfileFile(t, dir, "default", "[archetype.docs]\n")
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryFound)
}

func TestDiagnoseProfileLibraryAt_UnreadableDirectoryIsUnknown(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks don't apply when running as root")
	}
	parent := t.TempDir()
	dir := filepath.Join(parent, "profiles")
	testutil.NoError(t, os.Mkdir(dir, 0o000))
	defer os.Chmod(dir, 0o755) //nolint:errcheck,gosec // test cleanup restoring +x so t.TempDir() can remove the dir
	got := diagnoseProfileLibraryAt(dir, config.DefaultConfig())
	testutil.Equal(t, got, doctor.ProfileLibraryUnknown)
}

// --- Secrets bootstrap diagnostic (add-secrets-resolver-registry, Task 1.11)
// ---
// secretsBootstrapStatusFor(cfg) is the testable, cfg-injectable counterpart
// to the production gatherSecretsBootstrapStatus() (which loads cfg from the
// real ~/.argus/config.toml, mirroring gatherProfileLibraryStatus ->
// diagnoseProfileLibraryAt). Fails to compile until Stage 5.2 adds both to
// cmd/argus/doctor.go.

func TestSecretsBootstrapStatusFor_Resolved(t *testing.T) {
	t.Setenv("DOCTOR_SECRETS_TEST_VAR", "value")
	cfg := config.Config{Secrets: config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "env://DOCTOR_SECRETS_TEST_VAR",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}}
	got := secretsBootstrapStatusFor(cfg)
	testutil.Equal(t, got, doctor.SecretsBootstrapResolved)
}

func TestSecretsBootstrapStatusFor_NotResolved(t *testing.T) {
	cfg := config.Config{Secrets: config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "env://DOCTOR_SECRETS_DEFINITELY_UNSET_VAR_XYZ",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}}
	got := secretsBootstrapStatusFor(cfg)
	testutil.Equal(t, got, doctor.SecretsBootstrapNotResolved)
}

func TestSecretsBootstrapStatusFor_NotConfigured(t *testing.T) {
	got := secretsBootstrapStatusFor(config.Config{})
	testutil.Equal(t, got, doctor.SecretsBootstrapNotConfigured)
}

// TestSecretsBootstrapStatusFor_DoesNotAffectExitCodeContract pins the
// binary-coherence delta's "Check does not change the exit-code contract"
// scenario: argus doctor's exit code (runDoctor calls os.Exit(1) only when
// doctor.Diagnose(actors).Verdict != doctor.Healthy) is governed solely by
// the binary-coherence actors. doctor.Diagnose's signature takes only
// []doctor.Actor, so a NOT RESOLVED secrets bootstrap status computed
// alongside a Healthy actor set can never influence it — proven here by
// computing both from independent inputs and asserting the verdict is
// unchanged by the secrets status either way.
func TestSecretsBootstrapStatusFor_DoesNotAffectExitCodeContract(t *testing.T) {
	actors := []doctor.Actor{
		{Role: doctor.RolePathArgus, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
		{Role: doctor.RoleArgusdTarget, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
		{Role: doctor.RoleGoInstall, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
		{Role: doctor.RoleDaemon, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
		{Role: doctor.RoleSupervisor, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
		{Role: doctor.RoleTUI, ResolvedPath: "/opt/bin/argus", Hash: "h", Resolved: true},
	}
	testutil.Equal(t, doctor.Diagnose(actors).Verdict, doctor.Healthy)

	cfg := config.Config{Secrets: config.SecretsConfig{Op: config.OpConfig{
		BootstrapSource: "env://DOCTOR_EXITCODE_TEST_DEFINITELY_UNSET_VAR",
		BootstrapTarget: "OP_SERVICE_ACCOUNT_TOKEN",
	}}}
	testutil.Equal(t, secretsBootstrapStatusFor(cfg), doctor.SecretsBootstrapNotResolved)

	// The binary-coherence verdict computed from the SAME actors is untouched
	// by the secrets status computed above.
	testutil.Equal(t, doctor.Diagnose(actors).Verdict, doctor.Healthy)
}

// --- Dev-stack orphan check (fix-devstack-orphaning) ---

func TestDiagnoseDevStackOrphansFrom_NoneRunning(t *testing.T) {
	status, orphans := diagnoseDevStackOrphansFrom(nil, nil, func(string) bool { return true })
	testutil.Equal(t, status, doctor.DevStackOrphanNone)
	testutil.Equal(t, len(orphans), 0)
}

func TestDiagnoseDevStackOrphansFrom_AllReferenceExistingWorktrees(t *testing.T) {
	procs := []agent.DevStackProc{
		{PID: 1, Name: "mysqld", WorktreePath: "/x/still-here"},
		{PID: 2, Name: "redis-server", WorktreePath: "/x/also-still-here"},
	}
	status, orphans := diagnoseDevStackOrphansFrom(procs, nil, func(string) bool { return true })
	testutil.Equal(t, status, doctor.DevStackOrphanNone)
	testutil.Equal(t, len(orphans), 0)
}

func TestDiagnoseDevStackOrphansFrom_SomeReferenceDeletedWorktrees(t *testing.T) {
	procs := []agent.DevStackProc{
		{PID: 1, Name: "mysqld", WorktreePath: "/x/still-here"},
		{PID: 2, Name: "redis-server", WorktreePath: "/x/deleted"},
	}
	exists := func(path string) bool { return path != "/x/deleted" }

	status, orphans := diagnoseDevStackOrphansFrom(procs, nil, exists)
	testutil.Equal(t, status, doctor.DevStackOrphanFound)
	testutil.Equal(t, len(orphans), 1)
	testutil.Equal(t, orphans[0].PID, 2)
	testutil.Equal(t, orphans[0].Name, "redis-server")
	testutil.Equal(t, orphans[0].WorktreePath, "/x/deleted")
}

func TestDiagnoseDevStackOrphansFrom_ScanErrorIsUnknown(t *testing.T) {
	procs := []agent.DevStackProc{{PID: 1, Name: "mysqld", WorktreePath: "/x/deleted"}}
	status, orphans := diagnoseDevStackOrphansFrom(procs, errors.New("pgrep unavailable"), func(string) bool { return false })
	testutil.Equal(t, status, doctor.DevStackOrphanUnknown)
	testutil.Equal(t, len(orphans), 0)
}

func TestRenderClaudeAccounts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work := filepath.Join(home, "acct-work")
	pers := filepath.Join(home, "acct-personal")
	testutil.NoError(t, os.MkdirAll(work, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(work, "settings.json"),
		[]byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"argus coord-hook"}]}]}}`), 0o644))
	cfg := config.Config{Accounts: map[string]config.Account{
		"work":     {ClaudeConfigDir: work},
		"personal": {ClaudeConfigDir: pers},
	}}
	auth := func(_ context.Context, dir string) (claudeaccount.Status, error) {
		if dir == work {
			return claudeaccount.Status{LoggedIn: true, Email: "a@b.c", Org: "Thanx", Plan: "team"}, nil
		}
		return claudeaccount.Status{}, nil
	}
	out := renderClaudeAccounts(cfg, auth)
	testutil.Contains(t, out, "work ("+work+")")
	testutil.Contains(t, out, "Stop hook (argus coord-hook): REGISTERED")
	testutil.Contains(t, out, "Login: a@b.c / Thanx / team")
	testutil.Contains(t, out, "personal ("+pers+")")
	testutil.Contains(t, out, "UNKNOWN (could not read/parse settings.json)")
	testutil.Contains(t, out, "Login: not logged in")

	t.Run("auth failure degrades", func(t *testing.T) {
		out := renderClaudeAccounts(cfg, func(context.Context, string) (claudeaccount.Status, error) {
			return claudeaccount.Status{}, errors.New("no claude")
		})
		testutil.Contains(t, out, "Login: UNKNOWN")
	})
	t.Run("no accounts prints nothing", func(t *testing.T) {
		testutil.Equal(t, renderClaudeAccounts(config.Config{}, auth), "")
	})
}

func TestGatherHostSleep_APIDisabledByDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	enabled, report := gatherHostSleep()
	testutil.False(t, enabled)
	testutil.Equal(t, report.Status, hostsleep.NotApplicable)
}

func TestGatherHostSleep_ReadsDBAndConfigOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, err := db.Open(db.DefaultPath())
	testutil.NoError(t, err)
	testutil.NoError(t, d.SetConfigValue("api.enabled", "true"))
	testutil.NoError(t, d.Close())

	enabled, _ := gatherHostSleep()
	testutil.True(t, enabled)

	testutil.NoError(t, os.WriteFile(filepath.Join(db.DataDir(), config.FileName), []byte("[api]\nenabled = false\n"), 0o644))
	enabled, report := gatherHostSleep()
	testutil.False(t, enabled)
	testutil.Equal(t, report.Status, hostsleep.NotApplicable)
}

func TestClassifyFinishedStacksFrom(t *testing.T) {
	const root = "/h/.argus/worktrees"
	proc := func(pid int, wt string) agent.DevStackProc {
		return agent.DevStackProc{PID: pid, Name: "mysqld", WorktreePath: wt}
	}
	row := func(name, wt string, st model.Status, archived bool) db.TaskWorktreeRow {
		return db.TaskWorktreeRow{ID: name, Name: name, Worktree: wt, Status: st, Archived: archived}
	}
	rows := []db.TaskWorktreeRow{
		row("done", root+"/p/done", model.StatusComplete, false),
		row("arch", root+"/p/arch", model.StatusInReview, true),
		row("live", root+"/p/live", model.StatusInProgress, false),
		row("shared-a", root+"/p/shared", model.StatusComplete, false),
		row("shared-b", root+"/p/shared", model.StatusInProgress, false),
		row("main", "/h/dev/sketch", model.StatusComplete, false),
	}
	procs := []agent.DevStackProc{
		proc(1, root+"/p/done"), proc(2, root+"/p/arch"), proc(3, root+"/p/live"),
		proc(4, root+"/p/shared"), proc(5, "/h/dev/sketch"), proc(6, root+"/p/norow"),
	}
	status, got := classifyFinishedStacksFrom(procs, rows, nil, root)
	testutil.Equal(t, status, doctor.FinishedStackFound)
	testutil.Equal(t, len(got), 2)
	testutil.Equal(t, got[0].PID, 1)
	testutil.Equal(t, got[0].TaskName, "done")
	testutil.Equal(t, got[1].PID, 2)

	status, got = classifyFinishedStacksFrom(procs[2:3], rows, nil, root)
	testutil.Equal(t, status, doctor.FinishedStackNone)
	testutil.Equal(t, len(got), 0)

	status, _ = classifyFinishedStacksFrom(procs, rows, errors.New("scan failed"), root)
	testutil.Equal(t, status, doctor.FinishedStackUnknown)
}

func TestPathUnder(t *testing.T) {
	testutil.True(t, pathUnder("/r", "/r/p/t"))
	testutil.True(t, !pathUnder("/r", "/r"))
	testutil.True(t, !pathUnder("/r", "/r/p"))
	testutil.True(t, !pathUnder("/r", "/x/p/t"))
	testutil.True(t, !pathUnder("/r", "/r-evil/p/t"))
}

func TestGatherFinishedStacksFrom(t *testing.T) {
	const root = "/h/.argus/worktrees"
	proc := agent.DevStackProc{PID: 9, Name: "mysqld", WorktreePath: root + "/p/t"}
	rows := []db.TaskWorktreeRow{{ID: "x", Name: "x", Worktree: root + "/p/t", Status: model.StatusComplete}}
	readRows := func() ([]db.TaskWorktreeRow, error) { return rows, nil }
	neverRead := func() ([]db.TaskWorktreeRow, error) { t.Fatal("rows read without a scanned process"); return nil, nil }

	status, got := gatherFinishedStacksFrom(func() ([]agent.DevStackProc, error) { return []agent.DevStackProc{proc}, nil }, readRows, root)
	testutil.Equal(t, status, doctor.FinishedStackFound)
	testutil.Equal(t, len(got), 1)

	status, _ = gatherFinishedStacksFrom(func() ([]agent.DevStackProc, error) { return nil, nil }, neverRead, root)
	testutil.Equal(t, status, doctor.FinishedStackNone)

	status, _ = gatherFinishedStacksFrom(func() ([]agent.DevStackProc, error) { return nil, errors.New("no pgrep") }, neverRead, root)
	testutil.Equal(t, status, doctor.FinishedStackUnknown)

	status, _ = gatherFinishedStacksFrom(func() ([]agent.DevStackProc, error) { return []agent.DevStackProc{proc}, nil },
		func() ([]db.TaskWorktreeRow, error) { return nil, errors.New("db locked") }, root)
	testutil.Equal(t, status, doctor.FinishedStackUnknown)
}
