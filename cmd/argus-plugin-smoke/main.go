// argus-plugin-smoke is a black-box test harness for the argus plugin
// substrate. It exercises every plugin-facing HTTP surface (events SSE,
// task_meta, MCP tool registration, plugin views, settings sections, input
// injection) against a running argus daemon, asserts the contract holds, and
// cleans up after itself.
//
// Designed to run against the user's real local daemon. State mutations are
// scoped to a `smoke` token + a throwaway task that get torn down on exit.
//
// # Setup (one-time)
//
// Mint a scope token outside any sandbox (your normal shell):
//
//	argus token mint --scope smoke | awk '/^token:/ {print $2}' > ~/.argus/smoke-token
//	chmod 600 ~/.argus/smoke-token
//
// The harness reads the scope token from that file. It does not mint or
// revoke tokens itself — that path requires direct DB access which the
// argus sandbox-exec profile blocks. The token can be reused across runs.
//
// The harness ALSO reads the master token from ~/.argus/api-token (read-only,
// allowed by the sandbox). Master is used only for setup/teardown that's
// outside a plugin's normal surface (creating a sacrificial backend + task
// for the test target). Plugins never see master in production.
//
// # Usage
//
//	argus-plugin-smoke [-url http://127.0.0.1:7743] [-token-file ~/.argus/smoke-token]
//	                   [-master-token-file ~/.argus/api-token] [-project ARGUS]
//	                   [-scope smoke] [-verbose] [-keep]
//
// # Exit codes
//
//	0  every phase passed
//	1  a phase failed (details on stderr)
//	2  setup failed (couldn't read scope token, couldn't reach daemon, etc.)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

type smoke struct {
	baseURL     string
	scopeToken  string
	masterToken string
	scope       string
	project     string
	verbose     bool
	keep        bool
	httpClient  *http.Client

	// Resources allocated during run() and torn down on close(). The mu
	// guards taskID/backendName because cleanup may race a phase that
	// recorded a new resource.
	mu          sync.Mutex
	taskID      string // empty until phase 5 succeeds
	backendName string // empty until phase 5 succeeds

	// Phase results collected during run() for the end-of-run summary.
	results []phaseResult
}

type phaseResult struct {
	num    int
	name   string
	status string // "PASS" | "FAIL"
	detail string // error message on FAIL
}

func main() {
	urlFlag := flag.String("url", "http://127.0.0.1:7743", "argus API base URL")
	tokenFile := flag.String("token-file", filepath.Join(db.DataDir(), "smoke-token"), "path to a file containing the scope token plaintext")
	masterTokenFile := flag.String("master-token-file", filepath.Join(db.DataDir(), "api-token"), "path to the master token file (used only for setup/teardown)")
	projectFlag := flag.String("project", "ARGUS", "argus project name to host the throwaway task")
	scopeFlag := flag.String("scope", "smoke", "scope name the token was minted with (used for namespacing assertions)")
	verbose := flag.Bool("verbose", false, "verbose phase-by-phase logging")
	keep := flag.Bool("keep", false, "skip teardown (throwaway task + backend left in place for debugging)")
	flag.Parse()

	s, err := newSmoke(*urlFlag, *tokenFile, *masterTokenFile, *projectFlag, *scopeFlag, *verbose, *keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "setup: %v\n", err)
		os.Exit(2)
	}

	// os.Exit skips deferred functions, so cleanup MUST run before the call.
	// Order: run -> print summary -> cleanup -> exit. printSummary first so
	// the user sees results even if cleanup is slow.
	exit := 0
	if err := s.run(); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		exit = 1
	}
	s.printSummary()
	s.cleanup()
	os.Exit(exit)
}

