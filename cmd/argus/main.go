package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/ui"
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
			default:
				fmt.Fprintf(os.Stderr, "unknown daemon subcommand: %s\n", sub)
				os.Exit(1)
			}
			return
		}
	}

	runTUI()
}

func runTUI() {
	// Initialize UX debug log (separate from daemon log).
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
	var p *tea.Program
	var daemonConnected bool

	sockPath := daemon.DefaultSocketPath()
	client, err := dclient.Connect(sockPath)
	if err != nil {
		// No daemon running — auto-start one and retry.
		uxlog.Log("no daemon at %s, auto-starting...", sockPath)
		client, err = dclient.AutoStart(sockPath)
	}

	if err != nil {
		// Daemon failed to start — fall back to in-process runner.
		uxlog.Log("daemon connect failed: %v — falling back to in-process runner", err)
		inProc := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
			if p != nil {
				p.Send(ui.AgentFinishedMsg{TaskID: taskID, Err: err, Stopped: stopped, LastOutput: lastOutput})
			}
		})
		runner = inProc
	} else {
		// Connected to daemon.
		uxlog.Log("connected to daemon at %s", sockPath)
		daemonConnected = true
		client.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
			if p != nil {
				var exitErr error
				if info.Err != "" {
					exitErr = errors.New(info.Err)
				}
				p.Send(ui.AgentFinishedMsg{
					TaskID:     taskID,
					Err:        exitErr,
					Stopped:    info.Stopped,
					LastOutput: info.LastOutput,
				})
			}
		})
		runner = client
		defer client.Close()
	}

	m := ui.NewModel(database, runner, daemonConnected)
	p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.SetProgram(p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// If a daemon restart occurred, close the new client.
	if rc := m.RestartedClient(); rc != nil {
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

