package gitutil

import "time"

// ChangedFile represents a file from git status output.
type ChangedFile struct {
	Status string // e.g. "M", "A", "D", "??"
	Path   string
	IsDir  bool // true if this entry is a directory (trailing / in git status)
}

// GitStatusRefreshMsg carries the result of a background git status check.
type GitStatusRefreshMsg struct {
	TaskID      string
	Status      string // git status --short output
	Diff        string // git diff --stat (unstaged + staged) output
	BranchDiff  string // git diff --stat against merge-base (committed changes)
	BranchFiles string // git diff --name-status against merge-base (for file list)
}

// FileDiffMsg carries the result of an async file diff fetch.
type FileDiffMsg struct {
	TaskID   string
	FilePath string
	Diff     string
}

// DirFilesMsg carries the result of listing files in a directory.
type DirFilesMsg struct {
	TaskID  string
	DirPath string
	Files   []ChangedFile
}

// GitRefreshInterval is how long between automatic git status refreshes.
const GitRefreshInterval = 3 * time.Second
