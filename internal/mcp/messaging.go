package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// MessageStore is the subset of *db.DB used by the messaging tools. Defined
// as an interface so tests can mock without spinning up SQLite.
type MessageStore interface {
	InsertMessage(m *model.TaskMessage) (*model.TaskMessage, error)
	Inbox(toID string, f db.InboxFilter) ([]*model.TaskMessage, error)
	AckMessages(toID string, ids []string) (int, error)
	WaitForReply(ctx context.Context, questionID, fromID string) (*model.TaskMessage, error)
	DeleteMessagesForTask(taskID string) (int, error)
}

// MessageNudger writes a notification line into a target task's PTY when a
// message arrives. Best-effort — the message is durable regardless. The MCP
// handler only calls Nudge when SetMessageManager wired a non-nil nudger.
//
// Returns an error so the handler can log nudge failures; failure does NOT
// invalidate the send (the message row is committed before the nudge).
type MessageNudger interface {
	Nudge(targetTaskID string, line string) error
}

// maxAckIDsPerCall caps how many message IDs a single task_message_ack tool
// invocation may flip. 500 lines up with MaxInboxLimit so an agent draining
// a full inbox can ack the whole batch in one call. Above 500 we'd start
// approaching SQLite's default 999-variable cap on IN-clause placeholders.
const maxAckIDsPerCall = 500

// maxAskTimeoutSeconds caps task_ask's blocking wait. Two minutes is the
// outer envelope: an HTTP call to the MCP server held open longer is more
// likely a stuck client than a useful wait. Callers wanting longer waits
// poll task_inbox themselves.
const maxAskTimeoutSeconds = 120

// nudgeLineFormat is the single-line notification written to a live target
// session's PTY. Plain ASCII so any agent's terminal renders it identically.
// Format: "\n[argus] message from <id> (kind=<k>) — call task_inbox\n".
//
// Kept intentionally short so it doesn't dominate the recipient's screen
// and clearly tagged "[argus]" so an agent that pattern-matches operator
// messages can route it.
//
// **Security contract: %s args MUST be sanitized inputs.** Currently
// caller.ID (digit-only from generateID, see internal/db/db.go) and
// msg.Kind (typed enum, validated by model.ValidMessageKind before insert).
// Both are byte-safe — no ANSI escape sequences can reach the PTY through
// either. **Never put user-controllable strings (Body, names, sender labels)
// into this format string** — that's how a malicious task would inject
// terminal control sequences into a peer's PTY. If generateID ever moves
// to UUID/slug, audit each new character class for ESC/CSI bytes first.
const nudgeLineFormat = "\n[argus] new message from task %s (kind=%s) — call task_inbox to read.\n"

