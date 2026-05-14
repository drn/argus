package model

import (
	"errors"
	"time"
)

// MessageKind classifies a TaskMessage. Three values are recognized today:
//
//   - KindNote — fire-and-forget; the recipient may but need not respond.
//   - KindQuestion — sender expects a reply; the recipient should answer with
//     a KindAnswer message whose InReplyTo points at this message's ID.
//   - KindAnswer — reply to a prior question. InReplyTo MUST reference the
//     answered question's ID.
//
// The daemon doesn't enforce conversation semantics — Kind is documentation
// for the receiving agent, the PWA inbox view, and the task_ask polling loop
// that scopes its wait to (in_reply_to=<qid>, from=<recipient>).
type MessageKind string

const (
	KindNote     MessageKind = "note"
	KindQuestion MessageKind = "question"
	KindAnswer   MessageKind = "answer"
)

// ValidMessageKind reports whether s is one of the three recognized kinds.
// Empty string is treated as invalid here; callers default to KindNote before
// validating.
func ValidMessageKind(s MessageKind) bool {
	switch s {
	case KindNote, KindQuestion, KindAnswer:
		return true
	}
	return false
}

// MaxMessageBodyBytes caps a single message body. 64 KiB matches the
// task_set_result cap so an agent that produced one knows the other will
// accept the same payload. SQLite TEXT itself is unbounded; the cap exists
// to bound a misbehaving sender filling the table.
const MaxMessageBodyBytes = 64 * 1024

// TaskMessage is a single peer-to-peer message between two tasks. Stored in
// the task_messages table; one row per send. Read state is per-recipient
// (ReadAt), not per-conversation — answering a question does NOT auto-mark
// the question as read.
type TaskMessage struct {
	ID        string      `json:"id"`
	From      string      `json:"from"`
	To        string      `json:"to"`
	Kind      MessageKind `json:"kind"`
	Body      string      `json:"body"`
	InReplyTo string      `json:"in_reply_to,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	// `omitzero` requires Go 1.24+'s encoding/json (matches the module's
	// declared go 1.26 floor). Don't downgrade to `omitempty` — that's a
	// no-op on a non-pointer time.Time.
	ReadAt time.Time `json:"read_at,omitzero"`
}

// Validate returns nil if the message has the minimum fields needed to be
// inserted. The DB layer also enforces the body-size cap and the
// non-self-message invariant via dedicated sentinel errors, so this method
// is only the lightweight shape check.
func (m *TaskMessage) Validate() error {
	if m.From == "" {
		return errors.New("from is required")
	}
	if m.To == "" {
		return errors.New("to is required")
	}
	if m.Body == "" {
		return errors.New("body is required")
	}
	if !ValidMessageKind(m.Kind) {
		return errors.New("kind must be note, question, or answer")
	}
	if m.Kind == KindAnswer && m.InReplyTo == "" {
		return errors.New("kind=answer requires in_reply_to")
	}
	return nil
}
