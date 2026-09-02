package daemon

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/hera"
	"github.com/drn/argus/internal/heragater"
	"github.com/drn/argus/internal/inject"
	injectcodex "github.com/drn/argus/internal/inject/codex"
	injectopencode "github.com/drn/argus/internal/inject/opencode"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/push"
	"github.com/drn/argus/internal/scheduler"

	// Blank-imported for its init() side effect: registers the "things3"
	// factory with internal/todo so cfg.Todo.Backend = "things3" resolves.
	_ "github.com/drn/argus/internal/todo/things3"
	"github.com/drn/argus/internal/uxlog"
)

// prPollInterval is the cadence at which the daemon refreshes cached PR review
// state for every eligible task. Kept well above per-tick gh cost so a large
// task set stays under GitHub's authenticated rate limit (design.md).
const prPollInterval = 60 * time.Second

// prDefaultAliasCap is the default per-query alias cap (Decision 5, design.md).
// A repo group larger than this is split into sequential chunks so each query
// stays in the cheapest GraphQL complexity tier (ceil(nodeCount/100), min 1).
const prDefaultAliasCap = 100

// prPollDisableFlag is the basename of a sentinel file under the data dir. When
// it exists, the PR-status poller skips every cycle (issues zero gh queries) — an
// instant, no-restart kill-switch for the GitHub API budget. Drop the file to
// pause, remove it to resume; toggling never requires a daemon bounce.
const prPollDisableFlag = "pr-poller.disabled"

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
//
// The session-serving substrate (runner, stream-conn registry, exit-info cache,
// and the session-scoped RPC + stream handler) lives in the embedded
// *sessionCore, mounted here today and by the session-supervisor in P1 (see
// context/plans/session-supervisor.md). Promotion keeps every existing
// `d.runner`/`d.mu`/`d.streams`/`d.exitInfos`/`d.registerStream` call site and
// the session RPC methods byte-identical. `d.mu` (the core's mutex) guards the
// core's maps AND the daemon-local mcpPort/apiPort/listener fields below — one
// mutex, exactly as before the extraction.
type Daemon struct {
	*sessionCore
	db        *db.DB
	listener  net.Listener
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
	heraGater *heragater.Watcher   // hera plan-DAG gater; started when hera enabled
	// heraRecycleWatcher drives the self-service recycle_coord path
	// (add-coordinator-context-management D5): started when hera enabled,
	// polls for coordinators with a pending recycle request and restarts
	// each once its session goes idle.
	heraRecycleWatcher *hera.RecycleWatcher
	clipboard          *clipboard.Store // agent-staged clipboard, in-memory

	// Boot identity — recorded once at New() so the TUI can detect when the
	// on-disk binary has been rebuilt since the daemon started. binaryHash is
	// the primary staleness signal (see isDaemonStale); binaryMtime is kept
	// for the pre-BinaryHash fallback.
	binaryPath  string
	binaryMtime time.Time
	binaryHash  string
	vcs         buildid.VCS // daemon's own commit SHA + dirty flag; display-only (blank outside a git tree)
	bootedAt    time.Time

	// prFetch is the legacy single-branch fetch seam, retained as a fallback /
	// for any non-poller caller. The batched poller no longer calls it (it uses
	// prBatchFetch instead); see the batch seams below. Defaults to
	// gitutil.FetchPRState; tests swap it for a fake.
	prFetch func(ctx context.Context, worktreeDir, branch string) (model.PRState, string, error)

	// prBatchFetch resolves PR state for every branch in a repo group with a
	// single aliased GraphQL query. branches maps branch name → alias id; the
	// returned map is keyed by branch name. The int return is the GraphQL
	// complexity cost GitHub billed the query (logged per repo for observability
	// — Decision 4, design.md). Defaults to gitutil.FetchPRStatesBatch; tests
	// swap it for a fake so the poller never spawns a real gh process. A non-nil
	// error means keep-stale for the whole group (Decision 4).
	prBatchFetch func(ctx context.Context, repo string, branches map[string]string) (map[string]gitutil.PRResult, int, error)

	// prResolveRepo resolves a worktree's default GitHub repo ("owner/name") the
	// way gh would target it — the fallback when a task has no cached pr/url to
	// parse a repo from (Decision 2, design.md). Defaults to
	// gitutil.ResolveDefaultRepo; tests swap it. Runs off the UI thread (the
	// poller goroutine), consistent with all other git ops.
	prResolveRepo func(ctx context.Context, worktree string) (string, bool)

	// prAliasCap is the per-query alias cap that triggers chunking of an
	// oversized repo group (Decision 5). Defaults to prDefaultAliasCap; tests
	// set it small to force chunking without thousands of tasks.
	prAliasCap int

	// prDisableFlagPath is the absolute path to the poller kill-switch sentinel
	// (prPollDisableFlag under the data dir). When the file exists, every poll
	// cycle is skipped before any gh query. Defaults to the real data-dir path;
	// tests point it at a temp file. Empty disables the check entirely.
	prDisableFlagPath string

	// pollCycle counts PR-poll cycles since boot (incremented once per tick in
	// runPRPoller). It is the phase input to the dormancy-tiered cadence: a task
	// on stride N is queried on the cycles where (pollCycle + hash(id)) % N == 0,
	// spreading each tier's tasks across the stride window instead of bunching
	// them onto one cycle. Reset to 0 on restart (the schedule simply re-phases).
	pollCycle uint64

	// notifier is the reliable pane-delivery service. Created in Serve once
	// the runner and focus tracker are ready. Nil until Serve runs.
	notifier *notify.Notifier

	// focusTracker tracks which task pane a human is currently focused on.
	// Shared between the daemon (notifier gate), the API server (REST wiring),
	// and the TUI (focus signals). Created in Serve.
	focusTracker *notify.FocusTracker

	// supClient is non-nil iff the daemon is in supervisor mode (cfg.Supervisor
	// .Enabled): it is the supervisor-client mounted as d.runner. Its presence
	// flips two behaviors — exit handling is driven by its OnSessionExit relay
	// (wired in UseSupervisorRunner) rather than the in-process runner's onFinish,
	// and cleanup detaches it (Close) instead of StopAll-ing, so the supervisor's
	// agents survive the daemon bounce. nil ⇒ in-process mode (byte-identical to
	// pre-P2). Set once before Serve via UseSupervisorRunner.
	supClient SupervisorClient
}

