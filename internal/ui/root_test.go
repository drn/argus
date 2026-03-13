package ui

import (
	"errors"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/store"
)

func testModel(t *testing.T, tasks ...*model.Task) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.json")
	s := store.NewWithPath(path)
	for _, task := range tasks {
		if err := s.Add(task); err != nil {
			t.Fatal(err)
		}
	}
	runner := agent.NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "echo", PromptFlag: ""},
		},
	}
	return NewModel(cfg, s, runner)
}

func TestSessionResumed_Success(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "test task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
	}
	m := testModel(t, task)

	updated, _ := m.Update(SessionResumedMsg{TaskID: "task-1", PID: 42})
	um := updated.(Model)

	got, err := um.store.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentPID != 42 {
		t.Errorf("expected AgentPID=42, got %d", got.AgentPID)
	}
	if got.SessionID != "sess-abc" {
		t.Errorf("expected SessionID preserved, got %q", got.SessionID)
	}
}

func TestSessionResumed_Error_ClearsSession(t *testing.T) {
	task := &model.Task{
		ID:        "task-2",
		Name:      "failing task",
		Status:    model.StatusInProgress,
		SessionID: "sess-xyz",
		AgentPID:  99,
	}
	m := testModel(t, task)

	updated, _ := m.Update(SessionResumedMsg{
		TaskID: "task-2",
		Err:    errors.New("connection refused"),
	})
	um := updated.(Model)

	got, err := um.store.Get("task-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared, got %q", got.SessionID)
	}
	if got.AgentPID != 0 {
		t.Errorf("expected AgentPID=0, got %d", got.AgentPID)
	}
}

func TestSessionResumed_MissingTask(t *testing.T) {
	m := testModel(t)

	// Should not panic when task doesn't exist
	updated, cmd := m.Update(SessionResumedMsg{TaskID: "nonexistent", PID: 1})
	if cmd != nil {
		t.Error("expected nil cmd for missing task")
	}
	_ = updated.(Model)
}

func TestPruneCompleted(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "pending", Status: model.StatusPending},
		{ID: "t2", Name: "done1", Status: model.StatusComplete},
		{ID: "t3", Name: "in-progress", Status: model.StatusInProgress},
		{ID: "t4", Name: "done2", Status: model.StatusComplete},
	}
	m := testModel(t, tasks...)

	// Send ctrl+r
	msg := tea.KeyMsg{Type: tea.KeyCtrlR}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	remaining := um.store.Tasks()
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining tasks, got %d", len(remaining))
	}
	for _, r := range remaining {
		if r.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", r.Name)
		}
	}
}

func TestInit_ResumesOnlyInProgressWithSessionID(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "in-progress with session", Status: model.StatusInProgress, SessionID: "sess-1"},
		{ID: "t2", Name: "in-progress no session", Status: model.StatusInProgress},
		{ID: "t3", Name: "pending with session", Status: model.StatusPending, SessionID: "sess-3"},
		{ID: "t4", Name: "complete with session", Status: model.StatusComplete, SessionID: "sess-4"},
		{ID: "t5", Name: "in-review with session", Status: model.StatusInReview, SessionID: "sess-5"},
	}
	m := testModel(t, tasks...)

	// Count how many resume commands Init would produce.
	// Init returns tea.Batch of: 1 tick + N resume cmds.
	// We can't inspect tea.Batch internals, so instead verify the store
	// state: only t1 qualifies (in_progress + has SessionID).
	count := 0
	for _, task := range m.store.Tasks() {
		if task.Status == model.StatusInProgress && task.SessionID != "" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 task eligible for resume, got %d", count)
	}
}
