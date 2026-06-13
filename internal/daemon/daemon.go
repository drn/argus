package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/depswatcher"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/inject"
	injectcodex "github.com/drn/argus/internal/inject/codex"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/push"
	"github.com/drn/argus/internal/scheduler"
	"github.com/drn/argus/internal/uxlog"
)

// prPollInterval is the cadence at which the daemon refreshes cached PR review
// state for every eligible task. Kept well above per-tick gh cost so a large
// task set stays under GitHub's authenticated rate limit (design.md).
const prPollInterval = 60 * time.Second

// prPollConcurrency caps the number of concurrent `gh pr view` processes the
// poller spawns per tick. Bounded so a big task set can't fork-bomb gh.
const prPollConcurrency = 4

// DefaultSocketPath returns the default Unix socket path.
func DefaultSocketPath() string {
	return filepath.Join(db.DataDir(), "daemon.sock")
}

// DefaultPIDPath returns the default PID file path.
func DefaultPIDPath() string {
	return filepath.Join(db.DataDir(), "daemon.pid")
}

// ExitInfo holds the exit state of a finished session, cached briefly
// so clients can query it after the stream closes.
type ExitInfo struct {
	Err            string
	Stopped        bool
	LastOutput     []byte
	StreamLost     bool // true when stream disconnected but process exit not confirmed
	PendingRestart bool // true when a kick-restart is queued (TUI must skip status flip)
}

// CleanExit is the single, authoritative predicate for "the agent process
// exited on its own, normally" — the ONLY condition under which a task may be
// marked Complete. Everything else (explicit stop, non-zero exit / crash,
// fast-fail launch surfaced as an error, or an unconfirmed stream loss) is a
// recoverable exit and lands the task in InReview so the user can resume.
//
// Both status-flip sites — the daemon's transitionTaskOnExit (authoritative for
// headless/PWA) and the TUI's handleSessionExitUI (an idempotent retry that can
// win the race) — MUST derive their decision from this one predicate so they
// can never disagree. The rule is intentionally timing-independent: a missing
// binary / crash arrives as a non-empty Err, so we never need a wall-clock
// heuristic to tell "did real work happen" in the common case.
func (e ExitInfo) CleanExit() bool {
	return !e.Stopped && e.Err == "" && !e.StreamLost
}

// Daemon manages agent sessions and exposes them over a Unix socket.
type Daemon struct {
	db        *db.DB
	runner    *agent.Runner
	listener  net.Listener
	streams   map[string][]net.Conn // taskID → connected stream clients
	exitInfos map[string]ExitInfo   // taskID → cached exit info (brief)
	mu        sync.Mutex
	done      chan struct{}
	ready     chan struct{}        // closed when Serve has set listener (or failed)
	sockPath  string               // set by Serve, used by cleanup
	pidPath   string               // set by Serve, used by cleanup
	lockFile  *os.File             // singleton flock held for the daemon's lifetime
	mcpPort   int                  // actual MCP HTTP port in use (set after listen)
	apiPort   int                  // actual REST API HTTP port in use (set after listen)
	mcpServer *mcp.Server          // set when KB is enabled, shut down in cleanup
	kbIndexer *kb.Indexer          // set when KB is enabled, stopped in cleanup
	apiServer *api.Server          // set when API is enabled, shut down in cleanup
	scheduler *scheduler.Scheduler // recurring scheduled-task firer; always started
	deps      *depswatcher.Watcher // depends_on auto-resolver; always started
	clipboard *clipboard.Store     // agent-staged clipboard, in-memory

	// Boot identity — recorded once at New() so the TUI can detect when the
	// on-disk binary has been rebuilt since the daemon started.
	binaryPath  string
	binaryMtime time.Time
	bootedAt    time.Time

	// prFetch is the injectable seam the PR-status poller calls to resolve a
	// task branch's review state. Defaults to gitutil.FetchPRState; tests swap
	// it for a fake so the poller never spawns a real gh process.
	prFetch func(ctx context.Context, worktreeDir, branch string) (model.PRState, string, error)

	// notifier is the reliable pane-delivery service. Created in Serve once
	// the runner and focus tracker are ready. Nil until Serve runs.
	notifier *notify.Notifier

	// focusTracker tracks which task pane a human is currently focused on.
	// Shared between the daemon (notifier gate), the API server (REST wiring),
	// and the TUI (focus signals). Created in Serve.
	focusTracker *notify.FocusTracker
}

