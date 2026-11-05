package apiclient

import (
	"context"
	"net/url"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/sandboxprop"
)

// ListSandboxProposals returns proposals, optionally filtered by status.
func (c *Client) ListSandboxProposals(ctx context.Context, status string) ([]*db.SandboxProposal, error) {
	path := "/api/sandbox/proposals"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var resp struct {
		Proposals []*db.SandboxProposal `json:"proposals"`
	}
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Proposals, nil
}

// ApproveSandboxProposal approves with the master token. scope may be empty
// (daemon picks project when set); paths nil approves as proposed.
func (c *Client) ApproveSandboxProposal(ctx context.Context, id, scope string, paths []string) (*sandboxprop.ApproveResult, error) {
	body := map[string]any{"scope": scope}
	if paths != nil {
		body["paths"] = paths
	}
	var out sandboxprop.ApproveResult
	if err := c.doJSON(ctx, "POST", "/api/sandbox/proposals/"+url.PathEscape(id)+"/approve", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RejectSandboxProposal rejects a pending proposal.
func (c *Client) RejectSandboxProposal(ctx context.Context, id string) error {
	return c.doJSON(ctx, "POST", "/api/sandbox/proposals/"+url.PathEscape(id)+"/reject", nil, nil)
}
