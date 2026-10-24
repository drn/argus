package agent

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)


func TestBuildCmd_BridgeModPluginDir(t *testing.T) {
	restore := SetEnsureBridgeModForTest(func() (string, error) {
		return "/fake/home/.argus/bridge-mod/abc/argus-bridge", nil
	})
	defer restore()
	// The real routing/skills seams return "" under go test, so the only
	// injected flag here is the bridge's.
	t.Run("claude with bridge enabled", func(t *testing.T) {
		cfg := testConfig()
		cfg.Bridge.Enabled = true
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, cmd.Args[2],
			"claude --dangerously-skip-permissions --permission-mode plan --plugin-dir '/fake/home/.argus/bridge-mod/abc/argus-bridge' -- 'go'")
	})

	t.Run("resume keeps the flag before the resume id", func(t *testing.T) {
		cfg := testConfig()
		cfg.Bridge.Enabled = true
		task := &model.Task{Name: "t", SessionID: "sid", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, cfg, true)
		testutil.NoError(t, err)
		testutil.Equal(t, cmd.Args[2],
			"claude --dangerously-skip-permissions --permission-mode plan --plugin-dir '/fake/home/.argus/bridge-mod/abc/argus-bridge' --resume 'sid'")
	})

	t.Run("disabled", func(t *testing.T) {
		cfg := testConfig() // Bridge.Enabled false
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, cmd.Args[2], "claude --dangerously-skip-permissions --permission-mode plan -- 'go'")
	})

	t.Run("non-claude families never get it", func(t *testing.T) {
		cfg := testConfig()
		cfg.Bridge.Enabled = true
		for backend, want := range map[string]string{
			"codex":    "codex --dangerously-bypass-approvals-and-sandbox -- 'go'",
			"pi":       "pi 'go'",
			"opencode": "opencode --auto --prompt 'go'",
			"bare":     "my-agent -- 'go'",
		} {
			task := &model.Task{Name: "t", Backend: backend, Prompt: "go", Worktree: t.TempDir()}
			cmd, _, err := BuildCmd(task, cfg, false)
			testutil.NoError(t, err)
			testutil.Equal(t, cmd.Args[2], want)
		}
	})

	t.Run("materialize error is non-fatal", func(t *testing.T) {
		restore := SetEnsureBridgeModForTest(func() (string, error) { return "", errors.New("disk full") })
		defer restore()
		cfg := testConfig()
		cfg.Bridge.Enabled = true
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, cmd.Args[2], "claude --dangerously-skip-permissions --permission-mode plan -- 'go'")
	})

	t.Run("empty dir (go test) adds nothing", func(t *testing.T) {
		restore := SetEnsureBridgeModForTest(func() (string, error) { return "", nil })
		defer restore()
		cfg := testConfig()
		cfg.Bridge.Enabled = true
		task := &model.Task{Name: "t", Prompt: "go", Worktree: t.TempDir()}
		cmd, _, err := BuildCmd(task, cfg, false)
		testutil.NoError(t, err)
		testutil.Equal(t, cmd.Args[2], "claude --dangerously-skip-permissions --permission-mode plan -- 'go'")
	})
}

func TestFamilyBridgeModCapability(t *testing.T) {
	for _, tt := range []struct {
		backend string
		want    bool
	}{
		{"claude", true}, {"codex", false}, {"pi", false}, {"opencode", false},
		{"antigravity", false}, {"cursor", false}, {"my-custom-agent", false},
	} {
		t.Run(tt.backend, func(t *testing.T) {
			var got bool
			for _, f := range families {
				if string(f.Kind) == tt.backend {
					got = f.BridgeMod
				}
			}
			testutil.Equal(t, got, tt.want)
		})
	}
}
