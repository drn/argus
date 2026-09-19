package db

// Task-meta keys for cascade-nuke's operator-excluded stacked-branch
// bookkeeping (openspec fix-hera-nuke-cleanup, design.md D1's one exception
// to "re-derive everything from durable facts": the operator's explicit
// "don't delete this branch" choice has no other durable home, so it gets a
// minimal task_meta entry).
//
// Every other task_meta consumer stores one scalar value per (namespace,
// key) pair (e.g. hera.ready_to_close, or the "cleanup" namespace's
// safe/tier/reason merge-safety verdict cache in
// internal/api/cleanup_candidates.go). This one needs a SET of branch names
// per task instead, so it inverts that: the branch name is the row's KEY,
// and the value is a fixed sentinel — recording a branch is an upsert of its
// own row (naturally idempotent, per SetMeta's own ON CONFLICT upsert), and
// reading the set back is a plain ListMeta scan with no decode step. A
// dedicated namespace (rather than reusing the existing "cleanup" namespace)
// avoids a branch literally named "safe"/"tier"/"reason" colliding with the
// verdict cache's own keys.
const (
	// CleanupExcludedBranchNamespace is the task_meta namespace holding one
	// row per operator-excluded stacked branch for a cascade-nuked task.
	CleanupExcludedBranchNamespace = "cleanup.excluded_branches"
	// cleanupExcludedBranchValue is the sentinel value stamped on every row —
	// a row's presence under CleanupExcludedBranchNamespace is the signal;
	// the value itself carries no information.
	cleanupExcludedBranchValue = "true"
)

// ExcludeCleanupBranch records branchName as operator-excluded from
// stacked-branch auto-deletion for taskID, scoped to that task like every
// task_meta row. Idempotent: recording the same branch twice re-stamps
// updated_at but never duplicates the row.
func (d *DB) ExcludeCleanupBranch(taskID, branchName string) error {
	return d.SetMeta(taskID, CleanupExcludedBranchNamespace, branchName, cleanupExcludedBranchValue)
}

// ExcludedCleanupBranches returns every branch name the operator has
// excluded from stacked-branch auto-deletion for taskID. Never nil — a task
// with no exclusions recorded returns an empty slice.
func (d *DB) ExcludedCleanupBranches(taskID string) ([]string, error) {
	entries, err := d.ListMeta(taskID, CleanupExcludedBranchNamespace)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Key)
	}
	return out, nil
}
