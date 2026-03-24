package tui2

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sanitize"
)

// forkContext holds extracted context from a source task.
type forkContext struct {
	SourceName   string
	SourcePrompt string
	SourceStatus string
	SourceBranch string
	RecentOutput string // ANSI-stripped
	GitDiff      string
}

// maxOutputBytes is the maximum bytes to read from the session log tail.
const maxOutputBytes = 32 * 1024

// maxDiffBytes caps the git diff size to avoid bloating the context.
const maxDiffBytes = 64 * 1024

// extractForkContext reads context from the source task's session log and worktree.
func extractForkContext(task *model.Task) *forkContext {
	ctx := &forkContext{
		SourceName:   task.Name,
		SourcePrompt: task.Prompt,
		SourceStatus: task.Status.String(),
		SourceBranch: task.Branch,
	}

	// Read recent agent output from session log.
	ctx.RecentOutput = readSessionLogTail(task.ID)

	// Read git diff from the source worktree.
	if task.Worktree != "" {
		ctx.GitDiff = readGitDiff(task.Worktree)
	}

	return ctx
}

// readSessionLogTail reads the last maxOutputBytes from a task's session log,
// strips ANSI escape sequences, and returns clean text.
func readSessionLogTail(taskID string) string {
	logPath := agent.SessionLogPath(taskID)
	f, err := os.Open(logPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ""
	}

	size := info.Size()
	offset := int64(0)
	if size > maxOutputBytes {
		offset = size - maxOutputBytes
	}

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return ""
		}
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	return sanitize.CleanPTYOutput(string(data))
}

// readGitDiff runs git diff HEAD in the worktree and returns the output.
// Validates the worktree path is under a known worktree root before executing.
func readGitDiff(worktree string) string {
	if !isWorktreeSubdir(worktree) {
		return ""
	}
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if len(out) > maxDiffBytes {
		return string(out[:maxDiffBytes]) + "\n\n... (diff truncated)"
	}
	return string(out)
}

// sanitizeForkOutput delegates to the shared sanitize package.
// Kept as a package-level function for test compatibility.
func sanitizeForkOutput(s string) string {
	return sanitize.CleanPTYOutput(s)
}

// cleanLongLine delegates to the shared sanitize package.
// Kept as a package-level function for test compatibility.
func cleanLongLine(line string) string {
	return sanitize.CleanLongLine(line)
}

// stripANSI delegates to the shared sanitize package.
// Kept as a package-level function for test compatibility.
func stripANSI(s string) string {
	return sanitize.StripANSI(s)
}

// writeForkContextFiles writes .context/ files into the destination worktree.
func writeForkContextFiles(destWorktree string, ctx *forkContext) error {
	dir := destWorktree + "/.context"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .context dir: %w", err)
	}

	// fork-source.md — metadata and original prompt.
	var sb strings.Builder
	sb.WriteString("# Fork Source\n\n")
	fmt.Fprintf(&sb, "- **Source task:** %s\n", ctx.SourceName)
	fmt.Fprintf(&sb, "- **Status:** %s\n", ctx.SourceStatus)
	if ctx.SourceBranch != "" {
		fmt.Fprintf(&sb, "- **Branch:** %s\n", ctx.SourceBranch)
	}
	sb.WriteString("\n## Original Prompt\n\n")
	sb.WriteString(ctx.SourcePrompt)
	sb.WriteString("\n")
	if err := os.WriteFile(dir+"/fork-source.md", []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write fork-source.md: %w", err)
	}

	// fork-output.md — recent agent output (skip if empty).
	if ctx.RecentOutput != "" {
		content := "# Agent Output (last ~32KB)\n\n```\n" + ctx.RecentOutput + "\n```\n"
		if err := os.WriteFile(dir+"/fork-output.md", []byte(content), 0o644); err != nil {
			return fmt.Errorf("write fork-output.md: %w", err)
		}
	}

	// fork-diff.patch — git diff (skip if empty).
	if ctx.GitDiff != "" {
		if err := os.WriteFile(dir+"/fork-diff.patch", []byte(ctx.GitDiff), 0o644); err != nil {
			return fmt.Errorf("write fork-diff.patch: %w", err)
		}
	}

	return nil
}

// buildForkPrompt creates the prompt for the forked task, referencing .context/ files.
// Only references files that were actually written based on the extracted context.
func buildForkPrompt(source *model.Task, ctx *forkContext) string {
	var sb strings.Builder
	sb.WriteString("Continue the work from a previous attempt. Context from the previous agent session is available in `.context/` files in the working directory:\n\n")
	sb.WriteString("- `.context/fork-source.md` — source task metadata and original prompt\n")
	if ctx.RecentOutput != "" {
		sb.WriteString("- `.context/fork-output.md` — recent agent output\n")
	}
	if ctx.GitDiff != "" {
		sb.WriteString("- `.context/fork-diff.patch` — code changes made so far\n")
	}
	sb.WriteString("\nRead these files first to understand what was done and what remains.\n\n")
	sb.WriteString("Original prompt:\n")
	sb.WriteString(source.Prompt)
	return sb.String()
}
