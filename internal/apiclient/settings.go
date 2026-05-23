package apiclient

import (
	"context"
	"strconv"
)

// SandboxJSON mirrors api.sandboxJSON.
type SandboxJSON struct {
	Enabled          bool     `json:"enabled"`
	Available        bool     `json:"available"`
	DenyRead         []string `json:"deny_read"`
	ExtraWrite       []string `json:"extra_write"`
	AllowAppleEvents []string `json:"allow_apple_events"`
}

// KBJSON mirrors api.kbJSON.
type KBJSON struct {
	Enabled        bool   `json:"enabled"`
	MetisVaultPath string `json:"metis_vault_path"`
}

// APISettingsJSON mirrors api.apiSettings.
type APISettingsJSON struct {
	Enabled  bool `json:"enabled"`
	HTTPPort int  `json:"http_port"`
}

// DefaultsJSON mirrors api.defaultsJSON.
type DefaultsJSON struct {
	Backend string `json:"backend"`
}

// SettingsResp is the full /api/settings shape.
type SettingsResp struct {
	Sandbox  SandboxJSON     `json:"sandbox"`
	KB       KBJSON          `json:"kb"`
	API      APISettingsJSON `json:"api"`
	Defaults DefaultsJSON    `json:"defaults"`
}

// SandboxUpdate is the partial-update payload for the sandbox section.
type SandboxUpdate struct {
	Enabled          *bool     `json:"enabled,omitempty"`
	DenyRead         *[]string `json:"deny_read,omitempty"`
	ExtraWrite       *[]string `json:"extra_write,omitempty"`
	AllowAppleEvents *[]string `json:"allow_apple_events,omitempty"`
}

// KBUpdate is the partial-update payload for the KB section.
type KBUpdate struct {
	Enabled        *bool   `json:"enabled,omitempty"`
	MetisVaultPath *string `json:"metis_vault_path,omitempty"`
}

// APIUpdate is the partial-update payload for the api section.
type APIUpdate struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// DefaultsUpdate is the partial-update payload for the defaults section.
type DefaultsUpdate struct {
	Backend *string `json:"backend,omitempty"`
}

// SettingsUpdate is the request body for PUT /api/settings. Every section is
// a pointer so callers can update one section without round-tripping the rest.
type SettingsUpdate struct {
	Sandbox  *SandboxUpdate  `json:"sandbox,omitempty"`
	KB       *KBUpdate       `json:"kb,omitempty"`
	API      *APIUpdate      `json:"api,omitempty"`
	Defaults *DefaultsUpdate `json:"defaults,omitempty"`
}

