package apiclient

import (
	"context"
	"strconv"
)

// MessageJSON mirrors model.TaskMessage with JSON-friendly types. The TUI
// store adapter converts to model.TaskMessage for the existing message UI.
type MessageJSON struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	InReplyTo string `json:"in_reply_to,omitempty"`
	CreatedAt string `json:"created_at"`
	AckedAt   string `json:"acked_at,omitempty"`
}

// InboxFilter mirrors the query params accepted by /api/tasks/{id}/inbox.
type InboxFilter struct {
	UnreadOnly bool
	Sender     string
	Since      string
	Limit      int
}

// InboxResp is the inbox response envelope.
type InboxResp struct {
	Messages    []MessageJSON `json:"messages"`
	UnreadCount int           `json:"unread_count"`
}

// ListInbox fetches the inbox for the bound task.
func (c *Client) ListInbox(ctx context.Context, id string, f InboxFilter) (*InboxResp, error) {
	limit := ""
	if f.Limit > 0 {
		limit = strconv.Itoa(f.Limit)
	}
	unread := "true"
	if !f.UnreadOnly {
		unread = "false"
	}
	q := query("unread_only", unread, "sender", f.Sender, "since", f.Since, "limit", limit)
	var resp InboxResp
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/inbox"+q, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SendMessageReq is the body shape for POST /api/tasks/{id}/messages.
type SendMessageReq struct {
	To        string `json:"to"`
	Body      string `json:"body"`
	Kind      string `json:"kind,omitempty"`
	InReplyTo string `json:"in_reply_to,omitempty"`
}

// SendMessageResp is the create envelope returned on success.
type SendMessageResp struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
}

// SendMessage stages a message from the bound task. Master-only.
func (c *Client) SendMessage(ctx context.Context, fromID string, req SendMessageReq) (*SendMessageResp, error) {
	var resp SendMessageResp
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+fromID+"/messages", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AckInbox marks the supplied message IDs read for the bound task.
func (c *Client) AckInbox(ctx context.Context, id string, ids []string) (int, error) {
	var resp struct {
		Acked int `json:"acked"`
	}
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+id+"/inbox/ack", map[string]any{"ids": ids}, &resp); err != nil {
		return 0, err
	}
	return resp.Acked, nil
}

