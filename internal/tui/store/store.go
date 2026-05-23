// Package store defines the persistence interface the TUI talks to.
//
// In local mode, the App's store is a *db.DB (direct SQLite). In remote
// mode, it is an *apistore.Store (HTTP-backed). Both implementations share
// this Store interface so the rest of the TUI code is unaware which
// transport is in use.
//
// The interface is extracted from the set of methods the TUI actually calls
// on *db.DB — adding new persistence operations to the TUI requires
// extending Store here AND adding the corresponding REST endpoint + apistore
// method.
package store

import (
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// Store is the persistence interface the TUI process consumes. Every method
// matches an existing method on *db.DB so local mode (which passes a real
// *db.DB) satisfies the interface implicitly; the remote-mode implementation
// in internal/apistore proxies each call to the REST API.
//
// Method signatures intentionally mirror *db.DB exactly so future callers
// in the TUI don't need to know which backend they're hitting. Anything that
// can't be expressed in a single round trip lives outside this interface
// (e.g., the depswatcher tick loop runs in the daemon, not the TUI).
type Store interface {
	// Tasks returns every task row, both active and archived.
	Tasks() ([]*model.Task, error)
	// Get returns the task by ID. Returns (nil, db.ErrTaskNotFound) for
	// unknown IDs.
	Get(id string) (*model.Task, error)
	// Add inserts a new task row.
	Add(t *model.Task) error
	// Update writes a task row.
	Update(t *model.Task) error
	// Delete removes a task row.
	Delete(id string) error
	// Rename writes a new name to a task row.
	Rename(id, name string) error

	// Config returns the full config snapshot — projects, backends,
	// keybindings, sandbox, KB, API, defaults.
	Config() config.Config
	// Projects returns the projects map.
	Projects() (map[string]config.Project, error)
	// SetProject upserts a project by name.
	SetProject(name string, p config.Project) error
	// DeleteProject removes a project from config.
	DeleteProject(name string) error

	// AddSchedule inserts a new schedule row.
	AddSchedule(s *model.ScheduledTask) error
	// UpdateSchedule writes a schedule row.
	UpdateSchedule(s *model.ScheduledTask) error
	// DeleteSchedule removes a schedule row.
	DeleteSchedule(id string) error
	// GetSchedule returns the schedule by ID.
	GetSchedule(id string) (*model.ScheduledTask, error)

	// DeleteMessagesForTask removes every message addressed to / from the
	// given task ID. Returns the deletion count.
	DeleteMessagesForTask(taskID string) (int, error)

	// Schedules returns every persisted schedule. Used by the Settings tab.
	Schedules() ([]*model.ScheduledTask, error)

	// SetConfigValue writes a single config (key, value) pair. Used by the
	// Settings tab for sandbox/api/kb/defaults toggles.
	SetConfigValue(key, value string) error

	// Backends returns the configured agent backends keyed by name. Used by
	// the Settings tab and the task-creation form.
	Backends() (map[string]config.Backend, error)

	// SetBackend upserts a backend definition.
	SetBackend(name string, b config.Backend) error

	// DeleteBackend removes a backend from config.
	DeleteBackend(name string) error

	// orch.Store methods — required so dagactions can pass the store
	// through to orch.HaltDownstream / orch.Link / orch.Unlink without a
	// separate adapter.

	// SetDependsOn writes the depends_on column.
	SetDependsOn(id string, deps []string) error

	// SetPlanSlug writes the plan_slug column.
	SetPlanSlug(id, slug string) error

	// SetArchived flips the archived column.
	SetArchived(id string, archived bool) error
}

// Compile-time assertion: *db.DB satisfies Store. Imported as a side-effect
// here so any drift in either signature is caught at build time without
// pulling the db dep into every TUI file. The assertion lives in a separate
// build-only file because importing internal/db from within
// internal/tui/store would otherwise create a cycle through the test helpers
// that some db files reach for.
