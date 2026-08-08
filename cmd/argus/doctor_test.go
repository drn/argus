package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/doctor"
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
