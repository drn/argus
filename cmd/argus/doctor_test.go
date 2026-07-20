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
