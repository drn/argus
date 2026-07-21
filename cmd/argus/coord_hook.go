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
// Argus-spawned sessions — internal/agent/agent.go) plus the task carrying
// ANY hera role binding (coordinator, worker, or freelance); an unbound task
// is a silent no-op. Only a coordinator reaches the budget/nudge/hard-stop/
// recycle machinery — a worker or freelance session gets its context_size
// stamped (for the rail's context-pressure indicator) and stops there.
//
// See openspec/changes/add-coordinator-context-management/design.md
// Decision 1 and specs/coordinator-context-management/spec.md.

// coordHookEnv is the dependency-injection seam runCoordHook drives. Real
// implementations (below) reach the daemon's REST API and the transcript
// file; tests inject in-memory fakes so the gating/nudge logic is unit
// testable without a live daemon (context/knowledge/testing.md).
type coordHookEnv struct {
	Getenv          func(key string) string
	ResolveRoleKind func(taskID string) (string, error)
	// PendingRecycleAlready reports whether the coordinator has already
	// requested a self-service recycle (task_meta hera/pending_recycle ==
	// "true") — checked BEFORE emitting a graceful block decision so an
	// already-pending coordinator gets no-op'd instead of re-blocked (fix-
	// coordhook-idle-deadlock: re-blocking never lets the session go idle,
	// so RecycleWatcher's IsIdle gate never actually fires).
	PendingRecycleAlready func(taskID string) (bool, error)
	ReadContextSize       func(transcriptPath string) (int, error)
	StampContextSize      func(taskID string, size int) error
	Budget                func(taskID string) (int, error)
	// ForceRecycle calls the daemon's hard-stop escalation (Part B): an
	// immediate, idle-gate-free kill-and-restart of the coordinator's
	// session, fired once context_size crosses 1.5x budget regardless of
	// whether PendingRecycleAlready is true — the safety net for when the
	// graceful path is stuck waiting for idleness that never comes.
	ForceRecycle func(taskID string) error
	// ReadLastNudgedContextSize reads the context_size at which the
	// over-budget nudge last fired (task_meta hera/last_nudged_context_size,
	// throttle-coord-hook-nudge) — ok is false when the key has never been
	// stamped (no prior nudge this episode).
	ReadLastNudgedContextSize func(taskID string) (size int, ok bool, err error)
	// StampLastNudgedContextSize overwrites task_meta(hera,
	// last_nudged_context_size) with the size at which the nudge just fired —
	// called only on the path that actually emits a block decision, never on a
	// suppressed (throttled or pending-recycle) turn.
	StampLastNudgedContextSize func(taskID string, size int) error
	// NudgeIncrement reads the configured coordinator_nudge_increment: the
	// amount context_size must grow past the last-nudged size before the
	// nudge is allowed to re-fire.
	NudgeIncrement func(taskID string) (int, error)
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
// gates on ARGUS_TASK_ID + a resolved hera role BEFORE touching the
// transcript or the REST API (no-op with zero side effects when the task
// carries no hera binding at all — ResolveRoleKind resolves unbound tasks to
// ""), unconditionally stamps task_meta(hera, context_size) for ANY
// hera-bound role — coordinator, worker, or freelance (rail-context-high
// widened this from coordinator-only, so the rail's context-pressure
// indicator has a live signal for workers too) — regardless of budget, and
// writes a Stop-hook block decision to out only when the role is a
// coordinator AND at/over budget: the budget/nudge/hard-stop/recycle
// machinery below remains coordinator-only, since only a coordinator is
// long-lived enough to need it. Re-evaluates fresh on every invocation —
// nothing is remembered between processes — but the nudge itself is
// throttled (throttle-coord-hook-nudge): once it fires, it recurs only after
// context_size has grown by at least the configured increment past the size
// at which it last fired, not on every subsequent turn, and stops the very
// turn context_size drops back under budget.
func runCoordHook(stdin io.Reader, out, errOut io.Writer, env coordHookEnv) {
	taskID := env.Getenv("ARGUS_TASK_ID")
	if taskID == "" {
		return
	}

	kind, err := env.ResolveRoleKind(taskID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: resolve role kind: %v\n", err)
		return
	}
	if kind == "" {
		return
	}

	var in stopHookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: decode stdin: %v\n", err)
		return
	}

	size, err := env.ReadContextSize(in.TranscriptPath)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: read context size: %v\n", err)
		return
	}

	// Always stamp, regardless of kind or budget — this is the live signal a
	// human, the rail's context-pressure indicator, or the recycle mechanism
	// reads, with zero dependency on the session's cooperation.
	if err := env.StampContextSize(taskID, size); err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: stamp context size: %v\n", err)
	}

	// Budget/nudge/hard-stop/recycle enforcement is coordinator-only: a
	// worker or freelance session is stamped above and stops here.
	if kind != string(db.HeraKindCoordinator) {
		return
	}

	budget, err := env.Budget(taskID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: read budget: %v\n", err)
		return
	}
	if size < budget {
		return
	}

	// Hard-stop escalation (Part B): once 1.5x over budget, force the
	// recycle immediately instead of trusting the graceful path below — the
	// safety net for a human who keeps replying quickly enough that the
	// session's PTY never accumulates the 3s of silence IsIdle needs, even
	// with the pending_recycle idempotency fix in place. Fires regardless of
	// pending_recycle: that flag only gates the GRACEFUL path's re-blocking,
	// not this unconditional escalation, so the pending-recycle read is
	// skipped entirely here — it can't change the outcome.
	if size*2 >= budget*3 {
		if err := env.ForceRecycle(taskID); err != nil {
			_, _ = fmt.Fprintf(errOut, "coord-hook: force recycle: %v\n", err)
		}
		return
	}

	// Nudge increment throttle (throttle-coord-hook-nudge): once the nudge has
	// fired, it recurs only after context_size has grown by at least the
	// configured increment past the size at which it last fired — not on every
	// subsequent turn. The `size >= lastNudged` guard is required: without it, a
	// fresh over-budget episode following a recycle (context_size reset low, but
	// a stale-and-larger last_nudged_context_size still on record from the prior
	// session) would be wrongly suppressed by `size < lastNudged+increment`
	// alone. Read errors fail open (treated as "no prior nudge"/"no increment
	// configured") so a read failure never silently drops the nudge.
	lastNudged, hadLastNudged, err := env.ReadLastNudgedContextSize(taskID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: read last nudged context size: %v\n", err)
		hadLastNudged = false
	}
	increment, err := env.NudgeIncrement(taskID)
	if err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: read nudge increment: %v\n", err)
		increment = 0
	}
	if hadLastNudged && size >= lastNudged && size < lastNudged+increment {
		return
	}

	// Part A idempotency: once the coordinator has already requested a
	// self-service recycle, re-blocking here would force immediate
	// re-engagement on every subsequent Stop, so the session's PTY never
	// accumulates the 3s of silence IsIdle needs — RecycleWatcher would then
	// never see the session idle and the recycle would never actually fire
	// (the infinite-loop incident this fix addresses). Returning with no
	// decision lets Claude Code's Stop genuinely go through, giving the
	// watcher a real idle window. A read error falls back to treating the
	// flag as not-yet-pending (the pre-fix behavior) rather than silently
	// dropping the nudge.
	if pending, err := env.PendingRecycleAlready(taskID); err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: read pending recycle: %v\n", err)
	} else if pending {
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
		_, _ = fmt.Fprintf(errOut, "coord-hook: encode decision: %v\n", err)
		return
	}

	// Record the size at which this nudge fired so the increment throttle above
	// has a baseline for the next invocation. Only reached on the path that
	// actually emits a block decision — never on a turn suppressed by the
	// increment gate or by pending_recycle.
	if err := env.StampLastNudgedContextSize(taskID, size); err != nil {
		_, _ = fmt.Fprintf(errOut, "coord-hook: stamp last nudged context size: %v\n", err)
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
		Getenv:                     os.Getenv,
		ResolveRoleKind:            resolveRoleKindReal,
		PendingRecycleAlready:      pendingRecycleAlreadyReal,
		ReadContextSize:            readContextSizeReal,
		StampContextSize:           stampContextSizeReal,
		Budget:                     budgetReal,
		ForceRecycle:               forceRecycleReal,
		ReadLastNudgedContextSize:  readLastNudgedContextSizeReal,
		StampLastNudgedContextSize: stampLastNudgedContextSizeReal,
		NudgeIncrement:             nudgeIncrementReal,
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

// pendingRecycleAlreadyReal reads task_meta(hera, pending_recycle) for the
// given task (db.HeraMetaKeyPendingRecycle) — mirrors resolveRoleKindReal's
// shape exactly, against the same GET /api/tasks/{id}/meta?namespace=hera
// endpoint, just reading a different key. Kept as its own round trip rather
// than folded into resolveRoleKindReal: it's only ever consulted once a
// coordinator is already confirmed over budget (not on every Stop event), so
// the extra request is rare, and keeping the two reads independent avoids
// coupling role-kind gating to recycle-flag gating in one function.
func pendingRecycleAlreadyReal(taskID string) (bool, error) {
	respBody, err := coordHookRequest(http.MethodGet,
		"/api/tasks/"+taskID+"/meta?namespace="+db.HeraMetaNamespace, nil)
	if err != nil {
		return false, err
	}

	var parsed struct {
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return false, fmt.Errorf("decode task meta: %w", err)
	}
	for _, e := range parsed.Entries {
		if e.Key == db.HeraMetaKeyPendingRecycle {
			return e.Value == "true", nil
		}
	}
	return false, nil
}

// forceRecycleReal triggers the daemon's hard-stop escalation (Part B) over
// the Unix socket RPC connection — unlike every other coord-hook call, this
// does NOT go through the REST API: recycle_coord's kill-and-restart
// mechanism lives entirely daemon-side (agent.SessionRunner + hera.RecycleCoord),
// with no existing REST endpoint to reuse, so it's called the same way
// discoverAPIPort calls Daemon.Ports.
func forceRecycleReal(taskID string) error {
	client, err := coordHookDial()
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck // short-lived CLI client; close error is non-actionable

	var resp daemon.StatusResp
	if err := client.Call("Daemon.ForceRecycleCoordinator", &daemon.TaskIDReq{TaskID: taskID}, &resp); err != nil {
		return fmt.Errorf("Daemon.ForceRecycleCoordinator: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("Daemon.ForceRecycleCoordinator: %s", resp.Error)
	}
	return nil
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

// readLastNudgedContextSizeReal reads task_meta(hera, last_nudged_context_size)
// for the given task (db.HeraMetaKeyLastNudgedContextSize) — mirrors
// resolveRoleKindReal/pendingRecycleAlreadyReal's shape exactly, against the
// same GET /api/tasks/{id}/meta?namespace=hera endpoint, just reading a
// different key and parsing it as an int. ok is false (with no error) when the
// key is absent from the entries — no prior nudge this episode.
func readLastNudgedContextSizeReal(taskID string) (int, bool, error) {
	respBody, err := coordHookRequest(http.MethodGet,
		"/api/tasks/"+taskID+"/meta?namespace="+db.HeraMetaNamespace, nil)
	if err != nil {
		return 0, false, err
	}

	var parsed struct {
		Entries []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return 0, false, fmt.Errorf("decode task meta: %w", err)
	}
	for _, e := range parsed.Entries {
		if e.Key == db.HeraMetaKeyLastNudgedContextSize {
			size, err := strconv.Atoi(e.Value)
			if err != nil {
				return 0, false, fmt.Errorf("parse last nudged context size: %w", err)
			}
			return size, true, nil
		}
	}
	return 0, false, nil
}

// stampLastNudgedContextSizeReal overwrites task_meta(hera,
// last_nudged_context_size) via the same PUT /api/tasks/{id}/meta endpoint —
// mirrors stampContextSizeReal's shape exactly, just writing a different key.
func stampLastNudgedContextSizeReal(taskID string, size int) error {
	payload, err := json.Marshal(map[string]string{
		"namespace": db.HeraMetaNamespace,
		"key":       db.HeraMetaKeyLastNudgedContextSize,
		"value":     strconv.Itoa(size),
	})
	if err != nil {
		return fmt.Errorf("encode meta payload: %w", err)
	}
	_, err = coordHookRequest(http.MethodPut, "/api/tasks/"+taskID+"/meta", bytes.NewReader(payload))
	return err
}

// nudgeIncrementReal reads the project's configured coordinator_nudge_increment
// via GET /api/config — mirrors budgetReal's shape exactly. taskID is accepted
// (not currently used — the increment is a single global HeraConfig field, not
// per-project) to keep the same per-task signature as the other Real
// implementations, in case it ever becomes per-project.
func nudgeIncrementReal(_ string) (int, error) {
	respBody, err := coordHookRequest(http.MethodGet, "/api/config", nil)
	if err != nil {
		return 0, err
	}
	var cfg config.Config
	if err := json.Unmarshal(respBody, &cfg); err != nil {
		return 0, fmt.Errorf("decode config: %w", err)
	}
	return cfg.Hera.CoordinatorNudgeIncrement, nil
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
// message's total input token count: cache_read_input_tokens +
// cache_creation_input_tokens + input_tokens. cache_read_input_tokens ALONE
// is not a safe proxy for context size — it only counts tokens actually
// served from Anthropic's prompt cache this turn, and collapses toward zero
// on any cache miss (a long idle gap crossing the cache TTL is the common
// case for an idle hera worker), even though the real context is unchanged
// or larger: a cache miss rewrites the whole prior context as
// cache_creation_input_tokens instead of reading it, so summing all three
// fields is what actually tracks total context regardless of cache hit/miss
// state (rail-context-size-metric-fix). Reads the file in full rather than
// seeking from the end: JSONL has no fixed record size, so a true tail
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
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Type != "assistant" {
			continue
		}
		u := entry.Message.Usage
		size = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scan transcript: %w", err)
	}
	return size, nil
}
