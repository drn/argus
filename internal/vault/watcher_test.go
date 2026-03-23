package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestIsEligibleFile(t *testing.T) {
	t.Run("accepts .md files", func(t *testing.T) {
		testutil.True(t, IsEligibleFile("/vault/fix-bug.md"))
	})

	t.Run("rejects hidden files", func(t *testing.T) {
		testutil.False(t, IsEligibleFile("/vault/.hidden.md"))
	})

	t.Run("rejects icloud placeholders", func(t *testing.T) {
		testutil.False(t, IsEligibleFile("/vault/fix-bug.md.icloud"))
	})

	t.Run("rejects non-md files", func(t *testing.T) {
		testutil.False(t, IsEligibleFile("/vault/notes.txt"))
	})

	t.Run("rejects plain icloud files", func(t *testing.T) {
		testutil.False(t, IsEligibleFile("/vault/data.icloud"))
	})
}

func TestWatcher_processFile(t *testing.T) {
	t.Run("creates task for new md file", func(t *testing.T) {
		database := testDB(t)
		vaultDir := t.TempDir()

		// Write a test .md file.
		mdPath := filepath.Join(vaultDir, "fix-login.md")
		os.WriteFile(mdPath, []byte("Fix the login page"), 0o644)

		// Set up config with a todo project.
		database.SetConfigValue("defaults.todo_project", "test-proj")
		database.SetConfigValue("defaults.backend", "claude")

		var created *model.Task
		creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
			created = &model.Task{
				ID:       "test-id",
				Name:     name,
				Prompt:   prompt,
				Project:  project,
				TodoPath: todoPath,
			}
			return created, nil
		}

		w := NewWatcher(database, vaultDir, creator)
		w.processFile(mdPath)

		testutil.NotNil(t, created)
		testutil.Equal(t, created.Name, "fix-login")
		testutil.Contains(t, created.Prompt, "Fix the login page")
		testutil.Equal(t, created.Project, "test-proj")
		testutil.Equal(t, created.TodoPath, mdPath)
	})

	t.Run("skips empty files", func(t *testing.T) {
		database := testDB(t)
		vaultDir := t.TempDir()

		mdPath := filepath.Join(vaultDir, "empty.md")
		os.WriteFile(mdPath, []byte(""), 0o644)

		database.SetConfigValue("defaults.todo_project", "test-proj")

		called := false
		creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
			called = true
			return &model.Task{ID: "x"}, nil
		}

		w := NewWatcher(database, vaultDir, creator)
		w.processFile(mdPath)

		testutil.False(t, called)
	})

	t.Run("skips when no todo project configured", func(t *testing.T) {
		database := testDB(t)
		vaultDir := t.TempDir()

		mdPath := filepath.Join(vaultDir, "task.md")
		os.WriteFile(mdPath, []byte("some content"), 0o644)

		// Don't set defaults.todo_project.

		called := false
		creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
			called = true
			return &model.Task{ID: "x"}, nil
		}

		w := NewWatcher(database, vaultDir, creator)
		w.processFile(mdPath)

		testutil.False(t, called)
	})

	t.Run("deduplicates existing tasks", func(t *testing.T) {
		database := testDB(t)
		vaultDir := t.TempDir()

		mdPath := filepath.Join(vaultDir, "existing.md")
		os.WriteFile(mdPath, []byte("content"), 0o644)

		database.SetConfigValue("defaults.todo_project", "test-proj")

		// Pre-create a task linked to this path.
		task := &model.Task{Name: "existing", TodoPath: mdPath}
		database.Add(task)

		called := false
		creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
			called = true
			return &model.Task{ID: "x"}, nil
		}

		w := NewWatcher(database, vaultDir, creator)
		w.processFile(mdPath)

		testutil.False(t, called)
	})
}

func TestWatcher_scanExisting(t *testing.T) {
	database := testDB(t)
	vaultDir := t.TempDir()

	// Create some files.
	os.WriteFile(filepath.Join(vaultDir, "task1.md"), []byte("content1"), 0o644)
	os.WriteFile(filepath.Join(vaultDir, "task2.md"), []byte("content2"), 0o644)
	os.WriteFile(filepath.Join(vaultDir, ".hidden.md"), []byte("hidden"), 0o644)
	os.WriteFile(filepath.Join(vaultDir, "readme.txt"), []byte("text"), 0o644)

	database.SetConfigValue("defaults.todo_project", "proj")

	var created []string
	creator := func(name, prompt, project, todoPath string) (*model.Task, error) {
		created = append(created, name)
		return &model.Task{ID: name, Name: name, TodoPath: todoPath}, nil
	}

	w := NewWatcher(database, vaultDir, creator)
	w.scanExisting()

	testutil.Equal(t, len(created), 2)
	// Both .md files should be processed (order depends on ReadDir).
	testutil.True(t, contains(created, "task1"))
	testutil.True(t, contains(created, "task2"))
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