// New creates a new Daemon.
func New(database *db.DB) *Daemon {
	d := &Daemon{
		db:        database,
		streams:   make(map[string][]net.Conn),
		exitInfos: make(map[string]ExitInfo),
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		bootedAt:  time.Now(),
		clipboard: clipboard.New(),
		prFetch:   gitutil.FetchPRState,
	}

	// Capture the binary path + mtime at startup. The on-disk binary may be
	// rebuilt while the daemon keeps running with the old in-memory image —
	// the TUI compares its current binary mtime against this snapshot.
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		d.binaryPath = exe
		if st, err := os.Stat(exe); err == nil {
			d.binaryMtime = st.ModTime()
		}
	}

	// Create runner with onFinish callback that caches exit info, flips
	// the DB status, and notifies stream clients by closing their connections.
	d.runner = agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		slog.Info("session exited", "task", taskID, "stopped", stopped, "err", err, "lastOutputBytes", len(lastOutput))

		var errStr string
		if err != nil {
			errStr = err.Error()
		}

		// Snapshot HasPendingRestart once and stamp it onto ExitInfo so the
		// TUI can read it from the exit notification without an extra RPC
		// from the tview main goroutine (the gotcha at daemon-rpc.md:9).
		pending := d.runner.HasPendingRestart(taskID)

		ei := ExitInfo{
			Err:            errStr,
			Stopped:        stopped,
			LastOutput:     lastOutput,
			PendingRestart: pending,
		}
		d.mu.Lock()
		d.exitInfos[taskID] = ei
		conns := d.streams[taskID]
		delete(d.streams, taskID)
		d.mu.Unlock()

		// Flip the DB row out of InProgress. Without this, a daemon-only
		// setup (web-app users with no TUI attached) leaves the row stuck
		// in_progress forever — the API then reports idle:true and the PWA
		// pops a Resume modal for a task whose agent has already exited.
		// The TUI's HandleSessionExit also runs this transition; both call
		// sites are guarded by the StatusInProgress check, so whichever
		// fires first wins and the other becomes a no-op.
		//
		// SKIP when a kick-restart is in flight (KickRerender stopped the
		// session and queued a same-task Start that fires immediately from
		// the runner's exit goroutine). Transitioning to InReview here would
		// race the restart and leave the row in the wrong state mid-flip.
		if !pending {
			d.transitionTaskOnExit(taskID, ei.CleanExit())
		}

		// Capture session ID for backends that mint it themselves post-exit
		// (codex via state_5.sqlite, pi via session-file scan). The TUI's
		// handleSessionExitUI also fires this for foreground sessions; both
		// paths are idempotent (guard on SessionID == "" before scheduling).
		// Without this branch, headless / PWA-only users can never resume
		// codex or pi tasks because nothing ever writes back the UUID.
		go d.captureSessionIDPostExit(taskID)

		// Signal stream EOF to all connected clients by closing their connections.
		slog.Info("session exited, closing stream clients", "task", taskID, "clients", len(conns))
		for _, conn := range conns {
			conn.Close()
		}

		// Clear any agent-staged clipboard for the finished task — the
		// agent that staged it is gone, the user shouldn't see a stale
		// copy button after the session ends.
		d.clipboard.Clear(taskID)

		// Surface session lifecycle to plugins. Fired AFTER the runner has
		// removed the session and after the DB transition has run so an
		// SSE subscriber waking on this event sees a coherent snapshot.
		events.Emit(model.EventTypeSessionExited, taskID, map[string]any{
			"stopped":         stopped,
			"err":             errStr,
			"pending_restart": pending,
		})
	})

	return d
}

// Runner returns the underlying runner for direct access (e.g., AddWriter).
func (d *Daemon) Runner() *agent.Runner {
	return d.runner
}

