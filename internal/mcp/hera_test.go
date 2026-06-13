package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// testHeraServer creates a Server backed by real in-memory SQLite with hera
// and task management wired. Returns the server and the underlying DB so
// tests can add tasks, orchestrators, and roles as fixtures.
func testHeraServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	s := New(d, 0, "")
	s.SetTaskManager(
		func(input TaskCreateInput) (*model.Task, error) {
			return nil, fmt.Errorf("task creation not supported in this test")
		},
		d,
		&mockStopper{},
	)
	heraSvc := hera.New(d, nil) // nil notifier: delivery skipped, messages still persisted
	s.SetHeraService(heraSvc, d, fakeHeraSpawn(d))
	return s, d
}

// fakeHeraSpawn returns a HeraSpawner that mimics the daemon's born-bound spawn
// against the real in-memory DB but without a worktree/session: it persists a
// task row, stamps meta:hera.role=worker, and creates the role+binding. This
// lets MCP-arm tests exercise resolution + response formatting end-to-end. The
// real transactional LIFO unwinding is covered in the agent + db layers.
func fakeHeraSpawn(d *db.DB) HeraSpawner {
	return func(in HeraSpawnInput) (*HeraSpawnResult, error) {
		name, err := d.UniqueHeraRoleName(in.OrchestratorID, in.BaseName)
		if err != nil {
			return nil, err
		}
		task := &model.Task{
			Name:     name,
			Status:   model.StatusInProgress,
			Project:  in.Project,
			Worktree: "/wt/spawn-" + name,
			Prompt:   in.TaskPrompt,
		}
		if err := d.Add(task); err != nil {
			return nil, err
		}
		_ = d.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker))
		role, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: in.OrchestratorID,
			Name:           name,
			Kind:           db.HeraKindWorker,
			ArgusProject:   in.Project,
			Prompt:         in.RolePrompt,
		}, task.ID, task.Worktree)
		if err != nil {
			return nil, err
		}
		return &HeraSpawnResult{Task: task, Role: role, Binding: binding}, nil
	}
}

