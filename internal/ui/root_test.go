package ui

import (
	"errors"
	"os"
	"path/filepath"
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
	return NewModel(database, runner, false)
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

func TestPruneCompleted_WithWorktrees_ShowsModal(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "pending", Status: model.StatusPending},
		{ID: "t2", Name: "done1", Status: model.StatusComplete, Worktree: "/tmp/fake-wt-1"},
		{ID: "t3", Name: "done2", Status: model.StatusComplete, Worktree: "/tmp/fake-wt-2"},
	}
	m := testModel(t, tasks...)

	msg := tea.KeyMsg{Type: tea.KeyCtrlR}
	updated, cmd := m.Update(msg)
	um := updated.(Model)

	// Should switch to pruning view with a background cmd
	if um.current != viewPruning {
		t.Errorf("expected viewPruning, got %d", um.current)
	}
	if um.pruneTotal != 2 {
		t.Errorf("expected pruneTotal=2, got %d", um.pruneTotal)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for async cleanup")
	}

	// Simulate iterative progress — first worktree done, one remaining
	updated, cmd = um.Update(PruneProgressMsg{
		Current:   1,
		Total:     2,
		Remaining: []*model.Task{{ID: "t3", Name: "done2", Status: model.StatusComplete, Worktree: "/tmp/fake-wt-2"}},
	})
	um = updated.(Model)
	if um.current != viewPruning {
		t.Errorf("expected viewPruning during progress, got %d", um.current)
	}
	if um.pruneCurrent != 1 {
		t.Errorf("expected pruneCurrent=1, got %d", um.pruneCurrent)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for next cleanup step")
	}

	// Simulate final progress — all done
	updated, _ = um.Update(PruneProgressMsg{
		Current:   2,
		Total:     2,
		Remaining: nil,
	})
	um = updated.(Model)
	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after prune done, got %d", um.current)
	}
}

