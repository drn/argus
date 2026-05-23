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
	"sync"

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
//
// configMu guards cachedConfig: RefreshConfig writes from a background
// ticker goroutine in cmd/argus/remote.go while Config() reads from the
// tview tick goroutine and async refresh workers. config.Config contains
// maps (Projects, Backends); concurrent read+write of map headers without
// a mutex is a data race that go test -race catches.
type Store struct {
	c *apiclient.Client

	configMu     sync.RWMutex
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
//
// HTTP I/O and JSON parsing happen outside the lock; only the cache write
// is serialized. RLock readers in Config() never block on the network round
// trip.
func (s *Store) RefreshConfig(ctx context.Context) (config.Config, error) {
	raw, err := s.c.GetConfig(ctx)
	if err != nil {
		s.configMu.RLock()
		cur := s.cachedConfig
		s.configMu.RUnlock()
		return cur, err
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		s.configMu.RLock()
		cur := s.cachedConfig
		s.configMu.RUnlock()
		return cur, err
	}
	var cfg config.Config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		s.configMu.RLock()
		cur := s.cachedConfig
		s.configMu.RUnlock()
		return cur, err
	}
	s.configMu.Lock()
	s.cachedConfig = cfg
	s.configMu.Unlock()
	return cfg, nil
}

// Config returns the cached snapshot. Callers depending on a fresh value
// must call RefreshConfig first. RLock guarantees the returned value is a
// fully-written snapshot — never a half-mutated struct mid-RefreshConfig.
func (s *Store) Config() config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.cachedConfig
}

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

// SetProject upserts a project. POST creates; if that returns a 409
// conflict (project already exists) or a 5xx, we fall back to PUT. 4xx
// other than 409 surface as the POST error so a real validation failure
// (e.g. empty path) isn't masked by a second 4xx from PUT with the same
// body.
func (s *Store) SetProject(name string, p config.Project) error {
	body := projectToAPI(name, p)
	err := s.c.CreateProject(context.Background(), body)
	if err == nil {
		return nil
	}
	if !shouldFallbackUpsert(err) {
		return err
	}
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

// Schedules fetches the schedule list, parses RFC3339 timestamps, and
// returns the model shape. The wire format carries empty strings for
// zero times — best-effort parses, leaving the field zero on failure.
func (s *Store) Schedules() ([]*model.ScheduledTask, error) {
	wire, err := s.c.ListSchedules(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]*model.ScheduledTask, 0, len(wire))
	for _, w := range wire {
		sched := &model.ScheduledTask{
			ID:         w.ID,
			Name:       w.Name,
			Project:    w.Project,
			Prompt:     w.Prompt,
			Backend:    w.Backend,
			Schedule:   w.Schedule,
			Enabled:    w.Enabled,
			LastTaskID: w.LastTaskID,
			LastError:  w.LastError,
		}
		if w.CreatedAt != "" {
			if t, perr := timeParse(w.CreatedAt); perr == nil {
				sched.CreatedAt = t
			}
		}
		if w.LastRunAt != "" {
			if t, perr := timeParse(w.LastRunAt); perr == nil {
				sched.LastRunAt = t
			}
		}
		if w.NextRunAt != "" {
			if t, perr := timeParse(w.NextRunAt); perr == nil {
				sched.NextRunAt = t
			}
		}
		if w.RunOnceAt != "" {
			if t, perr := timeParse(w.RunOnceAt); perr == nil {
				sched.RunOnceAt = t
			}
		}
		out = append(out, sched)
	}
	return out, nil
}

// SetConfigValue maps the raw key to /api/settings's typed update body.
// Only the keys the SPA settings tab touches are supported here; the TUI
// settings tab uses the same set. Anything else returns an error so a
// silent drop never papers over a missing endpoint.
func (s *Store) SetConfigValue(key, value string) error {
	upd := apiclient.SettingsUpdate{}
	switch key {
	case "sandbox.enabled":
		b := value == "true"
		upd.Sandbox = &apiclient.SandboxUpdate{Enabled: &b}
	case "sandbox.deny_read":
		list := splitCSV(value)
		upd.Sandbox = &apiclient.SandboxUpdate{DenyRead: &list}
	case "sandbox.extra_write":
		list := splitCSV(value)
		upd.Sandbox = &apiclient.SandboxUpdate{ExtraWrite: &list}
	case "sandbox.allow_apple_events":
		list := splitCSV(value)
		upd.Sandbox = &apiclient.SandboxUpdate{AllowAppleEvents: &list}
	case "kb.enabled":
		b := value == "true"
		upd.KB = &apiclient.KBUpdate{Enabled: &b}
	case "kb.metis_vault_path":
		upd.KB = &apiclient.KBUpdate{MetisVaultPath: &value}
	case "api.enabled":
		b := value == "true"
		upd.API = &apiclient.APIUpdate{Enabled: &b}
	case "default_backend", "defaults.backend":
		upd.Defaults = &apiclient.DefaultsUpdate{Backend: &value}
	default:
		return errors.New("apistore: SetConfigValue: no remote handler for key " + key)
	}
	_, err := s.c.UpdateSettings(context.Background(), upd)
	return err
}

