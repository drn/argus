package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/usagebudget"
)

type moveSession struct {
	fakeSession
	idle, alive bool
}

func (s *moveSession) IsIdle() bool { return s.idle }
func (s *moveSession) Alive() bool  { return s.alive }

// moveRunner records kick/revive restarts; sess nil means no live session.
// dieOnKick marks the session dead when a kick fails (the exit race).
type moveRunner struct {
	sess       *moveSession
	kicked     *model.Task
	revived    *model.Task
	kickErr    error
	dieOnKick  bool
	pending    bool
	rows, cols uint16
}

func (r *moveRunner) HasPendingRestart(string) bool { return r.pending }

func (r *moveRunner) Get(string) SessionHandle {
	if r.sess == nil {
		return nil
	}
	return r.sess
}

func (r *moveRunner) KickRerender(task *model.Task, _ config.Config, rows, cols uint16) error {
	r.rows, r.cols = rows, cols
	if r.kickErr != nil {
		if r.dieOnKick {
			r.sess.alive = false
		}
		return r.kickErr
	}
	cp := *task
	r.kicked = &cp
	return nil
}

func (r *moveRunner) Revive(task *model.Task, _ config.Config, _, _ uint16) error {
	cp := *task
	r.revived = &cp
	return nil
}

type moveFixture struct {
	d        *db.DB
	task     *model.Task
	home     string
	worktree string
	workDir  string // the "work" account's claude_config_dir
}

// newMoveFixture builds a DB with a Claude-only "work" account, a Codex-only
// "cx" account, and a Claude task on the default account whose transcript
// (plus a sidecar dir) exists under ~/.claude.
func newMoveFixture(t *testing.T, withTranscript bool) *moveFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubAccountUsage(t, nil)
	d := accountFileDB(t, t.TempDir())
	cfg := d.Config()
	workDir, _, err := cfg.ClaudeConfigDir("work")
	testutil.NoError(t, err)

	wt := t.TempDir()
	task := &model.Task{ID: "mv", Name: "mover", Project: "proj", Backend: "test", Worktree: wt, SessionID: "sid-1", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))
	if withTranscript {
		writeTranscript(t, filepath.Join(home, ".claude"), wt, "sid-1", `{"type":"user","sessionId":"sid-1"}`+"\n")
	}
	return &moveFixture{d: d, task: task, home: home, worktree: wt, workDir: workDir}
}

func writeTranscript(t *testing.T, configDir, worktree, sid, body string) {
	t.Helper()
	proj, err := claudesession.ProjectDirIn(configDir, worktree)
	testutil.NoError(t, err)
	testutil.NoError(t, os.MkdirAll(filepath.Join(proj, sid, "subagents"), 0o700))
	testutil.NoError(t, os.WriteFile(filepath.Join(proj, sid+".jsonl"), []byte(body), 0o600))
	testutil.NoError(t, os.WriteFile(filepath.Join(proj, sid, "subagents", "a.jsonl"), []byte("sub\n"), 0o600))
}

func readTranscript(t *testing.T, configDir, worktree, sid string) (main, sub string) {
	t.Helper()
	proj, err := claudesession.ProjectDirIn(configDir, worktree)
	testutil.NoError(t, err)
	m, err := os.ReadFile(filepath.Join(proj, sid+".jsonl"))
	testutil.NoError(t, err)
	s, err := os.ReadFile(filepath.Join(proj, sid, "subagents", "a.jsonl"))
	testutil.NoError(t, err)
	return string(m), string(s)
}

