package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/db"
)

// --- `argus coord-hook`: the context-budget Stop hook ---
//
// Registered as a Claude Code `Stop` hook in the user's GLOBAL
// ~/.claude/settings.json (see runCoordHookCommand's doc comment and the
// README's "Context-budget Stop hook" section for the exact snippet — this
// is a one-time manual step Argus cannot perform itself, since it must not
// write to a user's global Claude Code settings on their behalf).
//
// Because the registration is global, this fires on EVERY Claude Code
// session on the machine, including sessions with no relation to Argus. It
// self-gates hard: the very first check is ARGUS_TASK_ID (only set for
// Argus-spawned sessions — internal/agent/agent.go) plus the bound role
// being a hera coordinator; anything else is a silent no-op.
//
// See openspec/changes/add-coordinator-context-management/design.md
// Decision 1 and specs/coordinator-context-management/spec.md.

// coordHookEnv is the dependency-injection seam runCoordHook drives. Real
// implementations (below) reach the daemon's REST API and the transcript
// file; tests inject in-memory fakes so the gating/nudge logic is unit
// testable without a live daemon (context/knowledge/testing.md).
type coordHookEnv struct {
	Getenv           func(key string) string
	ResolveRoleKind  func(taskID string) (string, error)
	ReadContextSize  func(transcriptPath string) (int, error)
	StampContextSize func(taskID string, size int) error
	Budget           func(taskID string) (int, error)
}

// stopHookInput is the subset of Claude Code's Stop hook stdin payload this
// subcommand consumes. Claude Code does not hand a Stop hook inline usage
// data — only the transcript path — so reading it is the only way to learn
// the session's current context size.
type stopHookInput struct {
	TranscriptPath string `json:"transcript_path"`
}

