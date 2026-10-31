package agent

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/accountusage"
	"github.com/drn/argus/internal/backendkind"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// ErrAccountMoveBusy rejects moving a task whose agent is actively working,
// unless the caller asked to force it.
var ErrAccountMoveBusy = errors.New("agent is working; pass force to interrupt it")

// AccountMoveInput selects the task and the target account: a configured
// account, "default", or "auto". Force interrupts a busy session.
type AccountMoveInput struct {
	TaskID  string
	Account string
	Force   bool
	// Rows/Cols size the restarted PTY (0 → 24x80; the pane resizes it later).
	Rows, Cols uint16
}

// AccountMoveResult reports a completed move. Resumed is false when no
// transcript existed to carry over. Started is false when the task had no
// live session to restart in place; the caller then starts it through its own
// restart path.
type AccountMoveResult struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Resumed bool   `json:"resumed"`
	Started bool   `json:"started"`
}

// accountMoveRunner is the slice of a session runner a move needs: the live
// handle, and the in-place stop-and-relaunch the runner already uses for
// rerenders (it reserves a pending restart so exit handlers skip their
// terminal status flips, and carries the task's account to the supervisor).
type accountMoveRunner interface {
	Get(taskID string) SessionHandle
	KickRerender(task *model.Task, cfg config.Config, rows, cols uint16) error
}

// MoveTaskAccount moves a Claude task to another account without any model
// turn: it copies the task's newest transcript into the target account,
// switches and persists the task's account (clearing the auto-routed flag),
// and restarts a live session in place under the target with --resume. The
// task row, worktree, branch and hera bindings are untouched.
func MoveTaskAccount(database *db.DB, runner accountMoveRunner, in AccountMoveInput) (*model.Task, AccountMoveResult, error) {
	task, err := database.Get(in.TaskID)
	if err != nil {
		return nil, AccountMoveResult{}, err
	}
	cfg := database.Config()
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("%w: %w", ErrAccount, err)
	}
	if backendkind.Of(backend.Command) != backendkind.Claude {
		return nil, AccountMoveResult{}, fmt.Errorf("%w: only Claude tasks can move accounts (backend %q)", ErrAccount, task.Backend)
	}
	from := accountusage.NormalizeAccount(task.Account)
	to, err := resolveMoveTarget(cfg, backend.Command, from, in.Account)
	if err != nil {
		return nil, AccountMoveResult{}, err
	}

	var sess SessionHandle
	if runner != nil {
		if h := runner.Get(task.ID); h != nil && h.Alive() {
			sess = h
		}
	}
	if sess != nil && !sess.IsIdle() && !in.Force {
		return nil, AccountMoveResult{}, ErrAccountMoveBusy
	}

	RefreshResumeSessionID(database, task)
	srcDir, err := ClaudeConfigDirForTask(task, cfg)
	if err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("%w: source account %q: %w", ErrAccount, from, err)
	}
	moved := *task
	moved.Account = storedAccount(to)
	moved.AccountAuto = false
	if _, err := resolveSpawnClaudeConfigDir(&moved, cfg, true); err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("%w: %w", ErrAccount, err)
	}
	dstDir, err := ClaudeConfigDirForTask(&moved, cfg)
	if err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("%w: target account %q: %w", ErrAccount, to, err)
	}
	resumed, err := copyClaudeTranscript(srcDir, dstDir, task.Worktree, task.SessionID)
	if err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("copy transcript to account %q: %w", to, err)
	}

	task.Account, task.AccountAuto = moved.Account, false
	if err := database.Update(task); err != nil {
		return nil, AccountMoveResult{}, fmt.Errorf("persist account move: %w", err)
	}
	res := AccountMoveResult{From: from, To: to, Resumed: resumed}
	logAccountMove(task.ID, res, "persisted")

	if sess == nil {
		return task, res, nil
	}
	rows, cols := in.Rows, in.Cols
	if rows == 0 || cols == 0 {
		rows, cols = 24, 80
	}
	restart := *task
	if resumed {
		err = runner.KickRerender(&restart, cfg, rows, cols)
	} else if reviver, ok := runner.(interface {
		Revive(*model.Task, config.Config, uint16, uint16) error
	}); ok {
		err = reviver.Revive(&restart, cfg, rows, cols)
	} else {
		err = runner.KickRerender(&restart, cfg, rows, cols)
	}
	if errors.Is(err, ErrSessionNotFound) {
		// The session exited between the liveness check and the kick.
		logAccountMove(task.ID, res, "session gone before restart; caller starts it")
		return task, res, nil
	}
	if err != nil {
		uxlog.Log("[account] move task=%s %s→%s restart failed: %v", task.ID, from, to, err)
		slog.Warn("[account] move restart failed", "task", task.ID, "from", from, "to", to, "err", err)
		return task, res, fmt.Errorf("account moved to %q but restart failed: %w", to, err)
	}
	res.Started = true
	logAccountMove(task.ID, res, "restarted in place")
	return task, res, nil
}