func TestMoveTaskAccount_LiveIdleSessionKickedOnTarget(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}}

	task, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.Equal(t, res, AccountMoveResult{From: "default", To: "work", Resumed: true, Started: true})
	testutil.Equal(t, task.Account, "work")

	main, sub := readTranscript(t, f.workDir, f.worktree, "sid-1")
	testutil.Contains(t, main, `"sid-1"`)
	testutil.Equal(t, sub, "sub\n")
	// The source copy is kept.
	readTranscript(t, filepath.Join(f.home, ".claude"), f.worktree, "sid-1")

	got, err := f.d.Get("mv")
	testutil.NoError(t, err)
	testutil.Equal(t, got.Account, "work")
	testutil.False(t, got.AccountRouted)
	testutil.NotNil(t, r.kicked)
	testutil.Equal(t, r.kicked.Account, "work")
	testutil.Equal(t, r.kicked.SessionID, "sid-1")
	testutil.Nil(t, r.revived)
}

func TestMoveTaskAccount_BusySessionNeedsForce(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: false, alive: true}}

	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.ErrorIs(t, err, ErrAccountMoveBusy)
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "")
	testutil.Nil(t, r.kicked)

	_, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work", Force: true})
	testutil.NoError(t, err)
	testutil.True(t, res.Started)
	testutil.Equal(t, r.kicked.Account, "work")
}

func TestMoveTaskAccount_NoTranscriptRevivesFresh(t *testing.T) {
	f := newMoveFixture(t, false)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}}

	_, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.Equal(t, res, AccountMoveResult{From: "default", To: "work", Resumed: false, Started: true})
	testutil.Nil(t, r.kicked)
	testutil.NotNil(t, r.revived)
	testutil.Equal(t, r.revived.Account, "work")
}

func TestMoveTaskAccount_NoLiveSessionLeavesStartToCaller(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{}

	task, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.Equal(t, res, AccountMoveResult{From: "default", To: "work", Resumed: true, Started: false})
	testutil.Equal(t, task.Account, "work")
	testutil.Nil(t, r.kicked)
}

func TestMoveTaskAccount_KickRaceFallsBackToCaller(t *testing.T) {
	f := newMoveFixture(t, true)
	// The daemon client flattens ErrSessionNotFound to a plain string.
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}, kickErr: errors.New("session not found"), dieOnKick: true}

	task, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.False(t, res.Started)
	testutil.Equal(t, task.Account, "work")
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "work")
}

func TestMoveTaskAccount_KickFailureRollsBack(t *testing.T) {
	f := newMoveFixture(t, true)
	f.task.AccountRouted = true
	testutil.NoError(t, f.d.Update(f.task))
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}, kickErr: errors.New("kick already pending")}

	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "kick already pending")
	testutil.Contains(t, err.Error(), `left on "default"`)
	// The old session is still running on default, so the row says default.
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "")
	testutil.True(t, got.AccountRouted)
}

func TestMoveTaskAccount_PendingRestartRefused(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}, pending: true}
	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work", Force: true})
	testutil.ErrorIs(t, err, ErrAccountMoveBusy)
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "")
	testutil.Nil(t, r.kicked)
}

func TestMoveTaskAccount_ConcurrentMoveRefused(t *testing.T) {
	f := newMoveFixture(t, true)
	testutil.True(t, claimAccountMove("mv"))
	_, _, err := MoveTaskAccount(f.d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.ErrorIs(t, err, ErrAccountMoveBusy)
	releaseAccountMove("mv")
	_, _, err = MoveTaskAccount(f.d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
}

func TestMoveTaskAccount_RestartUsesSessionSize(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}}
	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.Equal(t, r.cols, uint16(80))
	testutil.Equal(t, r.rows, uint16(24))
	_, _, err = MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "default", Rows: 50, Cols: 200})
	testutil.NoError(t, err)
	testutil.Equal(t, r.cols, uint16(200))
}