// seedCoordinator bootstraps an orchestrator + coordinator binding for a task
// at the given worktree, returning the coordinator task. Mirrors the
// hera_new_orchestrator tool path used by other tests.
func seedCoordinator(t *testing.T, s *Server, d *db.DB, orch, worktree string) *model.Task {
	t.Helper()
	task := addHeraTestTask(t, d, worktree)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": %q, "coordinator_role_name": "coord"
		}`, worktree, orch)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	return task
}

// addHeraTestTask inserts a task with the given worktree into the DB and
// returns it. The task is immediately usable for cwd-based resolution.
func addHeraTestTask(t *testing.T, d *db.DB, worktree string) *model.Task {
	t.Helper()
	task := &model.Task{
		Name:     "test-task",
		Status:   model.StatusInProgress,
		Project:  "test-project",
		Worktree: worktree,
	}
	testutil.NoError(t, d.Add(task))
	return task
}

// --- tools/list tests ---

func TestToolsList_HeraOff(t *testing.T) {
	// When hera is not wired, hera tools must be absent.
	s, taskDB, stopper := testServerWithTasks()
	_ = taskDB
	_ = stopper

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))

	for _, tool := range list.Tools {
		if strings.HasPrefix(tool.Name, "hera_") {
			t.Errorf("hera tool %q present without hera enabled", tool.Name)
		}
	}
}

func TestToolsList_HeraOn(t *testing.T) {
	s, _ := testHeraServer(t)

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))

	names := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}

	// All 9 hera tools must appear.
	for _, want := range []string{
		"hera_new_orchestrator", "hera_join", "hera_send", "hera_inbox",
		"hera_mark_read", "hera_status", "hera_spawn_worker",
		"hera_tree_updates", "hera_get_messages",
	} {
		if !names[want] {
			t.Errorf("hera tool missing from tools/list: %s", want)
		}
	}

	// KB tools must also be present (additive).
	if !names["kb_search"] {
		t.Error("kb_search missing after hera enabled")
	}
}

func TestToolsList_DupGuard_HeraEnabled(t *testing.T) {
	s, _, d := newMCPWithRegistry(t)
	// Wire hera so the dup-guard activates.
	heraSvc := hera.New(d, nil)
	s.SetHeraService(heraSvc, d, nil)
	s.SetTaskManager(
		func(input TaskCreateInput) (*model.Task, error) { return nil, nil },
		d,
		&mockStopper{},
	)

	// Register a plugin tool with scope "hera" — must be filtered.
	testutil.NoError(t, s.registry.Register("hera", ToolRegistration{
		Name:        "hera_new_orchestrator",
		Description: "external plugin copy",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb",
	}))

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))

	var count int
	for _, tool := range list.Tools {
		if tool.Name == "hera_new_orchestrator" {
			count++
			// The native one must survive; the plugin description differs.
			if tool.Description == "external plugin copy" {
				t.Error("dup-guard failed: plugin hera tool exposed instead of native")
			}
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 hera_new_orchestrator, got %d", count)
	}
}

func TestToolsList_DupGuard_IrisScope(t *testing.T) {
	s, _, d := newMCPWithRegistry(t)
	heraSvc := hera.New(d, nil)
	s.SetHeraService(heraSvc, d, nil)
	s.SetTaskManager(
		func(input TaskCreateInput) (*model.Task, error) { return nil, nil },
		d,
		&mockStopper{},
	)

	// Register an iris-scope plugin tool — must NOT be filtered.
	testutil.NoError(t, s.registry.Register("iris", ToolRegistration{
		Name:        "iris_gh_pr_create",
		Description: "iris PR create",
		InputSchema: json.RawMessage(`{}`),
		CallbackURL: "http://127.0.0.1/cb",
	}))

	resp := doRequest(t, s, "tools/list", nil)
	testutil.NoError(t, respErr(resp))

	result, _ := json.Marshal(resp.Result)
	var list ToolsListResult
	testutil.NoError(t, json.Unmarshal(result, &list))

	names := make(map[string]bool, len(list.Tools))
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	if !names["iris_gh_pr_create"] {
		t.Error("iris-scope plugin tool was incorrectly filtered by hera dup-guard")
	}
}

// --- hera_new_orchestrator ---

func TestHera_NewOrchestrator(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/alpha")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "myorch", "coordinator_role_name": "coord"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**orchestrator**: myorch")
	testutil.Contains(t, cr.Content[0].Text, "**role_name**: coord")
	testutil.Contains(t, cr.Content[0].Text, "**kind**: coordinator")
}

func TestHera_NewOrchestrator_MissingCwd(t *testing.T) {
	s, _ := testHeraServer(t)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_new_orchestrator",
		Arguments: json.RawMessage(`{"name":"x","coordinator_role_name":"c"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "cwd is required")
}

func TestHera_NewOrchestrator_AlreadyBound(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/alpha")

	// First call succeeds.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "myorch", "coordinator_role_name": "coord"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)

	// Second call for same task+orch must fail with "already has a live binding".
	resp2 := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "myorch", "coordinator_role_name": "coord2"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp2))
	cr2 := callResult(t, resp2)
	testutil.Equal(t, cr2.IsError, true)
	testutil.Contains(t, cr2.Content[0].Text, "already has a live binding")
}

// --- hera_join ---

