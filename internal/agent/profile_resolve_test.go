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

			gotModel, _, gotProf := ResolveModel(task, b, cfg)
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
	gotModel, _, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

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

// TestBuildCmd_ProfileEnv_IncludesEffort verifies ARGUS_EFFORT joins the
// existing trio (add-model-menu-selection D4/D7) and drives --effort
// injection, exactly mirroring how ARGUS_MODEL drives --model.
func TestBuildCmd_ProfileEnv_IncludesEffort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\neffort = \"high\"\n")

	task := &model.Task{
		ID: "t1e", Name: "t", Prompt: "go", Backend: "claude",
		Archetype: "code_slice", Worktree: t.TempDir(),
	}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	env := envMap(cmd.Env)
	testutil.Equal(t, env["ARGUS_PROFILE"], "default")
	testutil.Equal(t, env["ARGUS_ARCHETYPE"], "code_slice")
	testutil.Equal(t, env["ARGUS_MODEL"], "sonnet")
	testutil.Equal(t, env["ARGUS_EFFORT"], "high")
	testutil.Contains(t, cmd.Args[2], "--effort 'high'")
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
	for _, k := range []string{"ARGUS_PROFILE", "ARGUS_ARCHETYPE", "ARGUS_MODEL", "ARGUS_EFFORT"} {
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
	for _, k := range []string{"ARGUS_PROFILE", "ARGUS_ARCHETYPE", "ARGUS_MODEL", "ARGUS_EFFORT"} {
		if _, ok := env[k]; ok {
			t.Errorf("expected %s to be absent on fall-through; cmd.Env = %v", k, cmd.Env)
		}
	}
	// --model still reflects the backend default.
	testutil.Contains(t, cmd.Args[2], "--model 'gpt-5'")
}

// TestResolveModel_TaskProfileOverrideHonored verifies that a non-empty
// task.Profile overrides the project's bound profile during resolution.
func TestResolveModel_TaskProfileOverrideHonored(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	cfg.Projects = map[string]config.Project{
		"app": {Path: "/x", Profile: "lean"},
	}
	// lean profile (project binding) uses haiku; override uses sonnet.
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"haiku\"\n")
	writeLibraryProfile(t, "custom", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	task := &model.Task{
		Project:   "app",
		Archetype: "code_slice",
		Backend:   "claude",
		Profile:   "custom", // per-spawn override
	}
	gotModel, _, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "sonnet")
	if gotProf == nil {
		t.Fatal("expected a resolved profile")
	}
	testutil.Equal(t, gotProf.Name, "custom")
}

// TestResolveModel_EmptyTaskProfileFallsToProjectBinding verifies that an
// empty task.Profile falls through to the project's bound profile.
func TestResolveModel_EmptyTaskProfileFallsToProjectBinding(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	cfg.Projects = map[string]config.Project{
		"app": {Path: "/x", Profile: "lean"},
	}
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"haiku\"\n")

	task := &model.Task{
		Project:   "app",
		Archetype: "code_slice",
		Backend:   "claude",
		Profile:   "", // no override → use project binding
	}
	gotModel, _, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "haiku")
	if gotProf == nil {
		t.Fatal("expected a resolved profile")
	}
	testutil.Equal(t, gotProf.Name, "lean")
}

// TestResolveModel_InvalidTaskProfileOverrideFallsOpen verifies that a
// task.Profile naming a missing profile falls open (no --model), exactly as
// a missing project profile does.
func TestResolveModel_InvalidTaskProfileOverrideFallsOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	cfg.Projects = map[string]config.Project{
		"app": {Path: "/x", Profile: "lean"},
	}
	// lean profile exists on disk; the override points at a non-existent file.
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"haiku\"\n")

	task := &model.Task{
		Project:   "app",
		Archetype: "code_slice",
		Backend:   "claude",
		Profile:   "does-not-exist",
	}
	gotModel, _, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "") // falls open → no --model
	testutil.Nil(t, gotProf)
}

// TestBuildCmd_ProfileEnv_OverrideProfile verifies that ARGUS_PROFILE reflects
// the per-spawn override profile name, not the project's bound profile.
func TestBuildCmd_ProfileEnv_OverrideProfile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	cfg.Projects = map[string]config.Project{
		"app": {Path: "/x", Profile: "lean"},
	}
	writeLibraryProfile(t, "lean", "[archetype.code_slice]\nmodel = \"haiku\"\n")
	writeLibraryProfile(t, "custom", "[archetype.code_slice]\nmodel = \"sonnet\"\n")

	task := &model.Task{
		ID: "t4", Name: "t", Prompt: "go",
		Project:   "app",
		Backend:   "claude",
		Archetype: "code_slice",
		Profile:   "custom",
		Worktree:  t.TempDir(),
	}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)

	env := envMap(cmd.Env)
	testutil.Equal(t, env["ARGUS_PROFILE"], "custom")
	testutil.Equal(t, env["ARGUS_ARCHETYPE"], "code_slice")
	testutil.Equal(t, env["ARGUS_MODEL"], "sonnet")
	testutil.Contains(t, cmd.Args[2], "--model 'sonnet'")
}

