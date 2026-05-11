package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
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

// --- DataDir / DefaultPath tests ---

func TestDataDir(t *testing.T) {
	dir := DataDir()
	if dir == "" {
		t.Error("expected non-empty DataDir")
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("expected absolute path, got %q", dir)
	}
}

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if p == "" {
		t.Error("expected non-empty DefaultPath")
	}
	if filepath.Base(p) != "data.sql" {
		t.Errorf("expected data.sql, got %q", filepath.Base(p))
	}
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

func TestDB_Rename(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "original", Status: model.StatusInProgress}
	_ = d.Add(task)

	if err := d.Rename(task.ID, "renamed"); err != nil {
		t.Fatal(err)
	}

	got, _ := d.Get(task.ID)
	if got.Name != "renamed" {
		t.Errorf("name = %q, want %q", got.Name, "renamed")
	}
	// Rename must NOT overwrite other fields.
	if got.Status != model.StatusInProgress {
		t.Errorf("status = %s, want InProgress (rename should not touch other fields)", got.Status)
	}
}

func TestDB_RenameNotFound(t *testing.T) {
	d := testDB(t)
	if err := d.Rename("nonexistent", "foo"); err == nil {
		t.Error("expected error")
	}
}

func TestDB_RenameIfName(t *testing.T) {
	t.Run("matches expected → renames", func(t *testing.T) {
		d := testDB(t)
		task := &model.Task{Name: "slug", Status: model.StatusInProgress}
		_ = d.Add(task)
		ok, err := d.RenameIfName(task.ID, "slug", "haiku-name")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("ok = false, want true")
		}
		got, _ := d.Get(task.ID)
		if got.Name != "haiku-name" {
			t.Errorf("name = %q, want %q", got.Name, "haiku-name")
		}
	})

	t.Run("name drifted → no-op, no error", func(t *testing.T) {
		d := testDB(t)
		task := &model.Task{Name: "user-typed", Status: model.StatusInProgress}
		_ = d.Add(task)
		ok, err := d.RenameIfName(task.ID, "old-slug", "haiku-name")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("ok = true, want false (name drifted)")
		}
		got, _ := d.Get(task.ID)
		if got.Name != "user-typed" {
			t.Errorf("name = %q, want unchanged %q", got.Name, "user-typed")
		}
	})

	t.Run("missing task → error", func(t *testing.T) {
		d := testDB(t)
		_, err := d.RenameIfName("nonexistent", "slug", "haiku-name")
		if err == nil {
			t.Error("expected error for missing task")
		}
	})
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
	tasksAfterDel, err := d.Tasks()
	testutil.NoError(t, err)
	if len(tasksAfterDel) != 0 {
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
	remaining, err := d.Tasks()
	testutil.NoError(t, err)
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
	tasksAfterPrune, err := d.Tasks()
	testutil.NoError(t, err)
	if len(tasksAfterPrune) != 1 {
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

	tasks, err := d2.Tasks()
	testutil.NoError(t, err)
	if len(tasks) != 1 || tasks[0].Name != "persisted" {
		t.Errorf("persistence failed: got %d tasks", len(tasks))
	}
}

// TestDB_RoundTripsAllTaskFields exercises every field on model.Task end-to-end:
// Add → Tasks → Get. A misordered column in `taskColumns` / scan / INSERT /
// UPDATE silently corrupts adjacent fields (e.g. parsing `created_at` as
// `pinned`). This test asserts each non-zero value survives the round-trip
// so any future column drift surfaces immediately.
func TestDB_RoundTripsAllTaskFields(t *testing.T) {
	d := testDB(t)

	now := time.Now().UTC().Truncate(time.Second)
	original := &model.Task{
		Name:      "round-trip",
		Status:    model.StatusInReview,
		Project:   "proj-x",
		Branch:    "feature/x",
		Prompt:    "do the thing",
		Backend:   "claude",
		Worktree:  "/tmp/worktree",
		AgentPID:  4242,
		SessionID: "abcd-1234",
		Sandboxed: true,
		Archived:  false,
		Pinned:    true,
		CreatedAt: now.Add(-2 * time.Hour),
		StartedAt: now.Add(-time.Hour),
		EndedAt:   now,
	}
	if err := d.Add(original); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get(original.ID)
	testutil.NoError(t, err)

	// Compare each field — a positional drift in scan order would corrupt
	// a specific field, and the failing assertion names it.
	testutil.Equal(t, got.ID, original.ID)
	testutil.Equal(t, got.Name, original.Name)
	testutil.Equal(t, got.Status, original.Status)
	testutil.Equal(t, got.Project, original.Project)
	testutil.Equal(t, got.Branch, original.Branch)
	testutil.Equal(t, got.Prompt, original.Prompt)
	testutil.Equal(t, got.Backend, original.Backend)
	testutil.Equal(t, got.Worktree, original.Worktree)
	testutil.Equal(t, got.AgentPID, original.AgentPID)
	testutil.Equal(t, got.SessionID, original.SessionID)
	testutil.Equal(t, got.Sandboxed, original.Sandboxed)
	testutil.Equal(t, got.Archived, original.Archived)
	testutil.Equal(t, got.Pinned, original.Pinned)
	if !got.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, original.CreatedAt)
	}
	if !got.StartedAt.Equal(original.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got.StartedAt, original.StartedAt)
	}
	if !got.EndedAt.Equal(original.EndedAt) {
		t.Errorf("EndedAt: got %v, want %v", got.EndedAt, original.EndedAt)
	}

	// Update path round-trip: flip one Boolean and confirm the rest survive.
	got.Pinned = false
	if err := d.Update(got); err != nil {
		t.Fatal(err)
	}
	reread, err := d.Get(got.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, reread.Pinned, false)
	testutil.Equal(t, reread.AgentPID, original.AgentPID)
}

// --- Project tests ---

