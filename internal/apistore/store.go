// Package apistore implements internal/tui/store.Store on top of the REST
// API exposed by internal/api. It is the persistence layer used when the
// TUI runs in --remote mode against a daemon on another host (typically over
// Tailscale).
//
// Every method here proxies the equivalent *db.DB call to an apiclient
// request. The TUI is otherwise unaware which transport is in use — both
// *db.DB and *apistore.Store satisfy the same tui/store.Store interface, so
// callers stay identical.
//
// Concurrency: methods are safe for concurrent use because the underlying
// apiclient.Client is. They do block on HTTP RTT, however — callers in the
// tview main goroutine must dispatch via QueueUpdateDraw, the same pattern
// the daemon-client RemoteSession demands.
package apistore

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/store"
)

// Compile-time assertion: Store implements tui/store.Store.
var _ store.Store = (*Store)(nil)

// Store is the HTTP-backed implementation of tui/store.Store. It holds an
// apiclient.Client and caches the most recent config snapshot — Config() is
// called on every UI tick and synchronously refreshing it would burn one
// HTTP request per tick.
type Store struct {
	c *apiclient.Client

	// cachedConfig stores the most recently fetched config.Config snapshot
	// so Config() can return it without round-tripping. Refreshed by
	// RefreshConfig (callers may invoke this on a timer).
	cachedConfig config.Config
}

// New returns a Store wrapping c. The first Config() call returns the zero
// value of config.Config; call RefreshConfig before mounting the store on
// the App, or expect a brief startup gap until the first refresh tick.
func New(c *apiclient.Client) *Store {
	return &Store{c: c}
}

// RefreshConfig fetches /api/config and caches the result for subsequent
// Config() calls. The TUI store-adapter calls this on a background tick.
// Returns the new snapshot for callers that want to observe immediately.
func (s *Store) RefreshConfig(ctx context.Context) (config.Config, error) {
	raw, err := s.c.GetConfig(ctx)
	if err != nil {
		return s.cachedConfig, err
	}
	// Round-trip through json.Marshal/Unmarshal so the typed config.Config
	// (with its nested maps, pointers, and sandbox enabled-tristate) is
	// populated from the wire's untyped map.
	buf, err := json.Marshal(raw)
	if err != nil {
		return s.cachedConfig, err
	}
	var cfg config.Config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return s.cachedConfig, err
	}
	s.cachedConfig = cfg
	return cfg, nil
}

// Config returns the cached snapshot. Callers depending on a fresh value
// must call RefreshConfig first.
func (s *Store) Config() config.Config { return s.cachedConfig }

// Tasks returns every task as a full model.Task via /api/tasks-raw.
func (s *Store) Tasks() ([]*model.Task, error) {
	return s.c.ListTasksRaw(context.Background())
}

// Get returns a single task by ID. Translates 404 → db.ErrTaskNotFound so
// callers using errors.Is on the local sentinel continue to work.
func (s *Store) Get(id string) (*model.Task, error) {
	t, err := s.c.GetTaskRaw(context.Background(), id)
	if err != nil {
		if apiclient.IsNotFound(err) {
			return nil, db.ErrTaskNotFound
		}
		return nil, err
	}
	return t, nil
}

// Add inserts a task via POST /api/tasks-raw.
func (s *Store) Add(t *model.Task) error {
	return s.c.AddTaskRaw(context.Background(), t)
}

// Update writes the task via PUT /api/tasks/{id}/raw.
func (s *Store) Update(t *model.Task) error {
	return s.c.UpdateTaskRaw(context.Background(), t)
}

// Delete removes a task via DELETE /api/tasks/{id} (the standard endpoint
// also tears down the worktree / branch / log on the server).
func (s *Store) Delete(id string) error {
	return s.c.DeleteTask(context.Background(), id)
}

// Rename calls /api/tasks/{id}/rename.
func (s *Store) Rename(id, name string) error {
	return s.c.RenameTask(context.Background(), id, name)
}

// Projects fetches /api/projects/full and converts the wire ProjectJSON
// shape back to a config.Project map keyed by name.
func (s *Store) Projects() (map[string]config.Project, error) {
	projs, err := s.c.ListProjectsFull(context.Background())
	if err != nil {
		return nil, err
	}
	out := make(map[string]config.Project, len(projs))
	for _, p := range projs {
		out[p.Name] = projectFromAPI(p)
	}
	return out, nil
}

// SetProject upserts a project via POST or PUT depending on whether the
// project already exists in cache. Best-effort: tries POST first; on 409
// (already exists) falls back to PUT.
func (s *Store) SetProject(name string, p config.Project) error {
	body := projectToAPI(name, p)
	err := s.c.CreateProject(context.Background(), body)
	if err == nil {
		return nil
	}
	// On any error (typically 409 conflict for an existing project), fall
	// back to PUT — the daemon-side endpoint upserts so both create and
	// update land at the same place anyway.
	return s.c.UpdateProject(context.Background(), name, body)
}

// DeleteProject calls /api/projects/{name}.
func (s *Store) DeleteProject(name string) error {
	return s.c.DeleteProject(context.Background(), name)
}

// AddSchedule posts to /api/schedules and stamps the returned ID/CreatedAt
// back onto the caller's struct so the existing TUI flow (which expects
// AddSchedule to mutate s.ID) keeps working unchanged.
func (s *Store) AddSchedule(sch *model.ScheduledTask) error {
	req := scheduleReqFromModel(sch)
	resp, err := s.c.CreateSchedule(context.Background(), req)
	if err != nil {
		return err
	}
	sch.ID = resp.ID
	if resp.CreatedAt != "" {
		// Best-effort RFC3339 parse; the field is optional in the wire shape.
		if t, perr := timeParse(resp.CreatedAt); perr == nil {
			sch.CreatedAt = t
		}
	}
	return nil
}

// UpdateSchedule PUTs partial updates to /api/schedules/{id}.
func (s *Store) UpdateSchedule(sch *model.ScheduledTask) error {
	req := scheduleReqFromModel(sch)
	_, err := s.c.UpdateSchedule(context.Background(), sch.ID, req)
	return err
}

// DeleteSchedule calls /api/schedules/{id}.
func (s *Store) DeleteSchedule(id string) error {
	return s.c.DeleteSchedule(context.Background(), id)
}

// GetSchedule fetches the full model.ScheduledTask via /api/schedules/{id}/raw.
// Translates 404 → db.ErrScheduleNotFound to match the local-DB contract.
func (s *Store) GetSchedule(id string) (*model.ScheduledTask, error) {
	sch, err := s.c.GetScheduleRaw(context.Background(), id)
	if err != nil {
		if apiclient.IsNotFound(err) {
			return nil, db.ErrScheduleNotFound
		}
		return nil, err
	}
	return sch, nil
}

// DeleteMessagesForTask isn't exposed as a dedicated endpoint today. Archive
// already runs the same cleanup on the server side, so the TUI's archive
// flow gets it for free; this path is reachable only from the explicit
// "purge messages" Settings action which currently has no remote callers.
//
// Returning (0, nil) keeps the interface satisfied and is safe — the next
// archive of the same task fires the server-side cleanup. A dedicated
// endpoint is a phase-6 follow-up if the TUI's purge-messages path ever
// becomes important over remote.
func (s *Store) DeleteMessagesForTask(taskID string) (int, error) {
	_ = taskID
	return 0, errors.New("apistore: DeleteMessagesForTask not exposed over REST; archive the task instead")
}
