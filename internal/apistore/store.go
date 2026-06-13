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
	"github.com/drn/argus/internal/tui/settings"
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
		return s.cachedSnapshot(), err
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return s.cachedSnapshot(), err
	}
	var cfg config.Config
	if err := json.Unmarshal(buf, &cfg); err != nil {
		return s.cachedSnapshot(), err
	}
	s.configMu.Lock()
	s.cachedConfig = cfg
	s.configMu.Unlock()
	return cfg, nil
}

// cachedSnapshot returns the last cached config under RLock. It is the single
// reader of cachedConfig: RefreshConfig's error paths and Config() both route
// through it. RLock guarantees the returned value is a fully-written snapshot —
// never a half-mutated struct mid-RefreshConfig (the cache WRITE holds the
// full Lock). HTTP I/O and JSON parsing must stay OUTSIDE this helper so RLock
// readers never block on the network round trip.
func (s *Store) cachedSnapshot() config.Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.cachedConfig
}

// Config returns the cached snapshot. Callers depending on a fresh value
// must call RefreshConfig first.
func (s *Store) Config() config.Config {
	return s.cachedSnapshot()
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

// CreateTask is the remote-mode equivalent of agent.CreateAndStart: it POSTs
// to /api/tasks so the daemon runs the transactional worktree + session
// creation on its own host, then fetches the freshly-created row so the TUI
// can enter the agent view with a complete model.Task.
//
// It is deliberately NOT part of tui/store.Store — there is no *db.DB analog
// (local mode calls agent.CreateAndStart directly, which needs the in-process
// runner). The TUI type-asserts a.db to this method's interface when running
// remote. Base-branch override and attachment uploads aren't supported here:
// the JSON endpoint has no field for them (see gotchas/remote-tui.md).
//
// On success the daemon may asynchronously rename an auto-named task via
// Haiku; the returned Task carries the regex-slug name and the next list
// refresh picks up the final name.
func (s *Store) CreateTask(ctx context.Context, name, prompt, project, backend, taskModel string) (*model.Task, error) {
	resp, err := s.c.CreateTask(ctx, apiclient.CreateTaskReq{
		Name:    name,
		Prompt:  prompt,
		Project: project,
		Backend: backend,
		Model:   taskModel,
	})
	if err != nil {
		return nil, err
	}
	// The create response is lossy (id/name/status only). Fetch the full row
	// so the caller gets a complete model.Task. If that follow-up read fails
	// the task still exists on the server — fall back to a minimal struct
	// built from the create response so the UI can select + attach, and the
	// next list refresh fills in the rest.
	if t, gErr := s.c.GetTaskRaw(ctx, resp.ID); gErr == nil {
		return t, nil
	}
	st, _ := model.ParseStatus(resp.Status) // defaults to StatusPending on parse error
	return &model.Task{
		ID:      resp.ID,
		Name:    resp.Name,
		Status:  st,
		Project: project,
		Backend: backend,
		Model:   taskModel,
		Prompt:  prompt,
	}, nil
}

// ForkTask forks an existing task on the remote daemon via
// POST /api/tasks/{id}/fork and returns the resulting row.
//
// Like CreateTask, it is NOT part of tui/store.Store — the local fork path
// (agent.CreateAndStart with an OnWorktreeCreated hook) extracts the source's
// session log + git diff and writes .context/ fork files, none of which the
// server-side fork endpoint reconstructs. The remote fork is therefore
// DEGRADED: it starts from the source's original prompt + backend, linked via
// the task.forked event, without the context carryover. See gotchas/remote-tui.md.
//
// An empty prompt tells the server to inherit the source task's prompt; an
// empty name tells it to use "<src>-fork".
func (s *Store) ForkTask(ctx context.Context, srcID, name, prompt, project string) (*model.Task, error) {
	resp, err := s.c.ForkTask(ctx, srcID, apiclient.ForkReq{
		Name:    name,
		Prompt:  prompt,
		Project: project,
	})
	if err != nil {
		return nil, err
	}
	if t, gErr := s.c.GetTaskRaw(ctx, resp.ID); gErr == nil {
		return t, nil
	}
	st, _ := model.ParseStatus(resp.Status)
	return &model.Task{
		ID:      resp.ID,
		Name:    resp.Name,
		Status:  st,
		Project: project,
	}, nil
}

// RunSchedule fires a schedule out-of-cycle on the remote daemon via
// POST /api/schedules/{id}/run and returns the created task's ID. The daemon's
// scheduler runs the full fire server-side — task creation AND the schedule
// row's LastRunAt/LastTaskID/NextRunAt bookkeeping — so the TUI only needs to
// trigger it and refresh the Settings view afterward.
//
// NOT part of tui/store.Store — the local path drives agent.CreateAndStart +
// the schedule bookkeeping itself, which this method delegates to the server.
func (s *Store) RunSchedule(ctx context.Context, id string) (string, error) {
	resp, err := s.c.RunSchedule(ctx, id)
	if err != nil {
		return "", err
	}
	return resp.TaskID, nil
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

// DeleteArtifactsForTask isn't exposed as a dedicated endpoint. In remote
// mode the daemon owns artifact cleanup: deleting a task over REST
// (DELETE /api/tasks/{id}) runs the server-side DeleteArtifactsForTask +
// on-disk removal itself, so the TUI doesn't need to. Returning an error
// keeps the interface satisfied while signalling that the local TUI path
// should no-op this call when it's talking to a remote daemon.
func (s *Store) DeleteArtifactsForTask(taskID string) (int, error) {
	_ = taskID
	return 0, errors.New("apistore: DeleteArtifactsForTask not exposed over REST; the daemon cleans up on task delete")
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
	case "defaults.share_project":
		upd.Defaults = &apiclient.DefaultsUpdate{ShareProject: &value}
	case "defaults.permission_mode":
		upd.Defaults = &apiclient.DefaultsUpdate{PermissionMode: &value}
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
		out[b.Name] = config.Backend{Command: b.Command, PromptFlag: b.PromptFlag, Model: b.Model}
	}
	return out, nil
}

// SetBackend POSTs or PUTs depending on whether the backend already exists.
// Mirrors the local upsert semantics of *db.DB.SetBackend.
//
// POST first; on 409 conflict or 5xx, fall back to PUT. Other 4xx surface
// so an invalid payload (empty command) isn't masked by a second 4xx.
func (s *Store) SetBackend(name string, b config.Backend) error {
	body := apiclient.BackendJSON{Name: name, Command: b.Command, PromptFlag: b.PromptFlag, Model: b.Model}
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

// SetPinned writes only the pinned column. There is no dedicated REST endpoint,
// so we re-fetch the current row (authoritative, fresh name) and write it back
// via the raw endpoint mutating only Pinned through the model setter. The
// re-fetch is what makes this safe against the name-clobber the local SetPinned
// avoids structurally: the server's name is whatever a background autoname
// rename last wrote, not the TUI's stale snapshot.
func (s *Store) SetPinned(id string, pinned bool) error {
	ctx := context.Background()
	t, err := s.c.GetTaskRaw(ctx, id)
	if err != nil {
		return err
	}
	// Same-value no-op, symmetric with SetStatus's guard: skip the PUT when the
	// pinned bit already matches. Pinning has no derived side effect (no
	// timestamp re-stamp), so this is purely a redundant-round-trip saver, but
	// keeping the two setters symmetric avoids a confusing precedent.
	if t.Pinned == pinned {
		return nil
	}
	t.SetPinned(pinned)
	return s.c.UpdateTaskRaw(ctx, t)
}

// SetStatus writes only the status column (and derived timestamps). Same
// re-fetch-then-write pattern as SetPinned so the fresh server-side name is
// preserved instead of the caller's possibly-stale snapshot.
func (s *Store) SetStatus(id string, status model.Status) error {
	ctx := context.Background()
	t, err := s.c.GetTaskRaw(ctx, id)
	if err != nil {
		return err
	}
	// Same-status no-op, mirroring local db.SetStatus's fast path. Without this,
	// model.SetStatus(Complete) on an already-complete row would re-stamp
	// ended_at to "now" and the PUT would persist that drift — a divergence
	// from local mode. Skipping the write keeps the two backends in lockstep.
	if t.Status == status {
		return nil
	}
	t.SetStatus(status)
	return s.c.UpdateTaskRaw(ctx, t)
}

// ListMetaByNamespace reconstructs a task_meta namespace from the REST task
// list. Today only the "pr" namespace is exposed over REST (the task DTO
// carries pr_state) — that's the sole TUI caller. Any other namespace returns
// an empty map rather than an error so the tick degrades to blank cells
// instead of spamming the status bar.
//
// The returned shape mirrors db.DB.ListMetaByNamespace: taskID → key → value,
// with key "state". The map is never nil. The remote daemon's poller is the
// sole writer; this is a pure read, identical to the local path.
func (s *Store) ListMetaByNamespace(namespace string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string)
	if namespace != "pr" {
		return out, nil
	}
	// archived=all so a completed-but-archived task's PR badge still resolves
	// (parity with the local index scan, which is namespace-wide).
	tasks, err := s.c.ListTasks(context.Background(), apiclient.ListTasksFilter{Archived: "all"})
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		if t.PRState == "" {
			continue
		}
		out[t.ID] = map[string]string{"state": t.PRState}
	}
	return out, nil
}

