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
	tasks, err := database.Tasks()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, t := range tasks {
		if t.Status != model.StatusInProgress {
			continue
		}
		t.SetStatus(model.StatusInReview)
		if uerr := database.Update(t); uerr != nil {
			slog.Warn("reconcile: update failed", "task", t.ID, "name", t.Name, "err", uerr)
			continue
		}
		slog.Info("reconcile: stale in_progress → in_review", "task", t.ID, "name", t.Name)
		count++
	}
	return count, nil
}