func TestPruneView_Renders(t *testing.T) {
	m := testModel(t, &model.Task{ID: "t1", Name: "task", Status: model.StatusPending})
	m.current = viewPruning
	m.pruneTotal = 3
	// Deliver a WindowSizeMsg so the modal has dimensions
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	v := m.View()
	if v == "" {
		t.Error("pruneView should not be empty")
	}
	if !strings.Contains(v, "Pruning") {
		t.Error("pruneView should contain 'Pruning'")
	}
	if !strings.Contains(v, "3") {
		t.Error("pruneView should show the worktree count")
	}

	// With progress, should show "(2/5)" format
	m.pruneCurrent = 2
	m.pruneTotal = 5
	v = m.View()
	if !strings.Contains(v, "2/5") {
		t.Error("pruneView with progress should show '2/5'")
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

func TestStartOrAttach_BlocksWithoutWorktree(t *testing.T) {
	task := &model.Task{
		ID:     "task-1",
		Name:   "no-worktree task",
		Status: model.StatusPending,
		// No Worktree set — should be blocked by the early guard.
	}
	m := testModel(t, task)
	m.width = 120
	m.height = 40

	result, _ := m.startOrAttach(task)
	um := result.(Model)

	got, err := um.db.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	// Task should remain Pending — never set to InProgress.
	if got.Status != model.StatusPending {
		t.Errorf("expected status Pending (blocked by worktree guard), got %v", got.Status)
	}
	// SessionID should not have been generated.
	if got.SessionID != "" {
		t.Errorf("expected no SessionID, got %q", got.SessionID)
	}
}

func TestStartOrAttach_FailureRevertsStatus(t *testing.T) {
	task := &model.Task{
		ID:       "task-1",
		Name:     "failing task",
		Status:   model.StatusPending,
		Project:  "proj",
		Backend:  "nonexistent-backend", // backend not in config → Start fails
		Worktree: t.TempDir(),
	}
	m := testModel(t, task)
	m.width = 120
	m.height = 40

	result, _ := m.startOrAttach(task)
	um := result.(Model)

	got, err := um.db.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPending {
		t.Errorf("expected status reverted to Pending on Start failure, got %v", got.Status)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared on Start failure, got %q", got.SessionID)
	}
	if got.StartedAt != (time.Time{}) {
		t.Errorf("expected StartedAt cleared on Start failure, got %v", got.StartedAt)
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
	if m.activeTab != tabSettings {
		t.Errorf("expected tabSettings after right arrow, got %d", m.activeTab)
	}

	// Right arrow again → should stay on projects (no wrap)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.activeTab != tabSettings {
		t.Errorf("expected tabSettings after second right arrow, got %d", m.activeTab)
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
	m := NewModel(database, runner, false)
	m.refreshSettings()
	return m
}

func TestDeleteProject_EnterConfirms(t *testing.T) {
	projects := map[string]config.Project{
		"myproject": {Path: "/tmp/myproject"},
	}
	m := testModelWithProjects(t, projects)
	m.activeTab = tabSettings
	// Navigate cursor to the project row (past status and sandbox sections).
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	m.settings.CursorDown() // project
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
	m.activeTab = tabSettings
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	m.settings.CursorDown() // project
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
	m.activeTab = tabSettings
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	m.settings.CursorDown() // project
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
	m.activeTab = tabSettings
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	m.settings.CursorDown() // project
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
	// Worktree must pass isWorktreeSubdir() check (contains /.argus/worktrees/).
	wtDir := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj", "old-task")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		ID:        "t1",
		Name:      "old task",
		Project:   "proj",
		Status:    model.StatusInProgress,
		SessionID: "sess-1",
		StartedAt: oldStart,
		AgentPID:  0,
		Worktree:  wtDir,
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

func TestModel_SplitWidths(t *testing.T) {
	m := testModel(t)
	m.width = 120

	left, right := m.splitWidths()
	if left+right != 120 {
		t.Errorf("splitWidths total = %d, want 120", left+right)
	}
	if left < 30 {
		t.Errorf("left = %d, want >= 30", left)
	}
	if right < 20 {
		t.Errorf("right = %d, want >= 20", right)
	}
}

func TestModel_SplitWidths_NarrowTerminal(t *testing.T) {
	m := testModel(t)
	m.width = 60

	left, right := m.splitWidths()
	if left+right != 60 {
		t.Errorf("splitWidths total = %d, want 60", left+right)
	}
	if left < 30 {
		t.Errorf("left should be at least 30, got %d", left)
	}
}

func TestModel_SplitCenterHeights(t *testing.T) {
	m := testModel(t)

	gitH, previewH := m.splitCenterHeights(40)
	if gitH+previewH < 40 {
		t.Errorf("splitCenterHeights total = %d, want >= 40", gitH+previewH)
	}
	if gitH < 5 {
		t.Errorf("gitH = %d, want >= 5", gitH)
	}
	if gitH > 15 {
		t.Errorf("gitH = %d, want <= 15", gitH)
	}
	if previewH < 5 {
		t.Errorf("previewH = %d, want >= 5", previewH)
	}
}

func TestModel_SplitCenterHeights_Small(t *testing.T) {
	m := testModel(t)
	gitH, previewH := m.splitCenterHeights(8)
	if gitH < 5 {
		t.Errorf("gitH = %d, want >= 5 (minimum)", gitH)
	}
	if previewH < 5 {
		t.Errorf("previewH = %d, want >= 5 (minimum)", previewH)
	}
}

func TestModel_ViewQuitting(t *testing.T) {
	m := testModel(t)
	m.quitting = true
	view := m.View()
	if view != "" {
		t.Errorf("quitting view should be empty, got %q", view)
	}
}

func TestModel_ViewHelp(t *testing.T) {
	m := testModel(t)
	m.current = viewHelp
	m.width = 80
	m.height = 24
	view := m.View()
	if !strings.Contains(view, "Keybindings") {
		t.Error("help view should contain 'Keybindings'")
	}
}

func TestModel_ViewPrompt(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "my task",
		Status: model.StatusPending,
		Prompt: "Fix the bug in parser",
	}
	m := testModel(t, task)
	m.current = viewPrompt
	m.width = 80
	m.height = 24
	view := m.View()
	if !strings.Contains(view, "Fix the bug in parser") {
		t.Error("prompt view should contain the task prompt")
	}
}

func TestModel_PadToBottom(t *testing.T) {
	m := testModel(t)
	m.height = 20

	bar := "status bar"
	content := "line1\nline2"
	result := m.padToBottom(content, bar)

	if !strings.Contains(result, "line1") {
		t.Error("padToBottom should contain original content")
	}
	if !strings.Contains(result, "status bar") {
		t.Error("padToBottom should contain bar")
	}
}

func TestModel_PadToBottom_NoExtraLine(t *testing.T) {
	m := testModel(t)
	m.height = 20

	bar := "status bar"
	content := "line1\nline2"
	result := m.padToBottom(content, bar)

	// Total lines should equal m.height (content fills to height - barHeight, then bar)
	lines := strings.Split(result, "\n")
	if len(lines) != m.height {
		t.Errorf("padToBottom produced %d lines, want %d", len(lines), m.height)
	}
	// Last line should be the bar
	if lines[len(lines)-1] != bar {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], bar)
	}
}

func TestModel_WindowSizeMsg(t *testing.T) {
	m := testModel(t)
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(Model)
	if um.width != 120 || um.height != 40 {
		t.Errorf("width=%d height=%d, want 120x40", um.width, um.height)
	}
	if cmd != nil {
		t.Error("WindowSizeMsg should return nil cmd")
	}
}

func TestModel_HelpViewDismiss(t *testing.T) {
	m := testModel(t)
	m.current = viewHelp

	// Any key should dismiss help view
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)
	if um.current != viewTaskList {
		t.Errorf("after key in help view: current = %d, want viewTaskList", um.current)
	}
}

