package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// Attachment is a user-uploaded file to be written into the worktree before
// the agent starts. Name is the sanitized filename (no path components); Data
// is the raw bytes. Saved into <worktree>/.context/<name>.
type Attachment struct {
	Name string
	Data []byte
}

// CreateInput describes a new task to create and start.
//
// Only Name and Project are required. Backend defaults to cfg.Defaults.Backend.
// Rows/Cols default to 24/80 — agents auto-resize when the TUI attaches.
type CreateInput struct {
	Name       string
	Prompt     string
	Project    string
	Backend    string // optional; empty = cfg.Defaults.Backend
	BaseBranch string // optional; overrides projCfg.Branch for this task

	// DependsOn is the list of task IDs that must reach status=complete
	// before this task's agent session is started. When non-empty,
	// CreateAndStart persists the row in Pending state and skips Step 5
	// entirely — the worktree exists and the row is in the DB, but no
	// process runs yet. The dependency watcher (internal/depswatcher) then
	// auto-starts the session once every listed task reports complete.
	// Empty / nil restores the legacy single-task behaviour: start immediately.
	DependsOn []string

	// AutoName, when true, fires a fire-and-forget Haiku rename in a
	// background goroutine after the task is fully created. The DB write
	// is race-guarded: it only overwrites Name if the row's current Name
	// still equals the regex-derived slug. Callers should set this only
	// when Name was string-interpolated from Prompt (not user-typed and
	// not a structured slug like "<src>-fork" worth preserving).
	AutoName bool

	// Attachments are written to <worktree>/.context/<name> after worktree
	// creation but before the session starts, and their paths are appended
	// to Prompt so the agent sees them on first turn.
	Attachments []Attachment

	Rows uint16 // initial PTY rows (0 → 24)
	Cols uint16 // initial PTY cols (0 → 80)

	// OnWorktreeCreated runs after the worktree exists but before the task
	// row is persisted. Use this for writing per-worktree context files
	// (e.g. fork-task prompts). If it returns an error, the worktree is
	// removed and CreateAndStart returns the error.
	//
	// Runs on the calling goroutine — typically a background goroutine
	// spawned by the TUI. MUST NOT call tview widget methods or mutate
	// any state that assumes the tview main goroutine; those are data
	// races without QueueUpdateDraw.
	OnWorktreeCreated func(wtPath string) error

	// BeforeStart runs immediately before runner.Start. Used by the TUI to
	// bump its startGen counter so in-flight tick reconciliations see a new
	// generation.
	//
	// Runs on the calling goroutine (background, not tview main). Same
	// tview-safety caveat as OnWorktreeCreated applies.
	BeforeStart func()

	// AfterStart runs immediately after runner.Start returns, whether or
	// not it succeeded — callers must not assume the session is live when
	// this fires. On failure the unwind chain runs next. Used by the TUI
	// to post-bump startGen so ticks that captured runningIDs mid-RPC
	// skip stale reconciliation.
	//
	// Runs on the calling goroutine (background, not tview main). Same
	// tview-safety caveat as OnWorktreeCreated applies.
	AfterStart func()
}

