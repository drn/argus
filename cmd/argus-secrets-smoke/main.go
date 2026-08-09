// argus-secrets-smoke is a black-box test harness that closes the one
// verification gap left by add-secrets-resolver-registry (PR #932): every
// layer of the scheme-prefixed secret resolver registry
// (internal/agent/secretregistry.go) was unit-tested extensively — including
// real, unmocked `security` subprocess calls in some tests — and reviewed
// twice, but the actual end-to-end path was never verified: a REAL agent,
// spawned by the REAL running daemon, actually receiving a keychain://-
// resolved credential in its own live process environment.
//
// This harness proves that path both ways: a backend whose env_vars mapping
// carries one keychain:// source known to resolve (the same Keychain item
// this machine's [secrets.op].bootstrap_source already uses in production)
// and one that is deliberately bogus. It spawns a real, short-lived,
// non-interactive task under that backend and asserts, from the task's own
// captured output, that the good source landed in the child's environment
// and the bad source did not — without the process crashing, hanging, or
// otherwise misbehaving.
//
// Modeled directly on cmd/argus-plugin-smoke: same token setup, same
// phase/PASS-FAIL summary, same exit-code contract, same teardown discipline.
//
// # Setup (one-time)
//
// Reuses argus-plugin-smoke's existing token files verbatim — no new tokens
// are minted:
//
//	~/.argus/smoke-token  — a scope token (see argus-plugin-smoke's own doc
//	                         comment for how to mint one)
//	~/.argus/api-token    — the master token, used only for setup/teardown
//	                         (creating and deleting the sacrificial backend
//	                         and task)
//
// # A note on how the sacrificial backend gets its env_vars
//
// The REST backend CRUD surface (POST/PUT /api/backends) has NO env_vars
// field anywhere in its wire contract: backendJSON (internal/api/handlers.go)
// doesn't declare one, and handleCreateBackend/handleUpdateBackend construct
// the persisted config.Backend without it even if a caller's JSON body
// includes the key. This is a real, pre-existing gap in the REST surface —
// it predates PR #932 (which only *consumes* backend.EnvVars, never touches
// the CRUD handlers) — but it means a black-box REST client has NO way to
// configure a backend's credential mapping at all today. The only existing
// ways are the TUI Settings screen (which writes the full config.Backend,
// EnvVars included, straight to the DB) or hand-editing config.toml.
//
// This harness follows the TUI's own path: after creating the backend's base
// definition over REST (exactly like argus-plugin-smoke's ensureBashBackend),
// it opens ~/.argus/data.sql directly and calls the same db.SetBackend the
// Settings screen calls, to attach the env_vars mapping. This mirrors
// cmd/argus/tokens.go's own precedent ("the CLI talks to the SQLite database
// directly (WAL mode allows a writer while the daemon holds reads/writes)"),
// not a new pattern invented for this harness.
//
// # Usage
//
//	argus-secrets-smoke [-url http://127.0.0.1:7743] [-token-file ~/.argus/smoke-token]
//	                     [-master-token-file ~/.argus/api-token] [-db-path ~/.argus/data.sql]
//	                     [-project ARGUS] [-verbose] [-keep]
//
// # Exit codes
//
//	0  every phase passed
//	1  a phase failed (details on stderr)
//	2  setup failed (couldn't read a token, couldn't reach the daemon, etc.)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
)

// smoke holds everything a phase needs: connection details, both tokens, and
// the resources allocated during run() that cleanup() must tear down.
type smoke struct {
	baseURL     string
	scopeToken  string
	masterToken string
	dbPath      string
	project     string
	verbose     bool
	keep        bool
	httpClient  *http.Client

	// Resources + captured output allocated during run() and torn down in
	// cleanup(). mu guards them because cleanup may race a phase that just
	// recorded a new resource (mirrors argus-plugin-smoke's smoke struct).
	mu             sync.Mutex
	taskID         string // empty until the throwaway-task phase succeeds
	backendOwned   bool   // true only if THIS run created the backend row
	capturedOutput string // the throwaway task's captured stdout, cached once

	results []phaseResult
}

