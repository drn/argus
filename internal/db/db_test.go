package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- Task tests ---

func TestDB_AddAndGet(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "test task"}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Error("expected generated ID")
	}
	if task.CreatedAt.IsZero() {
		t.Error("expected generated CreatedAt")
	}

	got, err := d.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "test task" {
		t.Errorf("got name %q", got.Name)
	}
}

func TestDB_AddPreservesExistingID(t *testing.T) {
	d := testDB(t)

	task := &model.Task{ID: "custom-id", Name: "test"}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}
	if task.ID != "custom-id" {
		t.Errorf("ID was changed to %q", task.ID)
	}
}

func TestDB_Update(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "original"}
	_ = d.Add(task)

	task.Name = "updated"
	if err := d.Update(task); err != nil {
		t.Fatal(err)
	}

	got, _ := d.Get(task.ID)
	if got.Name != "updated" {
		t.Errorf("expected updated, got %q", got.Name)
	}
}

func TestDB_UpdateNotFound(t *testing.T) {
	d := testDB(t)
	if err := d.Update(&model.Task{ID: "nonexistent"}); err == nil {
		t.Error("expected error")
	}
}

func TestDB_Delete(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "delete me"}
	_ = d.Add(task)

	if err := d.Delete(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Get(task.ID); err == nil {
		t.Error("expected not found after delete")
	}
	if len(d.Tasks()) != 0 {
		t.Error("expected empty tasks after delete")
	}
}

func TestDB_DeleteNotFound(t *testing.T) {
	d := testDB(t)
	if err := d.Delete("nonexistent"); err == nil {
		t.Error("expected error")
	}
}

func TestDB_GetNotFound(t *testing.T) {
	d := testDB(t)
	if _, err := d.Get("nonexistent"); err == nil {
		t.Error("expected error")
	}
}

func TestDB_PruneCompleted(t *testing.T) {
	d := testDB(t)

	_ = d.Add(&model.Task{Name: "pending", Status: model.StatusPending})
	_ = d.Add(&model.Task{Name: "done1", Status: model.StatusComplete})
	_ = d.Add(&model.Task{Name: "in progress", Status: model.StatusInProgress})
	_ = d.Add(&model.Task{Name: "done2", Status: model.StatusComplete})

	pruned, err := d.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 2 {
		t.Errorf("expected 2 pruned, got %d", len(pruned))
	}
	remaining := d.Tasks()
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining, got %d", len(remaining))
	}
	for _, r := range remaining {
		if r.Status == model.StatusComplete {
			t.Errorf("completed task %q should have been pruned", r.Name)
		}
	}
}

func TestDB_PruneCompleted_NoneToRemove(t *testing.T) {
	d := testDB(t)

	_ = d.Add(&model.Task{Name: "pending", Status: model.StatusPending})

	pruned, err := d.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if pruned != nil {
		t.Errorf("expected nil pruned, got %d", len(pruned))
	}
	if len(d.Tasks()) != 1 {
		t.Error("expected task count unchanged")
	}
}

func TestDB_TaskPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sql")

	// Write with one instance
	d1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = d1.Add(&model.Task{Name: "persisted"})
	d1.Close()

	// Read with fresh instance
	d2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	tasks := d2.Tasks()
	if len(tasks) != 1 || tasks[0].Name != "persisted" {
		t.Errorf("persistence failed: got %d tasks", len(tasks))
	}
}

// --- Project tests ---

func TestDB_Projects(t *testing.T) {
	d := testDB(t)

	if err := d.SetProject("myapp", config.Project{Path: "/home/user/myapp", Backend: "claude"}); err != nil {
		t.Fatal(err)
	}

	projects := d.Projects()
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	p, ok := projects["myapp"]
	if !ok {
		t.Fatal("myapp not found")
	}
	if p.Path != "/home/user/myapp" {
		t.Errorf("path = %q", p.Path)
	}
	if p.Backend != "claude" {
		t.Errorf("backend = %q", p.Backend)
	}
}

func TestDB_DeleteProject(t *testing.T) {
	d := testDB(t)

	_ = d.SetProject("myapp", config.Project{Path: "/tmp"})
	if err := d.DeleteProject("myapp"); err != nil {
		t.Fatal(err)
	}
	if len(d.Projects()) != 0 {
		t.Error("expected 0 projects")
	}
}

