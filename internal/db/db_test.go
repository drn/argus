package db

import (
	"path/filepath"
	"testing"
	"time"

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

func TestDB_Project_SandboxInherit(t *testing.T) {
	d := testDB(t)

	// Project with no sandbox override (nil Enabled, empty paths)
	if err := d.SetProject("myapp", config.Project{Path: "/home/user/myapp"}); err != nil {
		t.Fatal(err)
	}
	projects := d.Projects()
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
	projects := d.Projects()
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
	projects := d.Projects()
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
	projects := d.Projects()
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

	projects := d.Projects()
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

	backends := d.Backends()
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

	backends := d.Backends()
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

	backends := d.Backends()
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

	tasks := d.Tasks()
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

	// Verify backends (claude default + codex + custom = 3)
	if len(cfg.Backends) != 3 {
		t.Fatalf("expected 3 backends, got %d", len(cfg.Backends))
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

			backends := d.Backends()
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

	backends := d.Backends()
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

	backends := d.Backends()
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

	backends := d.Backends()
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

	backends := d.Backends()
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
	backends := d.Backends()
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
	if got.PRURL != "" {
		t.Errorf("PRURL should be empty, got %q", got.PRURL)
	}
}

func TestDB_PRURL(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "pr task"}
	_ = d.Add(task)

	// Initially empty.
	got, _ := d.Get(task.ID)
	if got.PRURL != "" {
		t.Errorf("expected empty PRURL, got %q", got.PRURL)
	}

	// Set and persist.
	task.PRURL = "https://github.com/owner/repo/pull/42"
	_ = d.Update(task)

	got, _ = d.Get(task.ID)
	if got.PRURL != "https://github.com/owner/repo/pull/42" {
		t.Errorf("PRURL = %q", got.PRURL)
	}
}

func TestDB_BackendCommandRoundtrip(t *testing.T) {
	d := testDB(t)

	if err := d.SetBackend("codex", config.Backend{
		Command:    "codex --dangerously-bypass-approvals-and-sandbox",
		PromptFlag: "",
	}); err != nil {
		t.Fatal(err)
	}

	backends := d.Backends()
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

	backends := d.Backends()
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
	tasks := d.Tasks()
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
	remaining := d.Tasks()
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

	tasks := d2.Tasks()
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

// --- TodoPath tests ---

func TestDB_TodoPath_RoundTrip(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "todo task", TodoPath: "/vault/my-note.md"}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TodoPath != "/vault/my-note.md" {
		t.Errorf("TodoPath = %q, want %q", got.TodoPath, "/vault/my-note.md")
	}
}

func TestDB_TodoPath_Update(t *testing.T) {
	d := testDB(t)

	task := &model.Task{Name: "todo task", TodoPath: "/vault/old.md"}
	if err := d.Add(task); err != nil {
		t.Fatal(err)
	}

	task.TodoPath = "/vault/new.md"
	if err := d.Update(task); err != nil {
		t.Fatal(err)
	}

	got, err := d.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TodoPath != "/vault/new.md" {
		t.Errorf("TodoPath = %q, want %q", got.TodoPath, "/vault/new.md")
	}
}

func TestDB_TasksByTodoPath(t *testing.T) {
	d := testDB(t)

	t.Run("excludes tasks without todo_path", func(t *testing.T) {
		task := &model.Task{Name: "no path"}
		if err := d.Add(task); err != nil {
			t.Fatal(err)
		}
		m := d.TasksByTodoPath()
		if _, ok := m[""]; ok {
			t.Error("should not include tasks with empty todo_path")
		}
	})

	t.Run("returns linked tasks", func(t *testing.T) {
		task := &model.Task{Name: "linked", TodoPath: "/vault/linked.md"}
		if err := d.Add(task); err != nil {
			t.Fatal(err)
		}
		m := d.TasksByTodoPath()
		if got, ok := m["/vault/linked.md"]; !ok {
			t.Error("expected entry for /vault/linked.md")
		} else if got.Name != "linked" {
			t.Errorf("Name = %q, want %q", got.Name, "linked")
		}
	})

	t.Run("most recent task wins", func(t *testing.T) {
		first := &model.Task{Name: "first", TodoPath: "/vault/dup.md", CreatedAt: time.Now().Add(-time.Hour)}
		if err := d.Add(first); err != nil {
			t.Fatal(err)
		}
		second := &model.Task{Name: "second", TodoPath: "/vault/dup.md", CreatedAt: time.Now()}
		if err := d.Add(second); err != nil {
			t.Fatal(err)
		}
		m := d.TasksByTodoPath()
		if got := m["/vault/dup.md"]; got == nil || got.Name != "second" {
			name := ""
			if got != nil {
				name = got.Name
			}
			t.Errorf("expected most recent task 'second', got %q", name)
		}
	})
}
