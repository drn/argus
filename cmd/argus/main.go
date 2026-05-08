package main

import (
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
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

	runTUI()
}

func runTUI() {
	// Initialize UX debug log.
	if err := uxlog.Init(uxlog.Path(db.DataDir())); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot open ux log: %v\n", err)
	}
	defer uxlog.Close()
	uxlog.Log("=== argus TUI starting ===")

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
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
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