func TestHera_Join_ClaimMode(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/beta")

	// Bootstrap the orchestrator first.
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "name": "myorch", "coordinator_role_name": "coord"
		}`, task.Worktree)),
	})

	// Claim mode: omit role_name.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "claim mode")
	testutil.Contains(t, cr.Content[0].Text, "**role_name**: coord")
	testutil.Contains(t, cr.Content[0].Text, "**unread_message_count**: 0")
}

func TestHera_Join_AttachMode(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := addHeraTestTask(t, d, "/wt/coord")
	workerTask := &model.Task{
		Name:     "worker-task",
		Status:   model.StatusInProgress,
		Project:  "test-project",
		Worktree: "/wt/worker",
	}
	testutil.NoError(t, d.Add(workerTask))

	// Bootstrap coordinator.
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"myorch","coordinator_role_name":"coord"
		}`, coordTask.Worktree)),
	})

	// Worker attaches.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"myorch","role_name":"w1","kind":"worker"
		}`, workerTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "attach mode")
	testutil.Contains(t, cr.Content[0].Text, "**role_name**: w1")
	testutil.Contains(t, cr.Content[0].Text, "**kind**: worker")
}

func TestHera_Join_CoordinatorKindRejected(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/gamma")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"myorch","role_name":"c","kind":"coordinator"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "coordinator kind is not accepted here")
}

func TestHera_Join_AlreadyBound(t *testing.T) {
	s, d := testHeraServer(t)
	coordTask := addHeraTestTask(t, d, "/wt/c1")
	workerTask := &model.Task{
		Name: "wt2", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/w1",
	}
	testutil.NoError(t, d.Add(workerTask))

	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"myorch","coordinator_role_name":"coord"
		}`, coordTask.Worktree)),
	})

	// Attach once.
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"myorch","role_name":"w1","kind":"worker"
		}`, workerTask.Worktree)),
	})

	// Attach again — must be rejected.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"myorch","role_name":"w2","kind":"worker"
		}`, workerTask.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "already has a live binding")
}

// --- CallerContext / resolveCallerRole ---

func TestHera_CallerContext_ZeroBindings(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/unbound")

	// status requires resolveCallerRole; task has no binding.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"working"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "not bound to any hera role")
}

func TestHera_CallerContext_MultipleBindings(t *testing.T) {
	s, d := testHeraServer(t)
	ambiguous := &model.Task{
		Name: "amb", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/amb",
	}
	testutil.NoError(t, d.Add(ambiguous))

	// Create two separate orchestrators both binding to the same task.
	for _, orchName := range []string{"orchA", "orchB"} {
		orch, err := d.CreateHeraOrchestrator(orchName)
		testutil.NoError(t, err)
		_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID,
			Name:           "coord",
			Kind:           db.HeraKindCoordinator,
		}, ambiguous.ID, ambiguous.Worktree)
		testutil.NoError(t, err)
	}

	// resolveCallerRole without orchestrator param → ErrHeraAmbiguous path.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"working"
		}`, ambiguous.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "multiple orchestrators")
	testutil.Contains(t, cr.Content[0].Text, "orchA")
	testutil.Contains(t, cr.Content[0].Text, "orchB")
}

func TestHera_CallerContext_OrchestratorParam(t *testing.T) {
	s, d := testHeraServer(t)
	ambiguous := &model.Task{
		Name: "amb2", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/amb2",
	}
	testutil.NoError(t, d.Add(ambiguous))

	for _, orchName := range []string{"orchA", "orchB"} {
		orch, err := d.CreateHeraOrchestrator(orchName)
		testutil.NoError(t, err)
		_, _, err = d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
			OrchestratorID: orch.ID,
			Name:           "coord",
			Kind:           db.HeraKindCoordinator,
		}, ambiguous.ID, ambiguous.Worktree)
		testutil.NoError(t, err)
	}

	// Supplying orchestrator= disambiguates.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"orchA","status":"working"
		}`, ambiguous.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "orchA")
}

// --- hera_send ---

func setupOrchWithWorker(t *testing.T, s *Server, d *db.DB) (coordWorktree, workerWorktree string) {
	t.Helper()
	coordTask := &model.Task{
		Name: "coord-task", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/coord-" + t.Name(),
	}
	testutil.NoError(t, d.Add(coordTask))

	workerTask := &model.Task{
		Name: "worker-task", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/worker-" + t.Name(),
	}
	testutil.NoError(t, d.Add(workerTask))

	// Bootstrap orch with coordinator.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"test-orch","coordinator_role_name":"coord"
		}`, coordTask.Worktree)),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("hera_new_orchestrator failed: %s", cr.Content[0].Text)
	}

	// Attach worker.
	resp = doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"test-orch","role_name":"w1","kind":"worker"
		}`, workerTask.Worktree)),
	})
	cr = callResult(t, resp)
	if cr.IsError {
		t.Fatalf("hera_join failed: %s", cr.Content[0].Text)
	}

	return coordTask.Worktree, workerTask.Worktree
}