// CreateAndStart is the single entry point for fully-transactional task
// creation. It performs, in order: resolve project config, create worktree,
// run OnWorktreeCreated hook, persist task row, generate session ID, and
// start the agent session. Each side-effecting step registers a compensating
// cleanup that runs in LIFO order if any later step fails — so a failure
// leaves no orphan worktree, no orphan branch, and no orphan DB row behind.
//
// Callers that need to react to the slow worktree step (e.g. show a spinner)
// should invoke CreateAndStart from a goroutine; its steps are mutex-safe and
// do not require the tview main goroutine.
//
// The returned task has Status=InProgress and AgentPID populated. The session
// is live and ready to be attached to a UI pane.
func CreateAndStart(database *db.DB, runner SessionProvider, input CreateInput) (*model.Task, SessionHandle, error) {
	cfg := database.Config()

	projCfg, ok := cfg.Projects[input.Project]
	if !ok {
		return nil, nil, fmt.Errorf("project %q not found in config", input.Project)
	}
	if projCfg.Path == "" {
		return nil, nil, fmt.Errorf("project %q has no path configured", input.Project)
	}

	// Compensating-action stack: each successful side effect appends an undo
	// closure. unwind runs them LIFO. We run cleanups outside any critical
	// section — any cleanup failure is logged but cannot stop the chain.
	// The triggering label ("runner.Start", "OnWorktreeCreated", …) flows
	// into each cleanup via the unwindTrigger closure variable so per-step
	// error logs carry the trigger context.
	var cleanups []func(trigger string)
	var unwindTrigger string
	unwind := func(label string, cause error) {
		slog.Warn("CreateAndStart: unwinding", "trigger", label, "err", cause)
		unwindTrigger = label
		for i := len(cleanups) - 1; i >= 0; i-- {
			func(fn func(string)) {
				defer func() {
					if r := recover(); r != nil {
						slog.Error("CreateAndStart: unwind step panicked", "trigger", unwindTrigger, "recover", r)
					}
				}()
				fn(unwindTrigger)
			}(cleanups[i])
		}
	}

	// Step 1: create worktree. BaseBranch overrides projCfg.Branch when set —
	// the new-task form lets the user pick a custom start point per task.
	baseBranch := input.BaseBranch
	if baseBranch == "" {
		baseBranch = projCfg.Branch
	}
	wtPath, finalName, branchName, err := CreateWorktree(projCfg.Path, input.Project, input.Name, baseBranch)
	if err != nil {
		return nil, nil, fmt.Errorf("worktree: %w", err)
	}
	repoPath := projCfg.Path
	cleanups = append(cleanups, func(trigger string) {
		slog.Info("CreateAndStart unwind: remove worktree", "trigger", trigger, "path", wtPath, "branch", branchName)
		RemoveWorktreeAndBranch(wtPath, branchName, repoPath)
	})

	// Step 2a: write user-uploaded attachments into <worktree>/.context/.
	// Done before OnWorktreeCreated so fork-context files (which write into
	// the same dir) can't collide with attachment names by chance.
	attachPaths, err := writeAttachments(wtPath, input.Attachments)
	if err != nil {
		unwind("attachments", err)
		return nil, nil, fmt.Errorf("attachments: %w", err)
	}

	// Step 2b: optional per-worktree hook (e.g. fork context files).
	if input.OnWorktreeCreated != nil {
		if err := input.OnWorktreeCreated(wtPath); err != nil {
			unwind("OnWorktreeCreated", err)
			return nil, nil, fmt.Errorf("worktree hook: %w", err)
		}
	}

	// Append attachment paths to the prompt so the agent sees them on the
	// first turn. Done after the hook so any fork-context preamble appears
	// first in the prompt — fork is the established case.
	prompt := input.Prompt
	if len(attachPaths) > 0 {
		prompt = appendAttachmentList(prompt, attachPaths)
	}

	// Step 3: build task and persist.
	backend := input.Backend
	if backend == "" {
		backend = cfg.Defaults.Backend
	}
	task := &model.Task{
		Name:       finalName,
		Status:     model.StatusPending,
		Project:    input.Project,
		Prompt:     prompt,
		Backend:    backend,
		Worktree:   wtPath,
		Branch:     branchName,
		BaseBranch: baseBranch,
		DependsOn:  input.DependsOn,
	}
	// Persist sandbox state at creation time so the display reflects the
	// setting active when the task was launched, not the current setting.
	task.Sandboxed = IsTaskSandboxed(task, cfg)

	if err := database.Add(task); err != nil {
		unwind("db.Add", err)
		return nil, nil, fmt.Errorf("db add: %w", err)
	}
	// Capture task ID before registering the cleanup so it cannot drift.
	taskID := task.ID
	cleanups = append(cleanups, func(trigger string) {
		if dErr := database.Delete(taskID); dErr != nil {
			slog.Error("CreateAndStart unwind db.Delete failed", "trigger", trigger, "id", taskID, "err", dErr)
		}
	})

	// Step 4: generate session ID for Claude-style backends.
	// Codex and pi don't support --session-id; their IDs are captured post-exit.
	if resolved, berr := ResolveBackend(task, cfg); berr == nil && !IsCodexBackend(resolved.Command) && !IsPiBackend(resolved.Command) {
		task.SessionID = model.GenerateSessionID()
		if uErr := database.Update(task); uErr != nil {
			slog.Warn("CreateAndStart: persist session ID failed (continuing)", "id", taskID, "err", uErr)
		}
	}

	// Step 4b: short-circuit when the task is gated on upstream deps. The
	// worktree exists, the row is persisted, the session ID is reserved —
	// but we don't spawn the agent until depswatcher observes every listed
	// dep at status=complete. CreateAndStart returns (task, nil, nil) so
	// callers (HeadlessCreateTask, MCP) get the row back without a live
	// SessionHandle. AutoName still runs — Haiku rename does not need the
	// agent to be alive.
	if len(input.DependsOn) > 0 {
		slog.Info("task created blocked on deps", "id", taskID, "name", task.Name, "deps", input.DependsOn)
		if input.AutoName {
			go runAutoRename(database, taskID, task.Name, input.Prompt)
		}
		return task, nil, nil
	}

	// Step 5: start session.
	rows := input.Rows
	if rows == 0 {
		rows = 24
	}
	cols := input.Cols
	if cols == 0 {
		cols = 80
	}

	if input.BeforeStart != nil {
		input.BeforeStart()
	}
	sess, err := runner.Start(task, cfg, rows, cols, false)
	if input.AfterStart != nil {
		input.AfterStart()
	}
	if err != nil {
		unwind("runner.Start", err)
		return nil, nil, fmt.Errorf("start session: %w", err)
	}

	// Step 6: flip to InProgress and record PID. Happens after AfterStart so
	// startGen is already post-bumped by the time any tick can observe the
	// status change.
	task.SetStatus(model.StatusInProgress)
	task.StartedAt = time.Now()
	task.AgentPID = sess.PID()
	if uErr := database.Update(task); uErr != nil {
		slog.Warn("CreateAndStart: persist InProgress failed (session is running)", "id", taskID, "err", uErr)
	}

	slog.Info("task created and started", "id", taskID, "name", task.Name, "project", input.Project, "pid", sess.PID())

	// Fire-and-forget Haiku rename. Runs after the task is live so a slow
	// or failing LLM call cannot block task startup. The goroutine is
	// race-guarded — if the user manually renames before Haiku returns,
	// the rename is skipped.
	if input.AutoName {
		go runAutoRename(database, taskID, task.Name, input.Prompt)
	}

	return task, sess, nil
}

