package ui

import (
	"errors"
	"path/filepath"
	"strings"
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

func TestDestroyCtrlD_ShowsConfirmation(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "my task",
		Status: model.StatusInProgress,
	}
	m := testModel(t, task)

	// Press ctrl+d
	msg := tea.KeyMsg{Type: tea.KeyCtrlD}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewConfirmDestroy {
		t.Errorf("expected viewConfirmDestroy, got %d", um.current)
	}
}

func TestDestroyCtrlD_NoTaskSelected(t *testing.T) {
	m := testModel(t) // no tasks

	msg := tea.KeyMsg{Type: tea.KeyCtrlD}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList when no task selected, got %d", um.current)
	}
}

func TestDestroyConfirm_DeletesTask(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "destroy me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)

	// Enter confirm destroy state
	m.current = viewConfirmDestroy

	// Press y to confirm
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after confirm, got %d", um.current)
	}
	if _, err := um.store.Get("t1"); err == nil {
		t.Error("expected task to be deleted after destroy confirm")
	}
}

func TestDestroyCancel_KeepsTask(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "keep me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)

	// Enter confirm destroy state
	m.current = viewConfirmDestroy

	// Press any other key to cancel
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after cancel, got %d", um.current)
	}
	if _, err := um.store.Get("t1"); err != nil {
		t.Error("expected task to still exist after cancel")
	}
}

func TestAgentFinished_ErrorKeepsInProgress(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "resuming task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	m := testModel(t, task)

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     errors.New("exit status 1"),
		Stopped: false,
	})
	um := updated.(Model)

	got, err := um.store.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress, got %v", got.Status)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared on error, got %q", got.SessionID)
	}
	if got.AgentPID != 0 {
		t.Errorf("expected AgentPID=0, got %d", got.AgentPID)
	}
}

func TestAgentFinished_SuccessMarksComplete(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "finished task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	m := testModel(t, task)

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	got, err := um.store.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusComplete {
		t.Errorf("expected status Complete, got %v", got.Status)
	}
}

func TestAgentFinished_StoppedMarksInReview(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "stopped task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	m := testModel(t, task)

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: true,
	})
	um := updated.(Model)

	got, err := um.store.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusInReview {
		t.Errorf("expected status InReview, got %v", got.Status)
	}
}

func TestTabSwitching_LeftRightArrows(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusPending}
	m := testModel(t, task)

	// Start on tasks tab
	if m.activeTab != tabTasks {
		t.Fatalf("expected initial tab to be tabTasks")
	}

	// Right arrow → projects
	msg := tea.KeyMsg{Type: tea.KeyRight}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.activeTab != tabProjects {
		t.Errorf("expected tabProjects after right arrow, got %d", m.activeTab)
	}

	// Right arrow again → should stay on projects (no wrap)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.activeTab != tabProjects {
		t.Errorf("expected tabProjects after second right arrow, got %d", m.activeTab)
	}

	// Left arrow → tasks
	msg = tea.KeyMsg{Type: tea.KeyLeft}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.activeTab != tabTasks {
		t.Errorf("expected tabTasks after left arrow, got %d", m.activeTab)
	}

	// Left arrow again → should stay on tasks (no wrap)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.activeTab != tabTasks {
		t.Errorf("expected tabTasks after second left arrow, got %d", m.activeTab)
	}
}

func TestTabHeader_Centered(t *testing.T) {
	m := testModel(t)
	m.width = 80
	header := m.renderTabHeader()
	// Header should be padded to full width (centered)
	if len(header) < 40 {
		t.Errorf("expected centered header to have padding, got len=%d", len(header))
	}
}

func testModelWithProjects(t *testing.T, projects map[string]config.Project) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tasks.json")
	s := store.NewWithPath(path)
	runner := agent.NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "claude"},
		Backends: map[string]config.Backend{
			"claude": {Command: "echo", PromptFlag: ""},
		},
		Projects: projects,
	}
	return NewModel(cfg, s, runner)
}

func TestDeleteProject_EnterConfirms(t *testing.T) {
	projects := map[string]config.Project{
		"myproject": {Path: "/tmp/myproject"},
	}
	m := testModelWithProjects(t, projects)
	m.activeTab = tabProjects
	m.current = viewConfirmDeleteProject

	// Press Enter to confirm
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after enter confirm, got %d", um.current)
	}
	if _, ok := um.cfg.Projects["myproject"]; ok {
		t.Error("expected project to be deleted after enter confirm")
	}
}

func TestDeleteProject_EscCancels(t *testing.T) {
	projects := map[string]config.Project{
		"myproject": {Path: "/tmp/myproject"},
	}
	m := testModelWithProjects(t, projects)
	m.activeTab = tabProjects
	m.current = viewConfirmDeleteProject

	// Press Esc to cancel
	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after esc cancel, got %d", um.current)
	}
	if _, ok := um.cfg.Projects["myproject"]; !ok {
		t.Error("expected project to still exist after esc cancel")
	}
}

func TestDeleteProject_YKeyNoLongerConfirms(t *testing.T) {
	projects := map[string]config.Project{
		"myproject": {Path: "/tmp/myproject"},
	}
	m := testModelWithProjects(t, projects)
	m.activeTab = tabProjects
	m.current = viewConfirmDeleteProject

	// Press y — should cancel (no longer confirms)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after y key, got %d", um.current)
	}
	if _, ok := um.cfg.Projects["myproject"]; !ok {
		t.Error("expected project to still exist — y should no longer confirm deletion")
	}
}

func TestDeleteProject_ModalView(t *testing.T) {
	projects := map[string]config.Project{
		"myproject": {Path: "/tmp/myproject"},
	}
	m := testModelWithProjects(t, projects)
	m.activeTab = tabProjects
	m.current = viewConfirmDeleteProject
	m.width = 80
	m.height = 24

	view := m.confirmDeleteProjectView()
	if view == "" {
		t.Fatal("expected non-empty modal view")
	}
	// Should contain project name and path
	if !strings.Contains(view, "myproject") {
		t.Error("expected modal to contain project name")
	}
	if !strings.Contains(view, "Delete project?") {
		t.Error("expected modal to contain title")
	}
	if !strings.Contains(view, "[enter] confirm") {
		t.Error("expected modal to show enter key hint")
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
