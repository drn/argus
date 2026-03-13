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
func BuildCmd(task *model.Task, cfg config.Config) (*exec.Cmd, error) {
	backend, err := ResolveBackend(task, cfg)
	if err != nil {
		return nil, err
	}

	cmdStr := backend.Command
	if backend.PromptFlag != "" && task.Prompt != "" {
		cmdStr += " " + backend.PromptFlag + " " + shellQuote(task.Prompt)
	}

	cmd := exec.Command("sh", "-c", cmdStr)

	if dir := ResolveDir(task, cfg); dir != "" {
		cmd.Dir = dir
	}

	return cmd, nil
}

// shellQuote wraps a string in single quotes with proper escaping.
// Single quotes within the string are replaced with '\'' (end quote, escaped quote, start quote).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
