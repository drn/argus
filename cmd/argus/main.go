package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/launchagent"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/tui"
	"github.com/drn/argus/internal/uxlog"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			sub := "start"
			if len(os.Args) > 2 {
				sub = os.Args[2]
			}
			switch sub {
			case "start":
				runDaemon()
			case "stop":
				runDaemonStop()
			case "restart":
				runDaemonRestart()
			case "install":
				runDaemonInstall()
			case "uninstall":
				runDaemonUninstall()
			case "status":
				runDaemonStatus()
			default:
				fmt.Fprintf(os.Stderr, "unknown daemon subcommand: %s\n", sub)
				os.Exit(1)
			}
			return
		case "session-supervisor":
			sub := "start"
			if len(os.Args) > 2 {
				sub = os.Args[2]
			}
			switch sub {
			case "start":
				runSupervisor()
			case "stop":
				runSupervisorStop()
			case "status":
				runSupervisorStatus()
			default:
				fmt.Fprintf(os.Stderr, "unknown session-supervisor subcommand: %s\n", sub)
				os.Exit(1)
			}
			return
		case "kb":
			runKBCommand(os.Args[2:])
			return
		case "token":
			runTokenCommand(os.Args[2:])
			return
		case "validate":
			runValidateCommand(os.Args[2:])
			return
		case "coord-hook":
			runCoordHookCommand()
			return
		case "doctor":
			runDoctor()
			return
		}
	}

	// --remote URL [--token TOKEN] points the TUI at a remote argus host
	// instead of the local daemon. Token is also picked up from ARGUS_TOKEN.
	remoteURL, token := parseRemoteFlags(os.Args[1:])
	if remoteURL != "" {
		runRemoteTUI(remoteURL, token)
		return
	}

	runTUI()
}

// parseRemoteFlags scans args for --remote URL and --token TOKEN. Returns
// the two values (token defaults to $ARGUS_TOKEN when unset). Anything else
// is ignored — this is the only flag pair the TUI recognises today, so a
// custom mini-parser avoids pulling in the `flag` package and the noisy
// "unknown flag" exits it would emit for daemon subcommands above.
//
// Bare `--remote` / `--token` with no following value writes a diagnostic
// to stderr and leaves the value empty so the caller's required-arg check
// errors out cleanly. tokenFromFlag tracks whether --token came from the
// command line (visible in `ps aux`) vs ARGUS_TOKEN env so callers can
// nudge the user toward the safer path.
func parseRemoteFlags(args []string) (remoteURL, token string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--remote", "-remote":
			if i+1 < len(args) {
				remoteURL = args[i+1]
				i++
			} else {
				fmt.Fprintln(os.Stderr, "warning: --remote requires a URL")
			}
		case "--token", "-token":
			if i+1 < len(args) {
				token = args[i+1]
				i++
			} else {
				fmt.Fprintln(os.Stderr, "warning: --token requires a value")
			}
		}
	}
	if token == "" {
		token = os.Getenv("ARGUS_TOKEN")
	}
	return remoteURL, token
}

