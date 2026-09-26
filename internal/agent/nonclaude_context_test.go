package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// containsAny reports whether s contains any of substrs.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestBuildCmd_PiAndOpencodeSkillsAreSessionScoped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "argus-skills")
	previous := ensureSessionSkillsFn
	ensureSessionSkillsFn = func() (string, error) { return root, nil }
	defer func() { ensureSessionSkillsFn = previous }()
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"custom","skills":["/my/skill"]}`)
	cfg := nonClaudeContextConfig()
	skillsDir := filepath.Join(root, ".claude", "skills")

	piTask := &model.Task{Name: "pi", Backend: "pi", Prompt: "go", Worktree: t.TempDir()}
	piCmd, _, err := BuildCmd(piTask, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, piCmd.Args[2], "--skill '"+skillsDir+"'")
	if containsAny(piCmd.Args[2], "--add-dir") {
		t.Fatal("Pi must receive its own skill flag")
	}

	openTask := &model.Task{Name: "open", Backend: "opencode", Prompt: "go", Model: "provider/model", Worktree: t.TempDir()}
	openCmd, _, err := BuildCmd(openTask, cfg, false)
	testutil.NoError(t, err)
	var inline string
	for _, entry := range openCmd.Env {
		if strings.HasPrefix(entry, "OPENCODE_CONFIG_CONTENT=") {
			inline = strings.TrimPrefix(entry, "OPENCODE_CONFIG_CONTENT=")
		}
	}
	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(inline), &config))
	testutil.Equal(t, config["theme"], any("custom"))
	testutil.Equal(t, config["model"], any("provider/model"))
	testutil.DeepEqual(t, config["skills"], any([]any{"/my/skill", skillsDir}))
}

func TestOpencodeSessionConfigContent_InvalidExistingContent(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "not json")
	if _, err := opencodeSessionConfigContent("provider/model", "/skills"); err == nil {
		t.Fatal("invalid existing inline config must not be replaced")
	}
}

func TestOpencodeSessionConfigContent_MergesModelAndSkills(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"custom","skills":["/my/skill"],"model":"old/model"}`)

	got, err := opencodeSessionConfigContent("provider/model", "/skills")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["theme"], any("custom"))
	testutil.Equal(t, config["model"], any("provider/model"))
	testutil.DeepEqual(t, config["skills"], any([]any{"/my/skill", "/skills"}))
}

func TestOpencodeSessionConfigContent_ModelOnly(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")

	got, err := opencodeSessionConfigContent("provider/model", "")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["model"], any("provider/model"))
	if _, ok := config["skills"]; ok {
		t.Fatal("model-only override must not synthesize a skills key")
	}
}

func TestOpencodeSessionConfigContent_ReplacesExpandedModel(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"model":{"providerID":"old","model":"old-model"}}`)

	got, err := opencodeSessionConfigContent("provider/model", "")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["model"], any("provider/model"))
}

func TestOpencodeSessionConfigContent_NullSkillsIsReplaced(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"skills":null}`)

	got, err := opencodeSessionConfigContent("provider/model", "/skills")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["model"], any("provider/model"))
	testutil.DeepEqual(t, config["skills"], any([]any{"/skills"}))
}

