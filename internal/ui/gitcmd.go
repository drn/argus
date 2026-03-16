package ui

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
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

// FileDiffMsg carries the result of an async file diff fetch.
type FileDiffMsg struct {
	TaskID   string
	FilePath string
	Diff     string
}

// FetchFileDiff runs git diff for a specific file and returns colorized output.
func FetchFileDiff(taskID, worktree, filePath string) FileDiffMsg {
	msg := FileDiffMsg{TaskID: taskID, FilePath: filePath}
	if worktree == "" || filePath == "" {
		return msg
	}

	// Try uncommitted diff first (staged + unstaged) — raw output for parsing
	if out, err := runGit(worktree, "diff", "HEAD", "--", filePath); err == nil && out != "" {
		msg.Diff = out
		return msg
	}

	// Fall back to branch diff (committed changes vs merge-base)
	if base := findMergeBase(worktree); base != "" {
		if out, err := runGit(worktree, "diff", base+"..HEAD", "--", filePath); err == nil {
			msg.Diff = out
		}
	}

	// For untracked files, show the file contents as an "added" diff
	if msg.Diff == "" {
		if out, err := runGit(worktree, "diff", "--no-index", "/dev/null", filePath); err == nil || out != "" {
			msg.Diff = out
		}
	}

	return msg
}

// DirFilesMsg carries the result of listing files in a directory.
type DirFilesMsg struct {
	TaskID  string
	DirPath string
	Files   []ChangedFile
}

// FetchDirFiles lists untracked/changed files inside a directory in the worktree.
// Used for expanding directory entries in the file explorer.
func FetchDirFiles(taskID, worktree, dirPath string) DirFilesMsg {
	msg := DirFilesMsg{TaskID: taskID, DirPath: dirPath}
	if worktree == "" || dirPath == "" {
		return msg
	}

	// Path traversal guard: ensure resolved path stays within worktree
	fullDir := filepath.Join(worktree, dirPath)
	cleanWorktree := filepath.Clean(worktree) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(fullDir)+string(filepath.Separator), cleanWorktree) {
		return msg
	}

	// Use git ls-files to list untracked + modified files (respects .gitignore)
	if out, err := runGit(worktree, "ls-files", "--others", "--modified", "--exclude-standard", "--", dirPath); err == nil && out != "" {
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			msg.Files = append(msg.Files, ChangedFile{
				Status: "A",
				Path:   line,
			})
		}
	}

	// Get actual status for each file to show correct indicators
	if len(msg.Files) > 0 {
		if statusOut, err := runGit(worktree, "status", "--short", "--", dirPath); err == nil && statusOut != "" {
			statusMap := make(map[string]string)
			for _, sline := range strings.Split(strings.TrimRight(statusOut, "\n"), "\n") {
				if len(sline) < 4 {
					continue
				}
				st := strings.TrimSpace(sline[:2])
				p := strings.TrimSpace(sline[3:])
				statusMap[p] = st
			}
			for i := range msg.Files {
				if st, ok := statusMap[msg.Files[i].Path]; ok {
					msg.Files[i].Status = st
				}
			}
		}
	}

	return msg
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