func TestHera_Send_DefaultRoute_WorkerToCoordinator(t *testing.T) {
	s, d := testHeraServer(t)
	_, workerWt := setupOrchWithWorker(t, s, d)

	// Worker sends without explicit "to" → defaults to coordinator.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"hello coord","tldr":"greeting"
		}`, workerWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "Message sent")
	testutil.Contains(t, cr.Content[0].Text, "**to**: coord")
}

func TestHera_Send_ExplicitTo(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, workerWt := setupOrchWithWorker(t, s, d)
	_ = workerWt

	// Coordinator sends with explicit "to": worker.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"w1","body":"task for you","tldr":"assignment"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**to**: w1")
}

func TestHera_Send_CoordinatorMustSupplyTo(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	// Coordinator without "to" → error.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"x","tldr":"y"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "coordinator senders must supply")
}

func TestHera_Send_UnknownRecipient(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"to":"ghost","body":"hello","tldr":"hi"
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "not found")
}

// --- hera_inbox ---

func TestHera_Inbox_MarksRead(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, workerWt := setupOrchWithWorker(t, s, d)

	// Worker sends a message to the coordinator.
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"status update","tldr":"update"
		}`, workerWt)),
	})

	// Coordinator reads inbox.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_inbox",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "status update")

	// Second inbox call → empty (message was marked read).
	resp2 := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_inbox",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp2))
	cr2 := callResult(t, resp2)
	testutil.Equal(t, cr2.IsError, false)
	testutil.Contains(t, cr2.Content[0].Text, "Inbox empty")
}

// --- hera_mark_read ---

func TestHera_MarkRead(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, workerWt := setupOrchWithWorker(t, s, d)

	// Worker sends two messages.
	var msgIDs []int64
	for i, tldr := range []string{"first", "second"} {
		r := doRequest(t, s, "tools/call", ToolCallParams{
			Name: "hera_send",
			Arguments: json.RawMessage(fmt.Sprintf(`{
				"cwd":%q,"body":"msg %d","tldr":%q
			}`, workerWt, i, tldr)),
		})
		cr := callResult(t, r)
		if cr.IsError {
			t.Fatalf("send failed: %s", cr.Content[0].Text)
		}
		// Extract message_id from output.
		var mid int64
		for _, line := range strings.Split(cr.Content[0].Text, "\n") {
			fmt.Sscanf(line, "- **message_id**: %d", &mid) //nolint:errcheck
		}
		if mid != 0 {
			msgIDs = append(msgIDs, mid)
		}
	}

	idsJSON, _ := json.Marshal(msgIDs)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_mark_read",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"message_ids":%s
		}`, coordWt, idsJSON)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, fmt.Sprintf("Marked %d", len(msgIDs)))
}

// --- hera_status ---

func TestHera_Status(t *testing.T) {
	s, d := testHeraServer(t)
	_, workerWt := setupOrchWithWorker(t, s, d)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"working"
		}`, workerWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**status**: working")
}

func TestHera_Status_InvalidEnum(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/status-bad")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"flying"
		}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "invalid status")
}

