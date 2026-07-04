// Package hera provides the Milestone 2 delivery service for the native Hera
// integration: role-addressed message store + reliable pane delivery via the
// existing notify.Notifier. No new delivery engine is introduced; the Notifier
// is the ONLY idle-gate implementation in the codebase (locked decision #3 from
// context/plans/merge-hera-into-argus.md).
package hera

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/notify"
)

// Store is the narrow DB surface the Service needs. Satisfied by *db.DB.
type Store interface {
	SendHeraMessage(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*db.HeraMessage, error)
	HeraInbox(roleID int64) ([]*db.HeraMessage, error)
	MarkHeraMessagesRead(roleID int64, ids []int64) (int, error)
	HeraMessagesByIDs(ids []int64) ([]*db.HeraMessage, error)
	MarkHeraMessageDelivered(id int64, mode string) error
	HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error)
	HeraRole(id int64) (*db.HeraRole, error)
}

// Notifier is the delivery hook. Satisfied by *notify.Notifier.
// Keeping this narrow decouples the service from the full notify package and
// makes fake implementations trivial to write in tests.
type Notifier interface {
	ReliableNotify(taskID, text, deliveryID string, opts notify.NotifyOpts) func()
	Cancel(taskID, deliveryID string)
}

// Service wires the hera message store to the reliable pane-delivery layer.
// Construct via New. M3 (MCP tools) and M4 (daemon) inject the live wired
// instances; tests inject fakes.
type Service struct {
	store    Store
	notifier Notifier // may be nil — delivery is skipped (still persists)
}

// New creates a Service. notifier may be nil; in that case messages are
// persisted but delivery is always skipped with a log entry.
func New(store Store, notifier Notifier) *Service {
	return &Service{store: store, notifier: notifier}
}

// Send validates and persists a hera message, then attempts reliable pane
// delivery to the recipient's live argus task (if any). Returns the persisted
// message on success regardless of delivery outcome — a notifier failure is
// soft-fail and never rolls back the stored message.
//
// Delivery semantics:
//   - Recipient has a live binding → ReliableNotify enqueues a doorbell line;
//     MarkHeraMessageDelivered stamps idle_submit + delivered_at (best-effort).
//   - Recipient has NO live binding → message stored as "queued_no_binding";
//     logged; no error returned.
//   - Notifier is nil → message stored as pending; logged; no error returned.
//
// NOTE: The doorbell line includes the from-role name and tldr, which are
// user-controlled strings. Acceptable under the cooperative single-user local
// threat model (same as all Argus content), but do NOT add user-controllable
// strings to the task_messages nudge line (which has a stricter security
// contract — see internal/mcp/messaging.go).
func (s *Service) Send(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*db.HeraMessage, error) {
	msg, err := s.store.SendHeraMessage(fromRoleID, toRoleID, body, tldr, inReplyTo)
	if err != nil {
		return nil, err
	}

	if s.notifier == nil {
		slog.Info("[hera] delivery skipped: no notifier", "msg_id", msg.ID)
		return msg, nil
	}

	// Resolve recipient's live binding → argus task ID.
	binding, err := s.store.HeraLiveBindingByRole(toRoleID)
	if errors.Is(err, db.ErrHeraNotFound) {
		// Soft-fail: no live binding — message is durable, delivery deferred.
		slog.Info("[hera] delivery skipped: no live binding for recipient role",
			"msg_id", msg.ID, "to_role_id", toRoleID)
		if stampErr := s.store.MarkHeraMessageDelivered(msg.ID, db.HeraDeliveryQueuedNoBinding); stampErr != nil {
			slog.Warn("[hera] failed to stamp queued_no_binding", "msg_id", msg.ID, "err", stampErr)
		}
		return msg, nil
	}
	if err != nil {
		// Unexpected DB error — soft-fail, don't error the caller.
		slog.Warn("[hera] delivery skipped: binding lookup error",
			"msg_id", msg.ID, "to_role_id", toRoleID, "err", err)
		return msg, nil
	}

	// Resolve from-role name for the doorbell line.
	fromRoleName := fmt.Sprintf("role:%d", fromRoleID) // fallback if lookup fails
	if fromRole, roleErr := s.store.HeraRole(fromRoleID); roleErr == nil {
		fromRoleName = fromRole.Name
	} else {
		slog.Warn("[hera] from-role lookup failed, using fallback label",
			"from_role_id", fromRoleID, "err", roleErr)
	}

	doorbellLine := fmt.Sprintf("[hera from %s] msg #%d — %s", fromRoleName, msg.ID, msg.Tldr)
	deliveryID := heraDeliveryID(msg.ID)
	s.notifier.ReliableNotify(binding.ArgusTaskID, doorbellLine, deliveryID, notify.NotifyOpts{})

	slog.Info("[hera] delivery enqueued",
		"msg_id", msg.ID, "task_id", binding.ArgusTaskID, "delivery_id", deliveryID)

	// Stamp delivered_at best-effort — a failure here must not error the send.
	if stampErr := s.store.MarkHeraMessageDelivered(msg.ID, db.HeraDeliveryIdleSubmit); stampErr != nil {
		slog.Warn("[hera] failed to stamp idle_submit", "msg_id", msg.ID, "err", stampErr)
	}

	return msg, nil
}