type phaseResult struct {
	num    int
	name   string
	status string // "PASS" | "FAIL"
	detail string // error message on FAIL
}

const secretsSmokeBackendName = "secrets-smoke"

func main() {
	urlFlag := flag.String("url", "http://127.0.0.1:7743", "argus API base URL")
	tokenFile := flag.String("token-file", filepath.Join(db.DataDir(), "smoke-token"), "path to a file containing the scope token plaintext")
	masterTokenFile := flag.String("master-token-file", filepath.Join(db.DataDir(), "api-token"), "path to the master token file (used only for setup/teardown)")
	dbPathFlag := flag.String("db-path", db.DefaultPath(), "path to the argus SQLite DB (used only to attach the sacrificial backend's env_vars — see package doc)")
	projectFlag := flag.String("project", "ARGUS", "argus project name to host the throwaway task")
	verbose := flag.Bool("verbose", false, "verbose phase-by-phase logging")
	keep := flag.Bool("keep", false, "skip teardown (throwaway task + backend left in place for debugging)")
	flag.Parse()

	s, err := newSmoke(*urlFlag, *tokenFile, *masterTokenFile, *dbPathFlag, *projectFlag, *verbose, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(2)
	}

	// os.Exit skips deferred functions, so cleanup MUST run before the call.
	// Order: run -> print summary -> cleanup -> exit, so results are visible
	// even if cleanup is slow (mirrors argus-plugin-smoke).
	exit := 0
	if err := s.run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		exit = 1
	}
	s.printSummary()
	s.cleanup()
	os.Exit(exit)
}

func newSmoke(baseURL, tokenFile, masterTokenFile, dbPath, project string, verbose, keep bool) (*smoke, error) {
	scopeTok, err := readTokenFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("read scope token at %s: %w", tokenFile, err)
	}
	masterTok, err := readTokenFile(masterTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read master token at %s: %w", masterTokenFile, err)
	}
	s := &smoke{
		baseURL:     strings.TrimRight(baseURL, "/"),
		scopeToken:  scopeTok,
		masterToken: masterTok,
		dbPath:      dbPath,
		project:     project,
		verbose:     verbose,
		keep:        keep,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
	if err := s.assertGET("/api/status", scopeTok, http.StatusOK); err != nil {
		return nil, fmt.Errorf("daemon reachability check at %s: %w", baseURL, err)
	}
	return s, nil
}

func readTokenFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(raw))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}

// phases lists the verification phases in execution order. Unlike
// argus-plugin-smoke, these have a strictly linear dependency chain (each
// needs the previous one's resource), so execution order equals numeric
// order here.
func (s *smoke) phases() []struct {
	num  int
	name string
	fn   func() error
} {
	return []struct {
		num  int
		name string
		fn   func() error
	}{
		{1, "master-token auth check", s.phase1AuthCheck},
		{2, "sacrificial backend created", s.phase2EnsureBackend},
		{3, "throwaway task spawned and completed", s.phase3AwaitCompletion},
		{4, "live keychain success path (SMOKE_KEYCHAIN_OK=yes)", s.phase4SuccessPath},
		{5, "live keychain fail-open path (SMOKE_KEYCHAIN_BAD=no)", s.phase5FailOpenPath},
	}
}

// run executes every phase in order, stopping at the first failure — later
// phases all depend on an earlier one's resource (backend needs nothing, the
// task needs the backend, the two assertions need the task's captured
// output), so there is no value in continuing past a failure.
func (s *smoke) run() error {
	for _, p := range s.phases() {
		err := p.fn()
		res := phaseResult{num: p.num, name: p.name}
		if err != nil {
			res.status = "FAIL"
			res.detail = err.Error()
			s.results = append(s.results, res)
			return fmt.Errorf("phase %d (%s): %w", p.num, p.name, err)
		}
		res.status = "PASS"
		s.results = append(s.results, res)
	}
	return nil
}

