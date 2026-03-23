package daemon

import (
	"fmt"
	"log"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeadlessCreateTask creates a task, its worktree, and starts an agent session
// without requiring a TUI. Used by both the vault watcher and the HTTP API.
//
// The project must exist in the config. If todoPath is non-empty, it is stored
// on the task for vault-task association. PTY dimensions default to 24x80
// (the agent resizes when a TUI attaches).
func HeadlessCreateTask(database *db.DB, runner *agent.Runner, name, prompt, project, todoPath string) (*model.Task, error) {
	cfg := database.Config()

	projCfg, ok := cfg.Projects[project]
	if !ok {
		return nil, fmt.Errorf("project %q not found in config", project)
	}
	if projCfg.Path == "" {
		return nil, fmt.Errorf("project %q has no path configured", project)
	}

	// Create worktree before persisting the task.
	wtPath, finalName, branchName, err := agent.CreateWorktree(projCfg.Path, project, name, projCfg.Branch)
	if err != nil {
		return nil, fmt.Errorf("worktree: %w", err)
	}

	task := &model.Task{
		Name:     finalName,
		Status:   model.StatusPending,
		Project:  project,
		Prompt:   prompt,
		Backend:  cfg.Defaults.Backend,
		Worktree: wtPath,
		Branch:   branchName,
		TodoPath: todoPath,
	}

	if err := database.Add(task); err != nil {
		return nil, fmt.Errorf("db add: %w", err)
	}

	// Generate session ID for Claude backends (Codex captures post-exit).
	resume := false
	backend, berr := agent.ResolveBackend(task, cfg)
	if berr == nil && !agent.IsCodexBackend(backend.Command) {
		task.SessionID = model.GenerateSessionID()
		database.Update(task) //nolint:errcheck
	}

	// Start session with default PTY dimensions.
	sess, err := runner.Start(task, cfg, 24, 80, resume)
	if err != nil {
		// Revert task to pending so it isn't left in a ghost state.
		task.SetStatus(model.StatusPending)
		task.SessionID = ""
		task.StartedAt = time.Time{}
		database.Update(task) //nolint:errcheck
		return nil, fmt.Errorf("start session: %w", err)
	}

	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	database.Update(task) //nolint:errcheck

	log.Printf("[headless] created task: id=%s name=%s project=%s pid=%d", task.ID, task.Name, project, sess.PID())
	return task, nil
}