// --- Menu-based archetype resolution and governance (add-model-menu-selection D6) ---

const menuGovernanceProfile = `
[archetype.code_slice]
menu = [
  { model = "sonnet", effort = "high" },
  { model = "opus", effort = "low" },
]
`

// TestResolveModel_MenuGovernance exercises every D6 governance branch: a
// matching full override is honored; a non-matching full override is
// substituted with the cheapest (first) menu entry; a partial override honors
// the set field and defaults the other from the cheapest entry with no
// membership check; and no override at all defaults to the cheapest entry.
func TestResolveModel_MenuGovernance(t *testing.T) {
	cases := []struct {
		name       string
		taskModel  string
		taskEffort string
		wantModel  string
		wantEffort string
	}{
		{"matching full override honored", "opus", "low", "opus", "low"},
		{"non-matching full override substituted with cheapest", "opus", "high", "sonnet", "high"},
		{"partial override model-only defaults effort from cheapest", "opus", "", "opus", "high"},
		{"partial override effort-only defaults model from cheapest", "", "low", "sonnet", "low"},
		{"neither set defaults to cheapest", "", "", "sonnet", "high"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			cfg := modelConfig()
			writeLibraryProfile(t, "default", menuGovernanceProfile)

			task := &model.Task{Model: tc.taskModel, Effort: tc.taskEffort, Archetype: "code_slice", Backend: "claude"}
			gotModel, gotEffort, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

			testutil.Equal(t, gotModel, tc.wantModel)
			testutil.Equal(t, gotEffort, tc.wantEffort)
			if gotProf == nil {
				t.Fatal("expected a resolved profile for a menu-governed archetype")
			}
			testutil.Equal(t, gotProf.Model, tc.wantModel)
			testutil.Equal(t, gotProf.Effort, tc.wantEffort)
		})
	}
}

// TestResolveModel_MenuPickInvalidForBackendFallsOpen verifies the menu path
// applies the same backend-validity fall-open contract as the scalar path
// (add-model-menu-selection "Profile-aware model resolution"): a menu authored
// with claude models, consulted by a task whose resolved backend is codex,
// must never inject a claude model name — resolution falls through to no
// model/no effort/no ResolvedProfile rather than the invalid pick.
func TestResolveModel_MenuPickInvalidForBackendFallsOpen(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", menuGovernanceProfile)

	task := &model.Task{Archetype: "code_slice", Backend: "codex"}
	gotModel, gotEffort, gotProf := ResolveModel(task, cfg.Backends["codex"], cfg)

	testutil.Equal(t, gotModel, "")
	testutil.Equal(t, gotEffort, "")
	testutil.Nil(t, gotProf)
}

// TestResolveModel_ScalarArchetypeNeverGated confirms a scalar (non-menu)
// archetype applies NO menu-membership check whatsoever — a full task
// override is honored unconditionally even though it matches neither the
// scalar profile pick nor (by construction) any menu.
func TestResolveModel_ScalarArchetypeNeverGated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\neffort = \"low\"\n")

	task := &model.Task{Model: "opus", Effort: "xhigh", Archetype: "code_slice", Backend: "claude"}
	gotModel, gotEffort, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "opus")
	testutil.Equal(t, gotEffort, "xhigh")
	testutil.Nil(t, gotProf)
}

// TestResolveModel_ScalarPairResolvedTogether verifies a scalar archetype's
// model and effort resolve together as a pair when neither is overridden
// (add-model-menu-selection D5).
func TestResolveModel_ScalarPairResolvedTogether(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := modelConfig()
	writeLibraryProfile(t, "default", "[archetype.code_slice]\nmodel = \"sonnet\"\neffort = \"high\"\n")

	task := &model.Task{Archetype: "code_slice", Backend: "claude"}
	gotModel, gotEffort, gotProf := ResolveModel(task, cfg.Backends["claude"], cfg)

	testutil.Equal(t, gotModel, "sonnet")
	testutil.Equal(t, gotEffort, "high")
	if gotProf == nil {
		t.Fatal("expected a resolved profile")
	}
	testutil.Equal(t, gotProf.Effort, "high")
}