// PluginSections fetches the registered plugin settings sections via GET
// /api/plugins/settings/sections and reconstructs settings.Section values
// (the wire shape elides the `spec` envelope for compactness, so we
// re-wrap fields into a FormSpec on the read side). Corrupt fields are
// dropped so a misbehaving plugin can't take the "Plugins" header offline.
func (s *Store) PluginSections() ([]settings.Section, error) {
	wire, err := s.c.ListPluginSections(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]settings.Section, 0, len(wire))
	for _, w := range wire {
		fields := make([]settings.FormField, 0, len(w.Fields))
		for _, f := range w.Fields {
			fields = append(fields, settings.FormField{
				Key:     f.Key,
				Label:   f.Label,
				Type:    settings.FieldType(f.Type),
				Default: f.Default,
				Min:     f.Min,
				Max:     f.Max,
				Options: f.Options,
			})
		}
		out = append(out, settings.Section{
			Scope:       w.Scope,
			Title:       w.Title,
			Type:        settings.SectionType(w.Type),
			CallbackURL: w.CallbackURL,
			Spec:        &settings.FormSpec{Fields: fields},
		})
	}
	return out, nil
}

// PruneCompleted removes every status=complete task on the daemon, sweeping
// their worktrees + branches and any orphan worktrees, via
// POST /api/maintenance/prune-completed. It returns the per-category counts.
//
// This is NOT part of the store.Store interface — the local TUI prune flow
// (agent.PrunePrepare) shells out to git/PTY directly, which only works
// against the local daemon. In remote mode the whole operation runs on the
// server, so the TUI type-asserts the store to a narrow remote-pruner
// interface and calls this instead. Master-only on the server.
func (s *Store) PruneCompleted(ctx context.Context) (pruned, worktrees, orphans int, err error) {
	rep, err := s.c.PruneCompleted(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return rep.Pruned, rep.Worktrees, rep.Orphans, nil
}
