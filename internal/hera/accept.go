package hera

import (
	"fmt"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// acceptDefaultBody is the notification sent to an accepted role, per Aaron's
// framing of the protocol (add-hera-accept-lifecycle): the coordinator's
// accept tells the agent it may wind down if it has nothing else pending.
// This is a message only – AcceptRole never stops or restarts a session.
const acceptDefaultBody = "Your work has been accepted and your task is now marked complete. If you have no other tasks to complete, you're free to consider yourself done and wind down."

// AcceptTldr is the fixed tldr for an AcceptRole notification.
const AcceptTldr = "Your work has been accepted – you're marked complete"

// AcceptStore is the narrow DB surface AcceptRole needs. Satisfied by the
// real *db.DB.
type AcceptStore interface {
	HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error)
	Get(taskID string) (*model.Task, error)
	SetStatus(taskID string, status model.Status) error
}

// AcceptSender is the message-delivery seam AcceptRole uses to notify the
// accepted role – matches (*hera.Service).Send's signature so the real
// Service satisfies it directly; tests inject a fake.
type AcceptSender interface {
	Send(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*db.HeraMessage, error)
}

// AcceptRole marks the hera role's bound task complete – the coordinator's
// explicit "I accept this work" decision (add-hera-accept-lifecycle) – and
// notifies the role. It is the single shared primitive behind both the
// hera_accept MCP tool and the plan-DAG gater's auto-accept-on-materialize.
//
// It acts from ANY non-complete status: unlike RollHeraWorkerToReview, the
// coordinator's accept is authoritative regardless of whether the target
// already self-reported done. On an ALREADY-complete task it is a full
// no-op – no status write, no notification – so a blocker gated by
// multiple dependents is only ever notified once, by whichever dependent
// materializes first.
//
// fromRoleID identifies the sender for the notification (the accepting
// coordinator's own role); it is never validated against roleID here – the
// callers (hera_accept, the gater) each enforce their own targeting rules.
// message, when non-empty, is appended to the default acceptance body.
//
// Returns (true, nil) when it flipped the status (the notification may still
// have failed – see the returned error), (false, nil) on the already-complete
// no-op, or a non-nil error if resolving the target or the status write itself
// failed (in which case flipped is always false) – or if flipped but the
// notification failed (flipped is true, err is non-nil).
func AcceptRole(store AcceptStore, sender AcceptSender, fromRoleID, roleID int64, message string) (bool, error) {
	binding, err := store.HeraLiveBindingByRole(roleID)
	if err != nil {
		return false, fmt.Errorf("accept: resolve binding for role %d: %w", roleID, err)
	}
	taskID := binding.ArgusTaskID

	task, err := store.Get(taskID)
	if err != nil {
		return false, fmt.Errorf("accept: load task %s: %w", taskID, err)
	}
	if task == nil {
		return false, fmt.Errorf("accept: task %s not found", taskID)
	}
	if task.Status == model.StatusComplete {
		return false, nil
	}

	if err := store.SetStatus(taskID, model.StatusComplete); err != nil {
		return false, fmt.Errorf("accept: set status complete for task %s: %w", taskID, err)
	}

	if sender != nil {
		body := acceptDefaultBody
		if message != "" {
			body += "\n\n" + message
		}
		if _, sErr := sender.Send(fromRoleID, roleID, body, AcceptTldr, nil); sErr != nil {
			return true, fmt.Errorf("accept: notify role %d: %w", roleID, sErr)
		}
	}

	return true, nil
}