func TestOpencodeSessionConfigContent_IncompatibleSkillsPreservesModel(t *testing.T) {
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"skills":"user-owned"}`)

	got, err := opencodeSessionConfigContent("provider/model", "/skills")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["model"], any("provider/model"))
	testutil.Equal(t, config["skills"], any("user-owned"))
}

func TestOpencodeSessionConfigContent_TrailingCommasAreAccepted(t *testing.T) {
	// OpenCode parses OPENCODE_CONFIG_CONTENT as JSONC (trailing commas
	// allowed), so a document it accepts must not read as malformed here.
	t.Setenv("OPENCODE_CONFIG_CONTENT", "{\n  \"theme\": \"custom\",\n  \"model\": \"old/model\",\n  \"skills\": [\"/my/skill\",],\n}")

	got, err := opencodeSessionConfigContent("provider/model", "/skills")
	testutil.NoError(t, err)

	var config map[string]any
	testutil.NoError(t, json.Unmarshal([]byte(got), &config))
	testutil.Equal(t, config["theme"], any("custom"))
	testutil.Equal(t, config["model"], any("provider/model"))
	testutil.DeepEqual(t, config["skills"], any([]any{"/my/skill", "/skills"}))
}

func TestStripJSONCTrailingCommas(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"object trailing comma", `{"a":1,}`, `{"a":1}`},
		{"array trailing comma", `{"a":[1,2,],}`, `{"a":[1,2]}`},
		// Only the comma is dropped; the whitespace that followed it stays, and
		// the result is still valid JSON.
		{"whitespace before closer", "{\"a\":1,  \n}", "{\"a\":1  \n}"},
		{"comma inside string kept", `{"a":"x,",}`, `{"a":"x,"}`},
		// A } that lives inside a string must not be mistaken for a closer.
		{"escaped quote inside string", `{"a":"q\"}",}`, `{"a":"q\"}"}`},
		{"real comma kept", `{"a":1,"b":2}`, `{"a":1,"b":2}`},
		{"string ending in comma-brace", `{"a":"},",}`, `{"a":"},"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, stripJSONCTrailingCommas(tc.in), tc.want)
		})
	}
}

func TestOpencodeSessionConfigContent_NonObjectInputs(t *testing.T) {
	// `null` decodes into a nil map (a different path than an unmarshal error)
	// and the rest are unmarshal errors; all must be rejected rather than
	// silently producing a config that drops the user's document.
	for _, raw := range []string{"null", "[1,2]", `"a string"`, "3", "true", "not json"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("OPENCODE_CONFIG_CONTENT", raw)
			if _, err := opencodeSessionConfigContent("provider/model", "/skills"); err == nil {
				t.Fatalf("non-object inline config %q must be rejected", raw)
			}
		})
	}
}

func TestOpencodeSessionConfigContent_AlreadyMergedIsByteIdentical(t *testing.T) {
	// Nothing to change ⇒ no re-marshal, so an operator's formatting and key
	// order survive and the child sees exactly the document it would have had.
	const raw = `{"model":"provider/model","skills":["/skills"],"theme":"custom"}`
	t.Setenv("OPENCODE_CONFIG_CONTENT", raw)

	got, err := opencodeSessionConfigContent("provider/model", "/skills")
	testutil.NoError(t, err)
	testutil.Equal(t, got, raw)
}

func TestNonClaudeRoutingContentReal_ReturnsEmbeddedContent(t *testing.T) {
	got, err := nonClaudeRoutingContentReal()
	testutil.NoError(t, err)
	if got == "" {
		t.Fatal("expected non-empty embedded routing content")
	}
}

func TestNonClaudeContextPrefix_NeitherBackendReturnsEmpty(t *testing.T) {
	got := nonClaudeContextPrefix(false, false)
	testutil.Equal(t, got, "")
}

func TestNonClaudeContextPrefix_CodexRoutingOnly(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false)
	testutil.Equal(t, got, "ROUTING\n\n---\n\n")
}

func TestNonClaudeContextPrefix_OpencodeRoutingOnly(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(false, true)
	testutil.Equal(t, got, "ROUTING\n\n---\n\n")
}

func TestNonClaudeContextPrefix_EmptyRoutingReturnsEmpty(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "", nil })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false)
	testutil.Equal(t, got, "")
}

func TestNonClaudeContextPrefix_SourceErrorsSkippedNotFatal(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "", errors.New("boom") })
	defer restoreRouting()

	got := nonClaudeContextPrefix(true, false)
	testutil.Equal(t, got, "")
}

// --- isCodex-gating seam: BuildCmd must call ensureCodexSkillsFn for codex
// only, never for claude/opencode/pi (which have no such equivalent). ---