func TestHera_Status_MetaMirrorSoftFail(t *testing.T) {
	// Verifies that a SetMeta failure does NOT undo the successful status update.
	// We can't inject SetMeta failures with a real *db.DB, but we can verify
	// that when the call succeeds the response is a success (not an error), and
	// that the tool completes even when SetMeta is a no-op (nil task meta row).
	s, d := testHeraServer(t)
	_, workerWt := setupOrchWithWorker(t, s, d)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_status",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"status":"done"
		}`, workerWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	// Must succeed regardless of meta mirror outcome.
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**status**: done")
}

// --- hera_get_messages ---

func TestHera_GetMessages_HappyPath(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, workerWt := setupOrchWithWorker(t, s, d)

	// Worker sends to coord.
	sendResp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"hello coord","tldr":"hi"
		}`, workerWt)),
	})
	cr := callResult(t, sendResp)
	if cr.IsError {
		t.Fatalf("send failed: %s", cr.Content[0].Text)
	}
	var msgID int64
	for _, line := range strings.Split(cr.Content[0].Text, "\n") {
		fmt.Sscanf(line, "- **message_id**: %d", &msgID) //nolint:errcheck
	}

	// Coord fetches by ID.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_get_messages",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"ids":[%d]
		}`, coordWt, msgID)),
	})
	testutil.NoError(t, respErr(resp))
	cr2 := callResult(t, resp)
	testutil.Equal(t, cr2.IsError, false)
	testutil.Contains(t, cr2.Content[0].Text, "hello coord")
}

func TestHera_GetMessages_AccessRule(t *testing.T) {
	// Messages outside the caller's orchestrator must return access denied.
	s, d := testHeraServer(t)

	// Set up orch1 with coord1 + worker1.
	orchWt1, workerWt1 := setupOrchWithWorker(t, s, d)

	// Set up a second independent orchestrator with a separate task.
	coordTask2 := &model.Task{
		Name: "coord2", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/coord2-" + t.Name(),
	}
	testutil.NoError(t, d.Add(coordTask2))
	workerTask2 := &model.Task{
		Name: "worker2", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/worker2-" + t.Name(),
	}
	testutil.NoError(t, d.Add(workerTask2))

	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"orch2","coordinator_role_name":"coord2"
		}`, coordTask2.Worktree)),
	})
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"orch2","role_name":"w2","kind":"worker"
		}`, workerTask2.Worktree)),
	})

	// Worker in orch2 sends to coord2.
	sendResp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_send",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"body":"private","tldr":"private"
		}`, workerWt1)), // worker1 in orch1
	})
	cr := callResult(t, sendResp)
	if cr.IsError {
		t.Fatalf("send failed: %s", cr.Content[0].Text)
	}
	var msgID int64
	for _, line := range strings.Split(cr.Content[0].Text, "\n") {
		fmt.Sscanf(line, "- **message_id**: %d", &msgID) //nolint:errcheck
	}

	// Worker2 (orch2) tries to fetch a message from orch1 → access denied.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_get_messages",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"orch2","ids":[%d]
		}`, workerTask2.Worktree, msgID)),
	})
	testutil.NoError(t, respErr(resp))
	cr2 := callResult(t, resp)
	testutil.Equal(t, cr2.IsError, false) // per-ID error, not top-level error

	// Result JSON must contain "access denied" for the requested ID.
	testutil.Contains(t, cr2.Content[0].Text, "access denied")

	_ = orchWt1 // used only to set up orch1
}

func TestHera_GetMessages_NotFound(t *testing.T) {
	s, d := testHeraServer(t)
	coordWt, _ := setupOrchWithWorker(t, s, d)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_get_messages",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"ids":[99999]
		}`, coordWt)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "not found")
}

// --- stub tools ---

func TestHera_SpawnWorker_UnboundCallerRejected(t *testing.T) {
	// A cwd that matches no task (hence no caller role) is rejected before any
	// spawn — exercises the caller-resolution failure path.
	s, _ := testHeraServer(t)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: json.RawMessage(`{"cwd":"/tmp","prompt":"do something"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "no task matches cwd")
}

func TestHera_TreeUpdates_Stub(t *testing.T) {
	s, _ := testHeraServer(t)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_tree_updates",
		Arguments: json.RawMessage(`{"cwd":"/tmp"}`),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "M5")
}

// --- disabled (heraEnabled=false) ---

func TestHera_Disabled_ReturnsError(t *testing.T) {
	// Without SetHeraService, all hera tools must return "hera not configured".
	s, _, _ := testServerWithTasks()
	for _, name := range []string{
		"hera_new_orchestrator", "hera_join", "hera_send",
		"hera_inbox", "hera_mark_read", "hera_status", "hera_get_messages",
	} {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      name,
			Arguments: json.RawMessage(`{"cwd":"/tmp"}`),
		})
		testutil.NoError(t, respErr(resp))
		cr := callResult(t, resp)
		if !cr.IsError {
			t.Errorf("%s: expected error when hera not configured, got success", name)
			continue
		}
		testutil.Contains(t, cr.Content[0].Text, "hera not configured")
	}
}

// --- Join claim mode reports unread count ---

func TestHera_Join_ClaimMode_UnreadCount(t *testing.T) {
	s, d := testHeraServer(t)
	task := addHeraTestTask(t, d, "/wt/uc")

	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_new_orchestrator",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"name":"ucorch","coordinator_role_name":"coord"
		}`, task.Worktree)),
	})

	// Add a second worker task and send messages to the coordinator.
	wt := &model.Task{
		Name: "wt-uc", Status: model.StatusInProgress,
		Project: "p", Worktree: "/wt/uc-worker",
	}
	testutil.NoError(t, d.Add(wt))
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd":%q,"orchestrator":"ucorch","role_name":"w","kind":"worker"
		}`, wt.Worktree)),
	})

	// Worker sends 2 messages.
	for i := 0; i < 2; i++ {
		doRequest(t, s, "tools/call", ToolCallParams{
			Name: "hera_send",
			Arguments: json.RawMessage(fmt.Sprintf(`{
				"cwd":%q,"body":"msg","tldr":"t%d"
			}`, wt.Worktree, i)),
		})
	}

	// Coordinator claims — must report 2 unread (HeraInbox, not Service.Inbox).
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q}`, task.Worktree)),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**unread_message_count**: 2")
}