// captureSessionIDPostExit refreshes a task's session UUID from backend state.
// Runs in its own goroutine from onFinish so it never blocks the runner exit
// path. The recapture decision is delegated to agent.NeedsSessionRecapture:
// codex/pi capture once (while SessionID is empty), Claude refreshes on every
// exit (a /clear mints a new UUID under ~/.claude/projects), unknown backends
// never recapture. Concurrent TUI-side capture is harmless: both paths run
// CaptureSessionID, a pure read of the same backend state, so last-writer-wins
// produces the same value in the common case.
//
// Edge case: if the user starts a brand-new session for the same task in
// the few-ms gap between onFinish and the TUI's QueueUpdateDraw, the two
// captures could observe different "newest" rows. The resulting SessionID
// still points at a valid session for the same task, so we intentionally
// accept this benign drift rather than serialize the two paths.
//
// No-op for unknown backends (dispatcher returns ("", nil)), for unchanged IDs
// (the common no-/clear Claude exit), and for tasks already deleted before the
// goroutine runs.
//
// NOTE on log lines: this logs without a backend-kind tag (e.g. "codex" /
// "pi"). The daemon's slog output already carries the structured task=<id>
// field, so a consumer can resolve the kind from the task row. The TUI's
// analog DOES include the tag because uxlog is a flat text channel and
// operators searching for "pi capture failed" need it inline. Keep this
// asymmetry intentional — don't mirror the TUI tag dance here.
func (d *Daemon) captureSessionIDPostExit(taskID string) {
	t, err := d.db.Get(taskID)
	if err != nil || t == nil || t.Worktree == "" {
		return
	}
	// Codex/Pi capture once (guard on empty SessionID); Claude refreshes on
	// every exit so a /clear-minted UUID supersedes the stale one. Unknown
	// backends never recapture.
	cfg := d.db.Config()
	if !agent.NeedsSessionRecapture(t, cfg) {
		return
	}
	sid, err := agent.CaptureSessionID(t, cfg)
	if err != nil {
		// Capture failure leaves the existing SessionID intact — never clobber.
		slog.Warn("daemon: session ID capture failed", "task", taskID, "err", err)
		return
	}
	if sid == "" || sid == t.SessionID {
		return // Unrecognized backend, or no change (common no-/clear exit).
	}
	t2, err := d.db.Get(taskID)
	if err != nil || t2 == nil {
		return
	}
	t2.SessionID = sid
	if uerr := d.db.Update(t2); uerr != nil {
		slog.Warn("daemon: session ID persist failed", "task", taskID, "err", uerr)
		return
	}
	slog.Info("daemon: session ID captured", "task", taskID, "sid", sid)
}

// transitionTaskOnExit flips an InProgress task to its terminal status when
// its session exits. Only a clean exit (ExitInfo.CleanExit — the process ended
// on its own with a zero exit code, e.g. the user typed Ctrl-D / `/exit`) lands
// the task in Complete; every other exit (explicit stop, crash, missing-binary
// launch failure, unconfirmed stream loss) lands in InReview so the user can
// resume rather than having a still-unfinished task silently marked done.
// No-op if the row has already moved on (e.g., the TUI's HandleSessionExit
// won the race, or the user manually changed status mid-exit).
func (d *Daemon) transitionTaskOnExit(taskID string, cleanExit bool) {
	t, err := d.db.Get(taskID)
	if err != nil || t == nil || t.Status != model.StatusInProgress {
		return
	}
	if cleanExit {
		t.SetStatus(model.StatusComplete)
	} else {
		t.SetStatus(model.StatusInReview)
	}
	if uerr := d.db.Update(t); uerr != nil {
		slog.Warn("session exit: status update failed", "task", taskID, "err", uerr)
		return
	}
	slog.Info("session exit: status flipped", "task", taskID, "status", t.Status.String())
}