func TestDB_Projects(t *testing.T) {
	d := testDB(t)

	if err := d.SetProject("myapp", config.Project{Path: "/home/user/myapp", Backend: "claude"}); err != nil {
		t.Fatal(err)
	}

	projects, err := d.Projects()
	testutil.NoError(t, err)
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
	projectsAfterDel, err := d.Projects()
	testutil.NoError(t, err)
	if len(projectsAfterDel) != 0 {
		t.Error("expected 0 projects")
	}
}

func TestDB_SetProjectUpdates(t *testing.T) {
	d := testDB(t)

	_ = d.SetProject("myapp", config.Project{Path: "/old"})
	_ = d.SetProject("myapp", config.Project{Path: "/new"})

	projects, err := d.Projects()
	testutil.NoError(t, err)
	if projects["myapp"].Path != "/new" {
		t.Errorf("expected /new, got %q", projects["myapp"].Path)
	}
}

func TestDB_Project_SandboxInherit(t *testing.T) {
	d := testDB(t)

	// Project with no sandbox override (nil Enabled, empty paths)
	if err := d.SetProject("myapp", config.Project{Path: "/home/user/myapp"}); err != nil {
		t.Fatal(err)
	}
	projects, err := d.Projects()
	testutil.NoError(t, err)
	p := projects["myapp"]
	if p.Sandbox.Enabled != nil {
		t.Errorf("expected nil Enabled (inherit), got %v", p.Sandbox.Enabled)
	}
	if len(p.Sandbox.DenyRead) != 0 {
		t.Errorf("expected empty DenyRead, got %v", p.Sandbox.DenyRead)
	}
	if len(p.Sandbox.ExtraWrite) != 0 {
		t.Errorf("expected empty ExtraWrite, got %v", p.Sandbox.ExtraWrite)
	}
}

func TestDB_Project_SandboxEnabledTrue(t *testing.T) {
	d := testDB(t)

	v := true
	proj := config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			Enabled: &v,
		},
	}
	if err := d.SetProject("myapp", proj); err != nil {
		t.Fatal(err)
	}
	projects, err := d.Projects()
	testutil.NoError(t, err)
	p := projects["myapp"]
	if p.Sandbox.Enabled == nil {
		t.Fatal("expected non-nil Enabled")
	}
	if !*p.Sandbox.Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestDB_Project_SandboxEnabledFalse(t *testing.T) {
	d := testDB(t)

	v := false
	proj := config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			Enabled: &v,
		},
	}
	if err := d.SetProject("myapp", proj); err != nil {
		t.Fatal(err)
	}
	projects, err := d.Projects()
	testutil.NoError(t, err)
	p := projects["myapp"]
	if p.Sandbox.Enabled == nil {
		t.Fatal("expected non-nil Enabled")
	}
	if *p.Sandbox.Enabled {
		t.Error("expected Enabled=false")
	}
}

func TestDB_Project_SandboxPaths(t *testing.T) {
	d := testDB(t)

	proj := config.Project{
		Path: "/home/user/myapp",
		Sandbox: config.ProjectSandboxConfig{
			DenyRead:   []string{"/secrets", "~/.private"},
			ExtraWrite: []string{"~/.npm", "/var/cache"},
		},
	}
	if err := d.SetProject("myapp", proj); err != nil {
		t.Fatal(err)
	}
	projects, err := d.Projects()
	testutil.NoError(t, err)
	p := projects["myapp"]
	if len(p.Sandbox.DenyRead) != 2 {
		t.Fatalf("expected 2 DenyRead paths, got %d: %v", len(p.Sandbox.DenyRead), p.Sandbox.DenyRead)
	}
	if p.Sandbox.DenyRead[0] != "/secrets" || p.Sandbox.DenyRead[1] != "~/.private" {
		t.Errorf("DenyRead = %v", p.Sandbox.DenyRead)
	}
	if len(p.Sandbox.ExtraWrite) != 2 {
		t.Fatalf("expected 2 ExtraWrite paths, got %d: %v", len(p.Sandbox.ExtraWrite), p.Sandbox.ExtraWrite)
	}
	if p.Sandbox.ExtraWrite[0] != "~/.npm" || p.Sandbox.ExtraWrite[1] != "/var/cache" {
		t.Errorf("ExtraWrite = %v", p.Sandbox.ExtraWrite)
	}
}

func TestDB_Project_SandboxRoundtrip(t *testing.T) {
	d := testDB(t)

	v := true
	proj := config.Project{
		Path:   "/home/user/myapp",
		Branch: "master",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    &v,
			DenyRead:   []string{"/deny-this"},
			ExtraWrite: []string{"/allow-this"},
		},
	}
	if err := d.SetProject("myapp", proj); err != nil {
		t.Fatal(err)
	}

	// Update with different values
	v2 := false
	proj2 := config.Project{
		Path:   "/home/user/myapp",
		Branch: "master",
		Sandbox: config.ProjectSandboxConfig{
			Enabled:    &v2,
			DenyRead:   []string{"/other-deny"},
			ExtraWrite: nil,
		},
	}
	if err := d.SetProject("myapp", proj2); err != nil {
		t.Fatal(err)
	}

	projects, err := d.Projects()
	testutil.NoError(t, err)
	p := projects["myapp"]
	if p.Sandbox.Enabled == nil || *p.Sandbox.Enabled {
		t.Errorf("expected Enabled=false after update, got %v", p.Sandbox.Enabled)
	}
	if len(p.Sandbox.DenyRead) != 1 || p.Sandbox.DenyRead[0] != "/other-deny" {
		t.Errorf("DenyRead = %v", p.Sandbox.DenyRead)
	}
	if len(p.Sandbox.ExtraWrite) != 0 {
		t.Errorf("expected empty ExtraWrite after update, got %v", p.Sandbox.ExtraWrite)
	}
}

// --- Backend tests ---