func TestMoveTaskAccount_SameConfigDirRejected(t *testing.T) {
	f := newMoveFixture(t, true)
	t.Setenv("ARGUS_TEST_PINNED_CCD", filepath.Join(f.home, "pinned"))
	testutil.NoError(t, f.d.SetBackend("test", config.Backend{Command: "claude", EnvVars: map[string]string{"CLAUDE_CONFIG_DIR": "env:ARGUS_TEST_PINNED_CCD"}}))
	_, _, err := MoveTaskAccount(f.d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.ErrorIs(t, err, ErrAccount)
	testutil.Contains(t, err.Error(), "same Claude config dir")
}

func TestMoveTaskAccount_AutoPicksAnotherAccount(t *testing.T) {
	f := newMoveFixture(t, true)
	stubAccountUsage(t, map[string]usagebudget.Reading{"default": {Percentage: 100}})
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}}

	_, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "auto"})
	testutil.NoError(t, err)
	testutil.Equal(t, res.To, "work")
}

func TestMoveTaskAccount_MoveBackKeepsNewerHistory(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}}
	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)

	// Turns taken on "work" grow its copy; moving back overwrites the stale default copy.
	writeTranscript(t, f.workDir, f.worktree, "sid-1", `{"type":"user","sessionId":"sid-1"}`+"\n"+`{"type":"assistant","sessionId":"sid-1"}`+"\n")
	_, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "default"})
	testutil.NoError(t, err)
	testutil.Equal(t, res.From, "work")
	testutil.Equal(t, res.To, "default")
	main, _ := readTranscript(t, filepath.Join(f.home, ".claude"), f.worktree, "sid-1")
	testutil.Contains(t, main, "assistant")
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "")
}

func TestMoveTaskAccount_ClearsRoutedFlag(t *testing.T) {
	f := newMoveFixture(t, true)
	f.task.AccountRouted = true
	testutil.NoError(t, f.d.Update(f.task))
	_, _, err := MoveTaskAccount(f.d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	got, _ := f.d.Get("mv")
	testutil.False(t, got.AccountRouted)
}

func TestMoveTaskAccount_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*moveFixture)
		in      AccountMoveInput
		wantIs  error
		wantMsg string
	}{
		{"unknown task", nil, AccountMoveInput{TaskID: "nope", Account: "work"}, db.ErrTaskNotFound, ""},
		{"empty target", nil, AccountMoveInput{TaskID: "mv"}, ErrAccount, "account is required"},
		{"same account", nil, AccountMoveInput{TaskID: "mv", Account: "default"}, ErrAccount, "already on account"},
		{"unknown target", nil, AccountMoveInput{TaskID: "mv", Account: "ghost"}, ErrAccount, "unknown account"},
		{"target lacks the backend's tool", nil, AccountMoveInput{TaskID: "mv", Account: "cx"}, ErrAccount, "does not support"},
		{"non-Claude task", func(f *moveFixture) {
			f.task.Backend = "codex"
			testutil.NoError(t, f.d.Update(f.task))
		}, AccountMoveInput{TaskID: "mv", Account: "cx"}, ErrAccount, "only Claude tasks"},
		{"auto with no other account", func(f *moveFixture) {
			f.task.Account = "work"
			testutil.NoError(t, f.d.Update(f.task))
		}, AccountMoveInput{TaskID: "mv", Account: "auto"}, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newMoveFixture(t, true)
			if tc.setup != nil {
				tc.setup(f)
			}
			before, _ := f.d.Get("mv")
			r := &moveRunner{sess: &moveSession{idle: true, alive: true}}
			_, _, err := MoveTaskAccount(f.d, r, tc.in)
			if tc.wantIs == nil {
				// "auto" from "work" resolves to "default" here: not a rejection.
				testutil.NoError(t, err)
				return
			}
			testutil.ErrorIs(t, err, tc.wantIs)
			if tc.wantMsg != "" {
				testutil.Contains(t, err.Error(), tc.wantMsg)
			}
			after, _ := f.d.Get("mv")
			testutil.Equal(t, after.Account, before.Account)
			testutil.Nil(t, r.kicked)
			_, statErr := os.Stat(f.workDir)
			testutil.True(t, os.IsNotExist(statErr))
		})
	}
}