func TestDB_SetProjectUpdates(t *testing.T) {
	d := testDB(t)

	_ = d.SetProject("myapp", config.Project{Path: "/old"})
	_ = d.SetProject("myapp", config.Project{Path: "/new"})

	projects := d.Projects()
	if projects["myapp"].Path != "/new" {
		t.Errorf("expected /new, got %q", projects["myapp"].Path)
	}
}

// --- Backend tests ---

func TestDB_Backends(t *testing.T) {
	d := testDB(t)

	// Should have default backend from seedDefaults
	backends := d.Backends()
	if _, ok := backends["claude"]; !ok {
		t.Error("expected default claude backend")
	}
}

func TestDB_SetBackend(t *testing.T) {
	d := testDB(t)

	if err := d.SetBackend("codex", config.Backend{Command: "codex", PromptFlag: "--prompt"}); err != nil {
		t.Fatal(err)
	}
	backends := d.Backends()
	if b, ok := backends["codex"]; !ok {
		t.Error("codex not found")
	} else if b.Command != "codex" {
		t.Errorf("command = %q", b.Command)
	}
}

// --- Config assembly tests ---

func TestDB_Config(t *testing.T) {
	d := testDB(t)

	cfg := d.Config()
	if cfg.Defaults.Backend != "claude" {
		t.Errorf("default backend = %q", cfg.Defaults.Backend)
	}
	if cfg.Keybindings.New != "n" {
		t.Errorf("keybinding new = %q", cfg.Keybindings.New)
	}
	if !cfg.UI.ShowElapsed {
		t.Error("ShowElapsed should be true")
	}
}

func TestDB_SetConfigValue(t *testing.T) {
	d := testDB(t)

	_ = d.SetConfigValue("ui.theme", "dark")
	cfg := d.Config()
	if cfg.UI.Theme != "dark" {
		t.Errorf("theme = %q", cfg.UI.Theme)
	}
}

// --- Migration tests ---

func TestMigration_FromLegacyFiles(t *testing.T) {
	// Set up legacy files in a temp dir
	legacyDir := t.TempDir()
	argusDir := filepath.Join(legacyDir, "argus")
	os.MkdirAll(argusDir, 0o755)

	// Legacy tasks.json
	tasks := []*model.Task{
		{ID: "t1", Name: "task one", Status: model.StatusPending},
		{ID: "t2", Name: "task two", Status: model.StatusInProgress},
	}
	tasksJSON, _ := json.Marshal(tasks)
	os.WriteFile(filepath.Join(argusDir, "tasks.json"), tasksJSON, 0o644)

	// Legacy config.toml
	configTOML := `
[defaults]
backend = "codex"

[backends.codex]
command = "codex"
prompt_flag = "--prompt"

[projects.myapp]
path = "/home/user/myapp"
backend = "codex"

[keybindings]
new = "a"

[ui]
theme = "dark"
show_elapsed = false
`
	os.WriteFile(filepath.Join(argusDir, "config.toml"), []byte(configTOML), 0o644)

	// Point XDG to temp dir so migration finds legacy files
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", legacyDir)
	defer os.Setenv("XDG_CONFIG_HOME", old)

	// Open a new database — should auto-migrate
	dbPath := filepath.Join(t.TempDir(), "data.sql")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Verify tasks were imported
	dbTasks := d.Tasks()
	if len(dbTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(dbTasks))
	}

	// Verify projects were imported
	projects := d.Projects()
	if _, ok := projects["myapp"]; !ok {
		t.Error("myapp project not imported")
	}

	// Verify config was imported
	cfg := d.Config()
	if cfg.Defaults.Backend != "codex" {
		t.Errorf("default backend = %q, want codex", cfg.Defaults.Backend)
	}
	if cfg.Keybindings.New != "a" {
		t.Errorf("keybinding new = %q, want a", cfg.Keybindings.New)
	}
	if cfg.UI.Theme != "dark" {
		t.Errorf("theme = %q, want dark", cfg.UI.Theme)
	}
	if cfg.UI.ShowElapsed {
		t.Error("ShowElapsed should be false")
	}

	// Verify backends were imported
	backends := d.Backends()
	if _, ok := backends["codex"]; !ok {
		t.Error("codex backend not imported")
	}
}

