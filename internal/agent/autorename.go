package agent

import (
	"context"
	"errors"
	"log/slog"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/llm"
	"github.com/drn/argus/internal/uxlog"
)

// autoRenameFn is the LLM-backed name generator. Tests swap it via the same
// package so they don't need to install the `claude` CLI.
var autoRenameFn = llm.GenerateName

// runAutoRename asks Haiku for a better task name and writes it to the DB
// when the original auto-generated slug is still in place. It is fire-and-
// forget: any error keeps the regex slug. The race guard (re-fetching the
// task and comparing names) ensures we never clobber a manual rename that
// happened while the LLM call was in flight.
//
// Runs from a goroutine; takes ownership of its own ctx/cancel.
func runAutoRename(database *db.DB, taskID, originalName, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), llm.DefaultTimeout)
	defer cancel()

	name, err := autoRenameFn(ctx, prompt)
	if err != nil {
		if errors.Is(err, llm.ErrUnavailable) {
			uxlog.Log("[autoname] skipped (claude CLI unavailable) task=%s", taskID)
			return
		}
		slog.Info("autoname: rename failed (keeping original)", "id", taskID, "name", originalName, "err", err)
		uxlog.Log("[autoname] failed task=%s name=%q err=%v", taskID, originalName, err)
		return
	}
	if name == originalName {
		uxlog.Log("[autoname] no-op task=%s name=%q (model produced same slug)", taskID, originalName)
		return
	}

	cur, err := database.Get(taskID)
	if err != nil {
		uxlog.Log("[autoname] db.Get failed task=%s err=%v", taskID, err)
		return
	}
	if cur.Name != originalName {
		uxlog.Log("[autoname] skipped task=%s (renamed externally to %q while haiku in flight)", taskID, cur.Name)
		return
	}

	if err := database.Rename(taskID, name); err != nil {
		uxlog.Log("[autoname] db.Rename failed task=%s err=%v", taskID, err)
		return
	}
	uxlog.Log("[autoname] renamed task=%s %q → %q", taskID, originalName, name)
	slog.Info("autoname: renamed", "id", taskID, "from", originalName, "to", name)
}