// stopHookDecision is Claude Code's Stop-hook JSON output contract: a
// "block" decision injects Reason as the next turn's context instead of
// letting the session stop. Omitted (never written) when the coordinator is
// under budget — Claude Code treats no stdout JSON as "allow stop".
type stopHookDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// runCoordHook is the pure, testable core of the coord-hook subcommand. It
// gates on ARGUS_TASK_ID + a resolved coordinator role BEFORE touching the
// transcript or the REST API (no-op with zero side effects otherwise),
// unconditionally stamps task_meta(hera, context_size) for a coordinator
// session regardless of budget, and writes a Stop-hook block decision to out
// only when at/over budget. Re-evaluates fresh on every invocation — nothing
// is remembered between processes, so the nudge recurs every turn the
// condition holds and stops the very turn it doesn't.
func runCoordHook(stdin io.Reader, out, errOut io.Writer, env coordHookEnv) {
	taskID := env.Getenv("ARGUS_TASK_ID")
	if taskID == "" {
		return
	}

	kind, err := env.ResolveRoleKind(taskID)
	if err != nil {
		fmt.Fprintf(errOut, "coord-hook: resolve role kind: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
		return
	}
	if kind != string(db.HeraKindCoordinator) {
		return
	}

	var in stopHookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		fmt.Fprintf(errOut, "coord-hook: decode stdin: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
		return
	}

	size, err := env.ReadContextSize(in.TranscriptPath)
	if err != nil {
		fmt.Fprintf(errOut, "coord-hook: read context size: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
		return
	}

	// Always stamp, regardless of budget — this is the live signal a human
	// or the recycle mechanism reads, with zero dependency on the
	// coordinator's cooperation.
	if err := env.StampContextSize(taskID, size); err != nil {
		fmt.Fprintf(errOut, "coord-hook: stamp context size: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
	}

	budget, err := env.Budget(taskID)
	if err != nil {
		fmt.Fprintf(errOut, "coord-hook: read budget: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
		return
	}
	if size < budget {
		return
	}

	dec := stopHookDecision{
		Decision: "block",
		Reason: fmt.Sprintf(
			"Context budget reached (%d/%d cache-read tokens). Reach a safe seam: bring design.md's open questions current, write a handoff_note, then call hera_status(request_recycle=true) to recycle.",
			size, budget,
		),
	}
	if err := json.NewEncoder(out).Encode(dec); err != nil {
		fmt.Fprintf(errOut, "coord-hook: encode decision: %v\n", err) //nolint:errcheck // best-effort diagnostic write; the hook returns either way
	}
}

// runCoordHookCommand wires runCoordHook to the real world: os.Stdin/Stdout/
// Stderr and realCoordHookEnv. Invoked from main.go's `coord-hook` dispatch.
//
// One-time manual setup required (Decision 1 — global, not project-scoped,
// because every Argus-spawned agent inherits the daemon's real HOME
// regardless of project): add to ~/.claude/settings.json —
//
//	{
//	  "hooks": {
//	    "Stop": [
//	      { "hooks": [ { "type": "command", "command": "argus coord-hook" } ] }
//	    ]
//	  }
//	}
//
// See the README's "Context-budget Stop hook" section for the full writeup.
func runCoordHookCommand() {
	runCoordHook(os.Stdin, os.Stdout, os.Stderr, realCoordHookEnv())
}

// realCoordHookEnv builds the production coordHookEnv: role kind and budget
// are read from the daemon's REST API (self-discovered port + master token,
// since no ARGUS_API_PORT/ARGUS_API_TOKEN is exported into the spawned
// session's environment), context size comes from tailing the transcript
// JSONL named in the hook's stdin.
func realCoordHookEnv() coordHookEnv {
	return coordHookEnv{
		Getenv:           os.Getenv,
		ResolveRoleKind:  resolveRoleKindReal,
		ReadContextSize:  readContextSizeReal,
		StampContextSize: stampContextSizeReal,
		Budget:           budgetReal,
	}
}

// coordHookHTTPClient caps every REST call this hook makes. It fires on
// every Stop event of every Claude Code session on the machine (Decision 1's
// self-gating gets non-coordinator sessions out cheaply before any network
// call, but a coordinator session should never hang its own terminal
// waiting on a wedged daemon).
var coordHookHTTPClient = &http.Client{Timeout: 5 * time.Second}

// coordHookDial connects to the daemon's Unix socket and returns a JSON-RPC
// client, mirroring the raw dial pattern main.go already uses for
// stopDaemon/runSupervisorStatus (rather than pulling in the heavier
// persistent-connection internal/daemon/client.Client for a one-shot call).
func coordHookDial() (*rpc.Client, error) {
	conn, err := net.DialTimeout("unix", daemon.DefaultSocketPath(), 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	if _, err := conn.Write([]byte("R")); err != nil {
		conn.Close() //nolint:errcheck // best-effort cleanup on the failure path
		return nil, fmt.Errorf("write daemon request byte: %w", err)
	}
	return jsonrpc.NewClient(conn), nil
}

// discoverAPIPort asks the running daemon for its live REST API port over
// the Unix socket (internal/daemon/rpc.go's Daemon.Ports) — neither the API
// nor the MCP port is stable across restarts (bindWithRetry), so hardcoding
// 7743 would silently break the moment a daemon starts on a busy port.
func discoverAPIPort() (int, error) {
	client, err := coordHookDial()
	if err != nil {
		return 0, err
	}
	defer client.Close() //nolint:errcheck // short-lived CLI client; close error is non-actionable

	var resp daemon.PortsResp
	if err := client.Call("Daemon.Ports", &daemon.Empty{}, &resp); err != nil {
		return 0, fmt.Errorf("Daemon.Ports: %w", err)
	}
	if resp.APIPort == 0 {
		return 0, fmt.Errorf("daemon REST API is not running (api.enabled=false?)")
	}
	return resp.APIPort, nil
}

// discoverAPIToken reads the daemon's master token file. Same well-known
// path the daemon itself loads/creates at boot (internal/daemon/daemon.go).
func discoverAPIToken() (string, error) {
	data, err := os.ReadFile(filepath.Join(db.DataDir(), "api-token"))
	if err != nil {
		return "", fmt.Errorf("read api token: %w", err)
	}
	return string(bytes.TrimSpace(data)), nil
}

// coordHookBaseURL builds the loopback base URL for a self-discovered port.
// Always 127.0.0.1 — this CLI runs on the same host as the daemon it's
// reporting to (it's a Stop hook for an Argus-spawned agent's own session).
func coordHookBaseURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// coordHookRequest performs an authenticated REST call against the daemon
// and returns the response body, folding the port/token discovery and
// status-code check every coord-hook REST call needs.
func coordHookRequest(method, path string, body io.Reader) ([]byte, error) {
	port, err := discoverAPIPort()
	if err != nil {
		return nil, err
	}
	token, err := discoverAPIToken()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(method, coordHookBaseURL(port)+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := coordHookHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-to-completion below; close error is non-actionable

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s %s: unexpected status %d", method, path, resp.StatusCode)
	}
	return respBody, nil
}

// resolveRoleKindReal reads task_meta(hera, role) for the given task — the
// role layer's own mirror of a bound role's kind (db.HeraMetaKeyRole; see
// internal/db/hera.go), kept fresh independently of the hera_* tables so
// this hook never needs direct DB access. An unbound task (no "role" entry)
// resolves to "" — the caller's kind != "coordinator" check treats that as
// the same no-op as any other non-coordinator kind.
func resolveRoleKindReal(taskID string) (string, error) {
	respBody, err := coordHookRequest(http.MethodGet,
		"/api/tasks/"+taskID+"/meta?namespace="+db.HeraMetaNamespace, nil)
	if err != nil {
		return "", err
	}

	var parsed struct {
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("decode task meta: %w", err)
	}
	for _, e := range parsed.Entries {
		if e.Key == db.HeraMetaKeyRole {
			return e.Value, nil
		}
	}
	return "", nil
}

// stampContextSizeReal overwrites task_meta(hera, context_size) via the same
// PUT /api/tasks/{id}/meta endpoint the SPA and every other plugin use — no
// direct DB access, so this hook works identically whether the daemon is
// local or (in principle) reached over Tailscale.
func stampContextSizeReal(taskID string, size int) error {
	payload, err := json.Marshal(map[string]string{
		"namespace": db.HeraMetaNamespace,
		"key":       db.HeraMetaKeyContextSize,
		"value":     strconv.Itoa(size),
	})
	if err != nil {
		return fmt.Errorf("encode meta payload: %w", err)
	}
	_, err = coordHookRequest(http.MethodPut, "/api/tasks/"+taskID+"/meta", bytes.NewReader(payload))
	return err
}

// budgetReal reads the project's configured coordinator_context_budget via
// GET /api/config — the same superset config snapshot the remote TUI uses,
// so this hook needs no bespoke endpoint. taskID is accepted (not currently
// used — the budget is a single global HeraConfig field, not per-project)
// to keep the same per-task signature as ResolveRoleKind/StampContextSize,
// in case budget ever becomes per-project.
func budgetReal(_ string) (int, error) {
	respBody, err := coordHookRequest(http.MethodGet, "/api/config", nil)
	if err != nil {
		return 0, err
	}
	var cfg config.Config
	if err := json.Unmarshal(respBody, &cfg); err != nil {
		return 0, fmt.Errorf("decode config: %w", err)
	}
	return cfg.Hera.CoordinatorContextBudget, nil
}

// readContextSizeReal scans the transcript JSONL for the latest assistant
// message's usage.cache_read_input_tokens. Reads the file in full rather
// than seeking from the end: JSONL has no fixed record size, so a true tail
// would need its own reverse-line-scan; a coordinator's Stop hook already
// pays one HTTP round trip per turn, so a linear file scan is not the
// dominant cost. Lines that aren't valid JSON, or aren't an assistant
// message, are skipped rather than erroring — a Stop hook must degrade
// gracefully on a transcript with interleaved event types it doesn't care
// about.
func readContextSizeReal(transcriptPath string) (int, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return 0, fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only fd; close error is non-actionable

	sc := bufio.NewScanner(f)
	// Transcript lines can carry large tool outputs; the default 64 KiB
	// scanner buffer truncates those, so raise the ceiling to 10 MiB.
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	size := 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Usage struct {
					CacheReadInputTokens int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" {
			continue
		}
		size = entry.Message.Usage.CacheReadInputTokens
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scan transcript: %w", err)
	}
	return size, nil
}
