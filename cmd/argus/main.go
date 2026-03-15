package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/rpc/jsonrpc"
	"os"

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

	// Try to connect to daemon; if that fails, fall back to in-process runner.
	var runner agent.SessionProvider
	var p *tea.Program

	sockPath := daemon.DefaultSocketPath()
	client, err := dclient.Connect(sockPath)
	if err != nil {
		// No daemon running — use in-process runner (original behavior).
		inProc := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
			if p != nil {
				p.Send(ui.AgentFinishedMsg{TaskID: taskID, Err: err, Stopped: stopped, LastOutput: lastOutput})
			}
		})
		runner = inProc
	} else {
		// Daemon is running — use client as session provider.
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

	m := ui.NewModel(database, runner)
	p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runDaemon() {
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