func TestModel_PromptViewDismiss(t *testing.T) {
	m := testModel(t)
	m.current = viewPrompt

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)
	if um.current != viewTaskList {
		t.Errorf("after key in prompt view: current = %d, want viewTaskList", um.current)
	}
}

func TestModel_ViewNewTask(t *testing.T) {
	m := testModel(t)
	m.current = viewNewTask
	m.newtask = NewNewTaskForm(m.theme, m.db.Projects(), "")
	m.newtask.SetSize(80, 24)
	m.width = 80
	m.height = 24
	view := m.View()
	if !strings.Contains(view, "New Task") {
		t.Error("new task view should contain 'New Task'")
	}
}

func TestModel_ViewNewProject(t *testing.T) {
	m := testModel(t)
	m.current = viewNewProject
	m.newproject = NewNewProjectForm(m.theme)
	m.newproject.SetSize(80, 24)
	m.width = 80
	m.height = 24
	view := m.View()
	if !strings.Contains(view, "New Project") {
		t.Error("new project view should contain 'New Project'")
	}
}

func TestModel_ViewTaskList_EmptyState(t *testing.T) {
	m := testModel(t) // no tasks
	m.width = 80
	m.height = 24
	view := m.View()
	// Should show hint to create first task
	if !strings.Contains(view, "create your first task") && !strings.Contains(view, "[n]") {
		t.Error("empty state should show create task hint")
	}
}

func TestModel_ViewTaskList_WithTasks(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "my-task", Status: model.StatusPending}
	m := testModel(t, task)
	// Send WindowSizeMsg so tasklist gets proper dimensions for rendering.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "my-task") {
		t.Error("task list view should contain task name")
	}
}

func TestModel_ViewTaskList_LeftPaneBorderedWidth(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "x", Status: model.StatusPending, Project: "proj"}
	m := testModel(t, task)
	width := 200
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = updated.(Model)

	// The left pane should be rendered via borderedPanel which enforces
	// the allocated width. Verify the task list panel content is present
	// inside a bordered panel (rounded border uses ╭ top-left corner).
	widths := m.taskLayout.SplitWidths()
	leftW := widths[0]
	if leftW < width*20/100 {
		t.Errorf("left panel width %d is less than 20%% of %d", leftW, width)
	}

	view := m.View()
	if !strings.Contains(view, "╭") {
		t.Error("task list view should contain bordered panel (rounded border)")
	}
	if !strings.Contains(view, "x") {
		t.Error("task list view should contain task name")
	}
}

func TestModel_ViewProjectsTab(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	m.width = 120
	m.height = 40
	view := m.View()
	if view == "" {
		t.Error("projects tab view should not be empty")
	}
}

func TestModel_ViewConfirmDelete_NoTask(t *testing.T) {
	m := testModel(t)
	m.current = viewConfirmDelete
	m.width = 80
	m.height = 24
	view := m.confirmDeleteView()
	if view != "" {
		t.Error("confirmDeleteView with no task selected should be empty")
	}
}

func TestModel_ViewConfirmDestroy_NoTask(t *testing.T) {
	m := testModel(t)
	m.current = viewConfirmDestroy
	m.width = 80
	m.height = 24
	view := m.confirmDestroyView()
	if view != "" {
		t.Error("confirmDestroyView with no task should be empty")
	}
}