func runTUI() {
	// Initialize UX debug log.
	if err := uxlog.Init(uxlog.Path(db.DataDir())); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot open ux log: %v\n", err)
	}
	defer uxlog.Close()
	uxlog.Log("=== argus TUI starting ===")

	// Redirect EVERY default logger that could write to the user's terminal
	// at the program level, NOT by editing the 30+ slog/log call sites that
	// run in the TUI process.
	//
	// WITHOUT these redirects, `slog.*` and stdlib `log.*` calls from anywhere
	// in the TUI process (autorename, agent.Runner, push notifications,
	// orchestration, scheduler, kb indexer, mcp server, etc.) write to
	// `os.Stderr`, which IS the user's terminal. tcell does NOT route through
	// os.Stderr, so those writes land at the cursor's current position,
	// corrupt the displayed cell state out from under tcell's diff tracker,
	// and survive on screen until the next `screen.Sync()` (Ctrl+L) repaints.
	// Symptoms include torn cells, scattered log fragments, mis-positioned
	// content, and stacked status bars — historically misdiagnosed as
	// tcell/tmux drift.
	//
	// Belt-and-braces approach:
	//   1. slog.SetDefault — catches every `slog.{Info,Error,Warn,Debug,...}` call.
	//   2. log.SetOutput — catches every stdlib `log.{Print*,Fatal*,Panic*}` call.
	//   3. Once `app.Run()` starts (below), the alt-screen takes over and ANY
	//      direct `fmt.Fprintf(os.Stderr, ...)` from inside argus is a bug.
	//      CLAUDE.md hard rule 6 forbids it; the regression test
	//      `TestSlogWithUxlogWriter_DoesNotReachStderr` in
	//      `internal/uxlog/uxlog_test.go` pins the slog+log wiring.
	//
	// The daemon does this at line 174 of `runDaemon`. The TUI MUST mirror it.
	// See CLAUDE.md hard rule and gotchas/ui-threading.md.
	slog.SetDefault(slog.New(slog.NewTextHandler(uxlog.Writer(), nil)))
	log.SetOutput(uxlog.Writer())

	// Top-level panic recovery for THIS goroutine. Logs the goroutine stack
	// to uxlog before re-panicking so a panic during app setup or app.Run
	// has its diagnostic info captured. tview's Application.Run installs its
	// own deferred screen.Fini(), so by the time the re-panic propagates the
	// alt-screen is restored and a brief panic message at the user's normal
	// terminal is acceptable. (For panics in OTHER goroutines — 40+ across
	// internal/tui, internal/agent — the OS-level fd 2 redirect just below
	// handles them at the kernel level since the main-goroutine recover
	// cannot reach them.)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			uxlog.Log("[tui] PANIC in main goroutine: %v\n%s", r, stack)
			panic(r)
		}
	}()

	database, err := db.Open(db.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	var runner agent.SessionProvider
	var daemonConnected bool
	var skew skewResult

	sockPath := daemon.DefaultSocketPath()
	client, err := dclient.Connect(sockPath)
	preExisting := err == nil // connected without auto-starting
	if err != nil {
		uxlog.Log("no daemon at %s, auto-starting...", sockPath)
		client, err = dclient.AutoStart(sockPath)
	}
	// Evaluate binary skew against BOTH the daemon and the connected supervisor.
	// Daemon-staleness is gated on preExisting: AutoStart fork/execs the current
	// binary, so a daemon we just spawned always matches the TUI and checking
	// would only produce false alarms. The supervisor, by contrast, is a
	// long-lived process the TUI did NOT fork — it can be stale even when we just
	// auto-started the daemon, so evaluateSkew checks it whenever it is present,
	// regardless of preExisting (see design D4 / gap #1).
	if err == nil {
		skew = evaluateSkew(client, preExisting)
	}

	// appRef is set after tui.New so the onFinish callback can reach the app.
	var appRef *tui.App

	if err != nil {
		uxlog.Log("daemon connect failed: %v — falling back to in-process runner", err)
		// In-process owns the runner exclusively, so any InProgress row in
		// the DB is from a prior process. The daemon does the same sweep
		// inside Serve(); this is the no-daemon analogue.
		if n, rerr := agent.ReconcileStaleSessions(database); rerr != nil {
			uxlog.Log("reconcile stale sessions failed: %v", rerr)
		} else if n > 0 {
			uxlog.Log("reconciled %d stale in_progress task(s) → in_review", n)
		}
		runner = agent.NewRunner(func(taskID string, exitErr error, stopped bool, lastOutput []byte) {
			if appRef != nil {
				appRef.NotifySessionExit(taskID, exitErr, stopped, lastOutput)
			}
		})
	} else {
		uxlog.Log("connected to daemon at %s", sockPath)
		daemonConnected = true
		runner = client
		defer client.Close()
	}

	// Wire up session exit callback for daemon mode BEFORE creating the app,
	// so no exit events can be missed during initialization.
	var appRef2 *tui.App
	if client != nil {
		client.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
			if a := appRef2; a != nil {
				a.HandleSessionExit(taskID, info)
			}
		})
	}

	app := tui.New(database, runner, daemonConnected)
	app.SetSkew(skew.daemonStale, skew.supervisorStale, skew.daemonIdentity, skew.supervisorIdentity)
	appRef = app
	appRef2 = app

	// Wire focus tracker: in daemon mode use the client (fires async RPC to
	// the daemon's FocusTracker); in in-process mode create a local tracker
	// and a Notifier that drives the in-process runner.
	if client != nil {
		app.SetFocusTracker(client)
	} else {
		ft := notify.NewFocusTracker(nil)
		app.SetFocusTracker(ft)
		// In-process notifier: reconcile periodically via a background goroutine.
		n := notify.New(notify.AdaptRunner(func(id string) notify.SessionHandleIface {
			return runner.Get(id)
		}), ft)
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				n.Reconcile(time.Now())
			}
		}()
	}

	// OS-level fd 2 redirect — installed RIGHT BEFORE app.Run() because
	// tcell only takes over the terminal once Run starts, so direct fd 2
	// writes don't corrupt anything until that moment. Setup-time errors
	// above (db.Open, daemon connect) wrote to the user's terminal as
	// intended via fmt.Fprintf(os.Stderr, ...). From here forward, ANY
	// direct fd 2 write would corrupt tcell's display, so we redirect.
	//
	// This catches what the slog/log Writer redirects can't:
	//   - Goroutine panic stack dumps (the Go runtime emits panic output
	//     directly to fd 2 via the runtime package, bypassing every
	//     Writer-based redirect and the main-goroutine defer recover).
	//   - Subprocess fd 2 inheritance — Go's exec defaults nil Stderr to
	//     /dev/null, but any future code that explicitly passes os.Stderr
	//     inherits this redirected fd 2 and fails safely toward uxlog.
	//   - Third-party libraries that write directly to fd 2.
	//
	// The original fd 2 is dup'd and restored EXPLICITLY right after
	// app.Run() returns (sync.Once guards against double-restore from the
	// deferred call). Without explicit restore before the post-Run error
	// branch below, `fmt.Fprintf(os.Stderr, "error: %v", err) + os.Exit(1)`
	// would silently swallow the error message into uxlog — os.Exit does
	// not run deferred functions.
	//
	// fd 1 is intentionally NOT redirected because computePTYSize calls
	// term.GetSize(int(os.Stdout.Fd())) and needs fd 1 to remain a TTY.
	// tcell uses /dev/tty directly so its I/O path is independent of fds.
	var restoreFd2 func()
	if f, ok := uxlog.Writer().(*os.File); ok {
		// fd values from *os.File.Fd() are guaranteed-small positive ints —
		// the uintptr → int conversion can never overflow. Silence gosec G115.
		stderrFd := int(os.Stderr.Fd()) //nolint:gosec // see comment
		uxlogFd := int(f.Fd())          //nolint:gosec // see comment
		origStderrFd, dupErr := syscall.Dup(stderrFd)
		if dupErr == nil {
			if d2Err := syscall.Dup2(uxlogFd, stderrFd); d2Err == nil {
				var once sync.Once
				restoreFd2 = func() {
					once.Do(func() {
						_ = syscall.Dup2(origStderrFd, stderrFd)
						_ = syscall.Close(origStderrFd)
					})
				}
				// Deferred restore covers panic paths (defers run during
				// panic unwind; os.Exit does not). Explicit restore below
				// (after app.Run returns) covers the normal-return error
				// path — sync.Once makes the second call a no-op.
				defer restoreFd2()
				uxlog.Log("[tui] fd 2 redirected to uxlog (catches goroutine panics, subprocess fd 2 inherit, third-party stderr)")
			} else {
				_ = syscall.Close(origStderrFd)
				uxlog.Log("[tui] fd 2 Dup2 failed: %v — goroutine panic stack dumps may still corrupt terminal", d2Err)
			}
		} else {
			uxlog.Log("[tui] fd 2 Dup (save original) failed: %v — skipped fd 2 redirect", dupErr)
		}
	}
	if restoreFd2 == nil {
		restoreFd2 = func() {}
	}

	runErr := app.Run()
	// EXPLICIT restore — must run before any fmt.Fprintf(os.Stderr, ...)
	// because os.Exit (below) does not run deferred functions and would
	// otherwise leave the error message in uxlog instead of the user's
	// terminal.
	restoreFd2()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		os.Exit(1)
	}
	// If a daemon restart occurred, close the new client.
	if rc := app.RestartedClient(); rc != nil {
		rc.Close()
	}
}

