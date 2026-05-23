package apiclient

import (
	"bytes"
	"context"
	"io"
)

// ProjectJSON mirrors api.projectJSON. Sandbox is left as raw map[string]any
// because the TUI's existing config.ProjectSandboxConfig already round-trips
// through this same JSON shape; an adapter in apistore handles the conversion.
type ProjectJSON struct {
	Name    string         `json:"name"`
	Path    string         `json:"path"`
	Branch  string         `json:"branch,omitempty"`
	Backend string         `json:"backend,omitempty"`
	Sandbox map[string]any `json:"sandbox,omitempty"`
}

// ListProjects returns just the project names (sorted).
func (c *Client) ListProjects(ctx context.Context) ([]string, error) {
	var resp struct {
		Projects []string `json:"projects"`
	}
	if err := c.doJSON(ctx, "GET", "/api/projects", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

// ListProjectsFull returns the projects' full configuration.
func (c *Client) ListProjectsFull(ctx context.Context) ([]ProjectJSON, error) {
	var resp struct {
		Projects []ProjectJSON `json:"projects"`
	}
	if err := c.doJSON(ctx, "GET", "/api/projects/full", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Projects, nil
}

// CreateProject saves a new project. Master-only.
func (c *Client) CreateProject(ctx context.Context, p ProjectJSON) error {
	return c.doJSON(ctx, "POST", "/api/projects", p, nil)
}

// UpdateProject overwrites the project keyed by name. Master-only.
func (c *Client) UpdateProject(ctx context.Context, name string, p ProjectJSON) error {
	return c.doJSON(ctx, "PUT", "/api/projects/"+name, p, nil)
}

// DeleteProject removes a project from config. Master-only.
func (c *Client) DeleteProject(ctx context.Context, name string) error {
	return c.doJSON(ctx, "DELETE", "/api/projects/"+name, nil, nil)
}

// BackendJSON mirrors api.backendJSON.
type BackendJSON struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	PromptFlag string `json:"prompt_flag,omitempty"`
}

// ListBackends returns the configured agent backends (sorted by name).
func (c *Client) ListBackends(ctx context.Context) ([]BackendJSON, error) {
	var resp struct {
		Backends []BackendJSON `json:"backends"`
	}
	if err := c.doJSON(ctx, "GET", "/api/backends", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Backends, nil
}

// CreateBackend saves a new backend definition. Master-only.
// Added in phase 2 (gap fill).
func (c *Client) CreateBackend(ctx context.Context, b BackendJSON) error {
	return c.doJSON(ctx, "POST", "/api/backends", b, nil)
}

// UpdateBackend overwrites the backend keyed by name. Master-only.
// Added in phase 2 (gap fill).
func (c *Client) UpdateBackend(ctx context.Context, name string, b BackendJSON) error {
	return c.doJSON(ctx, "PUT", "/api/backends/"+name, b, nil)
}

// DeleteBackend removes a backend from config. Master-only.
// Added in phase 2 (gap fill).
func (c *Client) DeleteBackend(ctx context.Context, name string) error {
	return c.doJSON(ctx, "DELETE", "/api/backends/"+name, nil, nil)
}

// bytesReader avoids importing bytes in every file that just needs a Reader
// from a []byte. Defined here because terminal.go is the only other caller.
func bytesReader(p []byte) io.Reader { return bytes.NewReader(p) }