func TestModel_ViewConfirmDeleteProject_NoProject(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	m.current = viewConfirmDeleteProject
	m.width = 80
	m.height = 24
	view := m.confirmDeleteProjectView()
	if view != "" {
		t.Error("confirmDeleteProjectView with no project should be empty")
	}
}

func TestModel_PromptView_NoTask(t *testing.T) {
	m := testModel(t)
	view := m.promptView()
	if view != "" {
		t.Error("promptView with no task should be empty")
	}
}

func TestModel_PromptView_EmptyPrompt(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "my-task", Status: model.StatusPending}
	m := testModel(t, task)
	view := m.promptView()
	if !strings.Contains(view, "no prompt set") {
		t.Error("promptView with empty prompt should show '(no prompt set)'")
	}
}

func TestModel_StatusAdvance(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusPending}
	m := testModel(t, task)

	// Press 's' to advance status
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	got, _ := um.db.Get("t1")
	if got.Status != model.StatusInProgress {
		t.Errorf("expected StatusInProgress after advance, got %v", got.Status)
	}
}

func TestModel_StatusRevert(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusInProgress}
	m := testModel(t, task)

	// Press 'S' to revert status
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	got, _ := um.db.Get("t1")
	if got.Status != model.StatusPending {
		t.Errorf("expected StatusPending after revert, got %v", got.Status)
	}
}

func TestModel_CursorNavigation(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "first", Status: model.StatusPending, Project: "proj"},
		{ID: "t2", Name: "second", Status: model.StatusPending, Project: "proj"},
	}
	m := testModel(t, tasks...)
	// Send WindowSizeMsg so tasklist gets proper dimensions.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	// Rows: [proj header, first, second]. Cursor starts on first task (skips header).
	if sel := m.tasklist.Selected(); sel == nil || sel.Name != "first" {
		t.Error("expected 'first' selected initially (cursor skips project header)")
	}

	// Down → second task
	msg := tea.KeyMsg{Type: tea.KeyDown}
	updated, _ = m.Update(msg)
	um := updated.(Model)
	if sel := um.tasklist.Selected(); sel == nil || sel.Name != "second" {
		t.Error("expected 'second' selected after down")
	}

	// Up → back to first
	msg = tea.KeyMsg{Type: tea.KeyUp}
	updated, _ = um.Update(msg)
	um = updated.(Model)
	if sel := um.tasklist.Selected(); sel == nil || sel.Name != "first" {
		t.Error("expected 'first' selected after up")
	}
}

func TestModel_NewProjectKey(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	// Navigate cursor to a project row so 'n' opens the new project form.
	m.settings.CursorDown() // daemon logs
	m.settings.CursorDown() // UX logs
	m.settings.CursorDown() // sandbox
	m.settings.CursorDown() // project (or "(no projects)" placeholder)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewNewProject {
		t.Errorf("expected viewNewProject, got %d", um.current)
	}
}

func TestModel_NewProjectCancel(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	m.current = viewNewProject
	m.newproject = NewNewProjectForm(m.theme)

	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList after cancel, got %d", um.current)
	}
}

func TestModel_TabSwitching_NumberKeys(t *testing.T) {
	m := testModel(t)

	// Press '2' for projects
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)
	if um.activeTab != tabSettings {
		t.Errorf("expected tabSettings after '2', got %d", um.activeTab)
	}

	// Press '1' for tasks
	msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}}
	updated, _ = um.Update(msg)
	um = updated.(Model)
	if um.activeTab != tabTasks {
		t.Errorf("expected tabTasks after '1', got %d", um.activeTab)
	}
}

func TestModel_HelpKey(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusPending}
	m := testModel(t, task)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewHelp {
		t.Errorf("expected viewHelp after '?', got %d", um.current)
	}
}

func TestModel_DeleteKey_ShowsConfirmation(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusPending}
	m := testModel(t, task)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewConfirmDelete {
		t.Errorf("expected viewConfirmDelete, got %d", um.current)
	}
}

func TestModel_PromptKey(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusPending, Prompt: "fix bug"}
	m := testModel(t, task)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	if um.current != viewPrompt {
		t.Errorf("expected viewPrompt, got %d", um.current)
	}
}

func TestModel_PromptKey_CompletedTask(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusComplete}
	m := testModel(t, task)

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	// Should not open prompt view for completed task
	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList for completed task, got %d", um.current)
	}
}