// GetSettings returns the current settings snapshot.
func (c *Client) GetSettings(ctx context.Context) (*SettingsResp, error) {
	var resp SettingsResp
	if err := c.doJSON(ctx, "GET", "/api/settings", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateSettings applies a partial update. Master-only.
func (c *Client) UpdateSettings(ctx context.Context, req SettingsUpdate) (*SettingsResp, error) {
	var resp SettingsResp
	if err := c.doJSON(ctx, "PUT", "/api/settings", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SkillJSON mirrors api.skillJSON.
type SkillJSON struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ListSkills returns the skill set, optionally narrowed by project (which
// includes the project's .claude/skills/) and a substring filter.
func (c *Client) ListSkills(ctx context.Context, project, filter string) ([]SkillJSON, error) {
	var resp struct {
		Skills []SkillJSON `json:"skills"`
	}
	path := "/api/skills" + query("project", project, "filter", filter)
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Skills, nil
}

// StatusResp mirrors api.statusResponse.
type StatusResp struct {
	OK       bool `json:"ok"`
	Sessions struct {
		Running int `json:"running"`
		Idle    int `json:"idle"`
	} `json:"sessions"`
	Tasks struct {
		Pending    int `json:"pending"`
		InProgress int `json:"in_progress"`
		InReview   int `json:"in_review"`
		Complete   int `json:"complete"`
	} `json:"tasks"`
}

// Status returns the daemon's at-a-glance counts. Used by the TUI to confirm
// daemon health on startup and by the PWA's status indicator.
func (c *Client) Status(ctx context.Context) (*StatusResp, error) {
	var resp StatusResp
	if err := c.doJSON(ctx, "GET", "/api/status", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SourcePathResp is the {"path":"…"} envelope used by the self-update flow.
type SourcePathResp struct {
	Path string `json:"path"`
}

// GetSourcePath returns the configured source path used by /api/update.
func (c *Client) GetSourcePath(ctx context.Context) (string, error) {
	var resp SourcePathResp
	if err := c.doJSON(ctx, "GET", "/api/source-path", nil, &resp); err != nil {
		return "", err
	}
	return resp.Path, nil
}

// SetSourcePath persists the source path for self-update. Master-only.
func (c *Client) SetSourcePath(ctx context.Context, path string) error {
	return c.doJSON(ctx, "PUT", "/api/source-path", SourcePathResp{Path: path}, nil)
}

// UpdateSelfResp mirrors the {"output":"…", "ok":bool} response.
type UpdateSelfResp struct {
	OK     bool   `json:"ok"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// UpdateSelf shells out the source-path's build script and restarts the
// daemon. Master-only.
func (c *Client) UpdateSelf(ctx context.Context) (*UpdateSelfResp, error) {
	var resp UpdateSelfResp
	if err := c.doJSON(ctx, "POST", "/api/update", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetLog returns the tail bytes of one of the daemon's whitelisted log files
// ("ux" or "daemon"). bytes caps the response; pass 0 for the server default.
func (c *Client) GetLog(ctx context.Context, name string, bytes int) ([]byte, error) {
	q := ""
	if bytes > 0 {
		q = "?bytes=" + strconv.Itoa(bytes)
	}
	resp, err := c.do(ctx, "GET", "/api/logs/"+name+q, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// max 1 MiB per server cap; safe to read all.
	out := make([]byte, 0, 16*1024)
	buf := make([]byte, 8192)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	return out, nil
}

// TokenJSON mirrors api.tokenJSON.
type TokenJSON struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
}

// ListTokens returns the per-device token catalogue (without secrets).
// Master-only.
func (c *Client) ListTokens(ctx context.Context) ([]TokenJSON, error) {
	var resp struct {
		Tokens []TokenJSON `json:"tokens"`
	}
	if err := c.doJSON(ctx, "GET", "/api/tokens", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tokens, nil
}

// CreateTokenResp is the secret-revealing create envelope.
type CreateTokenResp struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Label string `json:"label"`
}

// CreateToken mints a new device token. Returned secret is only visible at
// this moment — server stores SHA-256 hash. Master-only.
func (c *Client) CreateToken(ctx context.Context, label string) (*CreateTokenResp, error) {
	var resp CreateTokenResp
	if err := c.doJSON(ctx, "POST", "/api/tokens", map[string]string{"label": label}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RevokeToken removes a per-device token. Master-only.
func (c *Client) RevokeToken(ctx context.Context, id string) error {
	return c.doJSON(ctx, "DELETE", "/api/tokens/"+id, nil, nil)
}

// ConfigJSON is a raw decoded copy of config.Config returned by /api/config.
// Kept as a map so the TUI store adapter can hand it back to config.Config
// via a single round-trip through json.Marshal/Unmarshal, avoiding a parallel
// type definition that would drift on every config schema change.
type ConfigJSON = map[string]any

// GetConfig returns the daemon's full config.Config snapshot. Master-only.
// Added in phase 2 (gap fill) so a remote TUI doesn't need a dozen
// specialised endpoints for projects/backends/keybindings/sandbox/etc.
func (c *Client) GetConfig(ctx context.Context) (ConfigJSON, error) {
	out := make(ConfigJSON)
	if err := c.doJSON(ctx, "GET", "/api/config", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SessionState mirrors {"running":[…], "idle":[…]} from /api/sessions/state.
type SessionState struct {
	Running []string `json:"running"`
	Idle    []string `json:"idle"`
}

// GetSessionState returns the runner's live in-memory state. Used by the
// TUI's session-aware status polling. Added in phase 2 (gap fill).
func (c *Client) GetSessionState(ctx context.Context) (*SessionState, error) {
	var resp SessionState
	if err := c.doJSON(ctx, "GET", "/api/sessions/state", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// HasPendingRestart reports whether the runner has a kick-restart queued
// for the task. Added in phase 2 (gap fill).
func (c *Client) HasPendingRestart(ctx context.Context, id string) (bool, error) {
	var resp struct {
		Pending bool `json:"pending"`
	}
	if err := c.doJSON(ctx, "GET", "/api/sessions/"+id+"/pending-restart", nil, &resp); err != nil {
		return false, err
	}
	return resp.Pending, nil
}
