package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestAgentGitPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, policy string
		env          []string
		wantErr      bool
	}{
		{"default", "", []string{"GIT_CONFIG_COUNT=bad"}, false},
		{"inherit", "inherit", []string{"GIT_CONFIG_COUNT=bad"}, false},
		{"unsigned", "unsigned", nil, false},
		{"preserve", "unsigned", []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=Existing"}, false},
		{"parameters", "unsigned", []string{"GIT_CONFIG_PARAMETERS='commit.gpgsign=true'"}, false},
		{"last count wins", "unsigned", []string{"GIT_CONFIG_COUNT=bad", "GIT_CONFIG_COUNT=0"}, false},
		{"bad count", "unsigned", []string{"GIT_CONFIG_COUNT=bad"}, true},
		{"negative count", "unsigned", []string{"GIT_CONFIG_COUNT=-1"}, true},
		{"overflow count", "unsigned", []string{"GIT_CONFIG_COUNT=9223372036854775807"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.env...)
			got, err := agentGitPolicy(tc.env, tc.policy)
			testutil.DeepEqual(t, tc.env, original)
			if tc.wantErr {
				testutil.Contains(t, err.Error(), "GIT_CONFIG_COUNT")
				return
			}
			testutil.NoError(t, err)
			if tc.policy != "unsigned" {
				testutil.DeepEqual(t, got, tc.env)
				return
			}
			cmd := exec.Command("git", "config", "--get", "commit.gpgsign")
			cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}, got...)
			out, err := cmd.CombinedOutput()
			testutil.NoError(t, err)
			testutil.Equal(t, strings.TrimSpace(string(out)), "false")
			if tc.name == "preserve" {
				env := cmd.Env
				cmd = exec.Command("git", "config", "--get", "user.name")
				cmd.Env = env
				out, err = cmd.CombinedOutput()
				testutil.NoError(t, err)
				testutil.Equal(t, strings.TrimSpace(string(out)), "Existing")
			}
		})
	}
}

func TestBuildCmd_AgentCommitSigning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, resume := range []bool{false, true} {
		for _, policy := range []string{"", "inherit", "unsigned", "invalid"} {
			t.Run(policy+map[bool]string{false: "/fresh", true: "/resume"}[resume], func(t *testing.T) {
				cfg := testConfig()
				cfg.Git.AgentCommitSigning = policy
				// Backend mappings happen before the policy, including legacy -c env.
				t.Setenv("TEST_GIT_PARAMETERS", "'commit.gpgsign=true'")
				cfg.Backends["bare"] = config.Backend{Command: "my-agent", EnvVars: map[string]string{"GIT_CONFIG_PARAMETERS": "TEST_GIT_PARAMETERS"}}
				cmd, cleanup, err := BuildCmd(&model.Task{ID: "signing", Backend: "bare", Worktree: t.TempDir()}, cfg, resume)
				if policy == "invalid" {
					testutil.Contains(t, err.Error(), "git.agent_commit_signing")
					return
				}
				testutil.NoError(t, err)
				if cleanup != nil {
					t.Cleanup(cleanup)
				}
				probe := exec.Command("git", "config", "--get", "commit.gpgsign")
				probe.Env = cmd.Env
				out, err := probe.CombinedOutput()
				testutil.NoError(t, err)
				want := "true"
				if policy == "unsigned" {
					want = "false"
				}
				testutil.Equal(t, strings.TrimSpace(string(out)), want)
			})
		}
	}
}

func TestBuildCmd_AgentGitCountError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "invalid")
	cfg := testConfig()
	cfg.Git.AgentCommitSigning = "unsigned"
	_, _, err := BuildCmd(&model.Task{Backend: "bare", Worktree: t.TempDir()}, cfg, false)
	testutil.Contains(t, err.Error(), "invalid GIT_CONFIG_COUNT")
}

func TestUnsignedCommitSkipsSigner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "signed")
	signer := filepath.Join(t.TempDir(), "signer")
	testutil.NoError(t, os.WriteFile(signer, []byte("#!/bin/sh\nprintf invoked > \"$SIGNER_MARKER\"\nexit 1\n"), 0o700))
	global := filepath.Join(t.TempDir(), "gitconfig")
	body := "[user]\nname = Test\nemail = test@example.com\nsigningkey = fake\n[commit]\ngpgsign = true\n[gpg]\nformat = ssh\n[gpg \"ssh\"]\nprogram = " + signer + "\n"
	testutil.NoError(t, os.WriteFile(global, []byte(body), 0o600))
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("SIGNER_MARKER", marker)
	run := func(env []string, args ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		return cmd.CombinedOutput()
	}
	_, err := run(os.Environ(), "init", "-q")
	testutil.NoError(t, err)
	localPath := filepath.Join(dir, ".git", "config")
	localBefore, err := os.ReadFile(localPath)
	testutil.NoError(t, err)
	// Control proves the synthetic signer really runs and fails under inherit.
	_, err = run(os.Environ(), "commit", "--allow-empty", "-m", "signed")
	testutil.Equal(t, err != nil, true)
	_, err = os.Stat(marker)
	testutil.NoError(t, err)
	testutil.NoError(t, os.Remove(marker))
	env, err := agentGitPolicy(os.Environ(), "unsigned")
	testutil.NoError(t, err)
	out, err := run(env, "commit", "--allow-empty", "-m", "unsigned")
	if err != nil {
		t.Fatalf("unsigned commit: %v: %s", err, out)
	}
	_, err = os.Stat(marker)
	testutil.Equal(t, os.IsNotExist(err), true)
	contents, err := os.ReadFile(global)
	testutil.NoError(t, err)
	testutil.Equal(t, string(contents), body)
	// Explicit command-line signing still overrides the session default.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"config option", []string{"-c", "commit.gpgsign=true", "commit", "--allow-empty", "-m", "explicit"}},
		{"sign flag", []string{"commit", "-S", "--allow-empty", "-m", "explicit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := run(env, tc.args...)
			testutil.Equal(t, err != nil, true)
			_, err = os.Stat(marker)
			testutil.NoError(t, err)
			testutil.NoError(t, os.Remove(marker))
		})
	}
	localAfter, err := os.ReadFile(localPath)
	testutil.NoError(t, err)
	testutil.Equal(t, string(localAfter), string(localBefore))
}
