package hera

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

// DeliverBoardNotices drains the atomic board outbox on the existing notify
// tick. The store filters read, already-enqueued and unbound messages. A crash
// between resolution and this pass leaves the notice available for the next pass.
func DeliverBoardNotices(store interface {
	PendingHeraBoardNotices() ([]*db.HeraMessage, error)
}, svc *Service) {
	msgs, err := store.PendingHeraBoardNotices()
	if err != nil {
		uxlog.Log("[board] notice outbox failed: %v", err)
		slog.Warn("[board] notice outbox failed", "err", err)
		return
	}
	if len(msgs) > 0 {
		uxlog.Log("[board] delivering notices count=%d", len(msgs))
		slog.Info("[board] delivering notices", "count", len(msgs))
	}
	for _, msg := range msgs {
		svc.Deliver(msg)
	}
}

// BoardReader is also used by recovery paths that predate the board.
type BoardReader interface {
	ListHeraBoardItems(int64, bool) ([]*db.HeraBoardItem, error)
}

// BoardSummary restores the coordinator's maintenance context without marking read.
func BoardSummary(store any, role *db.HeraRole) string {
	if role.Kind != db.HeraKindCoordinator {
		return ""
	}
	r, ok := store.(BoardReader)
	if !ok {
		return ""
	}
	items, err := r.ListHeraBoardItems(role.ID, false)
	if err != nil {
		uxlog.Log("[board] recovery read failed role=%d: %v", role.ID, err)
		return "\nBoard unavailable; call hera_board_list to recover open items.\n"
	}
	var b strings.Builder
	b.WriteString("\nHuman board: put decisions and attention here; keep items current and resolve when settled.\n")
	for _, i := range items {
		fmt.Fprintf(&b, "- #%d [%s] %s\n", i.ID, i.Kind, i.Subject)
	}
	if len(items) == 0 {
		b.WriteString("(no open items)\n")
	}
	return b.String()
}
