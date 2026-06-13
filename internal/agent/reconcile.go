package agent

import (
	"log/slog"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// ReconcileStaleSessions flips DB tasks stuck at InProgress to InReview.
//
// Called once at startup (daemon Serve, or in-process TUI fallback) after the
// runner is created but before any new sessions are started. The runner is
// empty at that point, so any InProgress row in the DB describes a session
// that died with the previous process — flip it to InReview so the user (TUI
// or PWA) can resume or discard it.
//
// InReview is the right state because we don't know whether the agent
// finished or crashed; letting the user decide via Resume is safer than
// silently marking Complete.
func ReconcileStaleSessions(database *db.DB) (int, error) {
	flipped, err := ReconcileStaleSessionsExcept(database, nil)
	return len(flipped), err
}

// ReconcileStaleSessionsExcept is the session-supervisor variant of
// ReconcileStaleSessions: it flips InProgress→InReview for every task EXCEPT
// those whose ID is in alive — the set the supervisor still reports running
// across a daemon bounce. Those tasks are RE-ATTACHED (their agents never died),
// so flipping them to InReview would be a false termination; they stay
// InProgress. It returns the IDs it actually flipped — the TRUE orphans (the
// supervisor lost them, e.g. it also restarted) — so the caller can post an
// ARGUS_BOUNCED signal to exactly those and not to the re-attached ones.
//
// A nil/empty alive set flips every InProgress task, making this exactly
// equivalent to the in-process ReconcileStaleSessions (which is implemented as
// a thin wrapper). InReview (never Complete) is still the right landing state
// for an orphan: an inferred absence has no observed exit, so the #707 rule
// "Complete only on an observed clean exit" holds.
func ReconcileStaleSessionsExcept(database *db.DB, alive map[string]bool) ([]string, error) {
	tasks, err := database.Tasks()
	if err != nil {
		return nil, err
	}
	var flipped []string
	for _, t := range tasks {
		if t.Status != model.StatusInProgress {
			continue
		}
		if alive[t.ID] {
			continue // re-attached live supervisor session — leave InProgress
		}
		t.SetStatus(model.StatusInReview)
		if uerr := database.Update(t); uerr != nil {
			slog.Warn("reconcile: update failed", "task", t.ID, "name", t.Name, "err", uerr)
			continue
		}
		slog.Info("reconcile: stale in_progress → in_review", "task", t.ID, "name", t.Name)
		flipped = append(flipped, t.ID)
	}
	return flipped, nil
}