// Backends fetches the configured backend list and converts the wire shape
// back to config.Backend.
func (s *Store) Backends() (map[string]config.Backend, error) {
	wire, err := s.c.ListBackends(context.Background())
	if err != nil {
		return nil, err
	}
	out := make(map[string]config.Backend, len(wire))
	for _, b := range wire {
		out[b.Name] = config.Backend{Command: b.Command, PromptFlag: b.PromptFlag}
	}
	return out, nil
}

// SetBackend POSTs or PUTs depending on whether the backend already exists.
// Mirrors the local upsert semantics of *db.DB.SetBackend.
//
// POST first; on 409 conflict or 5xx, fall back to PUT. Other 4xx surface
// so an invalid payload (empty command) isn't masked by a second 4xx.
func (s *Store) SetBackend(name string, b config.Backend) error {
	body := apiclient.BackendJSON{Name: name, Command: b.Command, PromptFlag: b.PromptFlag}
	err := s.c.CreateBackend(context.Background(), body)
	if err == nil {
		return nil
	}
	if !shouldFallbackUpsert(err) {
		return err
	}
	return s.c.UpdateBackend(context.Background(), name, body)
}

// shouldFallbackUpsert decides whether a failed POST justifies retrying as
// PUT. Only 409 (conflict — row already exists) triggers the fallback:
//
//   - 4xx other than 409: validation rejected the body. A second attempt
//     with the same body will fail the same way; retrying masks the real
//     error from the caller.
//   - 5xx: server is in trouble. Retry with PUT can succeed only if the
//     5xx was transient AND the row already existed; the latter is what
//     409 already covers. Surface the 5xx directly.
//   - transport error: server state is unknown. Retrying with PUT could
//     create a duplicate or overwrite a row the caller did not intend to
//     touch. Surface the transport error so the caller can retry the POST
//     intentionally.
func shouldFallbackUpsert(err error) bool {
	var apiErr *apiclient.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == 409
}

// DeleteBackend removes the backend by name.
func (s *Store) DeleteBackend(name string) error {
	return s.c.DeleteBackend(context.Background(), name)
}

// SetDependsOn writes the depends_on column via orchestrator linking endpoints.
// No single REST endpoint covers "replace the whole list" — we read the
// current deps and apply diff (Link new, Unlink removed). Best-effort.
func (s *Store) SetDependsOn(id string, deps []string) error {
	ctx := context.Background()
	cur, err := s.c.GetDeps(ctx, id)
	if err != nil {
		return err
	}
	curSet := make(map[string]bool)
	if parents, ok := cur["parents"].([]any); ok {
		for _, p := range parents {
			if m, ok := p.(map[string]any); ok {
				if pid, ok := m["id"].(string); ok {
					curSet[pid] = true
				}
			}
		}
	}
	want := make(map[string]bool, len(deps))
	for _, d := range deps {
		want[d] = true
	}
	for d := range want {
		if !curSet[d] {
			if err := s.c.LinkTask(ctx, id, d); err != nil {
				return err
			}
		}
	}
	for d := range curSet {
		if !want[d] {
			if err := s.c.UnlinkTask(ctx, id, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetPlanSlug calls /api/tasks/{id}/plan-slug.
func (s *Store) SetPlanSlug(id, slug string) error {
	return s.c.SetPlanSlug(context.Background(), id, slug)
}

// SetArchived calls archive/unarchive.
func (s *Store) SetArchived(id string, archived bool) error {
	if archived {
		return s.c.ArchiveTask(context.Background(), id)
	}
	return s.c.UnarchiveTask(context.Background(), id)
}