// SupervisorClient is the daemon's view of a live session-supervisor connection:
// the full agent.SessionRunner surface (so it can BE d.runner), plus the exit
// relay + handshake + lifecycle hooks the daemon needs. The concrete impl is
// internal/daemon/client.Client — which imports THIS package, so the daemon must
// not import it back; it depends only on this interface, and cmd/argus injects
// the concrete value via UseSupervisorRunner.
type SupervisorClient interface {
	agent.SessionRunner
	// OnSessionExit registers the callback fired (with relayed ExitInfo, incl.
	// the GetExitInfo-failure ⇒ StreamLost backstop) when a supervisor session's
	// stream EOFs. The daemon wires it to handleSessionExit.
	OnSessionExit(func(taskID string, info ExitInfo))
	// Hello performs the protocol-version handshake with the supervisor.
	Hello() (HelloResp, error)
	// Close detaches the client (and its stream goroutines) WITHOUT stopping the
	// supervisor or its agents.
	Close() error
}

// UseSupervisorRunner switches the daemon into supervisor mode: it mounts the
// supervisor-client as d.runner — replacing the dormant in-process runner New
// created — and wires the client's exit relay to handleSessionExit so the #707
// status flip still runs against the daemon's DB. Call BEFORE Serve so every
// consumer (sessionCore RPC, scheduler, MCP, API server, notifier)
// captures the client. The in-process runner created in New is left unused; its
// onFinish never fires because it never starts a session.
func (d *Daemon) UseSupervisorRunner(c SupervisorClient) {
	d.runner = c
	d.supClient = c
	c.OnSessionExit(d.handleSessionExit)
}

// New creates a new Daemon.
func New(database *db.DB) *Daemon {
	d := &Daemon{
		db:        database,
		done:      make(chan struct{}),
		ready:     make(chan struct{}),
		bootedAt:  time.Now(),
		clipboard: clipboard.New(),
		prFetch:   gitutil.FetchPRState,

		prBatchFetch:      gitutil.FetchPRStatesBatch,
		prResolveRepo:     gitutil.ResolveDefaultRepo,
		prAliasCap:        prDefaultAliasCap,
		prDisableFlagPath: filepath.Join(db.DataDir(), prPollDisableFlag),
	}

	// Capture the binary path, hash, and mtime at startup. The on-disk binary
	// may be rebuilt while the daemon keeps running with the old in-memory
	// image — the TUI compares its current binary's content hash against this
	// snapshot (mtime is the pre-BinaryHash fallback).
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		d.binaryPath = exe
		if st, err := os.Stat(exe); err == nil {
			d.binaryMtime = st.ModTime()
		}
		if h, err := BinaryHashFile(exe); err == nil {
			d.binaryHash = h
		}
	}
	d.vcs = buildid.Current()

	// Create the in-process runner with an onFinish callback that builds the
	// ExitInfo and hands it to handleSessionExit (the shared DB-side exit sink).
	// The closure reads promoted core fields and d.runner; it only fires after a
	// session exits (post-Serve), by which time d.sessionCore is set below.
	//
	// In supervisor mode this runner is replaced (UseSupervisorRunner) before any
	// session starts, so this onFinish never fires there — the supervisor-client's
	// OnSessionExit relay calls handleSessionExit instead (with StreamLost set on
	// a failed relay). Both sources funnel through the one sink.
	runner := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		var errStr string
		if err != nil {
			errStr = err.Error()
		}
		// Snapshot HasPendingRestart once and stamp it onto ExitInfo so the
		// TUI can read it from the exit notification without an extra RPC
		// from the tview main goroutine (the gotcha at daemon-rpc.md:9).
		d.handleSessionExit(taskID, ExitInfo{
			Err:            errStr,
			Stopped:        stopped,
			LastOutput:     lastOutput,
			PendingRestart: d.runner.HasPendingRestart(taskID),
		})
	})

	// Mount the session-serving substrate. The daemon owns the runner (and thus
	// the onFinish wiring above); the core owns the stream registry, exit cache,
	// and the session RPC + stream handler, all promoted onto *Daemon.
	d.sessionCore = newSessionCore(runner, d.db.Config, d.done)

	return d
}

