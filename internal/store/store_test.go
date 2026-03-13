package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/model"
)

func tmpStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewWithPath(filepath.Join(dir, "tasks.json"))
}

func TestStore_LoadEmpty(t *testing.T) {
	s := tmpStore(t)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if len(s.Tasks()) != 0 {
		t.Error("expected empty tasks")
	}
}

func TestStore_AddAndGet(t *testing.T) {
	s := tmpStore(t)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}

	task := &model.Task{Name: "test task"}
	if err := s.Add(task); err != nil {
		t.Fatal(err)
	}

	if task.ID == "" {
		t.Error("expected generated ID")
	}
	if task.CreatedAt.IsZero() {
		t.Error("expected generated CreatedAt")
	}

	got, err := s.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test task" {
		t.Errorf("got name %q", got.Name)
	}
}

func TestStore_AddPreservesExistingID(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	task := &model.Task{ID: "custom-id", Name: "test"}
	if err := s.Add(task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "custom-id" {
		t.Errorf("ID was changed to %q", task.ID)
	}
}

func TestStore_Update(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	task := &model.Task{Name: "original"}
	_ = s.Add(task)

	task.Name = "updated"
	if err := s.Update(task); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Get(task.ID)
	if got.Name != "updated" {
		t.Errorf("expected updated, got %q", got.Name)
	}
}

func TestStore_UpdateNotFound(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	if err := s.Update(&model.Task{ID: "nonexistent"}); err == nil {
		t.Error("expected error")
	}
}

func TestStore_Delete(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	task := &model.Task{Name: "delete me"}
	_ = s.Add(task)

	if err := s.Delete(task.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(task.ID); err == nil {
		t.Error("expected not found after delete")
	}
	if len(s.Tasks()) != 0 {
		t.Error("expected empty tasks after delete")
	}
}

func TestStore_DeleteNotFound(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	if err := s.Delete("nonexistent"); err == nil {
		t.Error("expected error")
	}
}

func TestStore_GetNotFound(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	if _, err := s.Get("nonexistent"); err == nil {
		t.Error("expected error")
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.json")

	// Write with one store instance
	s1 := NewWithPath(path)
	_ = s1.Load()
	_ = s1.Add(&model.Task{Name: "persisted"})

	// Read with a fresh instance
	s2 := NewWithPath(path)
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	tasks := s2.Tasks()
	if len(tasks) != 1 || tasks[0].Name != "persisted" {
		t.Errorf("persistence failed: got %d tasks", len(tasks))
	}
}

func TestStore_TasksReturnsCopy(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()
	_ = s.Add(&model.Task{Name: "a"})

	tasks := s.Tasks()
	tasks[0] = nil // mutate the copy

	got, err := s.Get(s.Tasks()[0].ID)
	if err != nil || got.Name != "a" {
		t.Error("internal tasks should not be affected by copy mutation")
	}
}

func TestStore_PruneCompleted(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	pending := &model.Task{Name: "pending", Status: model.StatusPending}
	done1 := &model.Task{Name: "done1", Status: model.StatusComplete}
	inProg := &model.Task{Name: "in progress", Status: model.StatusInProgress}
	done2 := &model.Task{Name: "done2", Status: model.StatusComplete}
	_ = s.Add(pending)
	_ = s.Add(done1)
	_ = s.Add(inProg)
	_ = s.Add(done2)

	pruned, err := s.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 2 {
		t.Errorf("expected 2 pruned, got %d", len(pruned))
	}
	remaining := s.Tasks()
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(remaining))
	}
	for _, r := range remaining {
		if r.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", r.Name)
		}
	}
}

func TestStore_PruneCompleted_NoneToRemove(t *testing.T) {
	s := tmpStore(t)
	_ = s.Load()

	_ = s.Add(&model.Task{Name: "pending", Status: model.StatusPending})

	pruned, err := s.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != nil {
		t.Errorf("expected nil pruned, got %d", len(pruned))
	}
	if len(s.Tasks()) != 1 {
		t.Error("expected task count unchanged")
	}
}

func TestStore_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "tasks.json")
	s := NewWithPath(path)
	_ = s.Load()

	if err := s.Add(&model.Task{Name: "test"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "sub", "dir")); err != nil {
		t.Error("expected directory to be created")
	}
}
