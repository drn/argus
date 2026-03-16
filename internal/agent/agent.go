package agent

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// ResolveBackend returns the backend config for a task.
// Priority: task.Backend > project.Backend > cfg.Defaults.Backend.
func ResolveBackend(task *model.Task, cfg config.Config) (config.Backend, error) {
	name := cfg.Defaults.Backend

	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok && proj.Backend != "" {
			name = proj.Backend
		}
	}

	if task.Backend != "" {
		name = task.Backend
	}

	if name == "" {
		return config.Backend{}, fmt.Errorf("no backend configured")
	}

	backend, ok := cfg.Backends[name]
	if !ok {
		return config.Backend{}, fmt.Errorf("backend %q not found in config", name)
	}

	return backend, nil
}

// ResolveDir returns the working directory for a task.
// Returns the project path if configured, otherwise empty string.
func ResolveDir(task *model.Task, cfg config.Config) string {
	if task.Project == "" {
		return ""
	}
	if proj, ok := cfg.Projects[task.Project]; ok {
		return proj.Path
	}
	return ""
}

// BuildCmd constructs the exec.Cmd for running an agent on a task.
// If the task has a SessionID, the command uses --resume to reconnect.
// If resume is false and SessionID is set, it uses --session-id for a new session with a known ID.
// When sandbox is enabled and available, the command is wrapped with srt.
// The returned cleanup function removes the sandbox config temp file (nil if no sandbox).
func BuildCmd(task *model.Task, cfg config.Config, resume bool) (*exec.Cmd, func(), error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, nil, err
	}

	cmdStr := backend.Command

	if resume && task.SessionID != "" {
		// Resume an existing session — no prompt needed.
		cmdStr += " --resume " + shellQuote(task.SessionID)
	} else {
		// New session — pin the session ID so we can resume later
		if task.SessionID != "" {
			cmdStr += " --session-id " + shellQuote(task.SessionID)
		}
		if task.Prompt != "" {
			if backend.PromptFlag != "" {
				cmdStr += " " + backend.PromptFlag + " " + shellQuote(task.Prompt)
			} else {
				cmdStr += " " + shellQuote(task.Prompt)
			}
		}
	}

	// Wrap with sandbox if enabled and available
	var sandboxCleanup func()
	if cfg.Sandbox.Enabled && IsSandboxAvailable() && task.Worktree != "" {
		settingsPath, cleanup, serr := GenerateSandboxConfig(task.Worktree, cfg)
		if serr == nil {
			cmdStr = WrapWithSandbox(cmdStr, settingsPath)
			sandboxCleanup = cleanup
		}
		// If sandbox config generation fails, fall through to unsandboxed
	}

	cmd := exec.Command("sh", "-c", cmdStr)

	// Use worktree as working directory. Every task must have a worktree
	// set during creation — there is no fallback to the project directory.
	if task.Worktree != "" {
		cmd.Dir = task.Worktree
	}

	return cmd, sandboxCleanup, nil
}

// shellQuote wraps a string in single quotes with proper escaping.
// Single quotes within the string are replaced with '\'' (end quote, escaped quote, start quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