// handleSessionExit is the single DB-side sink for a finished session. It caches
// ExitInfo (for GetExitInfo), flips task status (#707), recaptures the backend
// session ID, closes TUI stream clients, clears the staged clipboard, and emits
// the lifecycle event. Two callers funnel through it:
//
//   - supervisor OFF: the in-process runner's onFinish builds ExitInfo from the
//     real Cmd.Wait result (StreamLost always false) and calls this directly.
//   - supervisor ON: the supervisor-client's OnSessionExit relay delivers the
//     ExitInfo it fetched from the supervisor via GetExitInfo — including the
//     GetExitInfo-RPC-failure ⇒ StreamLost backstop, which now guards the
//     supervisor→daemon boundary exactly as it has always guarded daemon→TUI.
//
// StreamLost short-circuits the status flip + recapture: a lost relay means the
// process MAY still be alive on the supervisor, so flipping to Complete/InReview
// would be wrong (#707 — "Complete only on an observed clean exit"). The
// StreamLost ExitInfo is still cached so the daemon's own TUI clients read it via
// GetExitInfo (a zero ExitInfo would CleanExit()=true → wrong Complete) and so
// their stream conns are closed.
func (d *Daemon) handleSessionExit(taskID string, ei ExitInfo) {
	slog.Info("session exited", "task", taskID, "stopped", ei.Stopped, "err", ei.Err, "streamLost", ei.StreamLost, "pending", ei.PendingRestart, "lastOutputBytes", len(ei.LastOutput))

	d.mu.Lock()
	d.exitInfos[taskID] = ei
	conns := d.streams[taskID]
	delete(d.streams, taskID)
	d.mu.Unlock()

	if ei.StreamLost {
		slog.Warn("session exit relay: stream lost — status unchanged, process may still be alive", "task", taskID, "clients", len(conns))
		for _, conn := range conns {
			conn.Close() //nolint:errcheck // best-effort EOF signal; conn is being discarded
		}
		return
	}

	// Flip the DB row out of InProgress. Without this, a daemon-only setup
	// (web-app users with no TUI attached) leaves the row stuck in_progress
	// forever — the API then reports idle:true and the PWA pops a Resume modal
	// for a task whose agent has already exited. The TUI's HandleSessionExit also
	// runs this transition; both sites are guarded by the StatusInProgress check,
	// so whichever fires first wins and the other becomes a no-op.
	//
	// SKIP when a kick-restart is in flight (KickRerender stopped the session and
	// queued a same-task Start). Transitioning here would race the restart and
	// leave the row in the wrong state mid-flip.
	if !ei.PendingRestart {
		d.transitionTaskOnExit(taskID, ei.CleanExit())
	}

	// Capture session ID for backends that mint it themselves post-exit (codex
	// via state_5.sqlite, pi via session-file scan; Claude refreshes on /clear).
	// In supervisor mode the daemon still owns this — it reads the worktree-scoped
	// backend state, which lives on the shared filesystem both processes see, and
	// the relay only fires after the supervisor observed Cmd.Wait, so the state
	// file is already written (design §5: recapture daemon-side on the exit relay).
	go d.captureSessionIDPostExit(taskID)

	// Signal stream EOF to all connected clients by closing their connections.
	slog.Info("session exited, closing stream clients", "task", taskID, "clients", len(conns))
	for _, conn := range conns {
		conn.Close() //nolint:errcheck // best-effort EOF signal; conn is being discarded
	}

	// Clear any agent-staged clipboard for the finished task — the agent that
	// staged it is gone; the user shouldn't see a stale copy button.
	d.clipboard.Clear(taskID)

	// Surface session lifecycle to plugins. Fired AFTER the DB transition so an
	// SSE subscriber waking on this event sees a coherent snapshot.
	events.Emit(model.EventTypeSessionExited, taskID, map[string]any{
		"stopped":         ei.Stopped,
		"err":             ei.Err,
		"pending_restart": ei.PendingRestart,
	})
}

