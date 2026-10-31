package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

type moveSession struct {
	fakeSession
	idle, alive bool
}

func (s *moveSession) IsIdle() bool { return s.idle }
func (s *moveSession) Alive() bool  { return s.alive }

// moveRunner records kick/revive restarts; sess nil means no live session.
type moveRunner struct {
	sess    *moveSession
	kicked  *model.Task
	revived *model.Task
	kickErr error
}

func (r *moveRunner) Get(string) SessionHandle {
	if r.sess == nil {
		return nil
	}
	return r.sess
}

func (r *moveRunner) KickRerender(task *model.Task, _ config.Config, _, _ uint16) error {
	if r.kickErr != nil {
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
	testutil.False(t, got.AccountAuto)
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
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}, kickErr: ErrSessionNotFound}

	_, res, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	testutil.False(t, res.Started)
}

func TestMoveTaskAccount_KickFailureReported(t *testing.T) {
	f := newMoveFixture(t, true)
	r := &moveRunner{sess: &moveSession{idle: true, alive: true}, kickErr: errors.New("boom")}

	_, _, err := MoveTaskAccount(f.d, r, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "boom")
	// The account change already persisted; the task stays on the target.
	got, _ := f.d.Get("mv")
	testutil.Equal(t, got.Account, "work")
}

func TestMoveTaskAccount_AutoPicksAnotherAccount(t *testing.T) {
	f := newMoveFixture(t, true)
	stubAccountUsage(t, map[string]accountusage.Usage{"default": {FiveHour: pct(100)}})
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

func TestMoveTaskAccount_ClearsAutoFlag(t *testing.T) {
	f := newMoveFixture(t, true)
	f.task.AccountAuto = true
	testutil.NoError(t, f.d.Update(f.task))
	_, _, err := MoveTaskAccount(f.d, &moveRunner{}, AccountMoveInput{TaskID: "mv", Account: "work"})
	testutil.NoError(t, err)
	got, _ := f.d.Get("mv")
	testutil.False(t, got.AccountAuto)
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