func runDaemon() {
	// Log to file since the daemon runs detached with no terminal.
	// Ensure data dir exists before opening the log (it may not on fresh install).
	if err := os.MkdirAll(db.DataDir(), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create data dir: %v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(db.DataDir(), "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open daemon log: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	// Wire slog to the same file. Without this, slog.* calls (used in
	// internal/push and internal/api) write to os.Stderr, which is /dev/null
	// for the detached daemon — so push send failures are invisible.
	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))

	// Pin the daemon's working directory to $HOME so relative project paths
	// resolve consistently regardless of how the daemon was launched. The
	// launchd plist sets WorkingDirectory=$HOME, but the TUI's auto-start
	// fallback (internal/daemon/client/autostart_fork.go) execs us without
	// cmd.Dir and we inherit wherever argus was launched (e.g. a repo dir when
	// running a local build). agent.CreateWorktree chdirs the project path for
	// `git worktree add`; a relative project path that resolves from $HOME then
	// fails with "chdir ...: no such file or directory" from any other cwd.
	// Non-fatal: a daemon with an unexpected cwd still works for absolute paths.
	if home, herr := os.UserHomeDir(); herr != nil {
		log.Printf("cannot resolve home dir; leaving working directory unchanged: %v", herr)
	} else if cerr := os.Chdir(home); cerr != nil {
		log.Printf("cannot chdir to home %q; leaving working directory unchanged: %v", home, cerr)
	} else {
		log.Printf("daemon working directory pinned to %s", home)
	}

	database, err := db.Open(db.DefaultPath())
	if err != nil {
		log.Fatalf("error opening database: %v", err)
	}
	defer database.Close()

	d := daemon.New(database)

	// Supervisor mode (cfg.Supervisor.Enabled, default ON as of P4): drive agent
	// PTYs through the out-of-process session-supervisor so the daemon can bounce
	// (to iterate on hera/coordination, or to pick up a rebuilt binary) without
	// interrupting agents. Connect to a live supervisor — auto-starting one if
	// absent — do the version handshake, and mount the client as the daemon's
	// runner BEFORE Serve so every consumer captures it. Any failure falls back to
	// the in-process runner (the retained OFF/rollback behavior): a broken
	// supervisor must never take the daemon offline. Set supervisor.enabled=false
	// (DB or config.toml) to force the in-process runner explicitly (rollback).
	if database.Config().Supervisor.Enabled {
		if c := connectSupervisor(); c != nil {
			d.UseSupervisorRunner(c)
			log.Printf("supervisor mode: daemon driving agents through %s", daemon.DefaultSupervisorSocketPath())
		} else {
			log.Printf("supervisor mode requested but unavailable; falling back to in-process runner")
		}
	}

	if err := d.Serve(daemon.DefaultSocketPath()); err != nil {
		if errors.Is(err, daemon.ErrDaemonAlreadyRunning) {
			// Lost the singleton race — another daemon is already serving the
			// socket. Exit cleanly so the TUI/launchd treats this as success.
			log.Printf("daemon already running; nothing to do")
			return
		}
		log.Fatalf("daemon error: %v", err)
	}
}