// pollPRStatesOnce runs a single PR-status refresh pass over every eligible
// task. Eligible = not archived AND has a non-empty Branch. For each eligible
// task it calls d.prFetch (the gitutil.FetchPRState seam) under a bounded
// worker pool (prPollConcurrency) and persists a successful result into
// task_meta namespace "pr" (keys "state" and "url").
//
// Keep-stale contract: a fetch that returns a non-nil error is transient — the
// existing cached value is left untouched (no write). A nil error (including an
// unambiguous PRNone or a PRUnknown for gh-absent) is authoritative and is
// written. This mirrors gitutil.FetchPRState's documented return contract.
func (d *Daemon) pollPRStatesOnce(ctx context.Context) {
	tasks, err := d.db.Tasks()
	if err != nil {
		uxlog.Log("[pr] poll: list tasks failed: %v", err)
		return
	}

	// Collect eligible tasks up front so the logged count is exact.
	eligible := make([]*model.Task, 0, len(tasks))
	for _, t := range tasks {
		if t == nil || t.Archived || t.Branch == "" {
			continue
		}
		eligible = append(eligible, t)
	}
	if len(eligible) == 0 {
		return
	}

	sem := make(chan struct{}, prPollConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var written, errored int

	for _, t := range eligible {
		wg.Add(1)
		sem <- struct{}{}
		go func(t *model.Task) {
			defer wg.Done()
			defer func() { <-sem }()

			state, url, ferr := d.prFetch(ctx, t.Worktree, t.Branch)
			if ferr != nil {
				// Transient failure — keep the cached value intact.
				mu.Lock()
				errored++
				mu.Unlock()
				uxlog.Log("[pr] poll: fetch failed for %s (keeping stale): %v", t.ID, ferr)
				return
			}
			if werr := d.db.SetMetaBatch(t.ID, "pr", map[string]string{
				"state": state.String(),
				"url":   url,
			}); werr != nil {
				uxlog.Log("[pr] poll: persist failed for %s: %v", t.ID, werr)
				return
			}
			mu.Lock()
			written++
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	uxlog.Log("[pr] poll: eligible=%d written=%d errored=%d", len(eligible), written, errored)
}

// runPRPoller is the PR-status poller goroutine body. It runs pollPRStatesOnce
// every prPollInterval until d.done is closed, at which point it returns so
// daemon shutdown does not hang. Extracted from Serve so tests can verify the
// d.done-gated exit deterministically without binding a real socket.
func (d *Daemon) runPRPoller() {
	ticker := time.NewTicker(prPollInterval)
	defer ticker.Stop()

	// Derive a cancelable context so a tick already running pollPRStatesOnce
	// (which spawns up to prPollConcurrency `gh` procs, each with a 5s timeout)
	// aborts immediately on shutdown instead of letting those procs run to
	// their timeout. cancel() fires both on the d.done branch and on return.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case <-d.done:
			cancel()
			return
		case <-ticker.C:
			d.pollPRStatesOnce(ctx)
		}
	}
}

// Clipboard returns the agent-staged clipboard store. Used by the API
// server (HTTP + SSE subscribe) and the MCP server (agent stages text).
func (d *Daemon) Clipboard() *clipboard.Store {
	return d.clipboard
}

// Serve starts listening on the given socket path and accepts connections.
// Blocks until Shutdown is called or the listener is closed.
func (d *Daemon) Serve(sockPath string) error {
	// Derive PID path from socket path so tests using temp dirs don't
	// touch ~/.argus/ and accidentally kill a real running daemon.
	pidPath := filepath.Join(filepath.Dir(sockPath), "daemon.pid")
	d.sockPath = sockPath
	d.pidPath = pidPath

	// Kill any existing daemon process before taking over the socket.
	killExistingDaemon(pidPath)

	// Singleton guard: exactly one daemon may own the socket. killExistingDaemon
	// is pid-file based and racy — a startup race (e.g. a launchd job and the
	// TUI autostart firing together at login) lets multiple daemons each pass
	// that check, unlink+rebind the socket, and coexist. The result is a
	// split-brain: the agent session lives in one daemon's runner while the
	// TUI's input/stream RPCs land on another, so keyboard input vanishes and
	// StartSession reports "session already exists". The flock makes the loser
	// of any such race exit cleanly. See gotchas/daemon-rpc.md.
	lockFile, lerr := acquireSingletonLock(daemonLockPath(sockPath), daemonLockTimeout)
	if lerr != nil {
		close(d.ready) // unblock Shutdown waiters even on early return
		if errors.Is(lerr, ErrDaemonAlreadyRunning) {
			slog.Info("another daemon already holds the singleton lock; exiting", "sock", sockPath)
			return ErrDaemonAlreadyRunning
		}
		return fmt.Errorf("acquire daemon lock: %w", lerr)
	}
	d.lockFile = lockFile

	// Remove stale socket file.
	os.Remove(sockPath)

	// Sweep DB for tasks stuck at InProgress. The previous daemon's sessions
	// (if any) are dead; the runner here is empty, so any InProgress row is
	// orphaned. Flip them to InReview so the TUI/PWA can resume or discard.
	// Done before the listener accepts connections so first-poll clients see
	// the reconciled state.
	if n, err := agent.ReconcileStaleSessions(d.db); err != nil {
		slog.Warn("reconcile stale sessions failed", "err", err)
	} else if n > 0 {
		slog.Info("reconciled stale sessions", "count", n)
	}

	// Emit ARGUS_BOUNCED into the inbox of every task that had a live session
	// when the previous daemon exited. Runs after stale-session reconcile so
	// tasks are already flipped to InReview before the signal lands. Runs
	// before accepting connections so agents that auto-resume see the signal
	// on their first inbox poll.
	if err := replayBounceSignals(d.db, db.DataDir()); err != nil {
		slog.Warn("bounce: replay failed", "err", err)
	}

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		close(d.ready) // unblock Shutdown even on listen failure
		return fmt.Errorf("listen: %w", err)
	}
	d.mu.Lock()
	d.listener = ln
	d.mu.Unlock()
	close(d.ready)
	if err := writePIDFile(pidPath); err != nil {
		ln.Close()
		return fmt.Errorf("pid file: %w", err)
	}

	cfg := d.db.Config()

	// Push manager — best-effort, single instance shared between the API
	// server's idle watcher and the scheduler's kick-off hook so both use
	// the same VAPID keypair and subscriber list. Nil disables push.
	pushMgr, perr := push.New(d.db)
	if perr != nil {
		slog.Warn("push disabled", "err", perr)
		pushMgr = nil
	}

	// Start the scheduler (recurring scheduled tasks). Always-on — empty
	// table is a no-op, so there's no setting to gate it. Created before
	// the MCP server so SetScheduleManager can be wired before listening.
	sch := scheduler.New(d.db, func(name, prompt, project, backend string) (*model.Task, error) {
		// Schedule names are user-edited (then suffixed with a timestamp) —
		// already meaningful; no auto-rename. backend is the per-schedule
		// override (sched.Backend); empty string falls back to the configured
		// default inside agent.CreateAndStart.
		return HeadlessCreateTask(d.db, d.runner, HeadlessInput{
			Name:    name,
			Prompt:  prompt,
			Project: project,
			Backend: backend,
		})
	})
	if pushMgr != nil {
		// Push when a scheduled task fires from the cron tick. RunNow
		// (manual user-triggered fires from the UI) is intentionally exempt
		// in scheduler.go — the user is right there, they don't need a
		// notification for an action they just took.
		sch.SetOnFire(func(task *model.Task) {
			name := task.Name
			if name == "" {
				name = task.ID
			}
			// Empty throttle key: each scheduler fire creates a fresh task ID,
			// so a "schedule:<taskID>" throttle would accumulate one entry per
			// fire forever (memory leak) and never actually suppress anything.
			// The scheduler's own NextRunAt bookkeeping already prevents
			// double-fires for the same cron tick.
			pushMgr.Notify("", name, "Scheduled task started", task.ID)
		})
	}
	d.scheduler = sch
	go func() {
		if err := sch.Start(); err != nil {
			slog.Error("scheduler start", "err", err)
		}
	}()

	// Start the depends_on watcher. Always-on — empty pending pool is a
	// no-op tick. Push fires the same channel as the scheduler so the
	// orchestrator user sees "stacked task started" notifications without
	// needing to keep the PWA open.
	dw := depswatcher.New(d.db, d.runner)
	if pushMgr != nil {
		dw.SetOnStart(func(task *model.Task) {
			name := task.Name
			if name == "" {
				name = task.ID
			}
			pushMgr.Notify("", name, "Blocked task started (deps resolved)", task.ID)
		})
	}
	d.deps = dw
	go dw.Start()

	// Reliable pane-delivery: create the FocusTracker and Notifier before
	// the MCP and API servers so both can be wired at construction.
	d.focusTracker = notify.NewFocusTracker(func(taskID string, focused bool) {
		events.Emit(model.EventTypeSessionFocus, taskID, map[string]any{"focused": focused})
	})
	d.notifier = notify.New(notify.AdaptRunner(func(id string) notify.SessionHandleIface {
		return d.runner.Get(id)
	}), d.focusTracker)

	// Plugin substrate (PR 4): build the runtime MCP-tool registry up front
	// so both the MCP server (which consults it on tools/list and tools/call)
	// and the API server (which exposes POST/DELETE /api/mcp/tools) see the
	// same persistent table. The registry itself has no goroutines — the
	// sweep loop below is the only background work.
	pluginRegistry := mcp.NewRegistry(d.db)

	// Plugin-MCP idle sweep (PR 4). Tools whose plugin has gone silent for
	// DefaultIdleWindow fall away on the next tick. The tick period is half
	// the window so a tool's eviction lands within at most one full window
	// of its last heartbeat. d.done gates the loop so daemon shutdown
	// terminates the goroutine promptly.
	go func() {
		ticker := time.NewTicker(mcp.DefaultIdleWindow / 2)
		defer ticker.Stop()
		for {
			select {
			case <-d.done:
				return
			case <-ticker.C:
				swept, err := pluginRegistry.SweepIdle(mcp.DefaultIdleWindow)
				if err != nil {
					slog.Error("mcp idle sweep", "err", err)
					continue
				}
				for _, t := range swept {
					slog.Info("mcp idle sweep", "scope", t.Scope, "name", t.Name, "last_seen", t.LastSeenAt.UTC().Format(time.RFC3339))
				}
			}
		}
	}()

	// PR-status poller (add-pr-review-indicator). Refreshes cached GitHub PR
	// review state for every non-archived task that has a branch, persisting
	// into task_meta namespace "pr". Mirrors the idle-sweep shutdown pattern:
	// d.done terminates the goroutine promptly on daemon shutdown. Each tick's
	// work is factored into pollPRStatesOnce so tests can drive a single pass
	// directly instead of racing the ticker.
	go d.runPRPoller()

	// Start MCP HTTP server and KB indexer (only when KB is enabled in settings).
	if cfg.KB.Enabled {
		mcpSrv := mcp.New(d.db, cfg.KB.HTTPPort, cfg.KB.MetisVaultPath)
		mcpSrv.SetTaskManager(
			func(input mcp.TaskCreateInput) (*model.Task, error) {
				return HeadlessCreateTask(d.db, d.runner, HeadlessInput{
					Name:       input.Name,
					Prompt:     input.Prompt,
					Project:    input.Project,
					Model:      input.Model,
					AutoName:   input.AutoName,
					BaseBranch: input.BaseBranch,
					DependsOn:  input.DependsOn,
					PlanSlug:   input.PlanSlug,
				})
			},
			d.db,
			d.runner,
		)
		mcpSrv.SetClipboard(d.clipboard)
		mcpSrv.SetScheduleManager(d.db, sch)
		mcpSrv.SetMessageManager(d.db, runnerNudger{notifier: d.notifier})
		// M8: gate on cfg.Hera.Enabled once the Settings toggle lands (Milestone 8).
		mcpSrv.SetHeraService(hera.New(d.db, d.notifier), d.db)
		mcpSrv.SetArtifactManager(d.db)
		mcpSrv.SetPluginRegistry(pluginRegistry)
		d.mcpServer = mcpSrv
		actualPort, err := mcpSrv.ListenAndServe()
		if err != nil {
			slog.Error("mcp server error", "err", err)
		} else {
			d.mu.Lock()
			d.mcpPort = actualPort
			d.mu.Unlock()
			slog.Info("mcp server listening", "port", actualPort)

			// Inject MCP config into Claude Code and Codex.
			go func() {
				if err := inject.InjectGlobal(actualPort); err != nil {
					slog.Error("inject claude", "err", err)
				} else {
					slog.Info("inject claude", "port", actualPort)
				}
				if err := injectcodex.InjectGlobal(actualPort); err != nil {
					slog.Error("inject codex", "err", err)
				} else {
					slog.Info("inject codex", "port", actualPort)
				}
				if err := inject.SetClaudeProjectMcpTrust(); err != nil {
					slog.Error("inject claude trust", "err", err)
				}
			}()
		}

		// Start the KB indexer for the Metis vault.
		if cfg.KB.MetisVaultPath != "" {
			idx := kb.NewIndexer(d.db, cfg.KB.MetisVaultPath)
			d.kbIndexer = idx
			go func() {
				if err := idx.Start(); err != nil {
					slog.Error("kb indexer start", "err", err)
				}
			}()
		}
	}

	// Start HTTP API server (when enabled in settings).
	if cfg.API.Enabled {
		tokenPath := filepath.Join(db.DataDir(), "api-token")
		token, err := api.LoadOrCreateToken(tokenPath)
		if err != nil {
			slog.Error("api token error", "err", err)
		} else {
			apiSrv := api.New(d.db, d.runner, token, func(name, prompt, project, backend, taskModel string, autoName bool) (*model.Task, error) {
				return HeadlessCreateTask(d.db, d.runner, HeadlessInput{
					Name:     name,
					Prompt:   prompt,
					Project:  project,
					Backend:  backend,
					Model:    taskModel,
					AutoName: autoName,
				})
			}, pushMgr)
			apiSrv.SetScheduler(sch)
			apiSrv.SetClipboard(d.clipboard)
			apiSrv.SetMCPRegistry(pluginRegistry)
			apiSrv.SetNotifier(d.notifier)
			apiSrv.SetFocusTracker(d.focusTracker)
			d.apiServer = apiSrv
			// Wire the events.Sink to the API server's event bus so emission
			// sites (db, orch, runner, this file) feed /api/events/stream.
			// Cleared on daemon shutdown so a torn-down apiSrv can't be
			// invoked after the listener is closed.
			events.SetSink(apiSrv)
			apiPort, err := apiSrv.ListenAndServe(cfg.API.HTTPPort)
			if err != nil {
				slog.Error("api server error", "err", err)
			} else {
				d.mu.Lock()
				d.apiPort = apiPort
				d.mu.Unlock()
				slog.Info("api server listening", "port", apiPort)
			}
		}
	}

	// Register RPC service.
	svc := &RPCService{daemon: d}
	server := rpc.NewServer()
	if err := server.RegisterName("Daemon", svc); err != nil {
		ln.Close()
		return fmt.Errorf("register rpc: %w", err)
	}

	// Trap signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		select {
		case <-sigCh:
			d.Shutdown()
		case <-d.done:
		}
		// Restore default signal handling so a subsequent SIGTERM from
		// killExistingDaemon (new daemon starting) terminates the process
		// instead of being swallowed by the buffered sigCh channel.
		signal.Stop(sigCh)
	}()

	slog.Info("daemon listening", "sockPath", sockPath, "pid", os.Getpid())

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-d.done:
				// Run cleanup on the main goroutine so it completes before
				// the process exits. Shutdown() only signals — it does not
				// do cleanup, because it runs on a different goroutine
				// (signal handler or RPC handler) that gets killed when
				// main() returns.
				d.cleanup()
				return nil // clean shutdown
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go d.handleConn(conn, server)
	}
}