// --- hera_spawn_worker (M4) ---

// spawnArgs builds the JSON arguments for a hera_spawn_worker call.
func spawnArgs(cwd, prompt, roleName, project, orch string) json.RawMessage {
	m := map[string]string{"cwd": cwd, "prompt": prompt}
	if roleName != "" {
		m["role_name"] = roleName
	}
	if project != "" {
		m["project"] = project
	}
	if orch != "" {
		m["orchestrator"] = orch
	}
	b, _ := json.Marshal(m)
	return b
}

func TestHera_SpawnWorker_HappyPath(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "Implement the parser", "parser-work", "", ""),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**orchestrator**: myorch")
	testutil.Contains(t, cr.Content[0].Text, "**role_name**: parser-work")
	testutil.Contains(t, cr.Content[0].Text, "**kind**: worker")
	testutil.Contains(t, cr.Content[0].Text, "**project**: test-project")

	// The orchestrator now has a worker role with the verbatim prompt.
	orch, err := d.HeraOrchestratorByName("myorch")
	testutil.NoError(t, err)
	workers, err := d.ListHeraRolesByKind(orch.ID, db.HeraKindWorker)
	testutil.NoError(t, err)
	testutil.Equal(t, len(workers), 1)
	testutil.Equal(t, workers[0].Name, "parser-work")
	testutil.Equal(t, workers[0].Prompt, "Implement the parser") // verbatim, no prefix
	testutil.Equal(t, workers[0].ArgusProject, "test-project")

	// The worker has a live binding under the orchestrator.
	bnd, err := d.HeraLiveBindingByTaskAndOrchestrator(workerTaskByName(t, d, "parser-work").ID, orch.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, bnd.RoleID, workers[0].ID)

	// meta:hera.role=worker stamped on the new task.
	wt := workerTaskByName(t, d, "parser-work")
	meta, err := d.ListMeta(wt.ID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	found := false
	for _, e := range meta {
		if e.Key == db.HeraMetaKeyRole && e.Value == string(db.HeraKindWorker) {
			found = true
		}
	}
	if !found {
		t.Error("meta:hera.role=worker not stamped on spawned task")
	}

	// The delivered task prompt is orientation-prefixed, with the verbatim
	// prompt after the separator.
	testutil.Contains(t, wt.Prompt, "born bound to hera orchestrator \"myorch\" under coordinator \"coord\"")
	testutil.Contains(t, wt.Prompt, "\n\n---\n\nImplement the parser")
}

// workerTaskByName finds the spawned worker task by its (unique) name.
func workerTaskByName(t *testing.T, d *db.DB, name string) *model.Task {
	t.Helper()
	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	for _, tk := range tasks {
		if tk.Name == name {
			return tk
		}
	}
	t.Fatalf("worker task %q not found", name)
	return nil
}

func TestHera_SpawnWorker_NonCoordinatorRejected(t *testing.T) {
	s, d := testHeraServer(t)
	// Bootstrap an orchestrator, then attach a worker from a different task and
	// have THAT worker try to spawn.
	seedCoordinator(t, s, d, "myorch", "/wt/coord")
	workerTask := addHeraTestTask(t, d, "/wt/worker")
	doRequest(t, s, "tools/call", ToolCallParams{
		Name: "hera_join",
		Arguments: json.RawMessage(fmt.Sprintf(`{
			"cwd": %q, "orchestrator": "myorch", "role_name": "w1", "kind": "worker"
		}`, workerTask.Worktree)),
	})

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(workerTask.Worktree, "do a thing", "", "", ""),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "only coordinators may spawn workers")
}