// Inbox returns unread messages for roleID (oldest first) and cancels any
// pending notifier deliveries for the returned messages — since reading implies
// the recipient has seen the doorbell; re-delivery would be redundant.
// Cancellation is best-effort: if there is no live binding (and thus no task
// ID to cancel against), it is silently skipped.
func (s *Service) Inbox(roleID int64) ([]*db.HeraMessage, error) {
	msgs, err := s.store.HeraInbox(roleID)
	if err != nil {
		return nil, err
	}

	if s.notifier != nil && len(msgs) > 0 {
		s.cancelDeliveries(roleID, msgs)
	}
	return msgs, nil
}

// MarkRead marks the supplied message IDs as read for roleID (only marks rows
// addressed to that role), and cancels any pending notifier deliveries for
// those messages. Returns the count of rows actually flipped.
func (s *Service) MarkRead(roleID int64, ids []int64) (int, error) {
	n, err := s.store.MarkHeraMessagesRead(roleID, ids)
	if err != nil {
		return 0, err
	}

	if s.notifier != nil && n > 0 {
		// Fetch only the successfully-marked messages for cancel.
		msgs, fetchErr := s.store.HeraMessagesByIDs(ids)
		if fetchErr != nil {
			slog.Warn("[hera] mark-read: failed to fetch messages for cancel", "err", fetchErr)
		} else {
			s.cancelDeliveries(roleID, msgs)
		}
	}
	return n, nil
}

// GetByIDs loads messages by their IDs. Missing IDs are silently skipped.
func (s *Service) GetByIDs(ids []int64) ([]*db.HeraMessage, error) {
	return s.store.HeraMessagesByIDs(ids)
}

// DeliverToRole writes text directly into roleID's live-bound task PTY,
// reusing the SAME idle-gated single-writer ReliableNotify primitive Send uses
// for message delivery (add-model-menu-selection hera_retier) — no new write
// path. Returns db.ErrHeraNotFound if the role has no live binding. Unlike
// Send, a nil notifier is a hard error rather than a soft-fail: a retier
// request has no durable message row to fall back on, so silently doing
// nothing would be an undetectable no-op for the caller.
func (s *Service) DeliverToRole(roleID int64, text, deliveryID string) error {
	if s.notifier == nil {
		return fmt.Errorf("hera: no notifier configured, cannot deliver")
	}
	binding, err := s.store.HeraLiveBindingByRole(roleID)
	if err != nil {
		return err
	}
	s.notifier.ReliableNotify(binding.ArgusTaskID, text, deliveryID, notify.NotifyOpts{})
	return nil
}

// cancelDeliveries cancels pending notifier deliveries for msgs addressed to
// roleID. Resolves the role's live binding once; skips if none found.
// Granularity note: cancels are per-message (deliveryID = "hera:<msgID>"),
// not task-scoped — only the specific pending doorbell is cancelled, not every
// pending delivery to that task. This mirrors how task_message_ack cancels per
// message ID in the task_messages path (internal/mcp/messaging.go).
func (s *Service) cancelDeliveries(roleID int64, msgs []*db.HeraMessage) {
	binding, err := s.store.HeraLiveBindingByRole(roleID)
	if err != nil {
		// No live binding → nothing registered to cancel.
		return
	}
	for _, m := range msgs {
		if m.ToRoleID != roleID {
			continue // paranoia: skip messages not addressed to this role
		}
		s.notifier.Cancel(binding.ArgusTaskID, heraDeliveryID(m.ID))
	}
}

// heraDeliveryID produces the delivery ID for a hera message, scoped to avoid
// collision with task_messages delivery IDs (which are raw numeric strings from
// generateID). The "hera:" prefix keeps the namespaces distinct.
func heraDeliveryID(msgID int64) string {
	return fmt.Sprintf("hera:%d", msgID)
}