// handleConn dispatches a connection based on its first byte:
// 'R' for JSON-RPC, 'S' for output streaming.
func (d *Daemon) handleConn(conn net.Conn, server *rpc.Server) {
	defer conn.Close()

	// Read dispatch byte.
	var prefix [1]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return
	}

	switch prefix[0] {
	case 'R':
		server.ServeCodec(jsonrpc.NewServerCodec(conn))
	case 'S':
		d.handleStream(conn)
	default:
		slog.Warn("conn: unknown prefix byte", "byte", fmt.Sprintf("0x%02x", prefix[0]))
	}
}

// registerStream registers a stream connection for a task.
func (d *Daemon) registerStream(taskID string, conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.streams[taskID] = append(d.streams[taskID], conn)
}

// unregisterStream removes a stream connection for a task.
func (d *Daemon) unregisterStream(taskID string, conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	conns := d.streams[taskID]
	for i, c := range conns {
		if c == conn {
			d.streams[taskID] = append(conns[:i], conns[i+1:]...)
			return
		}
	}
}

// Shutdown signals the daemon to stop. It closes the done channel and the
// listener, causing the Serve accept loop to exit. Actual cleanup (StopAll,
// file removal) happens in Serve's exit path on the main goroutine — this
// ensures cleanup completes before the process exits.
func (d *Daemon) Shutdown() {
	select {
	case <-d.done:
		return // already shutting down
	default:
		close(d.done)
	}

	// Wait for Serve to have set the listener (or failed to start).
	<-d.ready

	d.mu.Lock()
	ln := d.listener
	d.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
}

