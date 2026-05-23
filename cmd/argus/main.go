package main

import (
	"fmt"
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
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/launchagent"
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
		case "kb":
			runKBCommand(os.Args[2:])
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
	var daemonStale bool

	sockPath := daemon.DefaultSocketPath()
	client, err := dclient.Connect(sockPath)
	preExisting := err == nil // connected without auto-starting
	if err != nil {
		uxlog.Log("no daemon at %s, auto-starting...", sockPath)
		client, err = dclient.AutoStart(sockPath)
	}
	// Only meaningful when we connected to a daemon we did NOT just spawn.
	// AutoStart fork/execs the current binary, so the daemon's binary always
	// matches the TUI in that case — checking would just produce false alarms.
	if err == nil && preExisting {
		daemonStale = isDaemonStale(client)
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
	app.SetDaemonStale(daemonStale)
	appRef = app
	appRef2 = app

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

	database, err := db.Open(db.DefaultPath())
	if err != nil {
		log.Fatalf("error opening database: %v", err)
	}
	defer database.Close()

	d := daemon.New(database)
	if err := d.Serve(daemon.DefaultSocketPath()); err != nil {
		log.Fatalf("daemon error: %v", err)
	}
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

// isDaemonStale returns true when the running daemon's binary mtime differs
// from the TUI's on-disk binary — typically because argus was rebuilt while
// the daemon kept running, but it also fires on rollbacks or any other case
// where the two files differ. Detection is best-effort: if any step fails
// (older daemon without BootInfo, RPC error, missing binary, stat error),
// we return false to avoid nagging the user over benign issues.
func isDaemonStale(client *dclient.Client) bool {
	info, err := client.BootInfo()
	if err != nil {
		uxlog.Log("[tui] BootInfo failed: %v", err)
		return false
	}
	if info.BinaryMtime.IsZero() {
		return false // older daemon without BootInfo, or stat failed at boot
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	st, err := os.Stat(exe)
	if err != nil {
		return false
	}
	if st.ModTime().Equal(info.BinaryMtime) {
		return false
	}
	uxlog.Log("[tui] daemon binary stale: daemon mtime=%s tui mtime=%s",
		info.BinaryMtime.Format(time.RFC3339), st.ModTime().Format(time.RFC3339))
	return true
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
