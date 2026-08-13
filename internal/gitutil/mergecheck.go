package gitutil

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// IsAncestor reports whether commit is an ancestor of target via
// `git merge-base --is-ancestor`. A `--` separator guards commit/target
// against being parsed as a git option (e.g. a branch name starting with
// `-`) — both come from task/DB-sourced branch names, not a trusted literal.
//
// Exit code 0 means true, exit code 1 means false (an ordinary, expected
// outcome — not an error), and anything else (unresolvable ref, not a git
// repo, etc.) is returned as an error.
func IsAncestor(repoDir, commit, target string) (bool, error) {
	if repoDir == "" || commit == "" || target == "" {
		return false, fmt.Errorf("gitutil: IsAncestor requires non-empty repoDir, commit, and target")
	}
	_, err := runGit(repoDir, "merge-base", "--is-ancestor", "--", commit, target)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
}

// ResolveDefaultBranch resolves a project's default branch, preferring the
// given configured value when non-empty. When empty, it resolves the
// remote's HEAD branch (origin/HEAD, no network — reads the locally-cached
// symbolic ref) and falls back further to gitutil's existing
// priorityBranches order, then plain local "master"/"main".
//
// Returns the short branch name (e.g. "master", suitable for comparing
// against a GraphQL PR's baseRefName) and a ref usable directly with
// IsAncestor/merge-base (e.g. "origin/master" or "drn/master"). Performs no
// network operation — see the merge-safety classifier's design for why Tier
// A never fetches.
func ResolveDefaultBranch(repoDir, configured string) (short, ref string, err error) {
	if configured != "" {
		return shortBranchName(configured), configured, nil
	}
	if out, gitErr := runGit(repoDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); gitErr == nil {
		if r := strings.TrimSpace(out); r != "" {
			return shortBranchName(r), r, nil
		}
	}
	for _, b := range priorityBranches {
		if _, gitErr := runGit(repoDir, "rev-parse", "--verify", "--quiet", "--end-of-options", b); gitErr == nil {
			return shortBranchName(b), b, nil
		}
	}
	for _, b := range []string{"master", "main"} {
		if _, gitErr := runGit(repoDir, "rev-parse", "--verify", "--quiet", "--end-of-options", b); gitErr == nil {
			return b, b, nil
		}
	}
	return "", "", fmt.Errorf("gitutil: could not resolve a default branch for %q", repoDir)
}

// shortBranchName strips a remote prefix (e.g. "origin/master" -> "master");
// a ref with no "/" is returned unchanged.
func shortBranchName(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}
