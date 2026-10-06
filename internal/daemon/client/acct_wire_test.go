package client

import (
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// wireRecorder is a "Daemon" RPC service that records the task projection the
// client ships for KickRerender and Recycle.
type wireRecorder struct {
	mu      sync.Mutex
	kick    daemon.KickReq
	recycle daemon.RecycleReq
}

func (w *wireRecorder) KickRerender(req *daemon.KickReq, resp *daemon.StatusResp) error {
	w.mu.Lock()
	w.kick = *req
	w.mu.Unlock()
	resp.OK = true
	return nil
}

func (w *wireRecorder) Recycle(req *daemon.RecycleReq, resp *daemon.StatusResp) error {
	w.mu.Lock()
	w.recycle = *req
	w.mu.Unlock()
	resp.OK = true
	return nil
}

func newWireRecorderClient(t *testing.T) (*Client, *wireRecorder) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "w.sock")
	ln, err := net.Listen("unix", sock)
	testutil.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck
	rec := &wireRecorder{}
	server := rpc.NewServer()
	testutil.NoError(t, server.RegisterName("Daemon", rec))
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close() //nolint:errcheck
				prefix := make([]byte, 1)
				if _, err := io.ReadFull(conn, prefix); err != nil || prefix[0] != 'R' {
					return
				}
				server.ServeCodec(jsonrpc.NewServerCodec(conn))
			}(conn)
		}
	}()
	conn, err := net.Dial("unix", sock)
	testutil.NoError(t, err)
	_, err = conn.Write([]byte("R"))
	testutil.NoError(t, err)
	c := &Client{
		rpc:      jsonrpc.NewClient(conn),
		sockPath: sock,
		sessions: make(map[string]*RemoteSession),
		closed:   make(chan struct{}),
	}
	t.Cleanup(func() { c.rpc.Close() }) //nolint:errcheck
	return c, rec
}

// TestAcctWireKickRecycle pins that the in-place restart RPCs carry the task's
// account and sandbox override, which BuildCmd reads on the supervisor side.
func TestAcctWireKickRecycle(t *testing.T) {
	c, rec := newWireRecorderClient(t)
	task := &model.Task{ID: "k", Account: "work", SandboxOverride: "disabled"}

	t.Run("kick", func(t *testing.T) {
		testutil.NoError(t, c.KickRerender(task, config.Config{}, 24, 80))
		rec.mu.Lock()
		defer rec.mu.Unlock()
		testutil.Equal(t, rec.kick.Account, "work")
		testutil.Equal(t, rec.kick.SandboxOverride, "disabled")
	})
	t.Run("recycle", func(t *testing.T) {
		testutil.NoError(t, c.Recycle(task, config.Config{}, 24, 80))
		rec.mu.Lock()
		defer rec.mu.Unlock()
		testutil.Equal(t, rec.recycle.Account, "work")
		testutil.Equal(t, rec.recycle.SandboxOverride, "disabled")
	})
}

// TestSupAcctEnv runs a real session through client→RPC→supervisor→BuildCmd
// with a fake claude that records CLAUDE_CONFIG_DIR, proving the task's
// account survives the supervisor's task rebuild.
func TestSupAcctEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { database.Close() }) //nolint:errcheck

	acctDir := filepath.Join(t.TempDir(), "work")
	cfgFn := func() config.Config {
		c := database.Config()
		c.Sandbox.Enabled = false
		c.Accounts = map[string]config.Account{"work": {ClaudeConfigDir: acctDir}}
		return c
	}
	supSock := filepath.Join(t.TempDir(), "s.sock")
	sup := daemon.NewSupervisor(cfgFn)
	go sup.Serve(supSock) //nolint:errcheck
	t.Cleanup(func() { sup.Shutdown() })
	waitFile(t, supSock)
	sc, err := Connect(supSock)
	testutil.NoError(t, err)
	t.Cleanup(func() { sc.Close() }) //nolint:errcheck

	binDir := t.TempDir()
	envFile := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\necho \"dir=$CLAUDE_CONFIG_DIR\" > " + envFile + "\n"
	testutil.NoError(t, os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755))
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	testutil.NoError(t, database.SetBackend("be-acct", config.Backend{Command: "claude"}))
	task := &model.Task{
		ID: "acctrpc", Name: "acctrpc", Status: model.StatusInProgress,
		Backend: "be-acct", Worktree: t.TempDir(), Account: "work",
	}
	testutil.NoError(t, database.Add(task))

	_, err = sc.Start(task, config.Config{}, 24, 80, false)
	testutil.NoError(t, err)

	var got string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, rerr := os.ReadFile(envFile); rerr == nil && len(data) > 0 {
			got = strings.TrimSpace(string(data))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	testutil.Equal(t, got, "dir="+acctDir)
}
