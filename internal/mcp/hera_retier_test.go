package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

// --- hera_retier (add-model-menu-selection D8) ---

// retierNotifyCall records one ReliableNotify invocation.
type retierNotifyCall struct {
	taskID     string
	text       string
	deliveryID string
}

// fakeRetierNotifier is a minimal hera.Notifier fake that records every
// delivery so tests can assert exactly what was written to a target's PTY.
type fakeRetierNotifier struct {
	calls []retierNotifyCall
}

func (f *fakeRetierNotifier) ReliableNotify(taskID, text, deliveryID string, _ notify.NotifyOpts) func() {
	f.calls = append(f.calls, retierNotifyCall{taskID: taskID, text: text, deliveryID: deliveryID})
	return func() {}
}

func (f *fakeRetierNotifier) Cancel(string, string) {}

// retierTestServer wires a Server backed by real in-memory SQLite, with a
// fake notifier (so PTY deliveries are observable) and "claude"/"codex"
// backends registered for the unsupported-backend scenario.
func retierTestServer(t *testing.T) (*Server, *db.DB, *fakeRetierNotifier) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	testutil.NoError(t, d.SetConfigValue("defaults.backend", "claude"))
	testutil.NoError(t, d.SetBackend("claude", config.Backend{Command: "claude"}))
	testutil.NoError(t, d.SetBackend("codex", config.Backend{Command: "codex --dangerously-bypass-approvals-and-sandbox"}))

	s := New(d, 0, "")
	s.SetTaskManager(
		func(input TaskCreateInput) (*model.Task, error) {
			return nil, fmt.Errorf("task creation not supported in this test")
		},
		d,
		&mockStopper{},
	)
	notifier := &fakeRetierNotifier{}
	heraSvc := hera.New(d, notifier)
	s.SetHeraService(heraSvc, d, fakeHeraSpawn(d))
	return s, d, notifier
}

// writeRetierMenuProfile writes a "default" library profile whose code_slice
// archetype is a 2-entry menu (sonnet:high cheapest, opus:low second).
func writeRetierMenuProfile(t *testing.T) {
	t.Helper()
	dir := filepath.Join(db.DataDir(), "profiles")
	testutil.NoError(t, os.MkdirAll(dir, 0o750))
	body := "[archetype.code_slice]\nmenu = [\n  { model = \"sonnet\", effort = \"high\" },\n  { model = \"opus\", effort = \"low\" },\n]\n"
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "default.toml"), []byte(body), 0o600))
}

// addRetierTarget inserts a live task with the given backend+archetype
// (unbound to any worktree — resolved by task ID, not cwd, for this tool).
func addRetierTarget(t *testing.T, d *db.DB, backend, archetype string) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:      "target",
		Status:    model.StatusInProgress,
		Project:   "test-project",
		Worktree:  "/wt/target",
		Backend:   backend,
		Archetype: archetype,
	}
	testutil.NoError(t, d.Add(task))
	return task
}

// bindRetierWorker creates a worker role + live binding against target,
// without going through the spawn path (mirrors fakeHeraSpawn's DB-direct style).
func bindRetierWorker(t *testing.T, d *db.DB, orchID int64, roleName string, target *model.Task) *db.HeraRole {
	t.Helper()
	role, _, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orchID,
		Name:           roleName,
		Kind:           db.HeraKindWorker,
		ArgusProject:   target.Project,
		Archetype:      target.Archetype,
	}, target.ID, target.Worktree)
	testutil.NoError(t, err)
	return role
}

func retierArgs(cwd, orch, role, model, effort string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(
		`{"cwd": %q, "orchestrator": %q, "role": %q, "model": %q, "effort": %q}`,
		cwd, orch, role, model, effort))
}

