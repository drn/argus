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
func BuildCmd(task *model.Task, cfg config.Config, resume bool) (*exec.Cmd, error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, err
	}

	cmdStr := backend.Command

	// Inject the task name as the worktree name so worktrees match tasks (argus/<name>).
	if !resume && task.Name != "" {
		cmdStr = injectWorktreeName(cmdStr, "argus/"+task.Name)
	}

	if resume && task.SessionID != "" {
		// Resume an existing session — no prompt needed.
		// Strip --worktree/-w since the worktree already exists from the
		// original session; passing it again would create a second one.
		cmdStr = stripWorktreeFlag(cmdStr)
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

	cmd := exec.Command("sh", "-c", cmdStr)

	// For resume, the working directory MUST match where the session was
	// originally created. Sessions are project-scoped in Claude Code, so
	// --resume only finds sessions stored under the CWD's project hash.
	// Since sessions are created from the worktree (via --worktree flag),
	// we must resume from the worktree, not the main project directory.
	dir := ResolveDir(task, cfg)
	if resume && task.Worktree != "" {
		dir = task.Worktree
	} else if !resume && dir == "" && task.Worktree != "" {
		dir = task.Worktree
	}
	if dir != "" {
		cmd.Dir = dir
	}

	return cmd, nil
}

// stripWorktreeFlag removes --worktree / -w (and an optional value) from a
// command string. The flag accepts an optional name argument, e.g.
// "--worktree my-branch" or "-w my-branch". We remove both the flag and
// any non-flag token that follows it.
func stripWorktreeFlag(cmd string) string {
	parts := strings.Fields(cmd)
	var out []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "--worktree" || p == "-w" {
			// Skip the flag; also skip a following non-flag argument if present.
			if i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "-") {
				i++
			}
			continue
		}
		// Handle --worktree=value
		if strings.HasPrefix(p, "--worktree=") || strings.HasPrefix(p, "-w=") {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}

// injectWorktreeName finds the --worktree / -w flag in a command string
// and ensures it has the given name as its argument. If the flag already
// has a value, it is replaced. If the flag has no value, the name is inserted.
// If the flag is absent, the command is returned unchanged.
func injectWorktreeName(cmd, name string) string {
	parts := strings.Fields(cmd)
	var out []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if p == "--worktree" || p == "-w" {
			out = append(out, p, name)
			// Skip an existing non-flag argument if present.
			if i+1 < len(parts) && !strings.HasPrefix(parts[i+1], "-") {
				i++
			}
			continue
		}
		if strings.HasPrefix(p, "--worktree=") || strings.HasPrefix(p, "-w=") {
			out = append(out, "--worktree", name)
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, " ")
}

// shellQuote wraps a string in single quotes with proper escaping.
// Single quotes within the string are replaced with '\'' (end quote, escaped quote, start quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
