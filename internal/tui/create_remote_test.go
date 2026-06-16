package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/apistore"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/settings"
	"github.com/drn/argus/internal/tui/store"
)

// Compile-time canary: *apistore.Store must keep satisfying the structural
// remoteTaskCreator interface. The tui package deliberately doesn't import
// apistore in production code, so without this assertion a signature drift
// (like CreateTask gaining a parameter) would only surface at runtime in
// --remote mode as "task creation requires local mode".
var _ remoteTaskCreator = (*apistore.Store)(nil)

// stubStore satisfies store.Store with zero-value returns. It deliberately
// does NOT implement remoteTaskCreator, so it exercises createTaskTransactional's
// "neither local nor remote-capable" fallback branch.
type stubStore struct{}

var _ store.Store = stubStore{}

func (stubStore) Tasks() ([]*model.Task, error)   { return nil, nil }
func (stubStore) Get(string) (*model.Task, error) { return nil, nil }
func (stubStore) Add(*model.Task) error           { return nil }
func (stubStore) Update(*model.Task) error        { return nil }
func (stubStore) Delete(string) error             { return nil }
func (stubStore) Rename(string, string) error     { return nil }
func (stubStore) Config() config.Config           { return config.DefaultConfig() }
func (stubStore) Projects() (map[string]config.Project, error) {
	return map[string]config.Project{}, nil
}
func (stubStore) SetProject(string, config.Project) error   { return nil }
func (stubStore) DeleteProject(string) error                { return nil }
func (stubStore) AddSchedule(*model.ScheduledTask) error    { return nil }
func (stubStore) UpdateSchedule(*model.ScheduledTask) error { return nil }
func (stubStore) DeleteSchedule(string) error               { return nil }
func (stubStore) GetSchedule(string) (*model.ScheduledTask, error) {
	return nil, nil
}
func (stubStore) DeleteMessagesForTask(string) (int, error)  { return 0, nil }
func (stubStore) DeleteArtifactsForTask(string) (int, error) { return 0, nil }
func (stubStore) Schedules() ([]*model.ScheduledTask, error) { return nil, nil }
func (stubStore) SetConfigValue(string, string) error        { return nil }
func (stubStore) Backends() (map[string]config.Backend, error) {
	return map[string]config.Backend{}, nil
}
func (stubStore) SetBackend(string, config.Backend) error { return nil }
func (stubStore) DeleteBackend(string) error              { return nil }
func (stubStore) SetArchived(string, bool) error          { return nil }
func (stubStore) SetPinned(string, bool) error            { return nil }
func (stubStore) SetStatus(string, model.Status) error    { return nil }
func (stubStore) ListMetaByNamespace(string) (map[string]map[string]string, error) {
	return nil, nil
}
func (stubStore) PluginSections() ([]settings.Section, error) {
	return nil, nil
}

// creatorStore embeds stubStore and adds CreateTask, so it satisfies both
// store.Store and remoteTaskCreator — the remote-mode shape.
type creatorStore struct {
	stubStore
	gotName, gotPrompt, gotProject, gotBackend, gotModel string
	ret                                                  *model.Task
	err                                                  error
}

func (c *creatorStore) CreateTask(_ context.Context, name, prompt, project, backend, taskModel string) (*model.Task, error) {
	c.gotName, c.gotPrompt, c.gotProject, c.gotBackend, c.gotModel = name, prompt, project, backend, taskModel
	return c.ret, c.err
}

func TestCreateTaskTransactional_RemoteRoutesThroughCreateTask(t *testing.T) {
	cs := &creatorStore{ret: &model.Task{ID: "t1", Name: "slug", Status: model.StatusInProgress}}
	a := &App{db: cs, runner: agent.NewRunner(nil)}

	before := a.startGen.Load()
	got, err := a.createTaskTransactional(agent.CreateInput{
		Name:       "n",
		Prompt:     "build it",
		Project:    "proj",
		Backend:    "claude",
		Model:      "opus",
		BaseBranch: "develop", // ignored over REST — must not error
	})
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "t1")
	testutil.Equal(t, cs.gotPrompt, "build it")
	testutil.Equal(t, cs.gotProject, "proj")
	testutil.Equal(t, cs.gotBackend, "claude")
	testutil.Equal(t, cs.gotModel, "opus")
	// startGen is double-bumped (before + after) so a concurrent tick skips
	// reconciliation while the SSE stream attaches.
	testutil.Equal(t, a.startGen.Load(), before+2)
}

func TestCreateTaskTransactional_RemoteErrorPropagates(t *testing.T) {
	cs := &creatorStore{err: errors.New("project not found")}
	a := &App{db: cs, runner: agent.NewRunner(nil)}

	_, err := a.createTaskTransactional(agent.CreateInput{Prompt: "p", Project: "nope"})
	if err == nil {
		t.Fatal("expected error to propagate from remote CreateTask")
	}
	testutil.Contains(t, err.Error(), "project not found")
}

func TestCreateTaskTransactional_UnsupportedStore(t *testing.T) {
	a := &App{db: stubStore{}, runner: agent.NewRunner(nil)}

	_, err := a.createTaskTransactional(agent.CreateInput{Prompt: "p", Project: "proj"})
	if err == nil {
		t.Fatal("expected error for a store that is neither *db.DB nor remoteTaskCreator")
	}
	testutil.Contains(t, err.Error(), "requires local mode")
}

func TestCreateTaskTransactional_LocalUsesCreateAndStart(t *testing.T) {
	// HOME redirect: CreateAndStart resolves worktree paths through $HOME.
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	a := &App{db: d, runner: agent.NewRunner(nil)}

	// A project that isn't configured makes CreateAndStart fail fast (before
	// any worktree is created), which is enough to prove the local *db.DB
	// branch is taken rather than the remote/unsupported branches.
	_, err := a.createTaskTransactional(agent.CreateInput{
		Name:    "n",
		Prompt:  "p",
		Project: "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected CreateAndStart to fail on an unconfigured project")
	}
}
