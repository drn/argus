package ui

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

// FetchGitStatus runs git commands in the given worktree directory.
// Intended to be called from a tea.Cmd (off the main goroutine).
func FetchGitStatus(taskID, worktree string) GitStatusRefreshMsg {
	msg := GitStatusRefreshMsg{TaskID: taskID}

	if worktree == "" {
		return msg
	}

	if out, err := runGit(worktree, "status", "--short"); err == nil {
		msg.Status = strings.TrimRight(out, "\n")
	}

	if out, err := runGit(worktree, "diff", "HEAD", "--stat"); err == nil {
		msg.Diff = strings.TrimRight(out, "\n")
	}

	if base := findMergeBase(worktree); base != "" {
		if out, err := runGit(worktree, "diff", "--stat", base+"..HEAD"); err == nil {
			msg.BranchDiff = strings.TrimRight(out, "\n")
		}
		if out, err := runGit(worktree, "diff", "--name-status", base+"..HEAD"); err == nil {
			msg.BranchFiles = strings.TrimRight(out, "\n")
		}
	}

	return msg
}

// findMergeBase finds the merge-base between HEAD and the upstream or default branch.
func findMergeBase(worktree string) string {
	if base, err := runGit(worktree, "merge-base", "HEAD", "HEAD@{upstream}"); err == nil {
		if b := strings.TrimSpace(base); b != "" {
			return b
		}
	}
	for _, branch := range []string{"master", "main"} {
		if base, err := runGit(worktree, "merge-base", "HEAD", branch); err == nil {
			if b := strings.TrimSpace(base); b != "" {
				return b
			}
		}
	}
	return ""
}

func runGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return out.String(), err
}