var messagingToolDefs = []Tool{
	{
		Name: "task_message_send",
		Description: `Send a peer-to-peer message to another Argus task. Use this to share prompts, deliver intermediate output, or ask a question whose answer arrives asynchronously. Messages are durable — the recipient need not be live when the message lands.

The agent process must identify itself by passing either ` + "`id`" + ` (sub-tasks should use the ` + "`ARGUS_TASK_ID`" + ` env var) or ` + "`cwd`" + ` (Argus resolves to the task whose worktree the cwd lives under). Pass ` + "`to`" + ` as the recipient's task ID.

Kinds:
- ` + "`note`" + ` (default) — fire-and-forget; recipient may but need not respond.
- ` + "`question`" + ` — sender expects a reply. Receiver answers via task_message_send(kind=answer, in_reply_to=<this message ID>).
- ` + "`answer`" + ` — reply to a prior question. ` + "`in_reply_to`" + ` is required.

Caps: body ≤ 64 KiB. Each recipient holds ≤ 500 unread messages before sends reject (the recipient must ack to free capacity). Each sender is rate-limited to 50 sends/min. Self-messages rejected.

If the recipient has a live agent session, the daemon also writes a single notification line into their PTY (best-effort). Either way the message sits in the recipient's inbox until they call ` + "`task_inbox`" + `.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"to":          map[string]interface{}{"type": "string", "description": "Recipient task ID."},
				"body":        map[string]interface{}{"type": "string", "description": "Message body. Up to 64 KiB."},
				"kind":        map[string]interface{}{"type": "string", "description": "One of 'note' (default), 'question', 'answer'."},
				"in_reply_to": map[string]interface{}{"type": "string", "description": "Message ID being answered. Required when kind=answer."},
				"id":          map[string]interface{}{"type": "string", "description": "Caller's task ID. If omitted, cwd is used to resolve the caller."},
				"cwd":         map[string]interface{}{"type": "string", "description": "Working directory inside the caller's worktree. Used when id is omitted."},
			},
			"required": []string{"to", "body"},
		},
	},
	{
		Name: "task_inbox",
		Description: `Read messages addressed to the caller, oldest first. Does NOT auto-mark messages as read — pass the returned IDs to ` + "`task_message_ack`" + ` after processing.

Filters:
- ` + "`unread_only`" + ` (default true) — only messages whose ` + "`read_at`" + ` is empty.
- ` + "`sender`" + ` — restrict to messages sent by this task ID.
- ` + "`since`" + ` — RFC3339 timestamp; only messages with ` + "`created_at > since`" + `.
- ` + "`limit`" + ` (default 50, max 500).`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"unread_only": map[string]interface{}{"type": "boolean", "description": "Default true. False to include already-acked messages."},
				"sender":      map[string]interface{}{"type": "string", "description": "Filter to messages sent by this task ID."},
				"since":       map[string]interface{}{"type": "string", "description": "RFC3339 timestamp; only created_at > since."},
				"limit":       map[string]interface{}{"type": "integer", "description": "Max rows to return (default 50, max 500)."},
				"id":          map[string]interface{}{"type": "string", "description": "Caller's task ID."},
				"cwd":         map[string]interface{}{"type": "string", "description": "Working directory; used when id is omitted."},
			},
		},
	},
	{
		Name:        "task_message_ack",
		Description: `Mark messages as read. IDs not belonging to the caller are silently ignored (idempotent for partially-overlapping batches; avoids leaking message-existence info). Up to 500 IDs per call.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"message_ids": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Message IDs to mark read. Up to 500 per call.",
				},
				"id":  map[string]interface{}{"type": "string", "description": "Caller's task ID."},
				"cwd": map[string]interface{}{"type": "string", "description": "Working directory; used when id is omitted."},
			},
			"required": []string{"message_ids"},
		},
	},
	{
		Name: "task_ask",
		Description: `Convenience tool: send a question and (optionally) block until the recipient answers. Internally calls ` + "`task_message_send`" + ` with kind=question, then polls the message table for an answer.

` + "`timeout_seconds`" + ` controls blocking: 0 (default) returns immediately with the question ID so the caller can poll ` + "`task_inbox`" + ` themselves. Any value 1–120 blocks the MCP call for up to that many seconds. Hard cap is 120s — longer waits should be implemented client-side via polling.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"to":              map[string]interface{}{"type": "string", "description": "Recipient task ID."},
				"body":            map[string]interface{}{"type": "string", "description": "Question body. Up to 64 KiB."},
				"timeout_seconds": map[string]interface{}{"type": "integer", "description": "Block up to this many seconds for a reply. Default 0 (return immediately). Max 120."},
				"id":              map[string]interface{}{"type": "string", "description": "Caller's task ID."},
				"cwd":             map[string]interface{}{"type": "string", "description": "Working directory; used when id is omitted."},
			},
			"required": []string{"to", "body"},
		},
	},
}

// SetMessageManager wires inter-task messaging. The store is mandatory; the
// nudger is optional — when nil, sends still succeed but the target's PTY is
// not poked. Must be called before ListenAndServe.
func (s *Server) SetMessageManager(store MessageStore, nudger MessageNudger) {
	s.messages = store
	s.nudger = nudger
}

// messagingEnabled returns true when the message store has been wired AND
// task management is also wired (caller resolution requires it).
func (s *Server) messagingEnabled() bool {
	return s.messages != nil && s.taskMgmtEnabled()
}

func (s *Server) toolTaskMessageSend(id interface{}, args json.RawMessage) *Response {
	if !s.messagingEnabled() {
		return toolError(id, "messaging not configured")
	}
	var p struct {
		To        string `json:"to"`
		Body      string `json:"body"`
		Kind      string `json:"kind"`
		InReplyTo string `json:"in_reply_to"`
		ID        string `json:"id"`
		Cwd       string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if strings.TrimSpace(p.To) == "" {
		return toolError(id, "to is required")
	}
	if p.Body == "" {
		return toolError(id, "body is required")
	}

	caller, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Confirm the recipient exists before insert so we return a clean error
	// instead of letting a typo'd ID sit in the table forever. The DB layer
	// can't enforce this with a FK because tasks are soft-archivable.
	recipient, err := s.taskDB.Get(p.To)
	if err != nil || recipient == nil {
		return toolError(id, fmt.Sprintf("recipient task not found: %s", p.To))
	}

	kind := model.MessageKind(strings.TrimSpace(p.Kind))
	if kind == "" {
		kind = model.KindNote
	}

	msg, err := s.messages.InsertMessage(&model.TaskMessage{
		From:      caller.ID,
		To:        recipient.ID,
		Kind:      kind,
		Body:      p.Body,
		InReplyTo: strings.TrimSpace(p.InReplyTo),
	})
	if err != nil {
		// Translate sentinel errors to crisp tool-error strings without
		// leaking internal SQL state.
		switch {
		case errors.Is(err, db.ErrMessageBodyTooLarge):
			return toolError(id, fmt.Sprintf("body exceeds %d bytes", model.MaxMessageBodyBytes))
		case errors.Is(err, db.ErrMessageSelfSend):
			return toolError(id, "cannot send a message to self")
		case errors.Is(err, db.ErrMessageInboxFull):
			return toolError(id, fmt.Sprintf("recipient inbox is full (%d unread cap)", db.MaxUnreadPerRecipient))
		case errors.Is(err, db.ErrMessageRateLimited):
			return toolError(id, fmt.Sprintf("sender rate limit exceeded (%d/min)", db.MaxSendsPerMinute))
		default:
			log.Printf("[mcp] task_message_send failed: from=%s to=%s err=%v", caller.ID, recipient.ID, err)
			return toolError(id, fmt.Sprintf("send failed: %v", err))
		}
	}

	delivered := "queued"
	if s.nudger != nil {
		line := fmt.Sprintf(nudgeLineFormat, caller.ID, msg.Kind)
		if nudgeErr := s.nudger.Nudge(recipient.ID, line); nudgeErr == nil {
			delivered = "nudged"
		}
	}

	log.Printf("[mcp] task_message_send ok: from=%s to=%s kind=%s id=%s delivered=%s", caller.ID, recipient.ID, msg.Kind, msg.ID, delivered)
	return toolResult(id, fmt.Sprintf("Sent message %s to task %s (kind=%s, delivered=%s).", msg.ID, recipient.ID, msg.Kind, delivered))
}

func (s *Server) toolTaskInbox(id interface{}, args json.RawMessage) *Response {
	if !s.messagingEnabled() {
		return toolError(id, "messaging not configured")
	}
	var p struct {
		UnreadOnly *bool  `json:"unread_only"`
		Sender     string `json:"sender"`
		Since      string `json:"since"`
		Limit      int    `json:"limit"`
		ID         string `json:"id"`
		Cwd        string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	caller, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	// Default UnreadOnly to true when the caller omits it. A pointer
	// distinguishes "omitted" from "false".
	unread := true
	if p.UnreadOnly != nil {
		unread = *p.UnreadOnly
	}

	filter := db.InboxFilter{
		UnreadOnly: unread,
		Sender:     strings.TrimSpace(p.Sender),
		Limit:      p.Limit,
	}
	if since := strings.TrimSpace(p.Since); since != "" {
		t, perr := time.Parse(time.RFC3339Nano, since)
		if perr != nil {
			// Accept the more forgiving RFC3339 second precision too.
			t, perr = time.Parse(time.RFC3339, since)
			if perr != nil {
				return toolError(id, fmt.Sprintf("invalid since (must be RFC3339): %v", perr))
			}
		}
		filter.Since = t
	}

	msgs, err := s.messages.Inbox(caller.ID, filter)
	if err != nil {
		log.Printf("[mcp] task_inbox failed: id=%s err=%v", caller.ID, err)
		return toolError(id, fmt.Sprintf("inbox query failed: %v", err))
	}

	if len(msgs) == 0 {
		return toolResult(id, "Inbox empty.")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Inbox for task %s (%d message%s):\n", caller.ID, len(msgs), plural(len(msgs)))
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n--- %s ---\n", m.ID)
		fmt.Fprintf(&b, "from: %s\n", m.From)
		fmt.Fprintf(&b, "kind: %s\n", m.Kind)
		if m.InReplyTo != "" {
			fmt.Fprintf(&b, "in_reply_to: %s\n", m.InReplyTo)
		}
		fmt.Fprintf(&b, "created_at: %s\n", m.CreatedAt.Format(time.RFC3339))
		if !m.ReadAt.IsZero() {
			fmt.Fprintf(&b, "read_at: %s\n", m.ReadAt.Format(time.RFC3339))
		}
		fmt.Fprintf(&b, "body:\n%s\n", m.Body)
	}
	b.WriteString("\nCall task_message_ack with these IDs after processing.")
	return toolResult(id, b.String())
}

func (s *Server) toolTaskMessageAck(id interface{}, args json.RawMessage) *Response {
	if !s.messagingEnabled() {
		return toolError(id, "messaging not configured")
	}
	var p struct {
		MessageIDs []string `json:"message_ids"`
		ID         string   `json:"id"`
		Cwd        string   `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if len(p.MessageIDs) == 0 {
		return toolError(id, "message_ids is required (non-empty list)")
	}
	if len(p.MessageIDs) > maxAckIDsPerCall {
		return toolError(id, fmt.Sprintf("too many IDs (max %d per call)", maxAckIDsPerCall))
	}

	caller, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	n, err := s.messages.AckMessages(caller.ID, p.MessageIDs)
	if err != nil {
		log.Printf("[mcp] task_message_ack failed: id=%s err=%v", caller.ID, err)
		return toolError(id, fmt.Sprintf("ack failed: %v", err))
	}
	return toolResult(id, fmt.Sprintf("Acked %d of %d message ID%s.", n, len(p.MessageIDs), plural(len(p.MessageIDs))))
}

