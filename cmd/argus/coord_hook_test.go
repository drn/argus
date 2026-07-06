package main

import (
	"bytes"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// --- `argus coord-hook` Stop-hook subcommand (add-coordinator-context-management) ---
//
// These tests pin the `coordinator-context-management` delta spec's
// "Context-budget Stop hook stamps a live signal and nudges over budget"
// requirement. None of runCoordHook, coordHookEnv, or stopHookStdin exist
// yet (Stage 4 adds the `argus coord-hook` subcommand) — this file fails to
// compile until then, proving the gap per tasks.md 1.3.
//
// The seams below (coordHookEnv's function fields) are the contract the
// Stage 4 implementation is expected to satisfy: gate on ARGUS_TASK_ID +
// resolved role kind BEFORE touching the transcript or the REST API, always
// stamp context_size for a coordinator regardless of budget, and emit a Stop
// hook "block" decision (Claude Code's hook contract: JSON on stdout) only
// when at/over budget. Role resolution, transcript tailing, task_meta
// stamping, and budget lookup are injected via coordHookEnv rather than
// wired to the real daemon/REST/DB, matching this repo's function-field
// injection convention (context/knowledge/testing.md) so the gating and
// nudge-recurrence logic can be unit tested without a live daemon.
//
// coordHookEnv, stopHookStdin, and runCoordHook's exact shape are a
// reasonable proposal, not a mandate — Stage 4 may adjust the seam as long as
// the five scenarios below (no-op on missing ARGUS_TASK_ID, no-op on
// non-coordinator, unconditional stamp, over-budget nudge, nudge
// recurrence/stop) keep passing.

// fakeCoordHookEnv builds a coordHookEnv backed by simple in-memory fakes,
// recording every call so tests can assert exactly what fired.
type fakeCoordHookEnv struct {
	getenv          map[string]string
	roleKind        string // "" means "no role bound" (also covers ARGUS_TASK_ID unset)
	roleKindErr     error
	contextSize     int
	readContextErr  error
	budget          int
	budgetErr       error
	stampedTaskID   string
	stampedSize     int
	stampCalled     bool
	stampErr        error
	resolveCalled   bool
	resolvedTaskID  string
	readCalled      bool
	readTranscript  string
	budgetCalled    bool
	budgetForTaskID string
}

func (f *fakeCoordHookEnv) env() coordHookEnv {
	return coordHookEnv{
		Getenv: func(key string) string { return f.getenv[key] },
		ResolveRoleKind: func(taskID string) (string, error) {
			f.resolveCalled = true
			f.resolvedTaskID = taskID
			return f.roleKind, f.roleKindErr
		},
		ReadContextSize: func(transcriptPath string) (int, error) {
			f.readCalled = true
			f.readTranscript = transcriptPath
			return f.contextSize, f.readContextErr
		},
		StampContextSize: func(taskID string, size int) error {
			f.stampCalled = true
			f.stampedTaskID = taskID
			f.stampedSize = size
			return f.stampErr
		},
		Budget: func(taskID string) (int, error) {
			f.budgetCalled = true
			f.budgetForTaskID = taskID
			return f.budget, f.budgetErr
		},
	}
}

func stopHookStdin(transcriptPath string) *bytes.Reader {
	return bytes.NewReader([]byte(`{"transcript_path":"` + transcriptPath + `"}`))
}

// TestCoordHook_NoTaskID_NoOp pins "Non-hera session is a no-op": with no
// ARGUS_TASK_ID set, the hook must exit with no side effects — no role
// resolution, no transcript read, no task_meta write, no blocking decision.
func TestCoordHook_NoTaskID_NoOp(t *testing.T) {
	f := &fakeCoordHookEnv{getenv: map[string]string{}}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, false)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	testutil.Equal(t, f.budgetCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_NonCoordinatorRole_NoOp pins "Worker session is a no-op":
// with ARGUS_TASK_ID set but the bound role resolving to a non-coordinator
// kind, the hook must exit with no side effects.
func TestCoordHook_NonCoordinatorRole_NoOp(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:   map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind: "worker",
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.resolveCalled, true)
	testutil.Equal(t, f.readCalled, false)
	testutil.Equal(t, f.stampCalled, false)
	if strings.Contains(out.String(), "block") {
		t.Errorf("worker no-op path must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_Coordinator_AlwaysStampsContextSize pins "Context size is
// always stamped for a coordinator session": task_meta(hera, context_size)
// must be overwritten regardless of whether the budget is exceeded.
func TestCoordHook_Coordinator_AlwaysStampsContextSize(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 1000, // well under budget
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.readCalled, true)
	testutil.Equal(t, f.readTranscript, "/tmp/transcript.jsonl")
	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedTaskID, "task-1")
	testutil.Equal(t, f.stampedSize, 1000)
	if strings.Contains(out.String(), "block") {
		t.Errorf("under-budget coordinator must not emit a block decision; got stdout=%q", out.String())
	}
}

// TestCoordHook_Coordinator_OverBudget_EmitsNudge pins "Over-budget nudge
// repeats every turn until resolved" (first-turn half): at/over budget must
// emit a Stop hook block decision instructing the coordinator to reach a safe
// seam and recycle.
func TestCoordHook_Coordinator_OverBudget_EmitsNudge(t *testing.T) {
	f := &fakeCoordHookEnv{
		getenv:      map[string]string{"ARGUS_TASK_ID": "task-1"},
		roleKind:    "coordinator",
		contextSize: 250000,
		budget:      200000,
	}
	var out, errOut bytes.Buffer

	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out, &errOut, f.env())

	testutil.Equal(t, f.stampCalled, true)
	testutil.Equal(t, f.stampedSize, 250000)
	testutil.Contains(t, out.String(), "block")
	testutil.Contains(t, out.String(), "seam")
}

// TestCoordHook_OverBudgetNudge_RecursThenStops pins both "Over-budget nudge
// repeats every turn until resolved" (recurrence across two independent
// invocations, since each Stop event is a fresh process) and "Nudge stops the
// turn context drops back below budget".
func TestCoordHook_OverBudgetNudge_RecursThenStops(t *testing.T) {
	env := map[string]string{"ARGUS_TASK_ID": "task-1"}

	// Turn 1: over budget.
	f1 := &fakeCoordHookEnv{getenv: env, roleKind: "coordinator", contextSize: 250000, budget: 200000}
	var out1, errOut1 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out1, &errOut1, f1.env())
	testutil.Contains(t, out1.String(), "block")

	// Turn 2: still over budget — the nudge must recur (each hook invocation
	// re-evaluates fresh; nothing is remembered between processes).
	f2 := &fakeCoordHookEnv{getenv: env, roleKind: "coordinator", contextSize: 260000, budget: 200000}
	var out2, errOut2 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out2, &errOut2, f2.env())
	testutil.Contains(t, out2.String(), "block")

	// Turn 3: a recycle reset context_size back under budget — the nudge must
	// stop the very turn the condition becomes false.
	f3 := &fakeCoordHookEnv{getenv: env, roleKind: "coordinator", contextSize: 500, budget: 200000}
	var out3, errOut3 bytes.Buffer
	runCoordHook(stopHookStdin("/tmp/transcript.jsonl"), &out3, &errOut3, f3.env())
	if strings.Contains(out3.String(), "block") {
		t.Errorf("nudge must stop once context_size drops below budget; got stdout=%q", out3.String())
	}
	testutil.Equal(t, f3.stampCalled, true) // still stamps, just doesn't block
}