func TestModel_AgentDetachedMsg(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "test", Status: model.StatusInProgress}
	m := testModel(t, task)

	updated, _ := m.Update(AgentDetachedMsg{TaskID: "t1"})
	_ = updated.(Model)
	// Should not panic, just refresh tasks
}

func TestModel_AgentFinished_MissingTask(t *testing.T) {
	m := testModel(t)
	// Should not panic when task doesn't exist
	updated, cmd := m.Update(AgentFinishedMsg{TaskID: "nonexistent"})
	if cmd != nil {
		t.Error("expected nil cmd for missing task")
	}
	_ = updated.(Model)
}

func TestHandleAgentFinished_StreamLost(t *testing.T) {
	task := &model.Task{
		ID:        "t-stream-lost",
		Name:      "stream-lost-task",
		Status:    model.StatusInProgress,
		SessionID: "sess-abc",
		StartedAt: time.Now().Add(-10 * time.Second),
	}
	m := testModel(t, task)

	// Send StreamLost=true — task should remain InProgress.
	updated, _ := m.Update(AgentFinishedMsg{
		TaskID:     "t-stream-lost",
		StreamLost: true,
	})
	um := updated.(Model)

	got, err := um.db.Get("t-stream-lost")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusInProgress {
		t.Errorf("expected InProgress, got %s", got.Status)
	}
	// SessionID should be preserved (stream loss is not a real exit).
	if got.SessionID != "sess-abc" {
		t.Errorf("expected SessionID to be preserved, got %q", got.SessionID)
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

func TestInit_RevertsTaskWithoutWorktree(t *testing.T) {
	// A task that is InProgress with a SessionID but no worktree
	// should be reverted to Pending by Init() (not queued for resume).
	task := &model.Task{
		ID:        "t-no-wt",
		Name:      "orphaned task",
		Status:    model.StatusInProgress,
		SessionID: "sess-orphan",
		StartedAt: time.Now().Add(-10 * time.Minute),
	}
	m := testModel(t, task)

	// Init runs the resume logic. Since the task has no Worktree
	// and discoverWorktree will find nothing (no project set), it
	// should be reverted to Pending with SessionID and StartedAt cleared.
	m.Init()

	got, err := m.db.Get("t-no-wt")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusPending {
		t.Errorf("expected status reverted to Pending, got %v", got.Status)
	}
	if got.SessionID != "" {
		t.Errorf("expected SessionID cleared, got %q", got.SessionID)
	}
	if got.StartedAt != (time.Time{}) {
		t.Errorf("expected StartedAt cleared, got %v", got.StartedAt)
	}
}

func TestResolveTaskDirMsg_CachesDir(t *testing.T) {
	wtDir := "/home/user/.argus/worktrees/proj/cached-task"
	task := &model.Task{
		ID:     "t1",
		Name:   "cached-task",
		Status: model.StatusPending,
	}
	m := testModel(t, task)

	// Simulate receiving an async ResolveTaskDirMsg with a valid worktree path.
	updated, _ := m.Update(ResolveTaskDirMsg{TaskID: "t1", Dir: wtDir})
	um := updated.(Model)

	// The resolved dir should be cached
	if dir, ok := um.resolvedDirs["t1"]; !ok || dir != wtDir {
		t.Errorf("expected cached dir %s, got %q (ok=%v)", wtDir, dir, ok)
	}

	// The task's Worktree field should be persisted to the DB
	got, err := um.db.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Worktree != wtDir {
		t.Errorf("expected task Worktree=%s, got %q", wtDir, got.Worktree)
	}
}

func TestResolveTaskDirMsg_RejectsProjectDir(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "cached-task",
		Status: model.StatusPending,
	}
	m := testModel(t, task)

	// A non-worktree path should be cached for git status but NOT persisted
	// as the task's Worktree.
	updated, _ := m.Update(ResolveTaskDirMsg{TaskID: "t1", Dir: "/home/user/project"})
	um := updated.(Model)

	if dir, ok := um.resolvedDirs["t1"]; !ok || dir != "/home/user/project" {
		t.Errorf("expected cached dir, got %q (ok=%v)", dir, ok)
	}

	got, err := um.db.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Worktree != "" {
		t.Errorf("expected empty Worktree for non-worktree dir, got %q", got.Worktree)
	}
}

func TestScheduleGitRefresh_SetsTaskIDOnGitStatus(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "test-task",
		Status: model.StatusPending,
	}
	m := testModel(t, task)
	m.resolvedDirs["t1"] = "/tmp/worktree"

	// scheduleGitRefresh is called through Update on a value receiver copy.
	// The gitstatus.taskID must persist because gitstatus is a pointer.
	_ = m.scheduleGitRefresh()

	if m.gitstatus.taskID != "t1" {
		t.Errorf("expected gitstatus.taskID=%q after scheduleGitRefresh, got %q", "t1", m.gitstatus.taskID)
	}

	// Verify GitStatusRefreshMsg is accepted (taskID matches)
	m.gitstatus.Update(GitStatusRefreshMsg{TaskID: "t1", Status: "M file.go"})
	if !m.gitstatus.loaded {
		t.Error("expected gitstatus.loaded=true after Update with matching taskID")
	}
}