func TestMoveTaskAccount_AutoWithNoCandidate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stubAccountUsage(t, nil)
	d := createTestDB(t, t.TempDir())
	testutil.NoError(t, d.SetBackend("test", config.Backend{Command: "claude"}))
	testutil.NoError(t, d.Add(&model.Task{ID: "mv", Name: "m", Project: "proj", Backend: "test", Worktree: t.TempDir(), SessionID: "s"}))

	_, _, err := MoveTaskAccount(d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "auto"})
	testutil.ErrorIs(t, err, ErrAccount)
	testutil.Contains(t, err.Error(), "no other account")
}

func TestCopyClaudeTranscript_SymlinkedWorktree(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "wt")
	testutil.NoError(t, os.Symlink(real, link))
	physical, err := filepath.EvalSymlinks(link)
	testutil.NoError(t, err)
	src, dst := t.TempDir(), t.TempDir()
	writeTranscript(t, src, physical, "sid", "x\n")

	ok, err := copyClaudeTranscript(src, dst, link, "sid")
	testutil.NoError(t, err)
	testutil.True(t, ok)
	main, _ := readTranscript(t, dst, physical, "sid")
	testutil.Equal(t, main, "x\n")

	ok, err = copyClaudeTranscript(src, dst, link, "../escape")
	testutil.NoError(t, err)
	testutil.False(t, ok)
}

// stubAccountUsage installs a fixed per-account usage snapshot for the test.
func stubAccountUsage(t *testing.T, snap map[string]usagebudget.Reading) {
	t.Helper()
	t.Cleanup(SetAccountUsageSource(func(config.Config) map[string]usagebudget.Reading { return snap }))
}

func TestPickMoveTarget(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Accounts = map[string]config.Account{
		"work":     {ClaudeConfigDir: "/tmp/work"},
		"personal": {ClaudeConfigDir: "/tmp/personal"},
		"cx":       {CodexHome: "/tmp/cx"},
	}
	tests := []struct {
		name    string
		usage   map[string]usagebudget.Reading
		exclude string
		want    string
		wantOK  bool
	}{
		{"lowest usage wins", map[string]usagebudget.Reading{"default": {Percentage: 70}, "personal": {Percentage: 40}, "work": {Percentage: 10}}, "", "work", true},
		{"current account excluded", map[string]usagebudget.Reading{"default": {Percentage: 99}, "personal": {Percentage: 50}, "work": {Percentage: 60}}, "default", "personal", true},
		{"unknown counts as zero", map[string]usagebudget.Reading{"default": {Percentage: 5}, "personal": {Percentage: 30}}, "", "work", true},
		{"ties keep candidate order", nil, "", "default", true},
		{"ties after exclude", nil, "default", "personal", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, scores, ok := PickMoveTarget(cfg, tt.usage, "claude", tt.exclude)
			testutil.Equal(t, ok, tt.wantOK)
			testutil.Equal(t, got, tt.want)
			testutil.False(t, strings.Contains(scores, "cx="))
		})
	}

	only := config.DefaultConfig()
	_, _, ok := PickMoveTarget(only, nil, "claude", "default")
	testutil.False(t, ok)
}

func TestAccountUsage_DefaultReadsLocalCaches(t *testing.T) {
	t.Cleanup(SetAccountUsageSource(nil))
	t.Cleanup(usagebudget.SetBridgeReader(func(account string) (usagebudget.Reading, bool) {
		if account == "work" {
			return usagebudget.Reading{Percentage: 42}, true
		}
		return usagebudget.Reading{}, false
	}))
	cfg := config.DefaultConfig()
	cfg.Accounts = map[string]config.Account{"work": {ClaudeConfigDir: "/tmp/work"}, "cx": {CodexHome: "/tmp/cx"}}
	got := AccountUsage(cfg)
	testutil.Equal(t, got["work"].Percentage, 42.0)
	_, hasCX := got["cx"]
	testutil.False(t, hasCX)

	restore := SetAccountUsageSource(func(config.Config) map[string]usagebudget.Reading { return nil })
	testutil.Equal(t, len(AccountUsage(cfg)), 0)
	restore()
}