// StartPendingBlocked launches an agent session for a task that was created
// blocked on deps (DependsOn was non-empty at CreateAndStart time). The
// caller — typically internal/depswatcher — confirms every dep is complete
// before invoking this. Mirrors Steps 5 + 6 from CreateAndStart: start the
// session, transition to InProgress, stamp StartedAt + AgentPID. On runner
// failure the task stays in Pending so the watcher can retry on the next
// tick. On db.Update failure (extremely rare — SQLite write error mid-call)
// the session is live but the row still says Pending; the watcher's next
// tick will re-enter this function, see the existing session via the
// HasSession guard below, and only re-attempt the DB write rather than
// spawning a duplicate process.
//
// Returns (sess, nil) on success or (nil, err) on failure. No worktree
// cleanup happens on failure — the row and worktree already exist and
// belong to the caller. nil runner is treated as a programmer error.
func StartPendingBlocked(database *db.DB, runner SessionProvider, task *model.Task) (SessionHandle, error) {
	if runner == nil {
		return nil, fmt.Errorf("StartPendingBlocked: nil runner")
	}
	if task == nil {
		return nil, fmt.Errorf("StartPendingBlocked: nil task")
	}
	if task.Status != model.StatusPending {
		return nil, fmt.Errorf("StartPendingBlocked: task %s already in status %s", task.ID, task.Status.String())
	}

	// Idempotency: if a previous call already spawned the session but the
	// DB write below failed, the runner still has the session in its map.
	// runner.Start would overwrite that slot and orphan the live process,
	// so short-circuit to the DB-sync path instead. Cheap; HasSession is a
	// lock-protected map lookup.
	if runner.HasSession(task.ID) {
		if existing := runner.Get(task.ID); existing != nil {
			slog.Info("StartPendingBlocked: existing session found, syncing DB", "id", task.ID, "pid", existing.PID())
			task.SetStatus(model.StatusInProgress)
			task.AgentPID = existing.PID()
			if uErr := database.Update(task); uErr != nil {
				slog.Warn("StartPendingBlocked: re-sync DB failed (session still running)", "id", task.ID, "err", uErr)
			}
			return existing, nil
		}
	}

	cfg := database.Config()

	sess, err := runner.Start(task, cfg, 24, 80, false)
	if err != nil {
		return nil, fmt.Errorf("start session: %w", err)
	}

	// SetStatus(InProgress) stamps StartedAt when previously zero, which is
	// always the case for a blocked task that has never had a session.
	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	if uErr := database.Update(task); uErr != nil {
		slog.Warn("StartPendingBlocked: persist InProgress failed (session is running, watcher will re-sync on next tick)", "id", task.ID, "err", uErr)
	}
	slog.Info("blocked task unblocked and started", "id", task.ID, "name", task.Name, "pid", sess.PID())
	return sess, nil
}

