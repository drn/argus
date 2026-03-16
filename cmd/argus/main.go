package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/ui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "daemon":
			if len(os.Args) > 2 && os.Args[2] == "stop" {
				runDaemonStop()
				return
			}
			runDaemon()
			return
		}
	}

	runTUI()
}

func runTUI() {
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
		client, err = autoStartDaemon(sockPath)
	}

	if err != nil {
		// Daemon failed to start — fall back to in-process runner.
		inProc := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
			if p != nil {
				p.Send(ui.AgentFinishedMsg{TaskID: taskID, Err: err, Stopped: stopped, LastOutput: lastOutput})
			}
		})
		runner = inProc
	} else {
		// Connected to daemon.
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
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// autoStartDaemon launches the daemon as a background process and waits
// for it to be ready. Returns a connected client or an error.
func autoStartDaemon(sockPath string) (*dclient.Client, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	// Detach from parent process group so the daemon survives TUI exit.
	cmd.SysProcAttr = daemonSysProcAttr()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start daemon: %w", err)
	}
	// Release the child process so it isn't reaped when we exit.
	cmd.Process.Release()

	// Poll for the socket to become available.
	const (
		pollInterval = 50 * time.Millisecond
		maxWait      = 3 * time.Second
	)
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)
		if client, err := dclient.Connect(sockPath); err == nil {
			return client, nil
		}
	}

	return nil, fmt.Errorf("daemon did not become ready within %s", maxWait)
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

func runDaemonStop() {
	sockPath := daemon.DefaultSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot connect to daemon: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send RPC prefix byte.
	if _, err := conn.Write([]byte("R")); err != nil {
		fmt.Fprintf(os.Stderr, "write error: %v\n", err)
		os.Exit(1)
	}

	client := jsonrpc.NewClient(conn)
	defer client.Close()

	var resp daemon.StatusResp
	if err := client.Call("Daemon.Shutdown", &daemon.Empty{}, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("daemon stopped")
}