// connectSupervisor returns a session-supervisor client for the daemon to mount
// as its runner, or nil if no healthy supervisor can be reached/started (the
// caller then falls back to the in-process runner). It connects to a live
// supervisor, auto-starts one if absent, and requires a successful Hello
// handshake before committing — a half-broken supervisor is worse than the
// in-process fallback. On protocol skew it NEVER auto-restarts the running
// supervisor (that would SIGHUP its agents — design §4.4); it logs and proceeds
// within the running supervisor's capabilities.
func connectSupervisor() daemon.SupervisorClient {
	sock := daemon.DefaultSupervisorSocketPath()
	c, err := dclient.Connect(sock)
	if err != nil {
		// No live supervisor on the socket — auto-start one (Setsid-detached so
		// it outlives daemon bounces) and poll until it answers.
		c, err = dclient.AutoStartSupervisor(sock)
		if err != nil {
			log.Printf("supervisor: connect + auto-start both failed: %v", err)
			return nil
		}
	}

	hello, herr := c.Hello()
	if herr != nil {
		log.Printf("supervisor: handshake failed (%v); falling back to in-process runner", herr)
		c.Close() //nolint:errcheck // discarding an unhealthy client
		return nil
	}
	if !daemon.SupervisorProtocolMatch(hello) {
		log.Printf("supervisor: protocol skew daemon=v%d supervisor=v%d — proceeding within the running supervisor's capabilities (NOT auto-restarting a live supervisor; agents would die)",
			daemon.ProtocolVersion, hello.ProtocolVersion)
	} else {
		log.Printf("supervisor: connected protocol=v%d binary=%s", hello.ProtocolVersion, hello.BinaryPath)
	}
	return c
}