func TestHeraRetier_NonCoordinatorRejected(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	workerTask := addHeraTestTask(t, d, "/wt/worker")
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "orchestrator": "myorch", "role_name": "w1", "kind": "worker"
		}`, workerTask.Worktree)),
	})

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(workerTask.Worktree, "myorch", "w1", "opus", "high"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may retier")
	testutil.Equal(t, len(notifier.calls), 0)
}

// TestHeraRetier_MatchingPairDelivered: the target starts with no override
// (resolves to the cheapest menu entry, sonnet:high). Requesting the OTHER
// menu member (opus:low) is a valid membership match, honored outright, and
// since effort changes (high -> low) both /model and /effort are delivered.
func TestHeraRetier_MatchingPairDelivered(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	writeRetierMenuProfile(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	target := addRetierTarget(t, d, "claude", "code_slice")
	bindRetierWorker(t, d, orch.ID, "worker1", target)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(coord.Worktree, "myorch", "worker1", "opus", "low"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**model**: opus")
	testutil.Contains(t, cr.Content[0].Text, "**effort**: low")

	testutil.Equal(t, len(notifier.calls), 2)
	testutil.Equal(t, notifier.calls[0].taskID, target.ID)
	testutil.Equal(t, notifier.calls[0].text, "/model opus")
	testutil.Equal(t, notifier.calls[1].text, "/effort low")
}

// TestHeraRetier_OffMenuSubstitutedAndLogged: the requested pair (mistral,
// medium) matches no menu entry, so it is corrected to the cheapest
// (sonnet:high) — the delivered/reported pair reflects the substitution, not
// the request.
func TestHeraRetier_OffMenuSubstitutedAndLogged(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	writeRetierMenuProfile(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	target := addRetierTarget(t, d, "claude", "code_slice")
	bindRetierWorker(t, d, orch.ID, "worker1", target)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(coord.Worktree, "myorch", "worker1", "mistral", "medium"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**model**: sonnet")
	testutil.Contains(t, cr.Content[0].Text, "**effort**: high")

	// Delivered pair is the substituted cheapest entry, not the off-menu request.
	testutil.Equal(t, notifier.calls[0].text, "/model sonnet")
}

// TestHeraRetier_UnchangedEffortNotResent: requesting the pair that is
// ALREADY the target's currently-resolved pick (sonnet:high, the cheapest
// default) writes /model but skips /effort since nothing changed.
func TestHeraRetier_UnchangedEffortNotResent(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	writeRetierMenuProfile(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	target := addRetierTarget(t, d, "claude", "code_slice")
	bindRetierWorker(t, d, orch.ID, "worker1", target)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(coord.Worktree, "myorch", "worker1", "sonnet", "high"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	testutil.Equal(t, len(notifier.calls), 1)
	testutil.Equal(t, notifier.calls[0].text, "/model sonnet")
}

// TestHeraRetier_UnsupportedBackend: a non-Claude-style target returns an
// explicit error and writes nothing to the PTY.
func TestHeraRetier_UnsupportedBackend(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	target := addRetierTarget(t, d, "codex", "code_slice")
	bindRetierWorker(t, d, orch.ID, "worker1", target)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(coord.Worktree, "myorch", "worker1", "gpt-5", "high"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "not supported for backend")
	testutil.Equal(t, len(notifier.calls), 0)
}

// TestHeraRetier_RoleNotFound asserts retiering a role name absent from the
// orchestrator errors clearly rather than panicking or silently no-op'ing.
func TestHeraRetier_RoleNotFound(t *testing.T) {
	s, d, notifier := retierTestServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_retier",
		Arguments: retierArgs(coord.Worktree, "myorch", "ghost", "opus", "high"),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "not found")
	testutil.Equal(t, len(notifier.calls), 0)
}

func TestHeraRetier_MissingArgs(t *testing.T) {
	s, d, _ := retierTestServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	cases := []struct {
		name string
		args json.RawMessage
		want string
	}{
		{"missing cwd", json.RawMessage(`{"orchestrator":"myorch","role":"w","model":"opus","effort":"high"}`), "cwd is required"},
		{"missing orchestrator", json.RawMessage(fmt.Sprintf(`{"cwd":%q,"role":"w","model":"opus","effort":"high"}`, coord.Worktree)), "orchestrator is required"},
		{"missing role", json.RawMessage(fmt.Sprintf(`{"cwd":%q,"orchestrator":"myorch","model":"opus","effort":"high"}`, coord.Worktree)), "role is required"},
		{"missing model", json.RawMessage(fmt.Sprintf(`{"cwd":%q,"orchestrator":"myorch","role":"w","effort":"high"}`, coord.Worktree)), "model is required"},
		{"missing effort", json.RawMessage(fmt.Sprintf(`{"cwd":%q,"orchestrator":"myorch","role":"w","model":"opus"}`, coord.Worktree)), "effort is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := doRequest(t, s, "tools/call", ToolCallParams{Name: "hera_retier", Arguments: tc.args})
			cr := callResult(t, resp)
			testutil.Equal(t, cr.IsError, true)
			testutil.Contains(t, cr.Content[0].Text, tc.want)
		})
	}
}
