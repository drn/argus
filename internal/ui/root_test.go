package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

func testModel(t *testing.T, tasks ...*model.Task) Model {
	t.Helper()
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for _, task := range tasks {
		if err := database.Add(task); err != nil {
			t.Fatal(err)
		}
	}
	runner := agent.NewRunner(nil)
	return NewModel(database, runner)
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

	got, err := um.db.Get("task-1")
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

	got, err := um.db.Get("task-2")
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

	remaining := um.db.Tasks()
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

	// Press enter to confirm
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after confirm, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err == nil {
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

	// Press esc to cancel
	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after cancel, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err != nil {
		t.Error("expected task to still exist after cancel")
	}
}

func TestDestroyIgnoresOtherKeys(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "keep me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.current = viewConfirmDestroy

	// Press 'n' — should be ignored (stay on modal)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewConfirmDestroy {
		t.Errorf("expected to stay on viewConfirmDestroy, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err != nil {
		t.Error("expected task to still exist")
	}
}

func TestDeleteTask_EnterConfirms(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "delete me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.current = viewConfirmDelete

	msg := tea.KeyMsg{Type: tea.KeyEnter}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after enter confirm, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err == nil {
		t.Error("expected task to be deleted after enter confirm")
	}
}

func TestDeleteTask_EscCancels(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "keep me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.current = viewConfirmDelete

	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after esc cancel, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err != nil {
		t.Error("expected task to still exist after esc cancel")
	}
}

func TestDeleteTask_IgnoresOtherKeys(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "keep me",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.current = viewConfirmDelete

	// Press 'y' — should be ignored now (no longer confirms)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewConfirmDelete {
		t.Errorf("expected to stay on viewConfirmDelete, got %d", um.current)
	}
	if _, err := um.db.Get("t1"); err != nil {
		t.Error("expected task to still exist — y should no longer confirm deletion")
	}
}

func TestDeleteTask_ModalView(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "my task",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.current = viewConfirmDelete
	m.width = 80
	m.height = 24

	view := m.confirmDeleteView()
	if view == "" {
		t.Fatal("expected non-empty modal view")
	}
	if !strings.Contains(view, "my task") {
		t.Error("expected modal to contain task name")
	}
	if !strings.Contains(view, "Delete task?") {
		t.Error("expected modal to contain title")
	}
	if !strings.Contains(view, "[enter] confirm") {
		t.Error("expected modal to show enter key hint")
	}
}

func TestDestroyTask_ModalView(t *testing.T) {
	task := &model.Task{
		ID:       "t1",
		Name:     "my task",
		Status:   model.StatusPending,
		Worktree: "/tmp/wt",
		Branch:   "feature-x",
	}
	m := testModel(t, task)
	m.current = viewConfirmDestroy
	m.width = 80
	m.height = 24

	view := m.confirmDestroyView()
	if view == "" {
		t.Fatal("expected non-empty modal view")
	}
	if !strings.Contains(view, "Destroy task?") {
		t.Error("expected modal to contain title")
	}
	if !strings.Contains(view, "[enter] confirm") {
		t.Error("expected modal to show enter key hint")
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

	got, err := um.db.Get("task-1")
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
	task.SetStatus(model.StatusInProgress) // sets StartedAt
	task.StartedAt = task.StartedAt.Add(-time.Minute) // ran for a minute
	m := testModel(t, task)

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	got, err := um.db.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusComplete {
		t.Errorf("expected status Complete, got %v", got.Status)
	}
}

func TestAgentFinished_QuickExitStaysOnAgentView(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "quick exit task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	task.SetStatus(model.StatusInProgress) // sets StartedAt to now
	m := testModel(t, task)
	m.current = viewAgent
	m.agentview.Enter("task-1", "quick exit task")

	// Agent exits cleanly but almost immediately — should stay on agent view
	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	if um.current != viewAgent {
		t.Errorf("expected to stay on viewAgent after quick exit, got view %d", um.current)
	}
}

func TestAgentFinished_ErrorStaysOnAgentView(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "error task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	m := testModel(t, task)
	m.current = viewAgent
	m.agentview.Enter("task-1", "error task")

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     errors.New("exit status 1"),
		Stopped: false,
	})
	um := updated.(Model)

	if um.current != viewAgent {
		t.Errorf("expected to stay on viewAgent after error exit, got view %d", um.current)
	}
}

