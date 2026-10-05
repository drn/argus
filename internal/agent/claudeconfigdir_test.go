package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestClaudeConfigDirForTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	acct := filepath.Join(home, "acct-work")
	cfg := config.Config{Accounts: map[string]config.Account{"work": {ClaudeConfigDir: acct}}}

	t.Run("default account", func(t *testing.T) {
		dir, err := ClaudeConfigDirForTask(&model.Task{}, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, dir, filepath.Join(home, ".claude"))
	})
	t.Run("named account", func(t *testing.T) {
		dir, err := ClaudeConfigDirForTask(&model.Task{Account: "work"}, cfg)
		testutil.NoError(t, err)
		testutil.Equal(t, dir, acct)
	})
	t.Run("removed account fails loud", func(t *testing.T) {
		_, err := ClaudeConfigDirForTask(&model.Task{Account: "gone"}, cfg)
		testutil.Error(t, err)
	})
	t.Run("nil task", func(t *testing.T) {
		_, err := ClaudeConfigDirForTask(nil, cfg)
		testutil.Error(t, err)
	})
}

func TestCaptureClaudeSessionIDIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	acct := filepath.Join(home, "acct-work")
	wt := filepath.Join(home, "wt")
	projDir := filepath.Join(acct, "projects", claudesession.EncodeProjectDir(wt))
	testutil.NoError(t, os.MkdirAll(projDir, 0o755))
	id := "aaaaaaaa-bbbb-4ccc-9ddd-111111111111"
	testutil.NoError(t, os.WriteFile(filepath.Join(projDir, id+".jsonl"), []byte("{}\n"), 0o644))

	got, err := CaptureClaudeSessionIDIn(acct, wt)
	testutil.NoError(t, err)
	testutil.Equal(t, got, id)

	_, err = CaptureClaudeSessionID(wt)
	testutil.Error(t, err)
}