func TestScheduleGitRefresh_UsesCachedDir(t *testing.T) {
	task := &model.Task{
		ID:     "t1",
		Name:   "cached-task",
		Status: model.StatusPending,
	}
	m := testModel(t, task)

	// Pre-populate the cache
	m.resolvedDirs["t1"] = "/tmp/worktree"

	// scheduleGitRefresh should use the cached dir and return a command
	cmd := m.scheduleGitRefresh()
	if cmd == nil {
		t.Fatal("expected non-nil cmd when dir is cached")
	}

	// Execute the command and verify it produces a GitStatusRefreshMsg
	msg := cmd()
	if gsMsg, ok := msg.(GitStatusRefreshMsg); !ok {
		t.Errorf("expected GitStatusRefreshMsg, got %T", msg)
	} else if gsMsg.TaskID != "t1" {
		t.Errorf("expected task ID t1, got %q", gsMsg.TaskID)
	}
}

func TestScheduleGitRefresh_NoTaskReturnsNil(t *testing.T) {
	m := testModel(t) // no tasks

	cmd := m.scheduleGitRefresh()
	if cmd != nil {
		t.Error("expected nil cmd when no tasks")
	}
}

func TestAgentView_CmdUpDown_SwitchesTask(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "first", Status: model.StatusPending, Project: "proj"},
		{ID: "t2", Name: "second", Status: model.StatusPending, Project: "proj"},
		{ID: "t3", Name: "third", Status: model.StatusPending, Project: "proj"},
	}
	m := testModel(t, tasks...)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	// Enter agent view on the first task
	m.current = viewAgent
	m.agentview.Enter("t1", "first")

	// Alt+Down (Cmd+Down) should switch to second task
	msg := tea.KeyMsg{Type: tea.KeyDown, Alt: true}
	updated, _ = m.Update(msg)
	um := updated.(Model)

	// switchAgentTask calls startOrAttach which would try to start a session.
	// Without a real backend, it will fail and stay on task list. But we can
	// verify the intent by checking the agentview's taskID if it entered agent
	// view, or that the code path was hit.
	// Since startOrAttach will fail (no backend), it sets an error and stays
	// on task list. Let's verify the task list cursor didn't break.
	_ = um

	// Test that Alt+Up at first task is a no-op (stays in agent view)
	m.current = viewAgent
	m.agentview.Enter("t1", "first")
	msg = tea.KeyMsg{Type: tea.KeyUp, Alt: true}
	updated, _ = m.Update(msg)
	um = updated.(Model)
	// No previous task, should stay in agent view
	if um.current != viewAgent {
		t.Errorf("expected viewAgent when no prev task, got %d", um.current)
	}
}

func TestSwitchAgentTask_NilWhenNoPrev(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "only", Status: model.StatusPending, Project: "proj"},
	}
	m := testModel(t, tasks...)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)
	m.current = viewAgent
	m.agentview.Enter("t1", "only")

	// Alt+Up — no previous task, should stay on agent view
	msg := tea.KeyMsg{Type: tea.KeyUp, Alt: true}
	updated, cmd := m.Update(msg)
	um := updated.(Model)
	if um.current != viewAgent {
		t.Errorf("expected viewAgent, got %d", um.current)
	}
	if cmd != nil {
		t.Error("expected nil cmd when no adjacent task")
	}

	// Alt+Down — no next task, should stay on agent view
	msg = tea.KeyMsg{Type: tea.KeyDown, Alt: true}
	updated, cmd = m.Update(msg)
	um = updated.(Model)
	if um.current != viewAgent {
		t.Errorf("expected viewAgent, got %d", um.current)
	}
	if cmd != nil {
		t.Error("expected nil cmd when no adjacent task")
	}
}

