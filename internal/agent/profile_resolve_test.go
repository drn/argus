package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// writeLibraryProfile writes a profile TOML into the per-user library
// (<HOME>/.argus/profiles/<name>.toml). Callers MUST t.Setenv("HOME", ...)
// first so db.DataDir() resolves under a temp dir (never the real ~/.argus/).
func writeLibraryProfile(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(db.DataDir(), "profiles")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// envMap parses an exec.Cmd-style "KEY=VALUE" slice into a map, keeping the LAST
// value for a key (matching exec.Cmd.Env dedup semantics, where later entries
// win).
func envMap(kv []string) map[string]string {
	m := make(map[string]string, len(kv))
	for _, e := range kv {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

// TestResolveModel_ProfileChain exercises the full diligence-profile resolution
// precedence (add-diligence-profiles "Profile-aware model resolution").
func TestResolveModel_ProfileChain(t *testing.T) {
	const codeSlice = "[archetype.code_slice]\nmodel = \"sonnet\"\n"

	cases := []struct {
		name           string
		taskModel      string
		archetype      string
		backend        string // backend name in modelConfig()
		backendDefault string // backend.Model default
		profile        string // default.toml body; "" = no file on disk
		wantModel      string
		wantProfile    bool
	}{
		{
			name:        "task override wins, profile ignored",
			taskModel:   "opus",
			archetype:   "code_slice",
			backend:     "claude",
			profile:     codeSlice,
			wantModel:   "opus",
			wantProfile: false,
		},
		{
			name:        "profile model applied by archetype",
			archetype:   "code_slice",
			backend:     "claude",
			profile:     codeSlice,
			wantModel:   "sonnet",
			wantProfile: true,
		},
		{
			name:           "invalid-for-backend model falls through to default",
			archetype:      "code_slice",
			backend:        "codex", // sonnet is valid model but not a codex model
			backendDefault: "gpt-5",
			profile:        codeSlice,
			wantModel:      "gpt-5",
			wantProfile:    false,
		},
		{
			name:        "missing profile falls open to no model",
			archetype:   "code_slice",
			backend:     "claude",
			profile:     "", // no file written
			wantModel:   "",
			wantProfile: false,
		},
		{
			name:        "invalid profile falls open to no model",
			archetype:   "code_slice",
			backend:     "claude",
			profile:     "[archetype.code_slice]\nmodel = \"bogus-model\"\n", // fails validation
			wantModel:   "",
			wantProfile: false,
		},
		{
			name:        "no archetype skips the profile entirely",
			archetype:   "",
			backend:     "claude",
			profile:     codeSlice,
			wantModel:   "",
			wantProfile: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfg := modelConfig()
			if tc.backendDefault != "" {
				b := cfg.Backends[tc.backend]
				b.Model = tc.backendDefault
				cfg.Backends[tc.backend] = b
			}
			if tc.profile != "" {
				writeLibraryProfile(t, "default", tc.profile)
			}

			task := &model.Task{Model: tc.taskModel, Archetype: tc.archetype, Backend: tc.backend}
			b := cfg.Backends[tc.backend]

			gotModel, gotProf := ResolveModel(task, b, cfg)
			testutil.Equal(t, gotModel, tc.wantModel)

			if tc.wantProfile {
				if gotProf == nil {
					t.Fatalf("expected a resolved profile, got nil")
				}
				testutil.Equal(t, gotProf.Name, "default")
				testutil.Equal(t, gotProf.Archetype, tc.archetype)
				testutil.Equal(t, gotProf.Model, tc.wantModel)
			} else {
				testutil.Nil(t, gotProf)
			}
		})
	}
}

// TestResolveModel_ProfileBoundByProject confirms resolution honors the
// project's bound profile name (config.Project.Profile / ResolveProfileName),
// not just the implicit "default".
func TestResolveModel_ProfileBoundByProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	cfg.Projects = map[string]config.Project{
		"app": {Path: "/x", Profile: "lean"},
	}
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"haiku\"\n")

	task := &model.Task{Project: "app", Archetype: "code_slice", Backend: "claude"}
	gotModel, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "haiku")
	if gotProf == nil {
		t.Fatal("expected a resolved profile")
	}
	testutil.Equal(t, gotProf.Name, "lean")
}

// TestBuildCmd_ProfileEnv_PresentOnResolution verifies the env export and the
// --model injection when a bound profile resolves a backend-valid model
// (add-diligence-profiles "Profile environment injection").
func TestBuildCmd_ProfileEnv_PresentOnResolution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	task := &model.Task{
		ID: "t1", Name: "t", Prompt: "go", Backend: "claude",
		Archetype: "code_slice", Worktree: t.TempDir(),
	}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	env := envMap(cmd.Env)
	testutil.Equal(t, env["ARGUS_PROFILE"], "default")
	testutil.Equal(t, env["ARGUS_ARCHETYPE"], "code_slice")
	testutil.Equal(t, env["ARGUS_MODEL"], "sonnet")
	// The same resolution drives --model injection.
	testutil.Contains(t, cmd.Args[2], "--model 'sonnet'")
}

// TestBuildCmd_ProfileEnv_AbsentWithoutProfile verifies none of the profile env
// vars are exported when the task carries no archetype (so no profile resolves).
func TestBuildCmd_ProfileEnv_AbsentWithoutProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	// No archetype → resolution short-circuits → no profile env.
	task := &model.Task{ID: "t2", Name: "t", Prompt: "go", Backend: "claude", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	env := envMap(cmd.Env)
	for _, k := range []string{"ARGUS_PROFILE", "ARGUS_ARCHETYPE", "ARGUS_MODEL"} {
		if _, ok := env[k]; ok {
			t.Errorf("expected %s to be absent; cmd.Env = %v", k, cmd.Env)
		}
	}
}

// TestBuildCmd_ProfileEnv_AbsentOnFallThrough verifies the env vars are omitted
// when the profile model is not valid for the backend (resolution falls through
// to the backend default for --model, and exports no profile env).
func TestBuildCmd_ProfileEnv_AbsentOnFallThrough(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	b := cfg.Backends["codex"]
	b.Model = "gpt-5"
	cfg.Backends["codex"] = b
	// sonnet is a valid model name but not valid for the codex backend.
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	task := &model.Task{
		ID: "t3", Name: "t", Prompt: "go", Backend: "codex",
		Archetype: "code_slice", Worktree: t.TempDir(),
	}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	env := envMap(cmd.Env)
	for _, k := range []string{"ARGUS_PROFILE", "ARGUS_ARCHETYPE", "ARGUS_MODEL"} {
		if _, ok := env[k]; ok {
			t.Errorf("expected %s to be absent on fall-through; cmd.Env = %v", k, cmd.Env)
		}
	}
	// --model still reflects the backend default.
	testutil.Contains(t, cmd.Args[2], "--model 'gpt-5'")
}