func (s *Server) toolTaskAsk(id interface{}, args json.RawMessage) *Response {
	if !s.messagingEnabled() {
		return toolError(id, "messaging not configured")
	}
	var p struct {
		To             string `json:"to"`
		Body           string `json:"body"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		ID             string `json:"id"`
		Cwd            string `json:"cwd"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck

	if strings.TrimSpace(p.To) == "" {
		return toolError(id, "to is required")
	}
	if p.Body == "" {
		return toolError(id, "body is required")
	}
	if p.TimeoutSeconds < 0 {
		return toolError(id, "timeout_seconds must be >= 0")
	}
	if p.TimeoutSeconds > maxAskTimeoutSeconds {
		return toolError(id, fmt.Sprintf("timeout_seconds exceeds %d-second cap", maxAskTimeoutSeconds))
	}

	caller, err := s.resolveTask(p.ID, p.Cwd)
	if err != nil {
		return toolError(id, err.Error())
	}

	recipient, err := s.taskDB.Get(p.To)
	if err != nil || recipient == nil {
		return toolError(id, fmt.Sprintf("recipient task not found: %s", p.To))
	}

	msg, err := s.messages.InsertMessage(&model.TaskMessage{
		From: caller.ID,
		To:   recipient.ID,
		Kind: model.KindQuestion,
		Body: p.Body,
	})
	if err != nil {
		switch {
		case errors.Is(err, db.ErrMessageBodyTooLarge):
			return toolError(id, fmt.Sprintf("body exceeds %d bytes", model.MaxMessageBodyBytes))
		case errors.Is(err, db.ErrMessageSelfSend):
			return toolError(id, "cannot send a message to self")
		case errors.Is(err, db.ErrMessageInboxFull):
			return toolError(id, fmt.Sprintf("recipient inbox is full (%d unread cap)", db.MaxUnreadPerRecipient))
		case errors.Is(err, db.ErrMessageRateLimited):
			return toolError(id, fmt.Sprintf("sender rate limit exceeded (%d/min)", db.MaxSendsPerMinute))
		default:
			log.Printf("[mcp] task_ask insert failed: from=%s to=%s err=%v", caller.ID, recipient.ID, err)
			return toolError(id, fmt.Sprintf("send failed: %v", err))
		}
	}

	if s.nudger != nil {
		line := fmt.Sprintf(nudgeLineFormat, caller.ID, model.KindQuestion)
		_ = s.nudger.Nudge(recipient.ID, line) //nolint:errcheck // best-effort; nudge failure does not invalidate the durable message
	}

	if p.TimeoutSeconds == 0 {
		// Non-blocking mode: return the question ID and let the caller poll
		// task_inbox themselves.
		return toolResult(id, fmt.Sprintf("Question sent: id=%s. Poll task_inbox for the answer (in_reply_to=%s).", msg.ID, msg.ID))
	}

	// Parent the wait on shutdownCtx so a daemon shutdown propagates
	// cancellation into WaitForReply (which selects on ctx.Done() each tick
	// and returns nil, nil promptly). This keeps `argus daemon stop` snappy
	// even if a task_ask is mid-wait — http.Server.Shutdown then sees the
	// handler return rather than blocking for the full timeout_seconds.
	ctx, cancel := context.WithTimeout(s.shutdownCtx, time.Duration(p.TimeoutSeconds)*time.Second)
	defer cancel()
	reply, err := s.messages.WaitForReply(ctx, msg.ID, recipient.ID)
	if err != nil {
		log.Printf("[mcp] task_ask wait failed: q=%s err=%v", msg.ID, err)
		return toolError(id, fmt.Sprintf("wait failed: %v", err))
	}
	if reply == nil {
		return toolResult(id, fmt.Sprintf("Question sent: id=%s. No reply within %ds — poll task_inbox later (in_reply_to=%s).", msg.ID, p.TimeoutSeconds, msg.ID))
	}
	return toolResult(id, fmt.Sprintf("Reply to %s from %s (id=%s):\n\n%s", msg.ID, recipient.ID, reply.ID, reply.Body))
}

// plural returns "" when n == 1 and "s" otherwise. Used inline by inbox/ack
// tool output ("Acked 3 message IDs" vs "Acked 1 message ID") so the
// agent-facing strings stay grammatical without the caller branching at
// every fmt site.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