// Runner returns the underlying session runner. Typed as agent.SessionRunner
// since d.runner may be an in-process *agent.Runner (supervisor OFF) or a
// supervisor-client (supervisor ON).
func (d *Daemon) Runner() agent.SessionRunner {
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
	// Hera worker finish policy (BUG-050, locked decision #4) — the EXIT-HOOK
	// BACKSTOP. The PRIMARY trigger is hera_status("done") in the MCP arm
	// (Claude workers finish their report and go idle rather than exiting); this
	// exit hook catches the cases where a worker session actually ends. Both go
	// through db.RollHeraWorkerToReview so they can't drift: a task holding a
	// live worker-kind binding NEVER self-completes — it lands in_review (even on
	// a clean exit) and is stamped meta:hera.ready_to_close for coordinator/human
	// close-out. Coordinators/freelance and non-hera tasks are NOT rolled here and
	// fall through to the unchanged #707 rule below.
	if flipped, err := d.db.RollHeraWorkerToReview(taskID); err != nil {
		// Soft-fail: a hera lookup error must not change the default behaviour.
		slog.Warn("session exit: hera worker roll failed (using default policy)", "task", taskID, "err", err)
	} else if flipped {
		slog.Info("session exit: hera worker rolled to in_review", "task", taskID)
		return
	}

	// Non-worker (or worker no longer in_progress): unchanged #707 logic.
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

// heraSpawnWorker performs the transactional born-bound worker spawn (M4). It
// is injected into the MCP server via SetHeraService. The actual spawn semantics
// live in the shared agent.SpawnHeraWorker primitive (single source of truth,
// also called by the native Hera view's rail `w` key); this method is a thin
// adapter that translates the MCP payload to/from the shared types.
func (d *Daemon) heraSpawnWorker(in mcp.HeraSpawnInput) (*mcp.HeraSpawnResult, error) {
	res, err := agent.SpawnHeraWorker(d.db, d.runner, agent.HeraWorkerSpawnInput{
		OrchestratorID: in.OrchestratorID,
		BaseName:       in.BaseName,
		TaskPrompt:     in.TaskPrompt,
		RolePrompt:     in.RolePrompt,
		Project:        in.Project,
		Branch:         in.Branch,
		Backend:        in.Backend,
		Model:          in.Model,
		Archetype:      in.Archetype,
	})
	if err != nil {
		return nil, err
	}
	return &mcp.HeraSpawnResult{Task: res.Task, Role: res.Role, Binding: res.Binding}, nil
}

// heraReviveRole performs the PULL-revive gating+action (add-hera-revive) for
// the hera_revive MCP tool, injected via SetHeraReviver. The gating sequence
// lives in the shared hera.ReviveRole primitive (mirrors RecycleCoord's
// architecture); this method just supplies the real daemon.HeraReviveRunner
// adapter over d.db + d.runner. See design.md D3 for why the TUI's Enter-key
// revive (internal/tui/heraactions.go) is NOT routed through this same call —
// it keeps its own inline implementation, sharing every underlying primitive
// (agent.BlockedOnPrompt, db.ReviveHeraWorkerToInProgress,
// SessionRunner.KickRerender/StartOrReattach) but not the top-level orchestration.
func (d *Daemon) heraReviveRole(in mcp.HeraReviveInput) (string, error) {
	rr := NewHeraReviveRunner(d.db, d.runner, d.cfgFn)
	outcome, err := hera.ReviveRole(d.db, rr, in.TaskID, in.IsCoordinator)
	if err != nil {
		return "", err
	}
	return string(outcome), nil
}

// heraGaterMaterialize is the gater's Materializer adapter (add-hera-plan-substrate):
// it binds + starts a pre-created planned role via the shared
// agent.MaterializeHeraWorker primitive. The gater resolves project / base_branch /
// the check-in-prefixed prompt; this adapter just plumbs them into the agent layer.
func (d *Daemon) heraGaterMaterialize(role *db.HeraRole, taskPrompt, project, branch, backend, model string) error {
	_, err := agent.MaterializeHeraWorker(d.db, d.runner, agent.HeraMaterializeInput{
		Role:       role,
		TaskPrompt: taskPrompt,
		Project:    project,
		Branch:     branch,
		Backend:    backend,
		Model:      model,
	})
	return err
}

// heraGaterMaterializeSubCoord is the gater's second Materializer adapter
// (add-hera-subcoord-nodes): it materializes a pre-created planned SUBCOORD node
// as a distinct coordinator agent via the shared agent.MaterializeHeraSubCoordinator
// primitive (one new task carrying both a parent-worker binding and a child-coord
// binding). Wired via Watcher.SetSubCoordMaterializer; the worker path stays on
// heraGaterMaterialize.
func (d *Daemon) heraGaterMaterializeSubCoord(role *db.HeraRole, taskPrompt, project, branch, backend, model string) error {
	_, err := agent.MaterializeHeraSubCoordinator(d.db, d.runner, agent.HeraMaterializeInput{
		Role:       role,
		TaskPrompt: taskPrompt,
		Project:    project,
		Branch:     branch,
		Backend:    backend,
		Model:      model,
	})
	return err
}

// pollPRStatesOnce runs a single PR-status refresh pass over every eligible
// task. Eligible = not archived AND has a non-empty Branch AND its cached PR
// state (task_meta namespace "pr", key "state") is not terminal (merged/closed).
// A terminal state never changes, so once observed the task is excluded from all
// future polls — this conserves the GitHub API budget that re-polling long-merged
// PRs would otherwise drain. The skip reads the persisted cache, so it survives a
// daemon restart.
//
// Flow (design.md Decisions 2/4/5): eligible → group → chunk → fetch → apply.
// Eligible tasks are grouped by resolved PR repo (cached pr/url first, then the
// worktree's default GitHub repo via d.prResolveRepo). Each repo group is split
// into chunks of at most d.prAliasCap branches and resolved with one batched
// GraphQL query per chunk (d.prBatchFetch) — collapsing per-cycle GitHub API
// cost from O(tasks) to ~O(repos).
//
// Keep-stale contract: a chunk fetch returning a non-nil error is transient —
// every cached value for that chunk's tasks is left untouched (no write). A
// successful fetch is authoritative and each branch's state+url is written into
// task_meta namespace "pr", INCLUDING writing "none" when the query found no PR.
//
// Count semantics: skipped/written/errored count TASKS, not branches or repos,
// so the cycle invariant eligible == written + errored + skipped always holds.
// Two tasks sharing a branch share one PR — the branch is queried once and the
// single result is written to BOTH tasks (each counted in written); neither is
// silently dropped. The per-repo `cost=` line reports the real GraphQL cost
// summed across that repo's chunks (Decision 4, design.md).
func (d *Daemon) pollPRStatesOnce(ctx context.Context) {
	// Kill-switch: an operator-dropped sentinel file pauses the poller without a
	// daemon bounce. Checked before any DB read or gh query so a paused poller
	// costs nothing against the GitHub API budget. Logged each cycle (not spammy
	// — only while explicitly paused) so daemon.log shows the pause is in effect.
	if d.prDisableFlagPath != "" {
		if _, statErr := os.Stat(d.prDisableFlagPath); statErr == nil {
			slog.Info("[pr] poll skipped: paused via kill-switch", "flag", d.prDisableFlagPath)
			return
		}
	}

	tasks, err := d.db.Tasks()
	if err != nil {
		uxlog.Log("[pr] poll: list tasks failed: %v", err)
		return
	}

	// Read the persisted PR-state cache once so the eligibility filter can skip
	// tasks whose PR has already reached a terminal state (merged/closed). The
	// cache lives in task_meta namespace "pr" (the same place this poller
	// writes), so a terminal-state skip survives a daemon restart — a bounce
	// does not re-poll the whole backlog. On a batch-read error, fall back to
	// polling everything eligible (fail-open) so a transient meta failure never
	// silently halts all polling.
	prMeta, err := d.db.ListMetaByNamespace("pr")
	if err != nil {
		uxlog.Log("[pr] poll: read cached pr states failed (polling all eligible): %v", err)
		prMeta = nil
	}

	// Collect eligible tasks up front so the logged count is exact. Two kinds of
	// task are dropped here and counted in `skipped` (everything eligible by base
	// criteria but not queried this cycle), preserving len(eligible) == written +
	// errored:
	//   1. Terminal cached state (merged/closed) — skipped permanently; that
	//      state can never change, so re-polling can never return anything new.
	//   2. Dormancy cadence — an eligible task not due this cycle. GitHub's
	//      GraphQL budget is cost-based (~1 unit per branch resolved), so querying
	//      every dormant PR-less branch every cycle drains it; we poll them on a
	//      stride instead (prPollCadenceStride). An open PR overrides the stride
	//      and is always polled.
	now := time.Now()
	eligible := make([]*model.Task, 0, len(tasks))
	var skipped int
	for _, t := range tasks {
		if t == nil || t.Archived || t.Branch == "" {
			continue
		}
		// Parse the cached PR state once: it drives both the terminal skip and the
		// cadence floor (an open PR is always polled). A missing or unparseable
		// value is treated as PRNone (no known PR → dormancy-tiered).
		state := model.PRNone
		if raw := prMeta[t.ID]["state"]; raw != "" {
			if st, perr := model.ParsePRState(raw); perr == nil {
				state = st
			}
		}
		if state.IsTerminal() {
			skipped++
			uxlog.Log("[pr] poll: skip terminal %s (state=%s)", t.ID, state)
			continue
		}
		if stride := prPollCadenceStride(t, state, now); !prCadenceSelects(d.pollCycle, t.ID, stride) {
			skipped++
			continue
		}
		eligible = append(eligible, t)
	}
	if len(eligible) == 0 {
		if skipped > 0 {
			uxlog.Log("[pr] poll: eligible=0 skipped=%d", skipped)
		}
		return
	}

	// Build the grouping inputs. The alias id is a sanitized, GraphQL-safe
	// derivation of the task id (prAliasID) so the batched query never emits an
	// illegal alias; aliasToTask maps each alias back to its task for the write
	// pass. The branch is the GraphQL lookup key (headRefName); the cached
	// pr/url (when present) is authoritative for repo resolution, else the
	// worktree's default GitHub repo via d.prResolveRepo.
	aliasToTask := make(map[string]*model.Task, len(eligible))
	inputs := make([]gitutil.BranchRepoInput, 0, len(eligible))
	for _, t := range eligible {
		alias := prAliasID(t.ID)
		aliasToTask[alias] = t
		inputs = append(inputs, gitutil.BranchRepoInput{
			ID:        alias,
			Branch:    t.Branch,
			Worktree:  t.Worktree,
			CachedURL: prMeta[t.ID]["url"],
		})
	}

	groups := gitutil.GroupBranchesByRepo(ctx, inputs, d.prResolveRepo)

	var written, errored int
	aliasCap := d.prAliasCap
	if aliasCap < 1 {
		aliasCap = prDefaultAliasCap
	}

	for repo, branchToAliases := range groups {
		// Stable, deterministic chunk boundaries: order DISTINCT branches before
		// slicing so chunking (and any logs) are reproducible. Chunking is by
		// distinct branch — two tasks sharing a branch share the same PR, so the
		// branch is queried once and the result fans out to every alias for it.
		branches := make([]string, 0, len(branchToAliases))
		for branch := range branchToAliases {
			branches = append(branches, branch)
		}
		sort.Strings(branches)

		// repoCost accumulates the GraphQL cost across this repo's chunks so the
		// per-repo log line reports the real summed cost (Decision 4, design.md).
		var repoCost, repoBranches int

		for start := 0; start < len(branches); start += aliasCap {
			end := start + aliasCap
			if end > len(branches) {
				end = len(branches)
			}
			// chunk maps each distinct branch to ONE alias (the lookup key for the
			// query). Results fan back out to ALL aliases for that branch below.
			chunkBranches := branches[start:end]
			chunk := make(map[string]string, len(chunkBranches))
			for _, branch := range chunkBranches {
				chunk[branch] = branchToAliases[branch][0]
			}

			// chunkTaskCount is the number of TASKS this chunk covers (summed
			// across shared branches), so errored/written count tasks, not
			// branches — keeping eligible == written + errored + skipped.
			var chunkTaskCount int
			for _, branch := range chunkBranches {
				chunkTaskCount += len(branchToAliases[branch])
			}

			results, cost, ferr := d.prBatchFetch(ctx, repo, chunk)
			if ferr != nil {
				// Whole-chunk transient failure: keep every cached value in this
				// chunk stale (no write). This relies on FetchPRStatesBatch
				// surfacing GraphQL `errors` arrays as a non-nil error — the real
				// runner depends on `gh api graphql` exiting non-zero on them.
				errored += chunkTaskCount
				uxlog.Log("[pr] poll: repo=%s branches=%d fetch failed (keeping stale): %v", repo, len(chunk), ferr)
				continue
			}

			repoCost += cost
			repoBranches += len(chunk)

			for _, branch := range chunkBranches {
				res, ok := results[branch]
				if !ok {
					// Branch absent from a successful response — treat as no PR
					// (the batch fetcher maps empty nodes to PRNone, so this is
					// only reachable for a malformed partial response). Keep
					// stale rather than clobbering with a guessed value.
					uxlog.Log("[pr] poll: branch=%s missing from repo=%s response (keeping stale)", branch, repo)
					continue
				}
				// Fan the single PRResult out to every task sharing this branch:
				// they all point at the same PR, so each gets the same state+url.
				for _, alias := range branchToAliases[branch] {
					t := aliasToTask[alias]
					if t == nil {
						continue
					}
					if werr := d.db.SetMetaBatch(t.ID, "pr", map[string]string{
						"state": res.State.String(),
						"url":   res.URL,
					}); werr != nil {
						uxlog.Log("[pr] poll: persist failed for %s: %v", t.ID, werr)
						continue
					}
					written++
					if res.Merged {
						d.notifyCoordinatorOfMergedPR(t, res.URL)
						d.notifyRoleOfMergedPR(t, res.URL)
					}
				}
			}
		}

		if repoBranches > 0 {
			uxlog.Log("[pr] poll: repo=%s branches=%d cost=%d", repo, repoBranches, repoCost)
		}
	}

	uxlog.Log("[pr] poll: eligible=%d skipped=%d written=%d errored=%d", len(eligible), skipped, written, errored)
}

// resolveHeraRoleAndCoordinator resolves a task's most recent Hera role (any
// binding, live or ended, via ListHeraBindingsByTask's most-recent-first
// order) and that role's orchestrator's coordinator, mirroring the
// coordinator-resolution pattern the plan-DAG gater already uses for its own
// hold/fan-in notices. Shared by both merge-triggered notifications below so
// neither can drift on how the role/coordinator pair is found. Returns
// ok=false when the task never held a Hera role at all (the common case,
// since most PR-tracked tasks are not Hera-bound) or no coordinator role
// resolves for that orchestrator.
func (d *Daemon) resolveHeraRoleAndCoordinator(taskID string) (role, coord *db.HeraRole, ok bool) {
	bindings, err := d.db.ListHeraBindingsByTask(taskID)
	if err != nil || len(bindings) == 0 {
		return nil, nil, false
	}
	role, err = d.db.HeraRole(bindings[0].RoleID)
	if err != nil {
		return nil, nil, false
	}
	coords, err := d.db.ListHeraRolesByKind(role.OrchestratorID, db.HeraKindCoordinator)
	if err != nil || len(coords) == 0 {
		return nil, nil, false
	}
	return role, coords[0], true
}

// heraNotifyService builds a hera.Service for a merge-triggered notification.
// d.notifier is a concrete *notify.Notifier, set once by Serve before the PR
// poller ever starts; guarded here (rather than passed unconditionally) so a
// bare Daemon built via New() without Serve() (every test in this package)
// gets a TRUE nil hera.Notifier interface instead of a non-nil interface
// wrapping a nil pointer, which panics on the first Send.
func (d *Daemon) heraNotifyService() *hera.Service {
	var notifier hera.Notifier
	if d.notifier != nil {
		notifier = d.notifier
	}
	return hera.New(d.db, notifier)
}

// notifyCoordinatorOfMergedPR sends a nudge (never a status or hera
// role-status change) to a task's Hera coordinator once its PR resolves as
// genuinely merged (add-hera-accept-lifecycle) – a strong signal the work is
// worth reviewing/accepting, never treated as proof it is done.
//
// Entirely silent (no error, no log spam) on every resolution miss: most
// PR-tracked tasks are not Hera-bound at all, so a missing role or
// coordinator is the common case, not a failure. Delivery itself is
// best-effort – a send failure is logged, never returned, since a poll cycle
// must never fail over a notification.
func (d *Daemon) notifyCoordinatorOfMergedPR(t *model.Task, prURL string) {
	role, coord, ok := d.resolveHeraRoleAndCoordinator(t.ID)
	if !ok {
		return
	}
	if coord.ID == role.ID {
		return // the coordinator's own PR merged; it does not need telling
	}
	body := fmt.Sprintf("PR merged for %s (%s) - worth reviewing/accepting its work.", t.Name, prURL)
	tldr := fmt.Sprintf("PR merged: %s", t.Name)
	if _, sErr := d.heraNotifyService().Send(role.ID, coord.ID, body, tldr, nil); sErr != nil {
		uxlog.Log("[pr] poll: merge nudge failed for %s: %v", t.ID, sErr)
	}
}

// notifyRoleOfMergedPR asks the task's OWN Hera role to decide, on its own
// judgment, whether the just-merged PR means it's actually done
// (add-pr-merge-autocomplete). Rather than the daemon mechanically inferring
// "done" from structural signals (which a prior explicit decision – see
// context/knowledge/gotchas/orchestration.md – judged too ambiguous for
// Hera-descended tasks: squash merges, folded-in workers with no PR of their
// own), this hands the decision to the agent that actually has the context:
// the message tells it the PR merged and instructs it, if it has no further
// tasks here, to inform its coordinator (hera_send/hera_status) and mark
// itself complete via its own existing task_complete tool. No new
// completion primitive is introduced; the daemon only delivers information.
//
// Deliberately NOT scoped to worker-kind or gated on task status: a
// coordinator's or freelance role's own merged PR is an equally valid prompt
// for the same self-assessment, and a role still actively in_progress simply
// answers "yes, more to do" and continues – the agent's own judgment is the
// gate, not a role-kind or status precondition here. Fires independently of
// (and in addition to) notifyCoordinatorOfMergedPR above; the two target
// different recipients for different purposes (this one is actionable
// instruction to the role itself, the other is passive visibility for the
// coordinator) and neither is skipped because the other fired.
//
// Silent no-op when the task never held a Hera role, no coordinator role
// resolves (needed to identify the sender), or the resolved role IS ITSELF
// the coordinator: db.SendHeraMessage hard-rejects fromRoleID==toRoleID
// (ErrHeraMessageSelfSend) with no exception, so a coordinator's own bound
// task can never receive this notice via the hera_messages channel at all –
// an accepted structural gap, not a deliberate design choice (unlike
// notifyCoordinatorOfMergedPR's own self-skip above, which is a judgment
// call: "it does not need telling"). Best-effort delivery otherwise: a send
// failure is logged, never returned.
func (d *Daemon) notifyRoleOfMergedPR(t *model.Task, prURL string) {
	role, coord, ok := d.resolveHeraRoleAndCoordinator(t.ID)
	if !ok || role.ID == coord.ID {
		return
	}
	body := fmt.Sprintf(
		"Your PR merged: %s\n\nIf you have no further tasks on %s, please inform your coordinator and mark yourself complete (task_complete). If you still have work to do here, no action is needed — just continue.",
		prURL, t.Name)
	tldr := fmt.Sprintf("PR merged: %s — check for further tasks", t.Name)
	if _, sErr := d.heraNotifyService().Send(coord.ID, role.ID, body, tldr, nil); sErr != nil {
		uxlog.Log("[pr] poll: role merge notice failed for %s: %v", t.ID, sErr)
	}
}

// prPollCadenceStride returns how many poll cycles apart a task should be
// queried, tiered by dormancy to bound the GitHub GraphQL budget (cost-based —
// ~1 unit per branch resolved — so per-repo batching alone does not cap it). An
// open PR (draft/awaiting-review/changes-requested/approved) overrides the tier
// and is polled every cycle (stride 1) so externally-driven review/merge
// transitions surface promptly. Otherwise the stride is derived from the task's
// most recent lifecycle activity (latest of EndedAt/StartedAt/CreatedAt):
// within 1h → 1, 1h–24h → 5, 24h–7d → 15, older → 30.
func prPollCadenceStride(t *model.Task, state model.PRState, now time.Time) int {
	switch state {
	case model.PRDraft, model.PRAwaitingReview, model.PRChangesRequested, model.PRApproved:
		return 1
	}
	last := t.CreatedAt
	if t.StartedAt.After(last) {
		last = t.StartedAt
	}
	if t.EndedAt.After(last) {
		last = t.EndedAt
	}
	switch age := now.Sub(last); {
	case age < time.Hour:
		return 1
	case age < 24*time.Hour:
		return 5
	case age < 7*24*time.Hour:
		return 15
	default:
		return 30
	}
}

// prCadenceSelects reports whether a task on the given stride is due on this
// poll cycle. Stride 1 is always due. Otherwise selection is phased by a stable
// hash of the task id so a tier's tasks spread across the stride window rather
// than all firing on the same cycle (a thundering herd that would defeat the
// budget saving).
func prCadenceSelects(cycle uint64, taskID string, stride int) bool {
	if stride <= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(taskID))
	return (cycle+h.Sum64())%uint64(stride) == 0
}