func TestDB_Backends(t *testing.T) {
	d := testDB(t)

	// Should have default backend from seedDefaults
	backends, err := d.Backends()
	testutil.NoError(t, err)
	if _, ok := backends["claude"]; !ok {
		t.Error("expected default claude backend")
	}
}

func TestDB_SetBackend(t *testing.T) {
	d := testDB(t)

	if err := d.SetBackend("codex", config.Backend{Command: "codex", PromptFlag: "--prompt"}); err != nil {
		t.Fatal(err)
	}
	backends, err := d.Backends()
	testutil.NoError(t, err)
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

func TestMigration_FreshDB(t *testing.T) {
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
	backends, err := d.Backends()
	testutil.NoError(t, err)
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

	backends, err := d.Backends()
	testutil.NoError(t, err)
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

	backends, err := d.Backends()
	testutil.NoError(t, err)
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

func TestFixupBackends_CodexOldFlags(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Simulate a codex backend with old --yolo flag
	if err := d.SetBackend("codex", config.Backend{
		Command:    "codex --yolo",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b, ok := backends["codex"]
	if !ok {
		t.Fatal("expected codex backend")
	}
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["codex"].Command {
		t.Errorf("expected command %q, got %q", defaultCfg.Backends["codex"].Command, b.Command)
	}
}

func TestFixupBackends_CodexFullAuto(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Simulate codex with old --full-auto flag (pre-migration)
	if err := d.SetBackend("codex", config.Backend{
		Command:    "codex --full-auto",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b := backends["codex"]
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["codex"].Command {
		t.Errorf("expected command %q, got %q", defaultCfg.Backends["codex"].Command, b.Command)
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

	backends, err := d.Backends()
	testutil.NoError(t, err)
	got := backends["claude"]
	if got.Command != want.Command || got.PromptFlag != want.PromptFlag {
		t.Errorf("fixupBackends should not modify correct config: got command=%q flag=%q", got.Command, got.PromptFlag)
	}
}

func TestFixupBackends_RunsOnOpen(t *testing.T) {
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

	backends, err := d2.Backends()
	testutil.NoError(t, err)
	b := backends["claude"]
	defaultCfg := config.DefaultConfig()
	if b.Command != defaultCfg.Backends["claude"].Command {
		t.Errorf("expected command %q after reopen, got %q", defaultCfg.Backends["claude"].Command, b.Command)
	}
	if b.PromptFlag != "" {
		t.Errorf("expected empty prompt_flag after reopen, got %q", b.PromptFlag)
	}
}

// TestFixupBackends_InsertsMissingDefault pins the new ErrNoRows-then-INSERT
// path. Simulates a pre-existing DB that predates a new default backend (e.g.
// users upgrading to a build that ships pi): the row should be inserted on
// the next Open without requiring a schema bump.
func TestFixupBackends_InsertsMissingDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.sql")

	// First open — seedDefaults populates claude, codex, pi.
	d1, err := Open(dbPath)
	testutil.NoError(t, err)
	// Delete pi to simulate an upgrade from a pre-pi build.
	testutil.NoError(t, d1.DeleteBackend("pi"))
	backends1, err := d1.Backends()
	testutil.NoError(t, err)
	if _, ok := backends1["pi"]; ok {
		t.Fatal("setup: pi should be deleted before reopen")
	}
	d1.Close()

	// Second open — fixupBackends should re-insert pi.
	d2, err := Open(dbPath)
	testutil.NoError(t, err)
	defer d2.Close()

	backends2, err := d2.Backends()
	testutil.NoError(t, err)
	pi, ok := backends2["pi"]
	if !ok {
		t.Fatal("expected pi to be re-inserted by fixupBackends after Open")
	}
	defaultCfg := config.DefaultConfig()
	if pi.Command != defaultCfg.Backends["pi"].Command {
		t.Errorf("pi command after reinsert = %q, want %q", pi.Command, defaultCfg.Backends["pi"].Command)
	}
	if pi.PromptFlag != defaultCfg.Backends["pi"].PromptFlag {
		t.Errorf("pi prompt_flag after reinsert = %q, want %q", pi.PromptFlag, defaultCfg.Backends["pi"].PromptFlag)
	}
}

// --- Config edge case tests ---

func TestDB_Config_CleanupWorktrees(t *testing.T) {
	d := testDB(t)

	// Default: CleanupWorktrees should be nil (unset)
	cfg := d.Config()
	if cfg.UI.CleanupWorktrees != nil {
		t.Error("expected CleanupWorktrees to be nil by default")
	}
	if !cfg.UI.ShouldCleanupWorktrees() {
		t.Error("ShouldCleanupWorktrees should default to true")
	}

	// Set to true explicitly
	if err := d.SetConfigValue("ui.cleanup_worktrees", "true"); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if cfg.UI.CleanupWorktrees == nil {
		t.Fatal("expected CleanupWorktrees to be set")
	}
	if !*cfg.UI.CleanupWorktrees {
		t.Error("expected CleanupWorktrees to be true")
	}

	// Set to false
	if err := d.SetConfigValue("ui.cleanup_worktrees", "false"); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if cfg.UI.CleanupWorktrees == nil {
		t.Fatal("expected CleanupWorktrees to be set")
	}
	if *cfg.UI.CleanupWorktrees {
		t.Error("expected CleanupWorktrees to be false")
	}
	if cfg.UI.ShouldCleanupWorktrees() {
		t.Error("ShouldCleanupWorktrees should return false when explicitly set to false")
	}
}

func TestDB_Config_ShowElapsedFalse(t *testing.T) {
	d := testDB(t)

	// Default should be true
	cfg := d.Config()
	if !cfg.UI.ShowElapsed {
		t.Error("expected ShowElapsed default true")
	}

	// Override to false
	if err := d.SetConfigValue("ui.show_elapsed", "false"); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if cfg.UI.ShowElapsed {
		t.Error("expected ShowElapsed to be false after override")
	}
}

func TestDB_Config_ShowIconsFalse(t *testing.T) {
	d := testDB(t)

	// Default should be true
	cfg := d.Config()
	if !cfg.UI.ShowIcons {
		t.Error("expected ShowIcons default true")
	}

	// Override to false
	if err := d.SetConfigValue("ui.show_icons", "false"); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if cfg.UI.ShowIcons {
		t.Error("expected ShowIcons to be false after override")
	}
}

// --- Tasks ordering test ---

func TestDB_Tasks_OrderedByCreatedAt(t *testing.T) {
	d := testDB(t)

	now := time.Now()
	t3 := &model.Task{ID: "t3", Name: "third", CreatedAt: now.Add(2 * time.Second)}
	t1 := &model.Task{ID: "t1", Name: "first", CreatedAt: now}
	t2 := &model.Task{ID: "t2", Name: "second", CreatedAt: now.Add(1 * time.Second)}

	// Add in non-chronological order
	_ = d.Add(t3)
	_ = d.Add(t1)
	_ = d.Add(t2)

	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Name != "first" {
		t.Errorf("tasks[0] = %q, want first", tasks[0].Name)
	}
	if tasks[1].Name != "second" {
		t.Errorf("tasks[1] = %q, want second", tasks[1].Name)
	}
	if tasks[2].Name != "third" {
		t.Errorf("tasks[2] = %q, want third", tasks[2].Name)
	}
}

// --- Time roundtrip tests ---

func TestDB_TimeRoundtrip_ZeroTimes(t *testing.T) {
	d := testDB(t)

	task := &model.Task{
		Name:      "zero times",
		CreatedAt: time.Now(),
		// StartedAt and EndedAt left as zero values
	}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.StartedAt.IsZero() {
		t.Errorf("expected zero StartedAt, got %v", got.StartedAt)
	}
	if !got.EndedAt.IsZero() {
		t.Errorf("expected zero EndedAt, got %v", got.EndedAt)
	}
}

func TestDB_TimeRoundtrip_NonZeroTimes(t *testing.T) {
	d := testDB(t)

	now := time.Now()
	started := now.Add(-10 * time.Minute)
	ended := now.Add(-5 * time.Minute)

	task := &model.Task{
		Name:      "with times",
		CreatedAt: now,
		StartedAt: started,
		EndedAt:   ended,
	}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Compare with nanosecond truncation from RFC3339Nano roundtrip
	if got.CreatedAt.Sub(now).Abs() > time.Microsecond {
		t.Errorf("CreatedAt mismatch: got %v, want %v", got.CreatedAt, now)
	}
	if got.StartedAt.Sub(started).Abs() > time.Microsecond {
		t.Errorf("StartedAt mismatch: got %v, want %v", got.StartedAt, started)
	}
	if got.EndedAt.Sub(ended).Abs() > time.Microsecond {
		t.Errorf("EndedAt mismatch: got %v, want %v", got.EndedAt, ended)
	}
}

// --- Task with all fields ---

func TestDB_TaskAllFields(t *testing.T) {
	d := testDB(t)

	now := time.Now()
	task := &model.Task{
		ID:        "full-task",
		Name:      "full task",
		Status:    model.StatusInProgress,
		Project:   "myproject",
		Branch:    "feature/test",
		Prompt:    "implement the feature",
		Backend:   "claude",
		Worktree:  "/tmp/worktrees/full-task",
		AgentPID:  12345,
		SessionID: "sess-abc-123",
		CreatedAt: now.Add(-1 * time.Hour),
		StartedAt: now.Add(-30 * time.Minute),
		EndedAt:   now,
	}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get("full-task")
	if err != nil {
		t.Fatal(err)
	}

	if got.Name != "full task" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Status != model.StatusInProgress {
		t.Errorf("Status = %v", got.Status)
	}
	if got.Project != "myproject" {
		t.Errorf("Project = %q", got.Project)
	}
	if got.Branch != "feature/test" {
		t.Errorf("Branch = %q", got.Branch)
	}
	if got.Prompt != "implement the feature" {
		t.Errorf("Prompt = %q", got.Prompt)
	}
	if got.Backend != "claude" {
		t.Errorf("Backend = %q", got.Backend)
	}
	if got.Worktree != "/tmp/worktrees/full-task" {
		t.Errorf("Worktree = %q", got.Worktree)
	}
	if got.AgentPID != 12345 {
		t.Errorf("AgentPID = %d", got.AgentPID)
	}
	if got.SessionID != "sess-abc-123" {
		t.Errorf("SessionID = %q", got.SessionID)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if got.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}
	if got.EndedAt.IsZero() {
		t.Error("EndedAt should not be zero")
	}
}

// --- PruneCompleted returns worktree info ---

func TestDB_PruneCompleted_ReturnsWorktreeInfo(t *testing.T) {
	d := testDB(t)

	_ = d.Add(&model.Task{Name: "done1", Status: model.StatusComplete, Worktree: "/tmp/wt/done1"})
	_ = d.Add(&model.Task{Name: "done2", Status: model.StatusComplete, Worktree: "/tmp/wt/done2"})
	_ = d.Add(&model.Task{Name: "active", Status: model.StatusInProgress, Worktree: "/tmp/wt/active"})

	pruned, err := d.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 2 {
		t.Fatalf("expected 2 pruned, got %d", len(pruned))
	}

	worktrees := make(map[string]bool)
	for _, p := range pruned {
		worktrees[p.Worktree] = true
	}
	if !worktrees["/tmp/wt/done1"] {
		t.Error("expected /tmp/wt/done1 in pruned worktrees")
	}
	if !worktrees["/tmp/wt/done2"] {
		t.Error("expected /tmp/wt/done2 in pruned worktrees")
	}
}

func TestDB_WorktreePaths(t *testing.T) {
	d := testDB(t)

	_ = d.Add(&model.Task{Name: "t1", Worktree: "/tmp/wt/task1"})
	_ = d.Add(&model.Task{Name: "t2", Worktree: "/tmp/wt/task2"})
	_ = d.Add(&model.Task{Name: "t3", Worktree: ""}) // no worktree

	paths, err := d.WorktreePaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if !paths["/tmp/wt/task1"] {
		t.Error("expected /tmp/wt/task1")
	}
	if !paths["/tmp/wt/task2"] {
		t.Error("expected /tmp/wt/task2")
	}
}

func TestDB_WorktreePaths_Empty(t *testing.T) {
	d := testDB(t)

	paths, err := d.WorktreePaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected 0 paths, got %d", len(paths))
	}
}

// --- Multiple projects and backends in Config ---

func TestDB_Config_MultipleProjectsAndBackends(t *testing.T) {
	d := testDB(t)

	// Add multiple projects
	_ = d.SetProject("app1", config.Project{Path: "/home/user/app1", Branch: "main", Backend: "claude"})
	_ = d.SetProject("app2", config.Project{Path: "/home/user/app2", Branch: "develop", Backend: "codex"})

	// Add multiple backends
	_ = d.SetBackend("codex", config.Backend{Command: "codex", PromptFlag: "--prompt"})
	_ = d.SetBackend("custom", config.Backend{Command: "custom-agent", PromptFlag: "--input"})

	cfg := d.Config()

	// Verify projects
	if len(cfg.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(cfg.Projects))
	}
	if cfg.Projects["app1"].Path != "/home/user/app1" {
		t.Errorf("app1 path = %q", cfg.Projects["app1"].Path)
	}
	if cfg.Projects["app1"].Branch != "main" {
		t.Errorf("app1 branch = %q", cfg.Projects["app1"].Branch)
	}
	if cfg.Projects["app2"].Path != "/home/user/app2" {
		t.Errorf("app2 path = %q", cfg.Projects["app2"].Path)
	}
	if cfg.Projects["app2"].Backend != "codex" {
		t.Errorf("app2 backend = %q", cfg.Projects["app2"].Backend)
	}

	// Verify backends (claude default + codex + pi default + custom = 4).
	if len(cfg.Backends) != 4 {
		t.Fatalf("expected 4 backends, got %d", len(cfg.Backends))
	}
	if _, ok := cfg.Backends["pi"]; !ok {
		t.Error("expected hardcoded pi backend to be present")
	}
	if cfg.Backends["codex"].Command != "codex" {
		t.Errorf("codex command = %q", cfg.Backends["codex"].Command)
	}
	if cfg.Backends["codex"].PromptFlag != "--prompt" {
		t.Errorf("codex prompt_flag = %q", cfg.Backends["codex"].PromptFlag)
	}
	if cfg.Backends["custom"].Command != "custom-agent" {
		t.Errorf("custom command = %q", cfg.Backends["custom"].Command)
	}
}

// --- Config keybinding overrides ---

func TestDB_Config_AllKeybindingOverrides(t *testing.T) {
	d := testDB(t)

	overrides := map[string]string{
		"keybindings.attach":   "a",
		"keybindings.status":   "x",
		"keybindings.delete":   "D",
		"keybindings.quit":     "Q",
		"keybindings.help":     "h",
		"keybindings.filter":   "f",
		"keybindings.prompt":   "P",
		"keybindings.worktree": "W",
	}
	for k, v := range overrides {
		if err := d.SetConfigValue(k, v); err != nil {
			t.Fatalf("SetConfigValue(%q, %q): %v", k, v, err)
		}
	}

	cfg := d.Config()
	if cfg.Keybindings.Attach != "a" {
		t.Errorf("Attach = %q", cfg.Keybindings.Attach)
	}
	if cfg.Keybindings.Status != "x" {
		t.Errorf("Status = %q", cfg.Keybindings.Status)
	}
	if cfg.Keybindings.Delete != "D" {
		t.Errorf("Delete = %q", cfg.Keybindings.Delete)
	}
	if cfg.Keybindings.Quit != "Q" {
		t.Errorf("Quit = %q", cfg.Keybindings.Quit)
	}
	if cfg.Keybindings.Help != "h" {
		t.Errorf("Help = %q", cfg.Keybindings.Help)
	}
	if cfg.Keybindings.Filter != "f" {
		t.Errorf("Filter = %q", cfg.Keybindings.Filter)
	}
	if cfg.Keybindings.Prompt != "P" {
		t.Errorf("Prompt = %q", cfg.Keybindings.Prompt)
	}
	if cfg.Keybindings.Worktree != "W" {
		t.Errorf("Worktree = %q", cfg.Keybindings.Worktree)
	}
}

// --- Defaults.backend override ---

func TestDB_Config_DefaultsBackendOverride(t *testing.T) {
	d := testDB(t)

	if err := d.SetConfigValue("defaults.backend", "codex"); err != nil {
		t.Fatal(err)
	}
	cfg := d.Config()
	if cfg.Defaults.Backend != "codex" {
		t.Errorf("Defaults.Backend = %q, want codex", cfg.Defaults.Backend)
	}
}

func TestSeedDefaults_FixesCatAndTruePlaceholders(t *testing.T) {
	// Test that seedDefaults also fixes "cat" and "true" placeholder commands
	for _, placeholder := range []string{"cat", "true"} {
		t.Run(placeholder, func(t *testing.T) {
			d, err := OpenInMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()

			if err := d.SetBackend("claude", config.Backend{Command: placeholder, PromptFlag: ""}); err != nil {
				t.Fatal(err)
			}

			if err := d.runSeedDefaults(); err != nil {
				t.Fatal(err)
			}

			backends, err := d.Backends()
			testutil.NoError(t, err)
			b := backends["claude"]
			if b.Command == placeholder {
				t.Errorf("seedDefaults should have replaced placeholder %q", placeholder)
			}
			defaultCfg := config.DefaultConfig()
			if b.Command != defaultCfg.Backends["claude"].Command {
				t.Errorf("expected %q, got %q", defaultCfg.Backends["claude"].Command, b.Command)
			}
		})
	}
}

func TestSeedDefaults_SkipsNonPlaceholder(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Set a real custom command — seedDefaults should NOT overwrite it
	customCmd := "my-custom-claude --special"
	if err := d.SetBackend("claude", config.Backend{Command: customCmd, PromptFlag: "--p"}); err != nil {
		t.Fatal(err)
	}

	if err := d.runSeedDefaults(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	if backends["claude"].Command != customCmd {
		t.Errorf("seedDefaults overwrote custom command: got %q", backends["claude"].Command)
	}
}

func TestFixupBackends_FixesPromptFlagOnly(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Set correct command but wrong prompt flag
	defaultCfg := config.DefaultConfig()
	if err := d.SetBackend("claude", config.Backend{
		Command:    defaultCfg.Backends["claude"].Command,
		PromptFlag: "-p",
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b := backends["claude"]
	if b.PromptFlag != "" {
		t.Errorf("expected empty prompt_flag, got %q", b.PromptFlag)
	}
}

func TestFixupBackends_MissingPermissionModePlan(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Simulate a backend with --dangerously-skip-permissions but missing --permission-mode plan
	if err := d.SetBackend("claude", config.Backend{
		Command:    "claude --dangerously-skip-permissions",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b := backends["claude"]
	// Fixup appends --permission-mode plan to existing command (preserving customizations)
	want := "claude --dangerously-skip-permissions --permission-mode plan"
	if b.Command != want {
		t.Errorf("expected command %q, got %q", want, b.Command)
	}
}

func TestFixupBackends_PermissionModePreservesCustomFlags(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// User has custom flags — fixup should append, not replace
	if err := d.SetBackend("claude", config.Backend{
		Command:    "claude --dangerously-skip-permissions --model claude-opus-4-5",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b := backends["claude"]
	want := "claude --dangerously-skip-permissions --model claude-opus-4-5 --permission-mode plan"
	if b.Command != want {
		t.Errorf("expected command %q, got %q", want, b.Command)
	}
}

func TestFixupBackends_NonClaudeBackendUntouched(t *testing.T) {
	d, err := OpenInMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Add a non-default backend
	if err := d.SetBackend("gemini", config.Backend{Command: "gemini", PromptFlag: "-p"}); err != nil {
		t.Fatal(err)
	}

	if err := d.fixupBackends(); err != nil {
		t.Fatal(err)
	}

	// gemini is not in DefaultConfig, so fixupBackends should not touch it
	backends, err := d.Backends()
	testutil.NoError(t, err)
	if backends["gemini"].PromptFlag != "-p" {
		t.Errorf("gemini prompt_flag should be untouched, got %q", backends["gemini"].PromptFlag)
	}
}

// --- Update with all fields ---

func TestDB_UpdateAllFields(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "original"}
	_ = d.Add(task)

	now := time.Now()
	task.Name = "updated"
	task.Status = model.StatusComplete
	task.Project = "proj"
	task.Branch = "main"
	task.Prompt = "updated prompt"
	task.Backend = "codex"
	task.Worktree = "/tmp/wt"
	task.AgentPID = 42
	task.SessionID = "sess-x"
	task.StartedAt = now.Add(-1 * time.Hour)
	task.EndedAt = now

	if err := d.Update(task); err != nil {
		t.Fatal(err)
	}

	got, _ := d.Get(task.ID)
	if got.Status != model.StatusComplete {
		t.Errorf("Status = %v", got.Status)
	}
	if got.AgentPID != 42 {
		t.Errorf("AgentPID = %d", got.AgentPID)
	}
	if got.Worktree != "/tmp/wt" {
		t.Errorf("Worktree = %q", got.Worktree)
	}
	if got.EndedAt.IsZero() {
		t.Error("EndedAt should not be zero")
	}
}

func TestDB_SandboxedRoundTrip(t *testing.T) {
	d := testDB(t)

	t.Run("persisted on Add", func(t *testing.T) {
		task := &model.Task{Name: "sandboxed task", Sandboxed: true}
		testutil.NoError(t, d.Add(task))

		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Sandboxed, true)
	})

	t.Run("defaults to false", func(t *testing.T) {
		task := &model.Task{Name: "unsandboxed task"}
		testutil.NoError(t, d.Add(task))

		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Sandboxed, false)
	})

	t.Run("updated via Update", func(t *testing.T) {
		task := &model.Task{Name: "toggle sandbox"}
		testutil.NoError(t, d.Add(task))
		testutil.Equal(t, task.Sandboxed, false)

		task.Sandboxed = true
		testutil.NoError(t, d.Update(task))

		got, err := d.Get(task.ID)
		testutil.NoError(t, err)
		testutil.Equal(t, got.Sandboxed, true)
	})
}

func TestDB_BackendCommandRoundtrip(t *testing.T) {
	d := testDB(t)

	if err := d.SetBackend("codex", config.Backend{
		Command:    "codex --dangerously-bypass-approvals-and-sandbox",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b, ok := backends["codex"]
	if !ok {
		t.Fatal("codex not found")
	}
	if b.Command != "codex --dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("command = %q", b.Command)
	}
}

func TestDB_CodexDefaultCommand(t *testing.T) {
	d := testDB(t)

	// Default config should have the new codex command
	cfg := d.Config()
	codex, ok := cfg.Backends["codex"]
	if !ok {
		t.Fatal("expected codex backend in config")
	}
	if codex.Command != "codex --dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("command = %q", codex.Command)
	}
}

// --- DeleteBackend is not exposed, but we can test SetBackend overwrites ---

func TestDB_SetBackendOverwrites(t *testing.T) {
	d := testDB(t)

	_ = d.SetBackend("test", config.Backend{Command: "v1", PromptFlag: "--old"})
	_ = d.SetBackend("test", config.Backend{Command: "v2", PromptFlag: "--new"})

	backends, err := d.Backends()
	testutil.NoError(t, err)
	if backends["test"].Command != "v2" {
		t.Errorf("expected v2, got %q", backends["test"].Command)
	}
	if backends["test"].PromptFlag != "--new" {
		t.Errorf("expected --new, got %q", backends["test"].PromptFlag)
	}
}

// --- Empty tasks list ---

func TestDB_Tasks_Empty(t *testing.T) {
	d := testDB(t)
	tasks, err := d.Tasks()
	testutil.NoError(t, err)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

// --- PruneCompleted with all statuses ---

func TestDB_PruneCompleted_AllStatuses(t *testing.T) {
	d := testDB(t)

	_ = d.Add(&model.Task{Name: "pending", Status: model.StatusPending})
	_ = d.Add(&model.Task{Name: "in_progress", Status: model.StatusInProgress})
	_ = d.Add(&model.Task{Name: "in_review", Status: model.StatusInReview})
	_ = d.Add(&model.Task{Name: "complete", Status: model.StatusComplete})

	pruned, err := d.PruneCompleted()
	if err != nil {
		t.Fatal(err)
	}
	if len(pruned) != 1 {
		t.Errorf("expected 1 pruned, got %d", len(pruned))
	}
	if pruned[0].Name != "complete" {
		t.Errorf("pruned wrong task: %q", pruned[0].Name)
	}
	remaining, err := d.Tasks()
	testutil.NoError(t, err)
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining, got %d", len(remaining))
	}
}

func TestMigration_OnlyRunsOnce(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.sql")

	// First open — runs migration
	d1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = d1.Add(&model.Task{ID: "t1", Name: "added after migration"})
	d1.Close()

	// Second open — should NOT re-run migration
	d2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()

	tasks, err := d2.Tasks()
	testutil.NoError(t, err)
	if len(tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(tasks))
	}
}

func TestDB_SandboxConfig(t *testing.T) {
	d := testDB(t)

	// Default: sandbox disabled
	cfg := d.Config()
	if cfg.Sandbox.Enabled {
		t.Error("expected sandbox disabled by default")
	}

	// Enable sandbox
	if err := d.SetSandboxEnabled(true); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if !cfg.Sandbox.Enabled {
		t.Error("expected sandbox enabled after SetSandboxEnabled(true)")
	}

	// Disable sandbox
	if err := d.SetSandboxEnabled(false); err != nil {
		t.Fatal(err)
	}
	cfg = d.Config()
	if cfg.Sandbox.Enabled {
		t.Error("expected sandbox disabled after SetSandboxEnabled(false)")
	}
}

func TestDB_SandboxConfig_Paths(t *testing.T) {
	d := testDB(t)

	if err := d.SetConfigValue("sandbox.deny_read", "/secrets,~/.private"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetConfigValue("sandbox.extra_write", "~/.npm,/tmp/build"); err != nil {
		t.Fatal(err)
	}

	cfg := d.Config()
	if len(cfg.Sandbox.DenyRead) != 2 {
		t.Fatalf("expected 2 deny_read paths, got %d", len(cfg.Sandbox.DenyRead))
	}
	if len(cfg.Sandbox.ExtraWrite) != 2 {
		t.Fatalf("expected 2 extra_write paths, got %d", len(cfg.Sandbox.ExtraWrite))
	}
}

// TestDB_DeleteScheduleMissing covers the not-found path in DeleteSchedule.
func TestDB_DeleteScheduleMissing(t *testing.T) {
	d := testDB(t)
	err := d.DeleteSchedule("nonexistent-id")
	if !errors.Is(err, ErrScheduleNotFound) {
		t.Fatalf("expected ErrScheduleNotFound, got %v", err)
	}
}

// TestDB_AddSchedule_PreservesProvidedID covers the !ID == "" branch.
func TestDB_AddSchedule_PreservesProvidedID(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{
		ID:       "my-fixed-id",
		Name:     "named",
		Project:  "p",
		Prompt:   "do it",
		Schedule: "@hourly",
		Enabled:  true,
	}
	testutil.NoError(t, d.AddSchedule(s))
	testutil.Equal(t, s.ID, "my-fixed-id")

	got, err := d.GetSchedule("my-fixed-id")
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, "my-fixed-id")
}

// TestDB_KBPendingTasks_Multiple covers ordering and multi-row paths.
func TestDB_KBPendingTasks_Multiple(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.KBAddPendingTask("first", "proj", "src1.md"))
	testutil.NoError(t, d.KBAddPendingTask("second", "proj", "src2.md"))

	tasks := d.KBPendingTasks()
	testutil.Equal(t, len(tasks), 2)
}

// TestDB_OpenInMemoryClose covers Close on an in-memory DB.
func TestDB_OpenInMemoryClose(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
}

// TestDB_OpenAndClose covers a full disk-backed open/close lifecycle.
func TestDB_OpenAndClose(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "data.sql"))
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
}

// closedDB returns a *DB whose underlying *sql.DB has been closed, so any
// subsequent query/exec will return an error. Used to exercise the err != nil
// branches in CRUD methods that wrap db.conn.Query / db.conn.Exec.
func closedDB(t *testing.T) *DB {
	t.Helper()
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	testutil.NoError(t, d.Close())
	return d
}

func TestDB_ErrorPaths(t *testing.T) {
	t.Run("Tasks query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.Tasks()
		testutil.Error(t, err)
	})

	t.Run("Add exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.Add(&model.Task{Name: "x"})
		testutil.Error(t, err)
	})

	t.Run("Update exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.Update(&model.Task{ID: "x"})
		testutil.Error(t, err)
	})

	t.Run("Rename exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.Rename("id", "name")
		testutil.Error(t, err)
	})

	t.Run("RenameIfName exec error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.RenameIfName("id", "old", "new")
		testutil.Error(t, err)
	})

	t.Run("Delete exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.Delete("id")
		testutil.Error(t, err)
	})

	t.Run("Get query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.Get("id")
		testutil.Error(t, err)
	})

	t.Run("PruneCompleted query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.PruneCompleted()
		testutil.Error(t, err)
	})

	t.Run("WorktreePaths query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.WorktreePaths()
		testutil.Error(t, err)
	})

	t.Run("Backends query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.Backends()
		testutil.Error(t, err)
	})

	t.Run("SetBackend exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.SetBackend("x", config.Backend{Command: "y"})
		testutil.Error(t, err)
	})

	t.Run("DeleteBackend exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.DeleteBackend("x")
		testutil.Error(t, err)
	})

	t.Run("Projects query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.Projects()
		testutil.Error(t, err)
	})

	t.Run("SetProject exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.SetProject("x", config.Project{Path: "/tmp"})
		testutil.Error(t, err)
	})

	t.Run("DeleteProject exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.DeleteProject("x")
		testutil.Error(t, err)
	})

	t.Run("SetConfigValue exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.SetConfigValue("k", "v")
		testutil.Error(t, err)
	})

	t.Run("GetConfigValue query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.GetConfigValue("k")
		testutil.Error(t, err)
	})

	t.Run("KBUpsert begin error", func(t *testing.T) {
		d := closedDB(t)
		err := d.KBUpsert(&kb.Document{Path: "x.md"})
		testutil.Error(t, err)
	})

	t.Run("KBDelete begin error", func(t *testing.T) {
		d := closedDB(t)
		err := d.KBDelete("x.md")
		testutil.Error(t, err)
	})

	t.Run("KBSearch query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.KBSearch("foo", 10)
		testutil.Error(t, err)
	})

	t.Run("KBList query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.KBList("", 10)
		testutil.Error(t, err)
	})

	t.Run("KBList with prefix query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.KBList("notes/", 10)
		testutil.Error(t, err)
	})

	t.Run("KBGet query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.KBGet("x.md")
		testutil.Error(t, err)
	})

	t.Run("KBMetadataMap query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.KBMetadataMap()
		testutil.Error(t, err)
	})

	t.Run("KBAddPendingTask exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.KBAddPendingTask("n", "p", "s")
		testutil.Error(t, err)
	})

	t.Run("KBPendingTasks returns nil on query error", func(t *testing.T) {
		d := closedDB(t)
		got := d.KBPendingTasks()
		testutil.Equal(t, len(got), 0)
	})

	t.Run("KBDeletePendingTask exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.KBDeletePendingTask(1)
		testutil.Error(t, err)
	})

	t.Run("AddPushSubscription exec error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.AddPushSubscription(PushSubscription{Endpoint: "x"})
		testutil.Error(t, err)
	})

	t.Run("PushSubscriptions query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.PushSubscriptions()
		testutil.Error(t, err)
	})

	t.Run("DeletePushSubscription exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.DeletePushSubscription(1)
		testutil.Error(t, err)
	})

	t.Run("DeletePushSubscriptionByEndpoint exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.DeletePushSubscriptionByEndpoint("x")
		testutil.Error(t, err)
	})

	t.Run("AddAPIToken exec error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.AddAPIToken("l", "h", "1234")
		testutil.Error(t, err)
	})

	t.Run("APITokens query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.APITokens()
		testutil.Error(t, err)
	})

	t.Run("FindAPITokenByHash query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.FindAPITokenByHash("h")
		testutil.Error(t, err)
	})

	t.Run("RevokeAPIToken exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.RevokeAPIToken(1)
		testutil.Error(t, err)
	})

	t.Run("Schedules query error", func(t *testing.T) {
		d := closedDB(t)
		_, err := d.Schedules()
		testutil.Error(t, err)
	})

	t.Run("AddSchedule exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.AddSchedule(&model.ScheduledTask{Name: "x"})
		testutil.Error(t, err)
	})

	t.Run("UpdateSchedule exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.UpdateSchedule(&model.ScheduledTask{ID: "x"})
		testutil.Error(t, err)
	})

	t.Run("DeleteSchedule exec error", func(t *testing.T) {
		d := closedDB(t)
		err := d.DeleteSchedule("x")
		testutil.Error(t, err)
	})

	t.Run("WithTx begin error", func(t *testing.T) {
		d := closedDB(t)
		err := d.WithTx(func(tx *sql.Tx) error { return nil })
		testutil.Error(t, err)
	})
}