// AttachmentsDir is the worktree-relative directory where uploaded attachments
// are written. Same dir used by fork-context, which is fine — names are
// disambiguated by the API layer (see SaveAttachments).
const AttachmentsDir = ".context"

// writeAttachments saves each attachment under <wtPath>/.context/<name> and
// returns worktree-relative paths (with leading "./") suitable for embedding
// in a prompt. Names are sanitized by the caller before this is called; we
// still defend with filepath.Base to refuse path traversal.
//
// Within a single batch, duplicate names are auto-suffixed (foo.png,
// foo-1.png, foo-2.png, …) so a client uploading two same-named files
// never silently clobbers the first.
func writeAttachments(wtPath string, atts []Attachment) ([]string, error) {
	if len(atts) == 0 {
		return nil, nil
	}
	dir := filepath.Join(wtPath, AttachmentsDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	paths := make([]string, 0, len(atts))
	used := make(map[string]bool, len(atts))
	for _, a := range atts {
		name := filepath.Base(a.Name)
		if name == "" || name == "." || name == ".." {
			return nil, fmt.Errorf("invalid attachment name %q", a.Name)
		}
		name = uniqueAttachmentName(used, name)
		used[name] = true
		dst := filepath.Join(dir, name)
		// Name is filepath.Base'd above; dst stays under dir.
		if err := os.WriteFile(dst, a.Data, 0o600); err != nil { //nolint:gosec // path validated
			return nil, fmt.Errorf("write %s: %w", dst, err)
		}
		paths = append(paths, "./"+AttachmentsDir+"/"+name)
	}
	return paths, nil
}

// uniqueAttachmentName returns name unchanged if not already in `used`; else
// suffixes with -1, -2, … before the extension until a free slot is found.
// Caller must add the returned name to `used`.
func uniqueAttachmentName(used map[string]bool, name string) string {
	if !used[name] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !used[candidate] {
			return candidate
		}
	}
	// Fall back to original; the disk-write step will fail or clobber, but
	// at this point the user is on a 1000-collision pathological input.
	return name
}

// appendAttachmentList appends a human-readable list of attachment paths to
// the prompt so the agent sees them on the first turn. Returns the prompt
// unchanged when paths is empty.
func appendAttachmentList(prompt string, paths []string) string {
	if len(paths) == 0 {
		return prompt
	}
	var b strings.Builder
	if prompt != "" {
		b.WriteString(prompt)
		if !strings.HasSuffix(prompt, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Attached files:\n")
	for _, p := range paths {
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}
