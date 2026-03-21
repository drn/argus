package gitutil

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FetchGitStatus runs git commands in the given worktree directory.
// Intended to be called from a background goroutine.
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

// FetchFileDiff runs git diff for a specific file and returns raw output.
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

// ListRemoteBranches returns remote-tracking branch names (e.g. "origin/main")
// sorted with priority branches first. Returns nil if the path is not a git repo.
func ListRemoteBranches(repoDir string) []string {
	if repoDir == "" {
		return nil
	}
	out, err := runGit(repoDir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/")
	if err != nil {
		return nil
	}
	var branches []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, "/HEAD") {
			continue
		}
		branches = append(branches, line)
	}
	return sortBranchesWithPriority(branches)
}

// priorityBranches defines the preferred ordering for branch selection.
// upstream first for fork workflows where upstream is the canonical remote.
var priorityBranches = []string{
	"upstream/master",
	"origin/master",
	"upstream/main",
	"origin/main",
}

// sortBranchesWithPriority returns branches with priority entries first (in
// order), followed by the remaining branches sorted alphabetically.
func sortBranchesWithPriority(branches []string) []string {
	if len(branches) == 0 {
		return nil
	}
	prioritySet := make(map[string]bool, len(priorityBranches))
	for _, b := range priorityBranches {
		prioritySet[b] = true
	}

	var result []string
	for _, pb := range priorityBranches {
		for _, b := range branches {
			if b == pb {
				result = append(result, b)
				break
			}
		}
	}

	var rest []string
	for _, b := range branches {
		if !prioritySet[b] {
			rest = append(rest, b)
		}
	}
	sort.Strings(rest)
	return append(result, rest...)
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