// stopDaemon sends a shutdown RPC to the daemon. Returns (true, nil) if the
// daemon was stopped, (false, nil) if it wasn't running, or (false, err) on
// unexpected failures.
func stopDaemon(sockPath string) (bool, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		// Can't connect — daemon probably not running.
		return false, nil
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("R")); err != nil {
		return false, fmt.Errorf("write error: %w", err)
	}

	client := jsonrpc.NewClient(conn)
	defer client.Close()

	var resp daemon.StatusResp
	if err := client.Call("Daemon.Shutdown", &daemon.Empty{}, &resp); err != nil {
		return false, fmt.Errorf("shutdown error: %w", err)
	}
	return true, nil
}

func runDaemonStop() {
	sockPath := daemon.DefaultSocketPath()
	stopped, err := stopDaemon(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if stopped {
		fmt.Println("daemon stopped")
	} else {
		fmt.Println("no daemon running")
	}
}

// bootInfoProvider is the subset of *dclient.Client's API that evaluateSkew
// needs. Narrowing to an interface lets tests drive the full wiring
// (os.Executable → BinaryHashFile → staleDecision) against the test binary with
// a fake BootInfoResp, without a live daemon.
type bootInfoProvider interface {
	BootInfo() (daemon.BootInfoResp, error)
}

// skewResult carries the startup binary-skew evaluation: whether the daemon
// and/or supervisor run a different binary than the TUI, plus each stale
// process's rich display identity (commit SHA + dirty flag + resolved path,
// short content-hash fallback) for the modal to render.
type skewResult struct {
	daemonStale        bool
	supervisorStale    bool
	daemonIdentity     string
	supervisorIdentity string
}

// evaluateSkew fetches the daemon's BootInfo once and evaluates daemon +
// supervisor binary skew against the TUI's own on-disk binary. It replaces the
// former isDaemonStale, splitting the single daemon decision into a daemon and
// a supervisor decision over the enriched BootInfoResp.
//
// checkDaemon gates the DAEMON decision only (the caller passes preExisting): a
// daemon the TUI just auto-started forks the current binary and can never be
// stale, so checking it would only produce false alarms. The SUPERVISOR
// decision is ALWAYS evaluated when a supervisor is present — it is a
// long-lived process the TUI did not fork, so it can be stale even on the
// auto-start path (the blind spot design D4 closes).
//
// The signal is a content hash, NOT mtime. `go install` rewrites the binary
// (bumping its mtime) on every run even when the source is unchanged, and
// because ~/.argus/argusd symlinks to the same file the TUI runs, an mtime
// comparison flagged the daemon stale on every launch for habitual reinstallers
// even though the deterministic build was byte-identical. Hashing only differs
// on a real code change; mtime remains the fallback for a pre-BinaryHash daemon.
// VCS identity (commit SHA + dirty) is display-only and NEVER gates — it is
// blank for binaries built outside a git tree. Detection is best-effort: any
// failed step (RPC error, missing binary, hash/stat error) yields "not stale"
// so a benign local error never nags the user into a needless restart.
func evaluateSkew(client bootInfoProvider, checkDaemon bool) skewResult {
	var res skewResult
	info, err := client.BootInfo()
	if err != nil {
		uxlog.Log("[tui] BootInfo failed: %v", err)
		return res
	}
	exe, err := os.Executable()
	if err != nil {
		return res
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	// Read the TUI binary's identity. Hash it when EITHER the daemon or the
	// supervisor reported a hash to compare against; the mtime fallback below
	// serves only a pre-BinaryHash daemon.
	var (
		tuiHash     string
		tuiHashErr  bool
		tuiMtime    time.Time
		tuiMtimeErr bool
	)
	if info.BinaryHash != "" || (info.SupervisorPresent && info.SupervisorHash != "") {
		tuiHash, err = daemon.BinaryHashFile(exe)
		tuiHashErr = err != nil
	}
	if info.BinaryHash == "" {
		if st, serr := os.Stat(exe); serr == nil {
			tuiMtime = st.ModTime()
		} else {
			tuiMtimeErr = true
		}
	}

	if checkDaemon && staleDecision(info, tuiHash, tuiHashErr, tuiMtime, tuiMtimeErr) {
		res.daemonStale = true
		if info.BinaryHash != "" {
			uxlog.Log("[tui] daemon binary stale: daemon hash=%s tui hash=%s",
				shortHash(info.BinaryHash), shortHash(tuiHash))
		} else {
			uxlog.Log("[tui] daemon binary stale: daemon mtime=%s tui mtime=%s",
				info.BinaryMtime.Format(time.RFC3339), tuiMtime.Format(time.RFC3339))
		}
	}
	if supervisorStaleDecision(info, tuiHash, tuiHashErr) {
		res.supervisorStale = true
		uxlog.Log("[tui] supervisor binary stale: supervisor hash=%s tui hash=%s",
			shortHash(info.SupervisorHash), shortHash(tuiHash))
	}

	res.daemonIdentity = formatIdentity(info.VCS, info.BinaryHash, info.BinaryPath)
	if info.SupervisorPresent {
		res.supervisorIdentity = formatIdentity(info.SupervisorVCS, info.SupervisorHash, info.SupervisorPath)
	}
	return res
}

// supervisorStaleDecision is the pure supervisor-staleness core over the
// enriched BootInfoResp. It returns false (never stale) when no supervisor is
// present, when the supervisor speaks an older protocol that omits its hash
// (empty SupervisorHash ⇒ "unknown" — the additive-protocol feature-detect,
// never a false positive), or when the TUI's own binary hash could not be read.
// Otherwise it is a pure content-hash comparison; VCS info never gates.
func supervisorStaleDecision(info daemon.BootInfoResp, tuiHash string, tuiHashErr bool) bool {
	if !info.SupervisorPresent {
		return false
	}
	if info.SupervisorHash == "" {
		return false // pre-hash supervisor: identity unknown, never stale
	}
	if tuiHashErr {
		return false
	}
	return tuiHash != info.SupervisorHash
}

// formatIdentity renders a process's display identity for the skew modal: the
// commit SHA + dirty flag when VCS build info is present, else the short content
// hash, then the resolved path. Mirrors internal/doctor.displayIdentity. This is
// display-only — it never participates in the staleness decision.
func formatIdentity(v buildid.VCS, hash, path string) string {
	var ident string
	switch {
	case v.Present():
		ident = shortHash(v.Revision)
		if v.Modified {
			ident += " (dirty)"
		}
	case hash != "":
		ident = "sha:" + shortHash(hash)
	default:
		ident = "unknown"
	}
	if path != "" {
		return ident + " @ " + path
	}
	return ident
}

// staleDecision is the pure core of isDaemonStale: given the daemon's boot
// identity and the TUI binary's already-read current identity, it decides
// staleness. Split out from the I/O so every branch (hash match/mismatch,
// hash-read failure, mtime fallback, mtime-read failure) is unit-testable
// without a live daemon client or a real executable. A read failure of the
// TUI's own binary (tuiHashErr / tuiMtimeErr) yields "not stale" — a benign
// local error must never nag the user into a needless restart.
func staleDecision(info daemon.BootInfoResp, tuiHash string, tuiHashErr bool, tuiMtime time.Time, tuiMtimeErr bool) bool {
	if info.BinaryHash != "" {
		if tuiHashErr {
			return false
		}
		return tuiHash != info.BinaryHash
	}
	// Fallback: pre-BinaryHash daemon. Compare mtime.
	if info.BinaryMtime.IsZero() || tuiMtimeErr {
		return false // older daemon without BootInfo, or stat failed.
	}
	return !tuiMtime.Equal(info.BinaryMtime)
}

// shortHash truncates a hex digest for compact logging.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// runDaemonInstall installs the LaunchAgent so the daemon auto-starts at user
// login (macOS only). Reinstalling overwrites the previous plist.
func runDaemonInstall() {
	if !launchagent.Available() {
		fmt.Fprintln(os.Stderr, "auto-start is only supported on macOS")
		os.Exit(1)
	}
	daemonExe, err := launchagent.ResolveDaemonExe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve daemon exe: %v\n", err)
		os.Exit(1)
	}
	if err := launchagent.Install(daemonExe); err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}
	path, _ := launchagent.PlistPath()
	fmt.Printf("installed LaunchAgent at %s\n", path)
	fmt.Println("daemon will auto-start at login")
}