func TestMigration_NoLegacyFiles(t *testing.T) {
	// Point XDG to empty temp dir
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer os.Setenv("XDG_CONFIG_HOME", old)

	dbPath := filepath.Join(t.TempDir(), "data.sql")
	d, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Should have defaults
	cfg := d.Config()
	if cfg.Defaults.Backend != "claude" {
		t.Errorf("expected default backend, got %q", cfg.Defaults.Backend)
	}
	backends := d.Backends()
	if _, ok := backends["claude"]; !ok {
		t.Error("expected default claude backend")
	}
}

func TestSeedDefaults_FixesPlaceholderBackend(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Simulate a legacy config that had "echo" as the command
	if err := d.SetBackend("claude", config.Backend{Command: "echo", PromptFlag: ""}); err != nil {
		t.Fatal(err)
	}

	// Run seedDefaults — should fix the placeholder command
	if err := d.runSeedDefaults(); err != nil {
		t.Fatal(err)
	}

	backends := d.Backends()
	b, ok := backends["claude"]
	if !ok {
		t.Fatal("expected claude backend")
	}
	if b.Command == "echo" {
		t.Errorf("seedDefaults should have replaced placeholder 'echo' command, got %q", b.Command)
	}
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["claude"].Command {
		t.Errorf("expected default command %q, got %q", defaultCfg.Backends["claude"].Command, b.Command)
	}
}

func TestFixupBackends_MissingDangerouslySkipPermissions(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Simulate an outdated backend missing --dangerously-skip-permissions
	if err := d.SetBackend("claude", config.Backend{
		Command:    "claude --worktree",
		PromptFlag: "-p",
	}); err != nil {
		t.Fatal(err)
	}

	// fixupBackends should correct both the command and prompt flag
	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends := d.Backends()
	b, ok := backends["claude"]
	if !ok {
		t.Fatal("expected claude backend")
	}
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["claude"].Command {
		t.Errorf("expected command %q, got %q", defaultCfg.Backends["claude"].Command, b.Command)
	}
	if b.PromptFlag != defaultCfg.Backends["claude"].PromptFlag {
		t.Errorf("expected prompt_flag %q, got %q", defaultCfg.Backends["claude"].PromptFlag, b.PromptFlag)
	}
}

func TestFixupBackends_SkipsCorrectConfig(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	defaultCfg := config.DefaultConfig()
	want := defaultCfg.Backends["claude"]

	// Set the correct defaults — fixupBackends should not change them
	if err := d.SetBackend("claude", want); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends := d.Backends()
	got := backends["claude"]
	if got.Command != want.Command || got.PromptFlag != want.PromptFlag {
		t.Errorf("fixupBackends should not modify correct config: got command=%q flag=%q", got.Command, got.PromptFlag)
	}
}

func TestFixupBackends_RunsOnOpen(t *testing.T) {
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer os.Setenv("XDG_CONFIG_HOME", old)

	dbPath := filepath.Join(t.TempDir(), "data.sql")

	// First open — creates DB with correct defaults
	d1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Manually corrupt the backend to simulate an outdated config
	if err := d1.SetBackend("claude", config.Backend{
		Command:    "claude --worktree",
		PromptFlag: "-p",
	}); err != nil {
		t.Fatal(err)
	}
	d1.Close()

	// Second open — fixupBackends should repair it
	d2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	backends := d2.Backends()
	b := backends["claude"]
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["claude"].Command {
		t.Errorf("expected command %q after reopen, got %q", defaultCfg.Backends["claude"].Command, b.Command)
	}
	if b.PromptFlag != "" {
		t.Errorf("expected empty prompt_flag after reopen, got %q", b.PromptFlag)
	}
}

func TestMigration_OnlyRunsOnce(t *testing.T) {
	old := os.Getenv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer os.Setenv("XDG_CONFIG_HOME", old)

	dbPath := filepath.Join(t.TempDir(), "data.sql")

	// First open — runs migration
	d1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = d1.Add(&model.Task{ID: "t1", Name: "added after migration"})
	d1.Close()

	// Second open — should NOT re-run migration (which would not re-add tasks
	// due to INSERT OR IGNORE, but the version check should prevent it entirely)
	d2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	tasks := d2.Tasks()
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}