func newSmoke(baseURL, tokenFile, masterTokenFile, project, scope string, verbose, keep bool) (*smoke, error) {
	scopeTok, err := readTokenFile(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("read scope token at %s: %w\n\nFirst-time setup:\n  argus token mint --scope %s | awk '/^token:/ {print $2}' > %s\n  chmod 600 %s",
			tokenFile, err, scope, tokenFile, tokenFile)
	}
	masterTok, err := readTokenFile(masterTokenFile)
	if err != nil {
		return nil, fmt.Errorf("read master token at %s: %w", masterTokenFile, err)
	}
	s := &smoke{
		baseURL:     strings.TrimRight(baseURL, "/"),
		scopeToken:  scopeTok,
		masterToken: masterTok,
		project:     project,
		scope:       scope,
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

// phases lists the substrate verification phases in execution order. The
// ordering is not the numeric ordering — later-numbered phases run earlier
// when they're cheaper or build infrastructure for later ones (Phase 5 must
// happen before Phase 4 because the meta tests need a task ID).
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
		{1, "auth check", s.phase1AuthCheck},
		{3, "event stream", s.phase3EventStream},
		{5, "throwaway task", s.phase5ThrowawayTask},
		{4, "task_meta", s.phase4TaskMeta},
		{7, "plugin views", s.phase7PluginViews},
		{6, "MCP tool registration", s.phase6MCPToolRegistration},
		{10, "event resync", s.phase10EventResync},
		{9, "input injection", s.phase9InputInjection},
		{8, "stream section", s.phase8StreamSection},
	}
}

// run executes every phase in order, stopping at the first failure (phases
// have dependencies — Phase 4's metadata writes need Phase 5's task ID). Per-
// phase results are recorded in s.results for printSummary to display at exit.
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

// printSummary writes a one-line-per-phase table to stdout. Phases listed in
// execution order (which is not numeric order — see phases()). PASS / FAIL
// only; the harness fails fast on first error, so at most one FAIL row.
func (s *smoke) printSummary() {
	fmt.Println()
	fmt.Println("Phase results:")
	fmt.Println("  #   PHASE                            STATUS")
	fmt.Println("  --  -------------------------------  ------")
	for _, r := range s.results {
		fmt.Printf("  %-2d  %-31s  %s\n", r.num, r.name, r.status)
		if r.detail != "" {
			fmt.Printf("      %s\n", truncate(r.detail, 200))
		}
	}
}

// cleanup tears down resources allocated during the run. Safe to call
// multiple times; idempotent per resource. Skipped when --keep is set.
func (s *smoke) cleanup() {
	if s.keep {
		s.mu.Lock()
		taskID := s.taskID
		backendName := s.backendName
		s.mu.Unlock()
		if taskID != "" {
			fmt.Printf("--keep: task id=%s and backend %q left in place\n", taskID, backendName)
		}
		return
	}
	s.mu.Lock()
	taskID := s.taskID
	backendName := s.backendName
	s.taskID = ""
	s.backendName = ""
	s.mu.Unlock()

	if taskID != "" {
		if err := s.adminDELETE("/api/tasks/"+taskID, http.StatusOK); err != nil {
			s.logf("cleanup: delete task %s failed: %v", taskID, err)
		} else {
			s.logf("cleanup: deleted task %s", taskID)
		}
	}
	if backendName != "" {
		if err := s.adminDELETE("/api/backends/"+backendName, http.StatusOK); err != nil {
			s.logf("cleanup: delete backend %q failed: %v", backendName, err)
		} else {
			s.logf("cleanup: deleted backend %q", backendName)
		}
	}
}

// phase1AuthCheck confirms the externally-minted scope token authenticates
// against a scope-aware endpoint. The token itself is treated as opaque —
// the harness never mints or revokes.
func (s *smoke) phase1AuthCheck() error {
	if err := s.assertGET("/api/plugins/views", s.scopeToken, http.StatusOK); err != nil {
		return fmt.Errorf("scope-token auth against GET /api/plugins/views: %w", err)
	}
	s.logf("scope=%s token authenticates against /api/plugins/views", s.scope)
	return nil
}

// phase3EventStream opens the SSE channel against the live daemon, confirms
// the handshake succeeds, and closes cleanly. No event assertions yet — later
// phases drive the daemon and then call sub.WaitFor on the resulting events.
func (s *smoke) phase3EventStream() error {
	sub, err := s.startEventStream(0)
	if err != nil {
		return fmt.Errorf("open SSE: %w", err)
	}
	defer sub.Close()
	s.logf("SSE handshake OK; ready to consume events")
	return nil
}