// cleanup runs on the main goroutine (Serve's exit path) to ensure it
// completes before the process exits. If Shutdown ran these on its goroutine
// (signal/RPC handler), main() could return from Serve() first, killing
// the cleanup goroutine and leaving zombie agent processes + stale files.
func (d *Daemon) cleanup() {
	slog.Info("daemon shutting down")

	// Persist the live session set before stopping agents so hera workers can
	// detect the bounce on the next daemon start (see bounce.go).
	if err := writeLiveTasksFile(d.runner, db.DataDir()); err != nil {
		slog.Warn("bounce: persist live-tasks failed", "err", err)
	}

	d.runner.StopAll()

	// Stop the scheduler if running.
	if d.scheduler != nil {
		d.scheduler.Stop()
	}

	// Stop the depends_on watcher if running.
	if d.deps != nil {
		d.deps.Stop()
	}

	// Stop the KB indexer if running.
	if d.kbIndexer != nil {
		d.kbIndexer.Stop()
	}

	// Shut down the API HTTP server if running.
	if d.apiServer != nil {
		// Detach the events sink first so emission sites racing with
		// shutdown (e.g. db.Update from the post-exit transition) don't
		// hit a closed event bus.
		events.SetSink(nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.apiServer.Shutdown(ctx); err != nil {
			slog.Error("api server shutdown", "err", err)
		}
	}

	// Shut down the MCP HTTP server if running.
	if d.mcpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := d.mcpServer.Shutdown(ctx); err != nil {
			slog.Error("mcp server shutdown", "err", err)
		}
	}

	// Only clean up socket and PID files if we still own them.
	// A newer daemon may have already replaced these files — removing them
	// would break the newer daemon's stream connections.
	removeIfOwnedByPID(d.sockPath, d.pidPath, os.Getpid())

	// Release the singleton lock last, so a daemon waiting to take over only
	// proceeds once our socket/pid cleanup is done. Closing the fd releases
	// the flock; the lock file itself is intentionally left in place (flock
	// contenders must open the same inode). Process exit would release it
	// anyway, but explicit close keeps takeover fast and deterministic.
	if d.lockFile != nil {
		d.lockFile.Close() //nolint:errcheck // releasing the lock; close errors are non-actionable
		d.lockFile = nil
	}
}