// printSummary writes a one-line-per-phase table to stdout (mirrors
// argus-plugin-smoke's printSummary).
func (s *smoke) printSummary() {
	fmt.Println()
	fmt.Println("Phase results:")
	fmt.Println("  #   PHASE                                              STATUS")
	fmt.Println("  --  -------------------------------------------------  ------")
	for _, r := range s.results {
		fmt.Printf("  %-2d  %-49s  %s\n", r.num, r.name, r.status)
		if r.detail != "" {
			fmt.Printf("      %s\n", truncate(r.detail, 2000))
		}
	}
}

// cleanup tears down resources allocated during the run: the throwaway task,
// then the sacrificial backend (only if this run created it). Safe to call
// multiple times; skipped entirely when --keep is set. Deletion uses the
// REST DELETE endpoints — no env_vars value is ever involved in teardown, so
// the REST-vs-DB asymmetry that setup needed doesn't apply here.
func (s *smoke) cleanup() {
	if s.keep {
		s.mu.Lock()
		taskID := s.taskID
		owned := s.backendOwned
		s.mu.Unlock()
		if taskID != "" {
			fmt.Printf("--keep: task id=%s left in place\n", taskID)
		}
		if owned {
			fmt.Printf("--keep: backend %q left in place\n", secretsSmokeBackendName)
		}
		return
	}
	s.mu.Lock()
	taskID := s.taskID
	owned := s.backendOwned
	s.taskID = ""
	s.backendOwned = false
	s.mu.Unlock()

	if taskID != "" {
		if err := s.adminDELETE("/api/tasks/"+taskID, http.StatusOK); err != nil {
			s.logf("cleanup: delete task %s failed: %v", taskID, err)
		} else {
			s.logf("cleanup: deleted task %s", taskID)
		}
	}
	if owned {
		if err := s.adminDELETE("/api/backends/"+secretsSmokeBackendName, http.StatusOK); err != nil {
			s.logf("cleanup: delete backend %q failed: %v", secretsSmokeBackendName, err)
		} else {
			s.logf("cleanup: deleted backend %q", secretsSmokeBackendName)
		}
	}
}

// phase1AuthCheck confirms the master token (used exclusively for
// setup/teardown from here on) authenticates against the daemon. The scope
// token was already proven live in newSmoke's constructor check.
func (s *smoke) phase1AuthCheck() error {
	if err := s.assertGET("/api/status", s.masterToken, http.StatusOK); err != nil {
		return fmt.Errorf("master-token auth against GET /api/status: %w", err)
	}
	s.logf("master token authenticates against /api/status")
	return nil
}

// phase2EnsureBackend creates the sacrificial backend's base definition over
// REST, then attaches its env_vars credential mapping via a direct DB write
// (see the package doc + attachSecretsEnvVars for why REST alone can't do
// this). backendOwned is set so cleanup deletes it — even when the REST leg
// hit a 409 (pre-existing row from a prior --keep run), this run still
// (re)attached the mapping, so it takes ownership for cleanup rather than
// leaving a stale probe backend behind indefinitely.
func (s *smoke) phase2EnsureBackend() error {
	knownGood, err := resolveKnownGoodSource(s.dbPath)
	if err != nil {
		return err
	}
	s.logf("known-good source resolved from this daemon's own [secrets.op].bootstrap_source config")

	created, err := s.ensureSecretsBackendREST(secretsSmokeBackendName, secretsSmokeCommand)
	if err != nil {
		return fmt.Errorf("create backend base definition over REST: %w", err)
	}
	if created {
		s.logf("created backend %q via POST /api/backends", secretsSmokeBackendName)
	} else {
		s.logf("backend %q already existed (409); reusing and reattaching env_vars", secretsSmokeBackendName)
	}

	envVars := map[string]string{
		"SMOKE_KEYCHAIN_OK":  knownGood,
		"SMOKE_KEYCHAIN_BAD": bogusKeychainSource,
	}
	if err := attachSecretsEnvVars(s.dbPath, secretsSmokeBackendName, secretsSmokeCommand, envVars); err != nil {
		return fmt.Errorf("attach env_vars mapping via direct DB write: %w", err)
	}
	s.logf("attached env_vars mapping (2 entries) to backend %q via direct DB write", secretsSmokeBackendName)

	s.mu.Lock()
	s.backendOwned = true
	s.mu.Unlock()
	return nil
}