// prAliasID derives a GraphQL-safe alias from a task id. A GraphQL alias must
// match [_A-Za-z][_0-9A-Za-z]*; argus task ids are numeric, so a bare id would
// start with a digit (illegal). We prefix a constant "t" and replace any
// non-[0-9A-Za-z_] rune with "_", yielding a valid alias that still maps
// uniquely back to the task: the "t" prefix guarantees a letter lead, and the
// 1:1 alias→task map (aliasToTask) preserves the association even if two ids
// were to sanitize to the same string (last-writer-wins is impossible here
// because the caller dedups by task id upstream).
func prAliasID(id string) string {
	var b strings.Builder
	b.Grow(len(id) + 1)
	b.WriteByte('t')
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// runPRPoller is the PR-status poller goroutine body. It runs pollPRStatesOnce
// every prPollInterval until d.done is closed, at which point it returns so
// daemon shutdown does not hang. Extracted from Serve so tests can verify the
// d.done-gated exit deterministically without binding a real socket.
func (d *Daemon) runPRPoller() {
	ticker := time.NewTicker(prPollInterval)
	defer ticker.Stop()

	// Derive a cancelable context so a tick already running pollPRStatesOnce
	// (which issues one `gh api graphql` query per repo group, each with its own
	// timeout) aborts immediately on shutdown instead of letting those queries
	// run to their timeout. cancel() fires both on the d.done branch and on return.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case <-d.done:
			cancel()
			return
		case <-ticker.C:
			d.pollCycle++
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
	// Derive the singleton trio (sock/pid/lock) from the socket path so tests
	// using temp dirs don't touch ~/.argus/ and accidentally kill a real
	// running daemon. For ".../daemon.sock" this yields ".../daemon.pid" and
	// ".../daemon.lock" — byte-identical to the prior hard-wired derivation.
	sp := singletonPathsForSock(sockPath)
	pidPath := sp.pid
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
	lockFile, lerr := acquireSingletonLock(sp.lock, daemonLockTimeout)
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

	// Reconcile DB session state against reality before accepting connections so
	// first-poll clients see the settled state. Mode-aware (see ReconcileOnStartup):
	// in-process mode flips every stale InProgress→InReview and replays bounce
	// signals from the live-tasks file; supervisor mode re-attaches the agents the
	// supervisor kept alive and flips only the true orphans.
	d.ReconcileOnStartup()

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
	sch := scheduler.New(d.db, func(name, prompt, project, backend, taskModel string) (*model.Task, error) {
		// Schedule names are user-edited (then suffixed with a timestamp) —
		// already meaningful; no auto-rename. backend is the per-schedule
		// override (sched.Backend) and taskModel is the per-schedule model
		// override (sched.Model); empty strings fall back to the configured
		// defaults inside agent.CreateAndStart.
		return HeadlessCreateTask(d.db, d.runner, HeadlessInput{
			Name:    name,
			Prompt:  prompt,
			Project: project,
			Backend: backend,
			Model:   taskModel,
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

	// Host-suspend watchdog (detect-host-suspend). Detects a laptop sleep /
	// hibernate / VM pause from a large wall-clock gap between its own ticks and
	// broadcasts an advisory ARGUS_HOST_SUSPENDED note to every running task, so a
	// hera coordinator does not misread the concurrent silence (its workers were
	// frozen alongside it) as a stuck worker and spawn duplicate retry nodes. Runs
	// UNCONDITIONALLY (not gated on cfg.Hera.Enabled): a bare-worker coordinator
	// misjudges the same way, and the note is harmless for a non-coordinator.
	// Per-tick logic is factored into hostSuspendTick so tests drive one pass with
	// synthetic times instead of sleeping.
	go d.runHostSuspendWatcher()

	// Hera plan-DAG gater (add-hera-plan-substrate). Runs whenever hera is
	// enabled, independent of the KB/MCP server: it materializes already-authored
	// planned nodes whose blockers have all reached role-status done. Inert when no
	// plan exists (empty planned-node list every tick). The ping adapter sends the
	// failure-hold notice over the same hera.Service / notifier the MCP tools use.
	if cfg.Hera.Enabled {
		gaterSvc := hera.New(d.db, d.notifier)
		d.heraGater = heragater.New(d.db, d.heraGaterMaterialize,
			func(fromRoleID, coordRoleID int64, body, tldr string) error {
				// The held planned node (fromRoleID) is the sender, so Service.Send's
				// self-send guard never trips — it reads as "the node telling its
				// coordinator it cannot start". Delivery is best-effort (the node has
				// no live binding, so only the durable store write matters).
				_, err := gaterSvc.Send(fromRoleID, coordRoleID, body, tldr, nil)
				return err
			})
		// Route subcoord plan nodes through the distinct-coordinator materialize
		// path (add-hera-subcoord-nodes); without this the gater would fall back to
		// the worker path and never spawn a sub-coordinator agent.
		d.heraGater.SetSubCoordMaterializer(d.heraGaterMaterializeSubCoord)
		// Auto-accept a materialized node's blockers (add-hera-accept-lifecycle):
		// the same shared hera.AcceptRole primitive the hera_accept MCP tool
		// calls, reusing gaterSvc as the AcceptSender exactly as the ping
		// adapter above reuses it for Send.
		d.heraGater.SetAccepter(func(coordRoleID, blockerRoleID int64) error {
			_, err := hera.AcceptRole(d.db, gaterSvc, coordRoleID, blockerRoleID, "")
			return err
		})
		go d.heraGater.Start()

		// Self-service recycle_coord sweep (add-coordinator-context-management
		// D5). Independent of the gater above (that materializes planned
		// nodes; this recycles live coordinators) but gated on the same
		// cfg.Hera.Enabled — both are hera-native background loops with no
		// meaning when hera is off.
		d.heraRecycleWatcher = hera.NewRecycleWatcher(d.db, NewHeraRecycleRunner(d.db, d.runner, d.cfgFn))
		go d.heraRecycleWatcher.Start()
	}

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
				})
			},
			d.db,
			d.runner,
		)
		mcpSrv.SetClipboard(d.clipboard)
		mcpSrv.SetScheduleManager(d.db, sch)
		mcpSrv.SetMessageManager(d.db, runnerNudger{notifier: d.notifier})
		// Gate on cfg.Hera.Enabled: when disabled, the native hera_* tools are
		// not registered so the MCP server exposes no hera-scope tools. An
		// external Hera daemon (if running) can still register its tools via the
		// plugin proxy path — the dup-tool guard (hera-scope filter) is already
		// in place when the native service is wired, and is simply absent here.
		if cfg.Hera.Enabled {
			mcpSrv.SetHeraService(hera.New(d.db, d.notifier), d.db, d.heraSpawnWorker)
			mcpSrv.SetHeraReviver(d.heraReviveRole)
		}
		mcpSrv.SetArtifactManager(d.db)
		mcpSrv.SetProfileResolver(d.db)
		// d.db.Config() is read live on every tools/list/tools/call (see
		// TodoConfigStore) — a backend selected in Settings takes effect
		// immediately, with no daemon restart or re-wiring here.
		mcpSrv.SetTodoManager(d.db)
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
				if err := injectopencode.InjectGlobal(actualPort); err != nil {
					slog.Error("inject opencode", "err", err)
				} else {
					slog.Info("inject opencode", "port", actualPort)
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
	// Embed the same *sessionCore so the session-scoped RPC methods (promoted
	// from the core) register under "Daemon" exactly as before.
	svc := &RPCService{sessionCore: d.sessionCore, daemon: d}
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

	if d.supClient != nil {
		// Supervisor mode: the supervisor owns the agent PTYs and MUST survive
		// this daemon's exit — that is the entire point (agents keep running
		// across a daemon bounce; P3 re-attaches them). So do NOT StopAll (it
		// would kill them) and do NOT write the bounce live-tasks file (re-attach
		// supersedes the bounce-signal replay; that's P3). Just detach the client
		// connection + its stream goroutines.
		if err := d.supClient.Close(); err != nil {
			slog.Warn("supervisor client close", "err", err)
		}
	} else {
		// In-process mode (pre-P2 path, byte-identical): persist the live session
		// set before stopping agents so hera workers detect the bounce on the next
		// daemon start (see bounce.go), then stop every agent.
		if err := writeLiveTasksFile(d.runner, db.DataDir()); err != nil {
			slog.Warn("bounce: persist live-tasks failed", "err", err)
		}
		d.runner.StopAll()
	}

	// Stop the scheduler if running.
	if d.scheduler != nil {
		d.scheduler.Stop()
	}

	// Stop the hera plan-DAG gater if running.
	if d.heraGater != nil {
		d.heraGater.Stop()
	}

	// Stop the hera recycle_coord self-service watcher if running.
	if d.heraRecycleWatcher != nil {
		d.heraRecycleWatcher.Stop()
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