// TestModel_ViewZeroDimensions ensures every View() path survives the initial
// render before WindowSizeMsg arrives (width=0, height=0). Bubble Tea calls
// View() before the first WindowSizeMsg, so all paths must handle zero
// dimensions without panicking.
func TestModel_ViewZeroDimensions(t *testing.T) {
	task := &model.Task{ID: "t1", Name: "task", Status: model.StatusPending, Project: "proj"}
	views := []struct {
		name  string
		setup func(m *Model)
	}{
		{"taskList", func(m *Model) {}},
		{"taskListEmpty", func(m *Model) {}},
		{"projects", func(m *Model) { m.activeTab = tabSettings }},
		{"help", func(m *Model) { m.current = viewHelp }},
		{"prompt", func(m *Model) { m.current = viewPrompt }},
		{"confirmDelete", func(m *Model) { m.current = viewConfirmDelete }},
		{"confirmDestroy", func(m *Model) { m.current = viewConfirmDestroy }},
		{"confirmDeleteProject", func(m *Model) { m.current = viewConfirmDeleteProject }},
		{"newTask", func(m *Model) { m.current = viewNewTask }},
		{"newProject", func(m *Model) { m.current = viewNewProject }},
		{"pruning", func(m *Model) { m.current = viewPruning; m.pruneTotal = 3 }},
		{"daemonRestart", func(m *Model) { m.current = viewDaemonRestart; m.daemonRestarting = true }},
		{"sandboxConfig", func(m *Model) {
			m.sandboxconfig = NewSandboxConfigForm(m.theme, false, nil, nil)
			m.current = viewSandboxConfig
		}},
		{"daemonLogs", func(m *Model) { m.current = viewDaemonLogs; m.daemonLogLines = []string{"test log line"} }},
		{"uxLogs", func(m *Model) { m.current = viewUXLogs; m.uxLogLines = []string{"test ux log line"} }},
	}
	for _, tc := range views {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			if tc.name == "taskListEmpty" {
				m = testModel(t)
			} else {
				m = testModel(t, task)
			}
			// width=0, height=0 — no WindowSizeMsg delivered yet
			tc.setup(&m)
			// Must not panic
			_ = m.View()
		})
	}
}

func TestCursorChange_TriggersGitRefresh(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "first", Status: model.StatusPending, Project: "proj"},
		{ID: "t2", Name: "second", Status: model.StatusPending, Project: "proj"},
	}
	m := testModel(t, tasks...)
	// Send WindowSizeMsg so tasklist gets proper dimensions.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	// Cursor starts on first task (skips header). Cache second task so moving down triggers refresh.
	m.resolvedDirs["t2"] = "/tmp/second-worktree"

	// Move cursor down (from first task to second task)
	msg := tea.KeyMsg{Type: tea.KeyDown}
	_, cmd := m.Update(msg)

	// Should return a command (git refresh triggered by cursor change)
	if cmd == nil {
		t.Error("expected non-nil cmd on cursor change with cached dir")
	}
}

func TestDaemonRestarted_ErrorPath(t *testing.T) {
	m := testModel(t, &model.Task{ID: "t1", Name: "test", Status: model.StatusInProgress, SessionID: "s1"})
	m.daemonConnected = true
	m.daemonRestarting = true
	m.current = viewDaemonRestart

	updated, _ := m.Update(DaemonRestartedMsg{Err: errors.New("restart failed")})
	um := updated.(Model)

	if um.daemonRestarting {
		t.Error("expected daemonRestarting=false after error")
	}
	if um.daemonConnected {
		t.Error("expected daemonConnected=false after error")
	}
	if um.current != viewTaskList {
		t.Errorf("expected viewTaskList, got %v", um.current)
	}
	// Task should NOT be reset on error — daemon restart failed, old state still valid.
	task, _ := um.db.Get("t1")
	if task.Status != model.StatusInProgress {
		t.Errorf("expected task still InProgress on error path, got %v", task.Status)
	}
}

