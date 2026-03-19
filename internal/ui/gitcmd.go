package ui

import "github.com/drn/argus/internal/gitutil"

// Re-export from gitutil so existing BT code compiles without import changes.

// FetchGitStatus runs git commands in the given worktree directory.
func FetchGitStatus(taskID, worktree string) gitutil.GitStatusRefreshMsg {
	return gitutil.FetchGitStatus(taskID, worktree)
}

// FetchFileDiff runs git diff for a specific file.
func FetchFileDiff(taskID, worktree, filePath string) gitutil.FileDiffMsg {
	return gitutil.FetchFileDiff(taskID, worktree, filePath)
}

// FetchDirFiles lists untracked/changed files inside a directory.
func FetchDirFiles(taskID, worktree, dirPath string) gitutil.DirFilesMsg {
	return gitutil.FetchDirFiles(taskID, worktree, dirPath)
}

// Type aliases for backward compatibility.
type GitStatusRefreshMsg = gitutil.GitStatusRefreshMsg
type FileDiffMsg = gitutil.FileDiffMsg
type DirFilesMsg = gitutil.DirFilesMsg
type ChangedFile = gitutil.ChangedFile
