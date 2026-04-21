package agent

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// CreateInput describes a new task to create and start.
//
// Only Name and Project are required. Backend defaults to cfg.Defaults.Backend.
// Rows/Cols default to 24/80 — agents auto-resize when the TUI attaches.
type CreateInput struct {
	Name       string
	Prompt     string
	Project    string
	Backend    string // optional; empty = cfg.Defaults.Backend
	PRURL      string // optional; set for review tasks
	TodoPath   string // optional; set when created from a vault .md file
	BaseBranch string // optional; overrides projCfg.Branch for this task

	Rows uint16 // initial PTY rows (0 → 24)
	Cols uint16 // initial PTY cols (0 → 80)

	// OnWorktreeCreated runs after the worktree exists but before the task
	// row is persisted. Use this for writing per-worktree context files
	// (e.g. fork-task prompts). If it returns an error, the worktree is
	// removed and CreateAndStart returns the error.
	//
	// Runs on the calling goroutine — typically a background goroutine
	// spawned by the TUI. MUST NOT call tview widget methods or mutate
	// any state that assumes the tview main goroutine; those are data
	// races without QueueUpdateDraw.
	OnWorktreeCreated func(wtPath string) error

	// BeforeStart runs immediately before runner.Start. Used by the TUI to
	// bump its startGen counter so in-flight tick reconciliations see a new
	// generation.
	//
	// Runs on the calling goroutine (background, not tview main). Same
	// tview-safety caveat as OnWorktreeCreated applies.
	BeforeStart func()

	// AfterStart runs immediately after runner.Start returns, whether or
	// not it succeeded — callers must not assume the session is live when
	// this fires. On failure the unwind chain runs next. Used by the TUI
	// to post-bump startGen so ticks that captured runningIDs mid-RPC
	// skip stale reconciliation.
	//
	// Runs on the calling goroutine (background, not tview main). Same
	// tview-safety caveat as OnWorktreeCreated applies.
	AfterStart func()
}

// CreateAndStart is the single entry point for fully-transactional task
// creation. It performs, in order: resolve project config, create worktree,
// run OnWorktreeCreated hook, persist task row, generate session ID, and
// start the agent session. Each side-effecting step registers a compensating
// cleanup that runs in LIFO order if any later step fails — so a failure
// leaves no orphan worktree, no orphan branch, and no orphan DB row behind.
//
// Callers that need to react to the slow worktree step (e.g. show a spinner)
// should invoke CreateAndStart from a goroutine; its steps are mutex-safe and
// do not require the tview main goroutine.
//
// The returned task has Status=InProgress and AgentPID populated. The session
// is live and ready to be attached to a UI pane.
func CreateAndStart(database *db.DB, runner SessionProvider, input CreateInput) (*model.Task, SessionHandle, error) {
	cfg := database.Config()

	projCfg, ok := cfg.Projects[input.Project]
	if !ok {
		return nil, nil, fmt.Errorf("project %q not found in config", input.Project)
	}
	if projCfg.Path == "" {
		return nil, nil, fmt.Errorf("project %q has no path configured", input.Project)
	}

	// Compensating-action stack: each successful side effect appends an undo
	// closure. unwind runs them LIFO. We run cleanups outside any critical
	// section — any cleanup failure is logged but cannot stop the chain.
	// The triggering label ("runner.Start", "OnWorktreeCreated", …) flows
	// into each cleanup via the unwindTrigger closure variable so per-step
	// error logs carry the trigger context.
	var cleanups []func(trigger string)
	var unwindTrigger string
	unwind := func(label string, cause error) {
		slog.Warn("CreateAndStart: unwinding", "trigger", label, "err", cause)
		unwindTrigger = label
		for i := len(cleanups) - 1; i >= 0; i-- {
			func(fn func(string)) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("CreateAndStart: unwind step panicked", "trigger", unwindTrigger, "recover", r)
					}
				}()
				fn(unwindTrigger)
			}(cleanups[i])
		}
	}

	// Step 1: create worktree. BaseBranch overrides projCfg.Branch when set —
	// the new-task form lets the user pick a custom start point per task.
	baseBranch := input.BaseBranch
	if baseBranch == "" {
		baseBranch = projCfg.Branch
	}
	wtPath, finalName, branchName, err := CreateWorktree(projCfg.Path, input.Project, input.Name, baseBranch)
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: %w", err)
	}
	repoPath := projCfg.Path
	cleanups = append(cleanups, func(trigger string) {
		slog.Info("CreateAndStart unwind: remove worktree", "trigger", trigger, "path", wtPath, "branch", branchName)
		RemoveWorktreeAndBranch(wtPath, branchName, repoPath)
	})

	// Step 2: optional per-worktree hook (e.g. fork context files).
	if input.OnWorktreeCreated != nil {
		if err := input.OnWorktreeCreated(wtPath); err != nil {
			unwind("OnWorktreeCreated", err)
			return nil, nil, fmt.Errorf("worktree hook: %w", err)
		}
	}

	// Step 3: build task and persist.
	backend := input.Backend
	if backend == "" {
		backend = cfg.Defaults.Backend
	}
	task := &model.Task{
		Name:     finalName,
		Status:   model.StatusPending,
		Project:  input.Project,
		Prompt:   input.Prompt,
		Backend:  backend,
		Worktree: wtPath,
		Branch:   branchName,
		PRURL:    input.PRURL,
		TodoPath: input.TodoPath,
	}
	// Persist sandbox state at creation time so the display reflects the
	// setting active when the task was launched, not the current setting.
	task.Sandboxed = IsTaskSandboxed(task, cfg)

	if err := database.Add(task); err != nil {
		unwind("db.Add", err)
		return nil, nil, fmt.Errorf("db add: %w", err)
	}
	// Capture task ID before registering the cleanup so it cannot drift.
	taskID := task.ID
	cleanups = append(cleanups, func(trigger string) {
		if dErr := database.Delete(taskID); dErr != nil {
			slog.Error("CreateAndStart unwind db.Delete failed", "trigger", trigger, "id", taskID, "err", dErr)
		}
	})

	// Step 4: generate session ID for Claude-style backends (Codex captures post-exit).
	if resolved, berr := ResolveBackend(task, cfg); berr == nil && !IsCodexBackend(resolved.Command) {
		task.SessionID = model.GenerateSessionID()
		if uErr := database.Update(task); uErr != nil {
			slog.Warn("CreateAndStart: persist session ID failed (continuing)", "id", taskID, "err", uErr)
		}
	}

	// Step 5: start session.
	rows := input.Rows
	if rows == 0 {
		rows = 24
	}
	cols := input.Cols
	if cols == 0 {
		cols = 80
	}

	if input.BeforeStart != nil {
		input.BeforeStart()
	}
	sess, err := runner.Start(task, cfg, rows, cols, false)
	if input.AfterStart != nil {
		input.AfterStart()
	}
	if err != nil {
		unwind("runner.Start", err)
		return nil, nil, fmt.Errorf("start session: %w", err)
	}

	// Step 6: flip to InProgress and record PID. Happens after AfterStart so
	// startGen is already post-bumped by the time any tick can observe the
	// status change.
	task.SetStatus(model.StatusInProgress)
	task.StartedAt = time.Now()
	task.AgentPID = sess.PID()
	if uErr := database.Update(task); uErr != nil {
		slog.Warn("CreateAndStart: persist InProgress failed (session is running)", "id", taskID, "err", uErr)
	}

	slog.Info("task created and started", "id", taskID, "name", task.Name, "project", input.Project, "pid", sess.PID())
	return task, sess, nil
}