// phase3AwaitCompletion spawns the throwaway task, waits for the one-liner
// to run to completion on its own, and caches its captured output for
// phases 4 and 5 to assert against (no further HTTP calls needed there).
func (s *smoke) phase3AwaitCompletion() error {
	taskName := fmt.Sprintf("argus-secrets-smoke-%d", time.Now().Unix())
	taskID, err := s.createSmokeTask(taskName, secretsSmokeBackendName)
	if err != nil {
		return fmt.Errorf("create throwaway task: %w", err)
	}
	s.mu.Lock()
	s.taskID = taskID
	s.mu.Unlock()
	s.logf("created task id=%s name=%q project=%q backend=%q", taskID, taskName, s.project, secretsSmokeBackendName)

	status, err := s.awaitTaskDone(taskID, 10*time.Second)
	if err != nil {
		return err
	}
	if status != "complete" {
		out, _ := s.fetchOutput(taskID)
		return fmt.Errorf("task %s ended in status %q (want \"complete\" — a clean exit); captured output: %s",
			taskID, status, truncate(out, 2000))
	}
	s.logf("task %s completed cleanly (status=complete)", taskID)

	out, err := s.fetchOutputUntilMarkers(taskID, 2*time.Second)
	if err != nil {
		return fmt.Errorf("fetch captured output: %w", err)
	}
	s.mu.Lock()
	s.capturedOutput = out
	s.mu.Unlock()
	s.logf("captured output (%d bytes) cached for assertion phases", len(out))
	return nil
}

// phase4SuccessPath proves the real, live success path: a keychain:// source
// that resolves lands its target var set and non-empty in the spawned
// agent's own environment.
func (s *smoke) phase4SuccessPath() error {
	s.mu.Lock()
	out := s.capturedOutput
	s.mu.Unlock()
	return assertOutputContains(out, "SMOKE_KEYCHAIN_OK=yes", "live keychain success path")
}

// phase5FailOpenPath proves the real, live fail-open path: a keychain://
// source that does NOT resolve leaves its target var unset — and, just as
// importantly, the task still completed and produced output at all (phase 3
// already required status=="complete"), so it neither crashed nor hung.
func (s *smoke) phase5FailOpenPath() error {
	s.mu.Lock()
	out := s.capturedOutput
	s.mu.Unlock()
	return assertOutputContains(out, "SMOKE_KEYCHAIN_BAD=no", "live keychain fail-open path")
}

// --- small HTTP helpers (mirror argus-plugin-smoke's own) ---

func (s *smoke) assertGET(path, token string, want int) error {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: status %d (want %d): %s", path, resp.StatusCode, want, truncate(string(body), 200))
	}
	return nil
}

// adminPOST sends a JSON POST authenticated with the master token. Caller
// owns the response body and must Close() it.
func (s *smoke) adminPOST(path, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, s.baseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.masterToken)
	req.Header.Set("Content-Type", "application/json")
	return s.httpClient.Do(req)
}

// adminDELETE sends a DELETE authenticated with the master token and expects
// `want` status.
func (s *smoke) adminDELETE(path string, want int) error {
	req, err := http.NewRequest(http.MethodDelete, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.masterToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE %s: status %d (want %d): %s", path, resp.StatusCode, want, truncate(string(body), 200))
	}
	return nil
}

// scopedGET issues a GET authenticated with the scope token — the token a
// real plugin would use for everything except setup/teardown. Caller owns
// the response body and must Close() it.
func (s *smoke) scopedGET(path string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.scopeToken)
	return s.httpClient.Do(req)
}

func (s *smoke) logf(format string, args ...any) {
	if s.verbose {
		fmt.Printf("[secrets-smoke] "+format+"\n", args...)
	}
}

func truncate(str string, n int) string {
	if len(str) <= n {
		return str
	}
	return str[:n] + "..."
}

// decodeJSON is a small shared helper so each REST call site doesn't repeat
// the same status-check-then-decode dance.
func decodeJSON(resp *http.Response, want int, label string, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: status %d (want %d): %s", label, resp.StatusCode, want, truncate(string(body), 200))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