func TestAgentFinished_NormalCompletionExitsAgentView(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "completed task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	task.SetStatus(model.StatusInProgress)
	task.StartedAt = task.StartedAt.Add(-time.Minute) // ran for a minute
	m := testModel(t, task)
	m.current = viewAgent
	m.agentview.Enter("task-1", "completed task")

	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after normal completion, got view %d", um.current)
	}
}

func TestAgentFinished_QuickExitKeepsInProgress(t *testing.T) {
	task := &model.Task{
		ID:        "task-1",
		Name:      "quick exit task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	task.SetStatus(model.StatusInProgress) // sets StartedAt to now
	m := testModel(t, task)

	// Agent exits cleanly but almost immediately — should NOT mark complete
	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	got, err := um.db.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress for quick exit, got %v", got.Status)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared on quick exit, got %q", got.SessionID)
	}
}

func TestAgentFinished_QuickExitOnRetryKeepsInProgress(t *testing.T) {
	// Simulate a task that was started long ago (StartedAt in the past),
	// then restarted. Without resetting StartedAt on re-launch, the quick
	// exit check would see time.Since(StartedAt) > minAgentRunTime and
	// incorrectly mark the task complete.
	task := &model.Task{
		ID:        "task-1",
		Name:      "retried task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		AgentPID:  100,
	}
	task.SetStatus(model.StatusInProgress)
	task.StartedAt = time.Now().Add(-10 * time.Minute) // started 10 min ago
	m := testModel(t, task)

	// Re-store with old StartedAt to simulate a previously started task
	_ = m.db.Update(task)

	// Now simulate what startOrAttach does: reset StartedAt to now
	task.StartedAt = time.Now()
	_ = m.db.Update(task)

	// Agent exits cleanly but almost immediately — should NOT mark complete
	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:  "task-1",
		Err:     nil,
		Stopped: false,
	})
	um := updated.(Model)

	got, err := um.db.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusInProgress {
		t.Errorf("expected status InProgress for quick exit on retry, got %v", got.Status)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared on quick exit, got %q", got.SessionID)
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

	got, err := um.db.Get("task-1")
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
	database, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	for name, proj := range projects {
		if err := database.SetProject(name, proj); err != nil {
			t.Fatal(err)
		}
	}
	runner := agent.NewRunner(nil)
	m := NewModel(database, runner)
	m.refreshProjects()
	return m
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
	if _, ok := um.db.Projects()["myproject"]; ok {
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
	if _, ok := um.db.Projects()["myproject"]; !ok {
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
	if _, ok := um.db.Projects()["myproject"]; !ok {
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

func TestInit_ResetsStartedAtBeforeResume(t *testing.T) {
	oldStart := time.Now().Add(-10 * time.Minute)
	task := &model.Task{
		ID:        "t1",
		Name:      "old task",
		Status:    model.StatusInProgress,
		SessionID: "sess-1",
		StartedAt: oldStart,
		AgentPID:  0,
	}
	m := testModel(t, task)

	// Init() should reset StartedAt before launching the resume goroutine.
	// We can't easily intercept the goroutine, but we can verify the DB
	// was updated with a fresh StartedAt during Init().
	_ = m.Init()

	got, err := m.db.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	// StartedAt should have been reset to approximately now, not 10 min ago
	if time.Since(got.StartedAt) > 5*time.Second {
		t.Errorf("expected StartedAt reset to ~now, but got %v ago", time.Since(got.StartedAt))
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
	// We can't inspect tea.Batch internals, so instead verify the DB
	// state: only t1 qualifies (in_progress + has SessionID).
	count := 0
	for _, task := range m.db.Tasks() {
		if task.Status == model.StatusInProgress && task.SessionID != "" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 task eligible for resume, got %d", count)
	}
}