func TestHera_SpawnWorker_ProjectOverride(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "do a thing", "w", "other-project", ""),
	})
	testutil.NoError(t, respErr(resp))
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**project**: other-project")
}

func TestHera_SpawnWorker_MissingArgs(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	t.Run("missing cwd", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "hera_spawn_worker",
			Arguments: json.RawMessage(`{"prompt":"x"}`),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "cwd is required")
	})
	t.Run("missing prompt", func(t *testing.T) {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name:      "hera_spawn_worker",
			Arguments: json.RawMessage(fmt.Sprintf(`{"cwd":%q,"prompt":"   "}`, coord.Worktree)),
		})
		cr := callResult(t, resp)
		testutil.Equal(t, cr.IsError, true)
		testutil.Contains(t, cr.Content[0].Text, "prompt is required")
	})
}

func TestHera_SpawnWorker_NotConfigured(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	s.heraSpawn = nil // simulate a daemon that didn't wire the spawner

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "x", "", "", ""),
	})
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "spawn not configured")
}

func TestHera_SpawnWorker_SpawnerError(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")
	s.heraSpawn = func(HeraSpawnInput) (*HeraSpawnResult, error) {
		return nil, fmt.Errorf("worktree creation failed")
	}
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "x", "", "", ""),
	})
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "spawn worker: worktree creation failed")
}

func TestHera_SpawnWorker_MultiCoordinatorDisambiguation(t *testing.T) {
	s, d := testHeraServer(t)
	// One task is coordinator in two orchestrators.
	coord := addHeraTestTask(t, d, "/wt/multi")
	for _, orch := range []string{"orchA", "orchB"} {
		resp := doRequest(t, s, "tools/call", ToolCallParams{
			Name: "hera_new_orchestrator",
			Arguments: json.RawMessage(fmt.Sprintf(`{
				"cwd": %q, "name": %q, "coordinator_role_name": "coord"
			}`, coord.Worktree, orch)),
		})
		testutil.Equal(t, callResult(t, resp).IsError, false)
	}

	// Spawn without orchestrator → ambiguous.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "x", "w", "", ""),
	})
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, true)
	testutil.Contains(t, cr.Content[0].Text, "multiple orchestrators")

	// Spawn with orchestrator=orchB → resolves to that one.
	resp2 := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "x", "w", "", "orchB"),
	})
	cr2 := callResult(t, resp2)
	testutil.Equal(t, cr2.IsError, false)
	testutil.Contains(t, cr2.Content[0].Text, "**orchestrator**: orchB")
}

func TestHera_SpawnWorker_RoleNameSlugAndUnique(t *testing.T) {
	s, d := testHeraServer(t)
	coord := seedCoordinator(t, s, d, "myorch", "/wt/coord")

	// role_name omitted → slug derived from the prompt.
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "Fix the login bug!", "", "", ""),
	})
	cr := callResult(t, resp)
	testutil.Equal(t, cr.IsError, false)
	testutil.Contains(t, cr.Content[0].Text, "**role_name**: fix-the-login-bug")

	// A second spawn that derives the same base gets uniquified.
	resp2 := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "hera_spawn_worker",
		Arguments: spawnArgs(coord.Worktree, "Fix the login bug!", "", "", ""),
	})
	cr2 := callResult(t, resp2)
	testutil.Equal(t, cr2.IsError, false)
	testutil.Contains(t, cr2.Content[0].Text, "**role_name**: fix-the-login-bug-2")
}

func TestDeriveHeraWorkerName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fix the login bug", "fix-the-login-bug"},
		{"", "worker"},
		{"!!!", "worker"},
		{"  Spaces  here ", "spaces-here"},
		{"this prompt is definitely much longer than forty characters total", "this-prompt-is-definitely-much-longer-th"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			testutil.Equal(t, deriveHeraWorkerName(c.in), c.want)
		})
	}
}