// TestDB_OpenInvalidDir hits the MkdirAll error path in Open.
func TestDB_OpenInvalidDir(t *testing.T) {
	// Create a regular file, then try to open a DB whose parent path includes
	// that file as a "directory" — MkdirAll fails.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	testutil.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0o644))

	_, err := Open(filepath.Join(blocker, "child", "data.sql"))
	testutil.Error(t, err)
}

// TestDB_AddSchedule_DefaultsCreatedAt covers the CreatedAt.IsZero branch.
func TestDB_AddSchedule_DefaultsCreatedAt(t *testing.T) {
	d := testDB(t)
	s := &model.ScheduledTask{Name: "n", Project: "p", Prompt: "x"}
	testutil.NoError(t, d.AddSchedule(s))
	if s.CreatedAt.IsZero() {
		t.Error("expected CreatedAt populated")
	}
	// pre-set CreatedAt should be preserved
	custom := time.Now().Add(-time.Hour)
	s2 := &model.ScheduledTask{Name: "n2", Project: "p", Prompt: "x", CreatedAt: custom}
	testutil.NoError(t, d.AddSchedule(s2))
	if !s2.CreatedAt.Equal(custom) {
		t.Errorf("CreatedAt overwritten; got %v want %v", s2.CreatedAt, custom)
	}
}
