package daemon

import (
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// HeadlessCreateTask creates a task, its worktree, and starts an agent session
// without requiring a TUI. Used by both the vault watcher and the HTTP API.
//
// Delegates to agent.CreateAndStart, which is fully transactional: any failure
// during worktree creation, DB insertion, or session start triggers LIFO
// cleanup of the preceding steps — so no orphan worktree, branch, or task row
// is left behind. This is important for the vault watcher, which dedups by
// todo_path: a ghost task row would permanently block retries for that file.
//
// BeforeStart/AfterStart hooks are intentionally nil — those are for the TUI's
// startGen tick-reconciliation counter, which has no analogue in headless mode.
func HeadlessCreateTask(database *db.DB, runner agent.SessionProvider, name, prompt, project, todoPath string) (*model.Task, error) {
	task, _, err := agent.CreateAndStart(database, runner, agent.CreateInput{
		Name:     name,
		Prompt:   prompt,
		Project:  project,
		TodoPath: todoPath,
	})
	return task, err
}