func TestBuildCmd_EnsureCodexSkills_CalledForCodex(t *testing.T) {
	called := false
	isolatedHome := filepath.Join(t.TempDir(), "codex-home")
	restore := SetEnsureCodexSkillsForTest(func() (string, error) {
		called = true
		return isolatedHome, nil
	})
	defer restore()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}
	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	if !called {
		t.Error("expected ensureCodexSkillsFn to be called for a codex backend")
	}
	for _, entry := range []string{"CODEX_HOME=" + isolatedHome, "CODEX_SQLITE_HOME=" + filepath.Join(os.Getenv("HOME"), ".codex")} {
		if !containsEnvEntry(cmd.Env, entry) {
			t.Errorf("expected child env to contain %q", entry)
		}
	}
}

func TestBuildCmd_EnsureCodexSkills_FailureDoesNotBlockLaunch(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	restore := SetEnsureCodexSkillsForTest(func() (string, error) {
		return "", errors.New("cannot create isolated home")
	})
	defer restore()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: t.TempDir()}
	cmd, cleanup, err := BuildCmd(task, cfg, false)
	if cleanup != nil {
		defer cleanup()
	}
	testutil.NoError(t, err)
	if containsEnvEntry(cmd.Env, "CODEX_HOME="+filepath.Join(os.Getenv("HOME"), ".local", "share", "argus", "codex-home")) {
		t.Fatal("failed preparation must not override CODEX_HOME")
	}
}

func containsEnvEntry(env []string, want string) bool {
	for _, entry := range env {
		if entry == want {
			return true
		}
	}
	return false
}

func TestBuildCmd_EnsureCodexSkills_NotCalledForOtherBackends(t *testing.T) {
	cfg := nonClaudeContextConfig()

	for _, backend := range []string{"claude", "opencode", "pi"} {
		t.Run(backend, func(t *testing.T) {
			called := false
			restore := SetEnsureCodexSkillsForTest(func() (string, error) {
				called = true
				return "", nil
			})
			defer restore()

			task := &model.Task{Name: "t", Backend: backend, Prompt: "go", Worktree: t.TempDir()}
			_, _, err := BuildCmd(task, cfg, false)
			testutil.NoError(t, err)
			if called {
				t.Errorf("expected ensureCodexSkillsFn NOT to be called for backend %q", backend)
			}
		})
	}
}

// --- BuildCmd integration: end-to-end wiring per the agent-execution delta spec ---

func nonClaudeContextConfig() config.Config {
	return config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude":   {Command: "claude"},
			"codex":    {Command: "codex --dangerously-bypass-approvals-and-sandbox"},
			"pi":       {Command: "pi"},
			"opencode": {Command: "opencode", PromptFlag: "--prompt"},
		},
	}
}

func TestBuildCmd_NonClaudeContextPrefix_CodexRoutingOnly(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "t", Backend: "codex", Prompt: "go", Worktree: worktree}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "ROUTING")
	testutil.Contains(t, cmd.Args[2], "go") // original prompt still present
	if containsAny(cmd.Args[2], "REPO", "CLAUDE.md") {
		t.Errorf("expected codex command to exclude CLAUDE.md content, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_NonClaudeContextPrefix_OpencodeRoutingOnly(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "CLAUDE.md"), []byte("REPO"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{Name: "t", Backend: "opencode", Prompt: "go", Worktree: worktree}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Contains(t, cmd.Args[2], "ROUTING")
	if containsAny(cmd.Args[2], "REPO", "CLAUDE.md") {
		t.Errorf("expected opencode command to exclude CLAUDE.md content, got %q", cmd.Args[2])
	}
}

func TestBuildCmd_NonClaudeContextPrefix_ClaudeUnaffected(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "claude", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "claude -- 'go'")
}

func TestBuildCmd_NonClaudeContextPrefix_PiUnaffected(t *testing.T) {
	restoreRouting := SetNonClaudeRoutingContentForTest(func() (string, error) { return "ROUTING", nil })
	defer restoreRouting()

	cfg := nonClaudeContextConfig()
	task := &model.Task{Name: "t", Backend: "pi", Prompt: "go", Worktree: t.TempDir()}

	cmd, _, err := BuildCmd(task, cfg, false)
	testutil.NoError(t, err)
	testutil.Equal(t, cmd.Args[2], "pi 'go'")
}