// runDaemonUninstall removes the LaunchAgent and unloads it from launchd.
func runDaemonUninstall() {
	if !launchagent.Available() {
		fmt.Fprintln(os.Stderr, "auto-start is only supported on macOS")
		os.Exit(1)
	}
	if err := launchagent.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("LaunchAgent removed")
}

// runDaemonStatus prints the LaunchAgent's installation state.
func runDaemonStatus() {
	if !launchagent.Available() {
		fmt.Println("auto-start: not available (macOS only)")
		return
	}
	s := launchagent.CurrentStatus()
	fmt.Printf("plist: %s\n", s.PlistPath)
	fmt.Printf("installed: %v\n", s.Installed)
	fmt.Printf("loaded:    %v\n", s.Loaded)
}

func runDaemonRestart() {
	sockPath := daemon.DefaultSocketPath()
	stopped, err := stopDaemon(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stop failed: %v\n", err)
		os.Exit(1)
	}

	if stopped {
		// Wait for socket cleanup before starting the new daemon.
		dclient.WaitForShutdown(sockPath, 3*time.Second)
		fmt.Println("daemon stopped, starting new instance...")
	} else {
		fmt.Println("no daemon running, starting new instance...")
	}
	runDaemon()
}

// configureProcessLogging routes the stdlib logger and the slog default to w.
// The supervisor (run detached or foreground) uses it so slog.*/log.* calls
// from agent.Runner and the session core never reach a controlling terminal —
// the same discipline runDaemon applies inline. Extracted so the wiring can be
// pinned by a regression test (the supervisor forks PTY children, so a stray
// stderr write is a real hazard; see CLAUDE.md rule 6).
func configureProcessLogging(w io.Writer) {
	log.SetOutput(w)
	slog.SetDefault(slog.New(slog.NewTextHandler(w, nil)))
}

