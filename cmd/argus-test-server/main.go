// argus-test-server boots a minimal API + runner against an in-memory DB
// for end-to-end testing of the web dashboard. Seeds a `bash`-backed task
// so the terminal can be exercised without depending on a real LLM CLI.
//
// HOME is overridden to a tempdir so this binary cannot touch the user's
// real ~/.argus state.
//
// Env:
//   ARGUS_TEST_PORT  — port to bind (default 7744)
//   ARGUS_TEST_TOKEN — bearer token (default "test-token")
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/api"
	"github.com/drn/argus/internal/clipboard"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

func main() {
	port := flag.Int("port", envOrInt("ARGUS_TEST_PORT", 7744), "port to bind")
	token := flag.String("token", envOr("ARGUS_TEST_TOKEN", "test-token"), "bearer token")
	flag.Parse()

	// Hard-isolate from real ~/.argus.
	tmpHome, err := os.MkdirTemp("", "argus-test-home-*")
	if err != nil {
		log.Fatalf("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpHome) //nolint:errcheck // best-effort cleanup
	if err := os.Setenv("HOME", tmpHome); err != nil {
		log.Fatalf("setenv HOME: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmpHome, ".argus"), 0o700); err != nil {
		log.Fatalf("mkdir argus: %v", err)
	}

	d, err := db.OpenInMemory()
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer d.Close() //nolint:errcheck // best-effort cleanup

	// Backend that runs `bash` interactively. PTY echo + prompt gives a
	// realistic terminal to exercise xterm.js with.
	if err := d.SetBackend("bash-test", config.Backend{
		Command:    "bash --noprofile --norc -i",
		PromptFlag: "",
	}); err != nil {
		log.Fatalf("set backend: %v", err)
	}
	if err := d.SetConfigValue("defaults.backend", "bash-test"); err != nil {
		log.Fatalf("set default: %v", err)
	}

	// Project — git-init the tempdir so CreateAndStart's worktree step
	// succeeds. Used by the multipart create-task flow which exercises the
	// full agent.CreateAndStart pipeline (the legacy JSON path goes through
	// the local creator callback below and bypasses worktree creation).
	projDir := filepath.Join(tmpHome, "test-proj")
	if err := os.MkdirAll(projDir, 0o750); err != nil {
		log.Fatalf("mkdir proj: %v", err)
	}
	if err := initTestRepo(projDir); err != nil {
		log.Fatalf("init test repo: %v", err)
	}
	if err := d.SetProject("test-proj", config.Project{Path: projDir, Branch: "HEAD"}); err != nil {
		log.Fatalf("set project: %v", err)
	}

	// Seed a running task using the bash backend. Prompt is set AFTER
	// runner.Start so the bash session itself starts clean (no extra positional
	// argument that would make bash exit with 127), but the in-DB Prompt is
	// non-empty so PWA specs can exercise the "View prompt" modal's content
	// path. Specs needing the empty-prompt placeholder branch can clear it
	// via window.currentTask.prompt = ''.
	task := &model.Task{
		Name:     "echo-bash",
		Project:  "test-proj",
		Backend:  "bash-test",
		Worktree: projDir,
		Status:   model.StatusPending,
	}
	if err := d.Add(task); err != nil {
		log.Fatalf("db add: %v", err)
	}

	runner := agent.NewRunner(nil)
	if _, err := runner.Start(task, d.Config(), 24, 80, false); err != nil {
		log.Fatalf("runner.Start: %v", err)
	}
	task.SetStatus(model.StatusInProgress)
	task.Prompt = "Investigate flaky CI runs and add retry logic."
	if err := d.Update(task); err != nil {
		log.Fatalf("db update: %v", err)
	}

	creator := func(name, prompt, project string, _ bool) (*model.Task, error) {
		t := &model.Task{
			Name:     name,
			Prompt:   prompt,
			Project:  project,
			Backend:  "bash-test",
			Worktree: projDir,
			Status:   model.StatusInProgress,
		}
		if err := d.Add(t); err != nil {
			return nil, err
		}
		if _, err := runner.Start(t, d.Config(), 24, 80, false); err != nil {
			return nil, err
		}
		return t, nil
	}

	srv := api.New(d, runner, *token, creator)
	srv.SetClipboard(clipboard.New())
	actualPort, err := srv.ListenAndServe(*port)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	// Test-only reset: stops all sessions, deletes all tasks, re-seeds the
	// echo-bash task. Bound under the same auth as the rest of the API.
	resetMux := http.NewServeMux()
	resetMux.HandleFunc("POST /test/reset", func(w http.ResponseWriter, r *http.Request) {
		runner.StopAll()
		// Wait for cleanup so the new task ID doesn't collide.
		time.Sleep(100 * time.Millisecond)
		ts, _ := d.Tasks()
		for _, t := range ts {
			d.Delete(t.ID) //nolint:errcheck
		}
		nt := &model.Task{
			Name:     "echo-bash",
			Project:  "test-proj",
			Backend:  "bash-test",
			Worktree: projDir,
			Status:   model.StatusPending,
		}
		if err := d.Add(nt); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if _, err := runner.Start(nt, d.Config(), 24, 80, false); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		nt.SetStatus(model.StatusInProgress)
		nt.Prompt = "Investigate flaky CI runs and add retry logic."
		d.Update(nt) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"reset":true,"task":%q}`, nt.ID) //nolint:errcheck
	})
	resetSrv := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", actualPort+10),
		Handler:           resetMux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go func() {
		if err := resetSrv.ListenAndServe(); err != nil {
			log.Printf("reset port: %v", err)
		}
	}()

	log.Printf("argus-test on http://127.0.0.1:%d  token=%s  task=%s  reset=http://127.0.0.1:%d/test/reset", actualPort, *token, task.ID, actualPort+10)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	<-sigs

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx) //nolint:errcheck
	log.Printf("shut down")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// initTestRepo creates a git repo with one commit so agent.CreateWorktree
// (which shells out to `git worktree add`) succeeds against this dir.
func initTestRepo(dir string) error {
	run := func(args ...string) error {
		c := exec.Command("git", args...) //nolint:gosec // test-only harness
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w: %s", args, err, out)
		}
		return nil
	}
	if err := run("init", "-q"); err != nil {
		return err
	}
	if err := run("config", "user.email", "test@test.com"); err != nil {
		return err
	}
	if err := run("config", "user.name", "Test"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o600); err != nil {
		return err
	}
	if err := run("add", "."); err != nil {
		return err
	}
	return run("commit", "-q", "-m", "init")
}

func envOrInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		_, err := fmt.Sscanf(v, "%d", &n)
		if err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