// --- realCoordHookEnv's production wiring ---
//
// The tests above pin runCoordHook's pure gating/nudge logic via injected
// fakes. These exercise the real implementations those fakes stand in for —
// transcript tailing (pure file I/O), token-file discovery, and the actual
// REST round trip against a live daemon — so the wiring itself (paths,
// header names, JSON field/key names) is proven, not just the contract.

// TestReadContextSizeReal_TailsLatestAssistantUsage confirms the scan finds
// the LAST assistant message's usage, tolerating blank and non-JSON lines
// and non-assistant event types interleaved in a real transcript.
func TestReadContextSizeReal_TailsLatestAssistantUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	lines := []string{
		`{"type":"user","message":{}}`,
		`{"type":"assistant","message":{"usage":{"cache_read_input_tokens":100}}}`,
		"",
		"not json",
		`{"type":"assistant","message":{"usage":{"cache_read_input_tokens":42000}}}`,
	}
	testutil.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600))

	size, err := readContextSizeReal(path)
	testutil.NoError(t, err)
	testutil.Equal(t, size, 42000)
}

func TestReadContextSizeReal_MissingFile_Errors(t *testing.T) {
	_, err := readContextSizeReal(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil {
		t.Fatal("expected an error for a missing transcript file")
	}
}

// TestDiscoverAPIToken_ReadsWellKnownPath pins the well-known path
// (db.DataDir()/api-token) the real daemon itself loads/creates at boot.
func TestDiscoverAPIToken_ReadsWellKnownPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	testutil.NoError(t, os.MkdirAll(db.DataDir(), 0o700))
	testutil.NoError(t, os.WriteFile(filepath.Join(db.DataDir(), "api-token"), []byte("deadbeef\n"), 0o600))

	token, err := discoverAPIToken()
	testutil.NoError(t, err)
	testutil.Equal(t, token, "deadbeef")
}

