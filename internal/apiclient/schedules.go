package apiclient

import "context"

// ScheduleJSON mirrors api.scheduleJSON. Times are RFC3339 strings (empty
// when the underlying value is zero).
type ScheduleJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Project    string `json:"project"`
	Prompt     string `json:"prompt"`
	Backend    string `json:"backend,omitempty"`
	Schedule   string `json:"schedule"`
	RunOnceAt  string `json:"run_once_at,omitempty"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at"`
	LastRunAt  string `json:"last_run_at,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	LastTaskID string `json:"last_task_id,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

// ScheduleReq mirrors api.scheduleRequest. Pointer fields allow partial
// updates on PUT — leave a field nil to keep the server's existing value.
// For create, pass non-nil pointers for Name/Project/Prompt/Schedule at
// minimum.
type ScheduleReq struct {
	Name      *string `json:"name,omitempty"`
	Project   *string `json:"project,omitempty"`
	Prompt    *string `json:"prompt,omitempty"`
	Backend   *string `json:"backend,omitempty"`
	Schedule  *string `json:"schedule,omitempty"`
	RunOnceAt *string `json:"run_once_at,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
}

// ListSchedules returns every persisted schedule. Master-only.
func (c *Client) ListSchedules(ctx context.Context) ([]ScheduleJSON, error) {
	var resp struct {
		Schedules []ScheduleJSON `json:"schedules"`
	}
	if err := c.doJSON(ctx, "GET", "/api/schedules", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Schedules, nil
}

// CreateSchedule persists a new schedule. Master-only.
func (c *Client) CreateSchedule(ctx context.Context, req ScheduleReq) (*ScheduleJSON, error) {
	var resp ScheduleJSON
	if err := c.doJSON(ctx, "POST", "/api/schedules", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateSchedule applies a partial update to a schedule. Master-only.
func (c *Client) UpdateSchedule(ctx context.Context, id string, req ScheduleReq) (*ScheduleJSON, error) {
	var resp ScheduleJSON
	if err := c.doJSON(ctx, "PUT", "/api/schedules/"+id, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteSchedule removes a schedule. Master-only.
func (c *Client) DeleteSchedule(ctx context.Context, id string) error {
	return c.doJSON(ctx, "DELETE", "/api/schedules/"+id, nil, nil)
}

// RunScheduleResp is the {"task_id":"…"} envelope returned by /run.
type RunScheduleResp struct {
	TaskID string `json:"task_id"`
}

// RunSchedule fires a schedule out-of-cycle. Master-only.
func (c *Client) RunSchedule(ctx context.Context, id string) (*RunScheduleResp, error) {
	var resp RunScheduleResp
	if err := c.doJSON(ctx, "POST", "/api/schedules/"+id+"/run", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