// phase4TaskMeta exercises /api/tasks/{id}/meta with the scope token:
//  1. PUT a key/value (no namespace in the body) and confirm the daemon
//     stamps it with the scope namespace.
//  2. GET back and verify the row round-trips.
//  3. PUT into a different namespace and confirm the daemon rejects with
//     403 — the scope guard is the only thing standing between two plugins
//     stomping each other's metadata.
func (s *smoke) phase4TaskMeta() error {
	if s.taskID == "" {
		return fmt.Errorf("no target task; Phase 5 must run first")
	}
	key := "phase4-key"
	value := fmt.Sprintf("phase4-value-%d", time.Now().UnixNano())

	// (1) Write via scope token (no namespace; daemon auto-derives).
	writeBody := fmt.Sprintf(`{"key":%q,"value":%q}`, key, value)
	resp, err := s.scopedRequest(http.MethodPut, "/api/tasks/"+s.taskID+"/meta", writeBody)
	if err != nil {
		return fmt.Errorf("PUT meta: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK, "PUT /meta"); err != nil {
		return err
	}
	s.logf("PUT meta key=%q wrote into auto-derived namespace", key)

	// (2) Read back; verify the entry matches.
	resp, err = s.scopedRequest(http.MethodGet, "/api/tasks/"+s.taskID+"/meta?namespace="+s.scope, "")
	if err != nil {
		return fmt.Errorf("GET meta: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET meta: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var decoded struct {
		Entries []struct {
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     string `json:"value"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode meta response: %w", err)
	}
	found := false
	for _, e := range decoded.Entries {
		if e.Namespace == s.scope && e.Key == key {
			if e.Value != value {
				return fmt.Errorf("meta round-trip: namespace=%s key=%s want %q got %q", e.Namespace, e.Key, value, e.Value)
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("meta round-trip: wrote %s.%s but GET did not return it (entries=%d)", s.scope, key, len(decoded.Entries))
	}
	s.logf("GET meta returned %d entries; round-trip OK for %s.%s", len(decoded.Entries), s.scope, key)

	// (3) Cross-namespace write must be rejected.
	hostileBody := fmt.Sprintf(`{"namespace":%q,"key":%q,"value":"x"}`, "not-"+s.scope, key)
	resp, err = s.scopedRequest(http.MethodPut, "/api/tasks/"+s.taskID+"/meta", hostileBody)
	if err != nil {
		return fmt.Errorf("PUT cross-ns: %w", err)
	}
	if err := expectStatus(resp, http.StatusForbidden, "PUT /meta with foreign namespace"); err != nil {
		return err
	}
	s.logf("cross-namespace PUT correctly rejected with 403")
	return nil
}

// phase8StreamSection exercises the settings-section registration surface
// with type:stream. The daemon stores the (scope, title, callback_url) row
// but doesn't proxy the WebSocket itself — the TUI connects directly to
// the plugin's callback URL on focus. So the harness asserts the daemon
// persists the right shape, makes it visible via the list endpoint, and
// honors cross-scope guards on unregister. The WS path itself is exercised
// by internal/tui/views/connector_test.go.
func (s *smoke) phase8StreamSection() error {
	plugin := newFakePlugin()
	defer plugin.Stop()
	title := fmt.Sprintf("smoke-stream-%d", time.Now().UnixNano())
	callback := plugin.WSURL() + "/stream/" + title

	regBody := fmt.Sprintf(`{"title":%q,"type":"stream","callback_url":%q}`, title, callback)
	resp, err := s.scopedRequest(http.MethodPost, "/api/plugins/settings/sections", regBody)
	if err != nil {
		return fmt.Errorf("POST stream section: %w", err)
	}
	if err := expectStatus(resp, http.StatusCreated, "POST stream section"); err != nil {
		return err
	}
	defer s.unregisterStreamSection(s.scope, title)
	s.logf("registered stream section title=%q callback=%s", title, callback)

	sections, err := s.listPluginSections()
	if err != nil {
		return fmt.Errorf("list sections: %w", err)
	}
	match := findSection(sections, s.scope, title)
	if match == nil {
		return fmt.Errorf("section %s/%s missing from list after register", s.scope, title)
	}
	if match.Type != "stream" {
		return fmt.Errorf("section type: got %q, want \"stream\"", match.Type)
	}
	if match.CallbackURL != callback {
		return fmt.Errorf("section callback: got %q, want %q", match.CallbackURL, callback)
	}
	s.logf("section appears in list with correct shape (type=%s)", match.Type)

	// Cross-scope unregister attempt: caller scope mismatches path scope.
	resp, err = s.scopedRequest(http.MethodDelete, "/api/plugins/settings/sections/not-"+s.scope+"/anything", "")
	if err != nil {
		return fmt.Errorf("cross-scope DELETE: %w", err)
	}
	if err := expectStatus(resp, http.StatusForbidden, "cross-scope DELETE section"); err != nil {
		return err
	}
	s.logf("cross-scope DELETE correctly rejected with 403")

	// Own-scope unregister succeeds.
	resp, err = s.scopedRequest(http.MethodDelete, "/api/plugins/settings/sections/"+s.scope+"/"+title, "")
	if err != nil {
		return fmt.Errorf("DELETE own section: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK, "DELETE own section"); err != nil {
		return err
	}
	s.logf("scope DELETE of own section succeeded")
	return nil
}

func (s *smoke) listPluginSections() ([]pluginSectionRow, error) {
	resp, err := s.scopedRequest(http.MethodGet, "/api/plugins/settings/sections", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET sections: status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var decoded struct {
		Sections []pluginSectionRow `json:"sections"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded.Sections, nil
}

type pluginSectionRow struct {
	Scope       string `json:"scope"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	CallbackURL string `json:"callback_url"`
}

func findSection(rows []pluginSectionRow, scope, title string) *pluginSectionRow {
	for i, r := range rows {
		if r.Scope == scope && r.Title == title {
			return &rows[i]
		}
	}
	return nil
}

func (s *smoke) unregisterStreamSection(scope, title string) {
	resp, err := s.scopedRequest(http.MethodDelete, "/api/plugins/settings/sections/"+scope+"/"+title, "")
	if err != nil {
		s.logf("cleanup: unregister section %s/%s: %v", scope, title, err)
		return
	}
	_ = resp.Body.Close()
}

// phase9InputInjection posts a marker line to the throwaway `cat` session's
// PTY and verifies the daemon delivered it. cat is a deterministic echo
// loop — what goes in comes out. The marker should appear in /output (and
// then appear AGAIN because terminal echo is on for the line-mode PTY, so
// we just look for "contains").
func (s *smoke) phase9InputInjection() error {
	if s.taskID == "" {
		return fmt.Errorf("no target task; Phase 5 must run first")
	}
	marker := fmt.Sprintf("PLUGIN-SMOKE-INPUT-%d", time.Now().UnixNano())
	input := marker + "\n"

	if err := s.postInput(s.taskID, input); err != nil {
		return fmt.Errorf("POST input: %w", err)
	}
	s.logf("posted %d bytes of input to task %s", len(input), s.taskID)

	// Bash needs a moment to execute. Poll up to 2 seconds for the marker
	// to surface in /output.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := s.fetchTaskOutput(s.taskID)
		if err != nil {
			return fmt.Errorf("fetch output: %w", err)
		}
		if strings.Contains(out, marker) {
			s.logf("input marker %q observed in task output", marker)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("input marker %q did not appear in task output within 2s", marker)
}

func (s *smoke) postInput(taskID, body string) error {
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/api/tasks/"+taskID+"/input", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.scopeToken)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /input: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	return nil
}

func (s *smoke) fetchTaskOutput(taskID string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/tasks/"+taskID+"/output?clean=1", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.scopeToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GET /output: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// phase10EventResync opens an SSE connection with since=1 and looks for a
// `resync` event. The substrate emits resync only when the caller's cursor
// is strictly older than the oldest retained event in the ring. On a daemon
// that has never overflowed its bounded ring (the common case in local
// development), OldestEventID == 1, no value of `since > 0` satisfies
// since < oldest, and resync never fires.
//
// So this phase has two valid outcomes:
//
//   - Resync emitted within a short window → behavior verified, log + pass.
//   - No resync within the window → daemon's oldest event id is ≤ 1, so
//     the test is not exercisable on this daemon. Log and pass.
//
// The harness never fails Phase 10 — it can't distinguish "resync broken"
// from "ring never overflowed" without a dedicated admin endpoint to inspect
// OldestEventID. The unit tests in events_test.go cover the SSE decoding;
// the handler's resync emission is exercised in internal/api/events_integration_test.go.
func (s *smoke) phase10EventResync() error {
	sub, err := s.startEventStream(1)
	if err != nil {
		return fmt.Errorf("open SSE with since=1: %w", err)
	}
	defer sub.Close()

	ev, err := sub.WaitFor(500*time.Millisecond, func(e model.Event) bool {
		return e.Type == model.EventTypeResync
	})
	if err != nil {
		s.logf("no resync event observed in 500ms (oldest event id likely ≤ 1; not exercisable here)")
		return nil
	}
	s.logf("daemon emitted resync (id=%d, payload=%s)", ev.ID, string(ev.Payload))
	return nil
}

// phase6MCPToolRegistration registers an MCP tool against the live registry,
// verifies the daemon force-prefixes by scope (rejects names that don't
// start with "<scope>_"), and exercises the un-register path. No invocation
// from an MCP client — that would require an MCP stdio client, which is
// out of scope for v1. The registry's proxy logic is exercised in unit
// tests inside internal/mcp.
func (s *smoke) phase6MCPToolRegistration() error {
	plugin := newFakePlugin()
	defer plugin.Stop()

	toolName := s.scope + "_hello"
	ok, err := s.registerMCPTool(toolName, plugin)
	if err != nil {
		return fmt.Errorf("register MCP tool: %w", err)
	}
	if !ok {
		return fmt.Errorf("daemon did not return 201 on registration")
	}
	defer s.unregisterMCPToolTolerant(toolName)
	s.logf("registered MCP tool %q (callback=%s)", toolName, plugin.URL())

	// (2) Wrong-prefix registration must be rejected. The substrate enforces
	// name = <scope>_<rest>; smuggling a tool under a different prefix would
	// let one plugin hijack another's namespace.
	hostileName := "not" + s.scope + "_hello"
	body := mcpRegBody(hostileName, plugin)
	resp, err := s.scopedRequest(http.MethodPost, "/api/mcp/tools", body)
	if err != nil {
		return fmt.Errorf("POST hostile tool: %w", err)
	}
	if err := expectStatus(resp, http.StatusBadRequest, "POST hostile-prefix tool"); err != nil {
		return err
	}
	s.logf("wrong-prefix MCP tool registration correctly rejected with 400")

	// (3) Unregister our own tool — must succeed.
	resp, err = s.scopedRequest(http.MethodDelete, "/api/mcp/tools/"+toolName, "")
	if err != nil {
		return fmt.Errorf("DELETE own tool: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK, "DELETE own MCP tool"); err != nil {
		return err
	}
	s.logf("unregistered MCP tool %q", toolName)

	// (4) Re-DELETE is idempotent: registry returns nil on missing-tool, the
	// handler returns 200. Pinning this so a future refactor that flips it
	// to 404 has to consciously update the harness — DELETE idempotency is
	// the standard HTTP convention.
	resp, err = s.scopedRequest(http.MethodDelete, "/api/mcp/tools/"+toolName, "")
	if err != nil {
		return fmt.Errorf("re-DELETE: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK, "re-DELETE of missing MCP tool"); err != nil {
		return err
	}
	s.logf("re-DELETE of missing tool returned 200 (idempotent, as expected)")
	return nil
}

func mcpRegBody(name string, plugin *fakePlugin) string {
	return fmt.Sprintf(`{"name":%q,"description":"smoke test tool","input_schema":{"type":"object","properties":{}},"callback_url":%q,"auth_header":%q}`,
		name, plugin.URL()+"/mcp/"+name, plugin.AuthHeader())
}

func (s *smoke) registerMCPTool(name string, plugin *fakePlugin) (bool, error) {
	body := mcpRegBody(name, plugin)
	resp, err := s.scopedRequest(http.MethodPost, "/api/mcp/tools", body)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	return true, nil
}

func (s *smoke) unregisterMCPToolTolerant(name string) {
	resp, err := s.scopedRequest(http.MethodDelete, "/api/mcp/tools/"+name, "")
	if err != nil {
		s.logf("cleanup: unregister MCP tool %q: %v", name, err)
		return
	}
	_ = resp.Body.Close()
}

// phase7PluginViews exercises the plugin-views CRUD surface end-to-end:
// register-as-scope, list-from-scope (filtered), list-from-master (all),
// delete-as-master, register-master-view, cross-scope delete must 403,
// list-from-scope post-cleanup is empty. Each step is one assertion on the
// real /api/plugins/views handlers.
func (s *smoke) phase7PluginViews() error {
	suffix := time.Now().UnixNano()
	smokeTitle := fmt.Sprintf("smoke-view-%d", suffix)
	masterTitle := fmt.Sprintf("master-view-%d", suffix)

	// (1) Register a view under the scope token.
	smokeID, err := s.registerScopeView(smokeTitle)
	if err != nil {
		return fmt.Errorf("register smoke view: %w", err)
	}
	defer s.adminDelete("/api/plugins/views/"+i64s(smokeID), "smoke view cleanup")
	s.logf("registered scope view id=%d title=%q", smokeID, smokeTitle)

	// (2) Scope-token GET returns only our row.
	scoped, err := s.listPluginViews(s.scopeToken)
	if err != nil {
		return err
	}
	for _, v := range scoped {
		if v.Scope != s.scope {
			return fmt.Errorf("scope GET leaked foreign-scope row: scope=%q title=%q", v.Scope, v.Title)
		}
	}
	if !containsView(scoped, smokeID) {
		return fmt.Errorf("scope GET did not include our just-registered view id=%d", smokeID)
	}
	s.logf("scope GET returned %d rows (all scope=%s)", len(scoped), s.scope)

	// (3) Master GET sees our row too (and possibly others; we only
	// assert ours is present).
	allViews, err := s.listPluginViews(s.masterToken)
	if err != nil {
		return err
	}
	if !containsView(allViews, smokeID) {
		return fmt.Errorf("master GET did not include our view id=%d", smokeID)
	}
	s.logf("master GET returned %d rows (ours included)", len(allViews))

	// (4) Register a master-scope view (scope="") so we can verify the
	// cross-scope delete guard.
	masterID, err := s.registerMasterView(masterTitle)
	if err != nil {
		return fmt.Errorf("register master view: %w", err)
	}
	defer s.adminDelete("/api/plugins/views/"+i64s(masterID), "master view cleanup")
	s.logf("registered master view id=%d title=%q", masterID, masterTitle)

	// (5) Scope token attempting to delete the master view must 403.
	resp, err := s.scopedRequest(http.MethodDelete, "/api/plugins/views/"+i64s(masterID), "")
	if err != nil {
		return fmt.Errorf("scoped DELETE on master view: %w", err)
	}
	if err := expectStatus(resp, http.StatusForbidden, "scoped DELETE of master view"); err != nil {
		return err
	}
	s.logf("cross-scope DELETE correctly rejected with 403")

	// (6) Scope token can delete its own view.
	resp, err = s.scopedRequest(http.MethodDelete, "/api/plugins/views/"+i64s(smokeID), "")
	if err != nil {
		return fmt.Errorf("scoped DELETE on own view: %w", err)
	}
	if err := expectStatus(resp, http.StatusOK, "scoped DELETE of own view"); err != nil {
		return err
	}
	s.logf("scope DELETE of own view succeeded")

	// (7) Confirm scope GET no longer sees the row.
	after, err := s.listPluginViews(s.scopeToken)
	if err != nil {
		return err
	}
	if containsView(after, smokeID) {
		return fmt.Errorf("smoke view id=%d still present after DELETE", smokeID)
	}
	return nil
}

func (s *smoke) registerScopeView(title string) (int64, error) {
	body := fmt.Sprintf(`{"title":%q,"callback_url":"http://127.0.0.1:0/unused"}`, title)
	resp, err := s.scopedRequest(http.MethodPost, "/api/plugins/views", body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("POST scope view: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, err
	}
	return v.ID, nil
}

func (s *smoke) registerMasterView(title string) (int64, error) {
	body := fmt.Sprintf(`{"title":%q,"callback_url":"http://127.0.0.1:0/unused"}`, title)
	resp, err := s.adminPOST("/api/plugins/views", body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("POST master view: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	var v struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return 0, err
	}
	return v.ID, nil
}

type viewRow struct {
	ID    int64  `json:"id"`
	Scope string `json:"scope"`
	Title string `json:"title"`
}

func (s *smoke) listPluginViews(token string) ([]viewRow, error) {
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/plugins/views", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET /api/plugins/views: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	var decoded struct {
		Views []viewRow `json:"views"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded.Views, nil
}

func containsView(rows []viewRow, id int64) bool {
	for _, v := range rows {
		if v.ID == id {
			return true
		}
	}
	return false
}

// adminDelete is a defer-friendly cleanup that tolerates 404 (resource
// already gone is success during teardown) and logs other failures via the
// verbose channel. Used for best-effort cleanup of resources whose creation
// can't be undone with a defer in the registering caller's stack frame
// (deferred functions can't return errors usefully).
func (s *smoke) adminDelete(path, label string) {
	req, err := http.NewRequest(http.MethodDelete, s.baseURL+path, nil)
	if err != nil {
		s.logf("cleanup: %s: %v", label, err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.masterToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logf("cleanup: %s: %v", label, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	s.logf("cleanup: %s failed: status %d: %s", label, resp.StatusCode, truncate(string(body), 200))
}

func i64s(n int64) string {
	return strconv.FormatInt(n, 10)
}

func expectStatus(resp *http.Response, want int, label string) error {
	defer resp.Body.Close()
	if resp.StatusCode == want {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("%s: status %d (want %d): %s", label, resp.StatusCode, want, truncate(string(body), 200))
}

// phase5ThrowawayTask creates a transient `cat`-backed backend ("smoke-cat")
// and a task on the configured project. Both are torn down in cleanup().
// The cat session is a deterministic echo loop — reads stdin, writes to
// stdout, exits on EOF. That keeps the PTY alive long enough for Phase 9
// (input injection) to round-trip a marker through it.
//
// If the backend already exists (e.g., from a prior --keep run), the harness
// reuses it but does NOT claim ownership for cleanup — it cannot tell
// whether the existing row was someone else's or a leftover from us.
func (s *smoke) phase5ThrowawayTask() error {
	const backendName = "smoke-cat"
	owned, err := s.ensureBashBackend(backendName)
	if err != nil {
		return fmt.Errorf("ensure backend %q: %w", backendName, err)
	}
	if owned {
		s.mu.Lock()
		s.backendName = backendName
		s.mu.Unlock()
		s.logf("registered backend %q (cat)", backendName)
	} else {
		s.logf("reusing pre-existing backend %q (cleanup skipped)", backendName)
	}

	taskName := fmt.Sprintf("argus-plugin-smoke-%d", time.Now().Unix())
	taskID, err := s.createBashTask(taskName, backendName)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	s.mu.Lock()
	s.taskID = taskID
	s.mu.Unlock()
	s.logf("created task id=%s name=%q project=%q backend=%q", taskID, taskName, s.project, backendName)
	return nil
}

// ensureBashBackend POSTs the backend definition. Returns owned=true when we
// just created it (cleanup later), owned=false when it already existed
// (cleanup skipped). Treats any 2xx as success.
//
// The command is `sh -c cat`, not just `cat`. Argus's BuildCmd
// unconditionally appends `--session-id <UUID>` to every non-Codex/non-Pi
// backend invocation — that's Claude's CLI flag — which makes any other
// backend (cat, sleep, custom scripts) error out on the unexpected args.
// Wrapping with `sh -c` absorbs the trailing args into $0/$1 where they're
// discarded, so the inner `cat` runs cleanly.
//
// Surfaced substrate gap: per-backend session-id support should be opt-in
// (the default should be off), or backends should declare what arg shape
// they accept. Documenting via this comment + the harness workaround.
func (s *smoke) ensureBashBackend(name string) (bool, error) {
	body := fmt.Sprintf(`{"name":%q,"command":"sh -c cat"}`, name)
	resp, err := s.adminPOST("/api/backends", body)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusCreated:
		return true, nil
	case resp.StatusCode == http.StatusConflict:
		return false, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, nil
	default:
		out, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("POST /api/backends: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
}

func (s *smoke) createBashTask(name, backend string) (string, error) {
	body := fmt.Sprintf(`{"name":%q,"project":%q,"backend":%q}`, name, s.project, backend)
	resp, err := s.adminPOST("/api/tasks", body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		out, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /api/tasks: status %d: %s", resp.StatusCode, truncate(string(out), 200))
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode task response: %w", err)
	}
	if decoded.ID == "" {
		return "", fmt.Errorf("daemon returned empty task id")
	}
	return decoded.ID, nil
}

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

// adminPOST sends a JSON POST authenticated with the master token. Used by
// phase 5 (backend + task creation) and cleanup. Caller owns the response
// body and must Close() it.
func (s *smoke) adminPOST(path, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, s.baseURL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.masterToken)
	req.Header.Set("Content-Type", "application/json")
	return s.httpClient.Do(req)
}

// scopedRequest builds a request with the scope token attached. body may be
// empty. Caller owns the returned response body.
func (s *smoke) scopedRequest(method, path, body string) (*http.Response, error) {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.scopeToken)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return s.httpClient.Do(req)
}

// adminDELETE sends a DELETE authenticated with the master token. Used by
// cleanup. Expects `want` status; returns an error otherwise.
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

func (s *smoke) logf(format string, args ...any) {
	if s.verbose {
		fmt.Printf("[smoke] "+format+"\n", args...)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