// resolveMoveTarget validates the requested target and resolves "auto".
func resolveMoveTarget(cfg config.Config, backendCommand, from, requested string) (string, error) {
	target := strings.TrimSpace(requested)
	if target == "" {
		return "", fmt.Errorf("%w: account is required", ErrAccount)
	}
	if config.IsAutoAccount(target) {
		choice, ok := PickAccount(cfg, backendCommand, from)
		if !ok {
			return "", fmt.Errorf("%w: no other account can run this task", ErrAccount)
		}
		uxlog.Log("[account] move: auto picked %s", choice.String())
		return choice.Account, nil
	}
	if err := cfg.ValidateAccount(target); err != nil {
		return "", fmt.Errorf("%w: %w", ErrAccount, err)
	}
	if !cfg.AccountSupports(target, backendCommand) {
		return "", fmt.Errorf("%w: account %q does not support this task's backend", ErrAccount, target)
	}
	if accountusage.NormalizeAccount(target) == from {
		return "", fmt.Errorf("%w: task is already on account %q", ErrAccount, from)
	}
	return accountusage.NormalizeAccount(target), nil
}

// storedAccount maps an account name to its stored form ("" for default).
func storedAccount(name string) string {
	if name == config.DefaultAccountName {
		return ""
	}
	return name
}

func logAccountMove(taskID string, res AccountMoveResult, stage string) {
	uxlog.Log("[account] move task=%s %s→%s resumed=%t: %s", taskID, res.From, res.To, res.Resumed, stage)
	slog.Info("[account] move", "task", taskID, "from", res.From, "to", res.To, "resumed", res.Resumed, "stage", stage)
}

// copyClaudeTranscript copies <sid>.jsonl and its <sid>/ sidecar dir from the
// source config dir's project folder into the destination's, for whichever of
// the logical / symlink-resolved worktree paths holds the transcript. Reports
// false (no error) when there is no transcript to copy.
func copyClaudeTranscript(srcDir, dstDir, worktree, sid string) (bool, error) {
	if worktree == "" || sid == "" || filepath.Base(sid) != sid {
		return false, nil
	}
	paths := []string{worktree}
	if physical, err := filepath.EvalSymlinks(worktree); err == nil && physical != worktree {
		paths = append(paths, physical)
	}
	for _, path := range paths {
		srcProj, err := claudesession.ProjectDirIn(srcDir, path)
		if err != nil {
			return false, err
		}
		src := filepath.Join(srcProj, sid+".jsonl")
		if info, err := os.Stat(src); err != nil || !info.Mode().IsRegular() {
			continue
		}
		dstProj, err := claudesession.ProjectDirIn(dstDir, path)
		if err != nil {
			return false, err
		}
		if err := os.MkdirAll(dstProj, 0o700); err != nil {
			return false, err
		}
		if err := copyFile(src, filepath.Join(dstProj, sid+".jsonl")); err != nil {
			return false, err
		}
		if info, err := os.Stat(filepath.Join(srcProj, sid)); err == nil && info.IsDir() {
			if err := copyTree(filepath.Join(srcProj, sid), filepath.Join(dstProj, sid)); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	return false, nil
}

// copyFile writes src to dst through a temp file + rename, so a reader never
// sees a half-written transcript and an older copy is replaced atomically.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".argus-move-*")
	if err != nil {
		return err
	}
	discard := func(err error) error {
		_ = os.Remove(tmp.Name()) // best effort: a stray temp file is harmless
		return err
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return discard(err)
	}
	if err := tmp.Close(); err != nil {
		return discard(err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return discard(err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return discard(err)
	}
	return nil
}

// copyTree copies regular files under src into dst, overwriting; symlinks and
// other special files are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case d.Type().IsRegular():
			return copyFile(path, target)
		}
		return nil
	})
}