func TestDiscoverAPIToken_MissingFile_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := discoverAPIToken(); err == nil {
		t.Fatal("expected an error when the token file is absent")
	}
}

// TestDiscoverAPIPort_NoDaemon_Errors covers coordHookDial's connect-failure
// branch without needing a live daemon.
func TestDiscoverAPIPort_NoDaemon_Errors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := discoverAPIPort(); err == nil {
		t.Fatal("expected an error when no daemon is listening")
	}
}

// waitForCoordHookDaemon polls Daemon.Ports over the Unix socket until the
// REST API has finished binding — Serve accepts RPC connections before the
// API listener (started later, in its own branch) reports a nonzero port.
func waitForCoordHookDaemon(t *testing.T, sockPath string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", sockPath); err == nil {
			_, _ = conn.Write([]byte("R"))
			client := jsonrpc.NewClient(conn)
			var resp daemon.PortsResp
			callErr := client.Call("Daemon.Ports", &daemon.Empty{}, &resp)
			_ = client.Close()
			if callErr == nil && resp.APIPort != 0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon REST API did not come up in time")
}

// TestCoordHookReal_REST runs a REAL daemon (under a fake HOME, so
// daemon.DefaultSocketPath()/db.DataDir() — neither injectable — resolve
// into a throwaway tree) and drives resolveRoleKindReal, stampContextSizeReal,
// and budgetReal against it end-to-end: RPC port discovery, the api-token
// file the daemon itself creates, and the actual PUT/GET /api/tasks/{id}/meta
// and GET /api/config routes.
//
// HOME must be a SHORT path from os.MkdirTemp, not t.TempDir(): the latter
// nests under the test name plus a run counter (".../TestCoordHookReal_REST/001"),
// and daemon.sock's full path then trips macOS's 104-byte sun_path limit —
// Serve's Listen fails with "bind: invalid argument" and the socket never
// appears (context/knowledge/gotchas/daemon-rpc.md).
func TestCoordHookReal_REST(t *testing.T) {
	home, err := os.MkdirTemp("", "ch")
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)

	database, err := db.Open(db.DefaultPath())
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	testutil.NoError(t, database.SetConfigValue("api.enabled", "true"))

	d := daemon.New(database)
	sockPath := daemon.DefaultSocketPath()
	go d.Serve(sockPath) //nolint:errcheck
	t.Cleanup(d.Shutdown)
	waitForCoordHookDaemon(t, sockPath)

	task := &model.Task{ID: "coord-task-1", Name: "coord", Status: model.StatusInProgress, Project: "test"}
	testutil.NoError(t, database.Add(task))
	testutil.NoError(t, database.SetMeta(task.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindCoordinator)))

	kind, err := resolveRoleKindReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, kind, string(db.HeraKindCoordinator))

	testutil.NoError(t, stampContextSizeReal(task.ID, 12345))
	entries, err := database.ListMeta(task.ID, db.HeraMetaNamespace)
	testutil.NoError(t, err)
	var stamped string
	for _, e := range entries {
		if e.Key == db.HeraMetaKeyContextSize {
			stamped = e.Value
		}
	}
	testutil.Equal(t, stamped, "12345")

	budget, err := budgetReal(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, budget, 200000)

	if _, err := resolveRoleKindReal("no-such-task"); err == nil {
		t.Fatal("expected an error resolving role kind for an unknown task id")
	}
}