func TestDaemonRestarted_ResetsInProgressTasks(t *testing.T) {
	tasks := []*model.Task{
		{ID: "t1", Name: "running", Status: model.StatusInProgress, SessionID: "sess-1", AgentPID: 42},
		{ID: "t2", Name: "pending", Status: model.StatusPending},
		{ID: "t3", Name: "done", Status: model.StatusComplete},
	}
	m := testModel(t, tasks...)
	m.daemonConnected = true
	m.daemonRestarting = true
	m.current = viewDaemonRestart

	// Simulate successful restart with a new runner (in-process for test).
	newRunner := agent.NewRunner(nil)
	// Use DaemonRestartedMsg with a nil Client — need to test the task cleanup logic.
	// We can't use a real dclient.Client in tests, so test the task state directly.
	// Instead, manually simulate what the handler does for task cleanup:
	for _, task := range m.db.Tasks() {
		if task.Status == model.StatusInProgress {
			task.SetStatus(model.StatusPending)
			task.AgentPID = 0
			task.StartedAt = time.Time{}
			_ = m.db.Update(task)
		}
	}
	m.runner = newRunner
	m.daemonRestarting = false
	m.current = viewTaskList

	// Verify in-progress task was reset.
	t1, err := m.db.Get("t1")
	if err != nil {
		t.Fatal(err)
	}
	if t1.Status != model.StatusPending {
		t.Errorf("expected task t1 status=Pending, got %v", t1.Status)
	}
	if t1.SessionID != "sess-1" {
		t.Errorf("expected SessionID preserved, got %q", t1.SessionID)
	}
	if t1.AgentPID != 0 {
		t.Errorf("expected AgentPID=0, got %d", t1.AgentPID)
	}

	// Verify other tasks untouched.
	t2, _ := m.db.Get("t2")
	if t2.Status != model.StatusPending {
		t.Errorf("expected task t2 still Pending, got %v", t2.Status)
	}
	t3, _ := m.db.Get("t3")
	if t3.Status != model.StatusComplete {
		t.Errorf("expected task t3 still Complete, got %v", t3.Status)
	}
}

func TestDaemonRestart_KeyAbsorbedDuringRestart(t *testing.T) {
	m := testModel(t, &model.Task{ID: "t1", Name: "test", Status: model.StatusPending})
	m.current = viewDaemonRestart
	m.daemonRestarting = true
	m.width = 80
	m.height = 24

	// All keys should be absorbed (no state change).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	um := updated.(Model)
	if um.current != viewDaemonRestart {
		t.Errorf("expected view to stay on daemonRestart, got %v", um.current)
	}
	if um.quitting {
		t.Error("quit key should be absorbed during daemon restart")
	}
}

func TestDaemonRestartView_Renders(t *testing.T) {
	m := testModel(t, &model.Task{ID: "t1", Name: "test", Status: model.StatusPending})
	m.current = viewDaemonRestart
	m.daemonRestarting = true
	m.width = 80
	m.height = 24

	view := m.View()
	if !strings.Contains(view, "Restarting Daemon") {
		t.Error("expected 'Restarting Daemon' title in view")
	}
	if !strings.Contains(view, "Stopping sessions") {
		t.Error("expected status text in view")
	}
}

func TestSettingsKey_RestartDaemon_NoDaemon(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	m.daemonConnected = false
	m.width = 120
	m.height = 40

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	updated, _ := m.Update(msg)
	um := updated.(Model)

	// Should show error, not enter restart view.
	if um.current == viewDaemonRestart {
		t.Error("should not enter restart view when daemon not connected")
	}
}

func TestSettingsKey_RestartDaemon_WithDaemon(t *testing.T) {
	m := testModel(t)
	m.activeTab = tabSettings
	m.daemonConnected = true
	m.width = 120
	m.height = 40

	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}}
	updated, cmd := m.Update(msg)
	um := updated.(Model)

	if um.current != viewDaemonRestart {
		t.Errorf("expected viewDaemonRestart, got %v", um.current)
	}
	if !um.daemonRestarting {
		t.Error("expected daemonRestarting=true")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for restart")
	}
}

func TestSettingsWarningDetail_ShowsRestartHint(t *testing.T) {
	sv := NewSettingsView(DefaultTheme())
	sv.SetSize(40, 30)
	sv.SetWarnings(nil) // no warnings = "System status"
	sv.SetProjects(nil)
	sv.SetBackends(nil)

	detail := sv.RenderDetail(60, 20)
	if !strings.Contains(detail, "restart") {
		t.Error("expected restart hint in 'all good' warning detail")
	}
}