// runSupervisor runs the session-supervisor serve loop in the foreground. It
// mirrors runDaemon's logging discipline (slog + stdlib log redirected to
// ~/.argus/supervisor.log) because the supervisor forks PTY children and must
// never write to fd 1/2 after startup — a stray write would leak into the
// controlling terminal of whatever launched it (or corrupt nothing when
// detached, where fd 2 is /dev/null). The daemon Setsid-forks this in P2; here
// `start` is the runnable entry point for that fork (and for tests/manual runs).
//
// The supervisor holds NO database of its own — it only reads config to resolve
// backends. We open the DB here solely to inject database.Config as the
// supervisor's cfgFn, keeping the *daemon.Supervisor type free of any DB
// coupling. P1 is dark: nothing connects to the supervisor yet.
func runSupervisor() {
	if err := os.MkdirAll(db.DataDir(), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create data dir: %v\n", err)
		os.Exit(1)
	}
	logPath := filepath.Join(db.DataDir(), "supervisor.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot open supervisor log: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close() //nolint:errcheck // process is exiting; close error is non-actionable
	// Route stdlib log + slog default to the file. Without this, slog.* calls
	// (in agent.Runner, the session core, etc.) write to os.Stderr — for a
	// detached supervisor that's /dev/null (invisible), for a foreground one
	// that's the terminal. Mirrors runDaemon's redirect; CLAUDE.md rule 6.
	configureProcessLogging(logFile)

	// Pin the working directory to $HOME so relative project paths resolve
	// consistently regardless of how the supervisor was launched (mirrors the
	// daemon). Non-fatal: absolute paths still work from any cwd.
	if home, herr := os.UserHomeDir(); herr != nil {
		log.Printf("cannot resolve home dir; leaving working directory unchanged: %v", herr)
	} else if cerr := os.Chdir(home); cerr != nil {
		log.Printf("cannot chdir to home %q; leaving working directory unchanged: %v", home, cerr)
	} else {
		log.Printf("supervisor working directory pinned to %s", home)
	}

	database, err := db.Open(db.DefaultPath())
	if err != nil {
		log.Fatalf("error opening database: %v", err)
	}
	defer database.Close() //nolint:errcheck // process is exiting; close error is non-actionable

	s := daemon.NewSupervisor(database.Config)
	if err := s.Serve(daemon.DefaultSupervisorSocketPath()); err != nil {
		if errors.Is(err, daemon.ErrDaemonAlreadyRunning) {
			// Lost the singleton race — another supervisor is already serving.
			// Exit cleanly so a launcher treats this as success.
			log.Printf("supervisor already running; nothing to do")
			return
		}
		log.Fatalf("supervisor error: %v", err)
	}
}

// stopSupervisor sends a graceful-shutdown RPC to the supervisor over its
// socket. Mirrors stopDaemon. Returns (true, nil) if stopped, (false, nil) if
// not running, or (false, err) on unexpected failure.
func stopSupervisor(sockPath string) (bool, error) {
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return false, nil // not running
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("R")); err != nil {
		return false, fmt.Errorf("write error: %w", err)
	}

	client := jsonrpc.NewClient(conn)
	defer client.Close() //nolint:errcheck // short-lived CLI client; close error is non-actionable

	var resp daemon.StatusResp
	if err := client.Call("Daemon.Shutdown", &daemon.Empty{}, &resp); err != nil {
		return false, fmt.Errorf("shutdown error: %w", err)
	}
	return true, nil
}

func runSupervisorStop() {
	stopped, err := stopSupervisor(daemon.DefaultSupervisorSocketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if stopped {
		fmt.Println("supervisor stopped")
	} else {
		fmt.Println("no supervisor running")
	}
}

// runSupervisorStatus pings the supervisor and prints whether it is responding
// plus its reported protocol version. Mirrors the daemon's status probe shape.
func runSupervisorStatus() {
	sockPath := daemon.DefaultSupervisorSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Println("supervisor: not running")
		return
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("R")); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}
	client := jsonrpc.NewClient(conn)
	defer client.Close() //nolint:errcheck // short-lived CLI client; close error is non-actionable

	var pong daemon.PongResp
	if err := client.Call("Daemon.Ping", &daemon.Empty{}, &pong); err != nil {
		fmt.Fprintf(os.Stderr, "ping error: %v\n", err)
		os.Exit(1)
	}
	var hello daemon.HelloResp
	if err := client.Call("Daemon.Hello", &daemon.Empty{}, &hello); err != nil {
		// Ping succeeded but Hello failed — still report running.
		fmt.Println("supervisor: running (protocol unknown)")
		return
	}
	fmt.Printf("supervisor: running (protocol v%d)\n", hello.ProtocolVersion)
}
