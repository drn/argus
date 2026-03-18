package agent

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// ResolveSandboxConfig returns the effective sandbox config for a task.
// Per-project settings are merged on top of the global config:
//   - project Enabled (non-nil) overrides the global Enabled flag
//   - project DenyRead paths are appended to the global list
//   - project ExtraWrite paths are appended to the global list
func ResolveSandboxConfig(task *model.Task, cfg config.Config) config.SandboxConfig {
	result := cfg.Sandbox
	if task.Project != "" {
		if proj, ok := cfg.Projects[task.Project]; ok {
			if proj.Sandbox.Enabled != nil {
				result.Enabled = *proj.Sandbox.Enabled
			}
			result.DenyRead = append(append([]string{}, result.DenyRead...), proj.Sandbox.DenyRead...)
			result.ExtraWrite = append(append([]string{}, result.ExtraWrite...), proj.Sandbox.ExtraWrite...)
		}
	}
	return result
}

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
// When sandbox is enabled and available, the command is wrapped with sandbox-exec.
// The returned cleanup function removes the sandbox config temp file (nil if no sandbox).
func BuildCmd(task *model.Task, cfg config.Config, resume bool) (*exec.Cmd, func(), error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, nil, err
	}

	cmdStr := backend.Command

	if resume {
		if backend.ResumeCommand != "" {
			// Codex-style: use dedicated resume command (replaces base command entirely).
			cmdStr = backend.ResumeCommand
		} else if task.SessionID != "" {
			// Claude-style: append --resume flag to base command.
			cmdStr += " --resume " + shellQuote(task.SessionID)
		}
	} else {
		// New session — only pin session ID for Claude-style backends (no ResumeCommand).
		if backend.ResumeCommand == "" && task.SessionID != "" {
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

	// Wrap with sandbox if enabled (effective config merges global + per-project overrides).
	var sandboxCleanup func()
	effectiveSandbox := ResolveSandboxConfig(task, cfg)
	if effectiveSandbox.Enabled && IsSandboxAvailable() && task.Worktree != "" {
		profilePath, params, cleanup, serr := GenerateSandboxConfig(task.Worktree, effectiveSandbox)
		if serr == nil {
			cmdStr = WrapWithSandbox(cmdStr, profilePath, params)
			sandboxCleanup = cleanup
		}
		// If sandbox config generation fails, fall through to unsandboxed
	}

	// Every task must have a worktree — never run in the project directory.
	if task.Worktree == "" {
		// sandboxCleanup is currently always nil here (sandbox block above
		// guards on task.Worktree != ""), but we clean up defensively in case
		// that guard is relaxed in the future.
		if sandboxCleanup != nil {
			sandboxCleanup()
		}
		return nil, nil, fmt.Errorf("task %q has no worktree set — refusing to start without worktree isolation", task.Name)
	}

	cmd := exec.Command("sh", "-c", cmdStr)
	cmd.Dir = task.Worktree

	return cmd, sandboxCleanup, nil
}

// shellQuote wraps a string in single quotes with proper escaping.
// Single quotes within the string are replaced with '\'' (end quote, escaped quote, start quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