// writePIDFile atomically writes the current process PID to a file.
func writePIDFile(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readPIDFile reads the PID from a PID file. Returns 0 if the file
// doesn't exist or can't be parsed.
func readPIDFile(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// killExistingDaemon reads the PID file and kills the existing daemon
// process if it's still alive. Waits briefly for it to exit.
func killExistingDaemon(pidPath string) {
	pid := readPIDFile(pidPath)
	if pid == 0 || pid == os.Getpid() {
		return
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}

	// Check if process is alive (signal 0 doesn't kill, just checks).
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return // process already dead
	}

	slog.Info("killing existing daemon", "pid", pid)
	_ = proc.Signal(syscall.SIGTERM)

	// Wait up to 2 seconds for it to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return // exited
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Force kill if still alive.
	slog.Warn("force-killing daemon", "pid", pid)
	_ = proc.Signal(syscall.SIGKILL)
}

// removeIfOwnedByPID removes the socket and PID files only if the PID file
// still contains our PID. Prevents a zombie daemon from deleting a newer
// daemon's socket.
func removeIfOwnedByPID(sockPath, pidPath string, ourPID int) {
	currentPID := readPIDFile(pidPath)
	if currentPID != ourPID {
		slog.Warn("skipping file cleanup", "pidFileOwner", currentPID, "ourPID", ourPID)
		return
	}
	os.Remove(sockPath)
	os.Remove(pidPath)
}
