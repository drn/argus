package apiclient

import "context"

// GitStatusJSON is a thin map view of gitutil.FetchGitStatus output. The TUI
// already renders the rich gitpanel from the gitutil package directly; here
// we just carry the JSON bytes back so an adapter can hand them to gitpanel.
type GitStatusJSON map[string]any

// GitStatus returns the worktree's git status for the bound task.
func (c *Client) GitStatus(ctx context.Context, id string) (GitStatusJSON, error) {
	out := make(GitStatusJSON)
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/git/status", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GitDiff returns the unified diff for path inside the bound task's
// worktree. path must be worktree-relative — absolute paths and ".." are
// rejected by the server.
func (c *Client) GitDiff(ctx context.Context, id, path string) (map[string]any, error) {
	out := make(map[string]any)
	q := query("path", path)
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/git/diff"+q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// FileTree returns the directory listing for dir inside the bound task's
// worktree. Empty dir lists the worktree root.
func (c *Client) FileTree(ctx context.Context, id, dir string) (map[string]any, error) {
	out := make(map[string]any)
	q := query("dir", dir)
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/files"+q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ClipboardEntry is the agent-staged clipboard snapshot for a task.
type ClipboardEntry struct {
	Text    string `json:"text"`
	Present bool   `json:"present"`
}

// GetClipboard returns the most recently staged clipboard entry for a task.
func (c *Client) GetClipboard(ctx context.Context, id string) (*ClipboardEntry, error) {
	var resp ClipboardEntry
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/clipboard", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetClipboard stages a clipboard entry for the bound task.
func (c *Client) SetClipboard(ctx context.Context, id, text string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/clipboard", map[string]string{"text": text}, nil)
}

// ClearClipboard removes the staged clipboard entry for the bound task.
func (c *Client) ClearClipboard(ctx context.Context, id string) error {
	return c.doJSON(ctx, "DELETE", "/api/tasks/"+id+"/clipboard", nil, nil)
}

// LinksResp is the {"links":[…]} envelope returned by /api/tasks/{id}/links.
// Each entry is whatever links.Link marshals to (url + label).
func (c *Client) GetLinks(ctx context.Context, id string) ([]map[string]any, error) {
	var resp struct {
		Links []map[string]any `json:"links"`
	}
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/links", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Links, nil
}
