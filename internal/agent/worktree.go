package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/inject"
	"github.com/drn/argus/internal/uxlog"
)

// sanitizeBranchName replaces the most common invalid git branch name characters.
// Covers spaces, control chars, and the characters forbidden by git-check-ref-format:
//   ~ ^ : ? * [ ] { } \   plus leading/trailing dots, consecutive dots, and @{.
var invalidBranchChars = regexp.MustCompile(`[[:cntrl:] ~^:?*\[\]{}\\]+`)

func sanitizeBranchName(name string) string {
	s := invalidBranchChars.ReplaceAllString(name, "-")
	s = strings.Trim(s, ".")             // cannot start or end with .
	s = strings.Trim(s, "/")             // cannot start or end with /
	s = strings.ReplaceAll(s, "..", "-")  // no consecutive dots
	s = strings.ReplaceAll(s, "//", "/")  // no consecutive slashes
	s = strings.ReplaceAll(s, "@{", "-")  // no @{
	s = strings.Trim(s, "-")
	if s == "" {
		s = "task" // fallback when name is entirely invalid characters
	}
	return s
}

// resolveStartPoint checks whether ref is a valid git ref in the given repo.
// If not, it tries origin/<ref> and upstream/<ref> as fallbacks.
// Returns the first valid ref found, or the original ref if none resolve.
func resolveStartPoint(repoDir, ref string) string {
	// HEAD is always valid.
	if ref == "HEAD" {
		return ref
	}
	// Check if the ref exists locally (local branch, tag, etc.).
	if isValidRef(repoDir, ref) {
		return ref
	}
	// Try remote-tracking branches.
	for _, remote := range []string{"upstream", "origin"} {
		candidate := remote + "/" + ref
		if isValidRef(repoDir, candidate) {
			return candidate
		}
	}
	return ref
}

func isValidRef(repoDir, ref string) bool {
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", ref)
	cmd.Dir = repoDir
	return cmd.Run() == nil
}

// WorktreeDir returns the deterministic worktree path for a task:
// ~/.argus/worktrees/<projectName>/<taskName>
func WorktreeDir(projectName, taskName string) string {
	return filepath.Join(db.DataDir(), "worktrees", projectName, taskName)
}

// CreateWorktree creates a git worktree at the deterministic path with branch
// argus/<taskName>. If the path conflicts with an existing worktree for a
// different task, appends -1, -2, etc. until a free slot is found. Returns
// the worktree path, the final task name (which may have a suffix), and the
// branch name (e.g. "argus/fix-bug").
func CreateWorktree(projectPath, projectName, taskName, baseBranch string) (wtPath, finalName, branchName string, err error) {
	if baseBranch == "" {
		baseBranch = "HEAD"
	}

	// Resolve baseBranch to a valid ref. If the local branch doesn't exist,
	// try remote-tracking branches (origin/<branch>, upstream/<branch>).
	baseBranch = resolveStartPoint(projectPath, baseBranch)

	// Prune stale worktree references. If a previous worktree directory was
	// deleted without `git worktree remove`, git still locks the branch to
	// the stale entry, causing `git worktree add` to fail.
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = projectPath
	_ = pruneCmd.Run() // best-effort; ignore errors

	// Sanitize the task name for use in branch names and directory paths.
	safeName := sanitizeBranchName(taskName)
	uxlog.Log("[worktree] CreateWorktree: project=%q task=%q safe=%q base=%q", projectName, taskName, safeName, baseBranch)

	// Try the base name first, then -1, -2, ... up to 99.
	candidate := safeName
	for i := 0; i <= 99; i++ {
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", safeName, i)
		}
		wtDir := WorktreeDir(projectName, candidate)

		// If worktree already exists at this path, skip to next suffix.
		if _, statErr := os.Stat(wtDir); statErr == nil {
			continue
		}

		// Ensure parent directory exists.
		if mkErr := os.MkdirAll(filepath.Dir(wtDir), 0o755); mkErr != nil {
			return "", "", "", fmt.Errorf("creating worktree parent dir: %w", mkErr)
		}

		branch := "argus/" + candidate
		cmd := exec.Command("git", "worktree", "add", "-b", branch, wtDir, baseBranch)
		cmd.Dir = projectPath
		if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
			uxlog.Log("[worktree] cmd1 failed: %v: %s", cmdErr, strings.TrimSpace(string(out)))

			// git worktree add can exit non-zero due to a post-checkout hook
			// failure even though the worktree was created successfully.
			// Check for a valid worktree (.git file inside) before trying the fallback.
			if isValidWorktreeDir(wtDir) {
				uxlog.Log("[worktree] cmd1 failed but worktree is valid (hook failure?), treating as success")
				inject.InjectWorktreeAll(wtDir)
				return wtDir, candidate, branch, nil
			}

			// If branch already exists, try without -b (attach to existing branch).
			cmd2 := exec.Command("git", "worktree", "add", wtDir, branch)
			cmd2.Dir = projectPath
			if out2, cmdErr2 := cmd2.CombinedOutput(); cmdErr2 != nil {
				uxlog.Log("[worktree] cmd2 failed: %v: %s", cmdErr2, strings.TrimSpace(string(out2)))

				// Check again for partial success from cmd2.
				if isValidWorktreeDir(wtDir) {
					uxlog.Log("[worktree] cmd2 failed but worktree is valid (hook failure?), treating as success")
					inject.InjectWorktreeAll(wtDir)
					return wtDir, candidate, branch, nil
				}
				return "", "", "", fmt.Errorf("git worktree add: %s: %s",
					cmdErr2, cleanGitOutput(out, out2))
			}
			uxlog.Log("[worktree] cmd2 succeeded (reused existing branch)")
		}

		// Inject MCP configs into the new worktree for both Claude and Codex.
		inject.InjectWorktreeAll(wtDir)

		uxlog.Log("[worktree] created: path=%q branch=%q", wtDir, branch)
		return wtDir, candidate, branch, nil
	}

	return "", "", "", fmt.Errorf("could not create worktree: too many name conflicts for %q", taskName)
}

// isValidWorktreeDir checks whether a directory looks like a valid git worktree
// (has a .git file inside). A bare directory without .git is likely a partial
// failure, not a successful worktree creation.
func isValidWorktreeDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// cleanGitOutput combines and cleans git command output for display in a
// single-line error message. Strips "Preparing worktree" boilerplate,
// extracts "fatal:" lines, replaces newlines with spaces.
func cleanGitOutput(outputs ...[]byte) string {
	var combined []byte
	for _, o := range outputs {
		combined = append(combined, o...)
	}
	s := strings.TrimSpace(string(combined))

	// Extract just the fatal error lines if present — they're the useful part.
	var fatals []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "fatal:") {
			fatals = append(fatals, line)
		}
	}
	if len(fatals) > 0 {
		return strings.Join(fatals, "; ")
	}

	// Fall back to the full output with newlines replaced.
	s = strings.ReplaceAll(s, "\n", " ")
	// Collapse multiple spaces.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
