package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/drn/argus/internal/apiclient"
	"github.com/drn/argus/internal/apistore"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui"
	"github.com/drn/argus/internal/uxlog"
)

// runRemoteTUI launches the TUI pointed at a remote argus host. The local
// SQLite database is NOT opened — every persistence call routes through the
// remote /api/* endpoints via apistore. Sessions stream via SSE via
// apiclient.Provider.
//
// Behaves like runTUI() in every other respect (uxlog, slog redirects, fd 2
// redirect, panic recovery). The only deltas from local mode:
//
//  1. No db.Open / db.ReconcileStaleSessions — the remote daemon owns the DB.
//  2. No daemon socket connection / autostart — REST is the transport.
//  3. No in-process runner fallback — apiclient.Provider is the only option.
//  4. Some Settings actions (restart daemon, update-self, install-launchagent)
//     are operationally meaningless from a remote process and silently no-op
//     today — clear errors land in the status bar when invoked. Phase 6
//     will hide them in the UI when the App is constructed with a remote
//     store.
func runRemoteTUI(baseURL, token string) {
	if token == "" {
		fmt.Fprintln(os.Stderr, "error: --remote requires --token TOKEN or ARGUS_TOKEN env var")
		os.Exit(2)
	}
	// --token TOKEN puts the secret in `ps aux` / /proc/$PID/cmdline.
	// Detect the cli-flag path and warn the user once before the tcell
	// alt-screen takes over. ARGUS_TOKEN env doesn't leak this way.
	if os.Getenv("ARGUS_TOKEN") == "" {
		fmt.Fprintln(os.Stderr, "warning: --token is visible in `ps`; prefer the ARGUS_TOKEN env var")
	}

	// Initialize UX debug log — same path as the local TUI so a developer
	// who alternates between local and remote sees one unified log file.
	if err := uxlog.Init(uxlog.Path(db.DataDir())); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot open ux log: %v\n", err)
	}
	defer uxlog.Close()
	uxlog.Log("=== argus TUI starting (remote mode) base=%s ===", baseURL)

	// Mirror the local-mode logger redirects. CLAUDE.md hard rule 6: nothing
	// in this process may write to the user's terminal once tcell takes over.
	slog.SetDefault(slog.New(slog.NewTextHandler(uxlog.Writer(), nil)))
	log.SetOutput(uxlog.Writer())

	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			uxlog.Log("[remote-tui] PANIC in main goroutine: %v\n%s", r, stack)
			panic(r)
		}
	}()

	// Build the apiclient + provider + apistore. Validate the connection up
	// front so a bad token / unreachable host fails loudly before the TUI
	// alt-screen takes over.
	c := apiclient.New(baseURL, token)
	if _, err := c.Status(context.Background()); err != nil {
		if apiclient.IsUnauthorized(err) {
			fmt.Fprintf(os.Stderr, "error: remote rejected token (401). Check --token / ARGUS_TOKEN.\n")
		} else {
			fmt.Fprintf(os.Stderr, "error: cannot reach remote argus at %s: %v\n", baseURL, err)
		}
		os.Exit(1)
	}
	uxlog.Log("[remote-tui] connected to %s", baseURL)

	store := apistore.New(c)
	if _, err := store.RefreshConfig(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: initial config fetch failed: %v (continuing — settings tab will show empty until next refresh)\n", err)
	}

	provider := apiclient.NewProvider(c)
	defer provider.Close()

	// appRef is set after tui.New so the onSessionExit callback can reach
	// the app — same pattern as runTUI's appRef.
	var appRef *tui.App
	provider.OnSessionExit(func(taskID string, info apiclient.SessionExitInfo) {
		if appRef == nil {
			return
		}
		// Reuse the daemon-client exit handler — apiclient.SessionExitInfo
		// shape mirrors daemon.ExitInfo on purpose so the TUI's existing
		// HandleSessionExit doesn't need a remote-specific branch.
		appRef.HandleSessionExit(taskID, daemon.ExitInfo{
			Err:        info.Err,
			Stopped:    info.Stopped,
			StreamLost: info.StreamLost,
			LastOutput: info.LastOutput,
		})
	})

	app := tui.New(store, provider, true)
	appRef = app

	// Periodic config refresh — the remote daemon may change settings (e.g.
	// projects added from the PWA); a 30 s pull keeps the cached snapshot
	// fresh enough for the TUI's Settings tab without burning a request per
	// frame. Cancelled on shutdown.
	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	defer refreshCancel()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-t.C:
				_, _ = store.RefreshConfig(refreshCtx)
			}
		}
	}()

	// fd 2 redirect — see runTUI for the rationale.
	var restoreFd2 func()
	if f, ok := uxlog.Writer().(*os.File); ok {
		stderrFd := int(os.Stderr.Fd()) //nolint:gosec
		uxlogFd := int(f.Fd())          //nolint:gosec
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
				defer restoreFd2()
			} else {
				_ = syscall.Close(origStderrFd)
			}
		}
	}
	if restoreFd2 == nil {
		restoreFd2 = func() {}
	}

	runErr := app.Run()
	restoreFd2()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", runErr)
		os.Exit(1)
	}
}
