package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
)

// bogusKeychainSource is a deliberately nonexistent Keychain item — it proves
// the fail-open path: the resolve fails, SMOKE_KEYCHAIN_BAD is absent from
// the spawned process's env, and the spawn does not crash or hang. This
// touches no real secret; the item genuinely does not exist on any machine.
const bogusKeychainSource = "keychain://this-item-definitely-does-not-exist-secrets-smoke-probe"

// secretsSmokeCommand is the sacrificial backend's command: a plain,
// non-interactive shell one-liner (NOT a real Claude/Codex CLI invocation)
// that starts, checks two env vars for PRESENCE ONLY (never prints a value),
// and exits promptly on its own — no hanging, no PTY interaction needed.
//
// It is wrapped in `sh -c` for the same reason argus-plugin-smoke's
// smoke-cat backend is: BuildCmd unconditionally appends `--session-id
// <UUID>` to every non-Codex/non-Pi/non-opencode backend invocation on a
// fresh spawn, and that gets appended to this ENTIRE string before the whole
// thing is run via an outer `sh -c <cmdStr>` (internal/agent/agent.go). The
// outer shell's own word-splitting treats our single-quoted script as one
// token, then the appended `--session-id <uuid>` becomes two more words
// which the INNER sh -c consumes as $0/$1 (per POSIX's `sh -c cmd
// [name [arg...]]` contract) and our script never references — discarded,
// exactly like smoke-cat's `cat` ignores them. Verified empirically against
// a local `sh -c` invocation with the same appended suffix before wiring
// this into the live daemon.
const secretsSmokeCommand = `sh -c 'echo SMOKE_KEYCHAIN_OK=$([ -n "$SMOKE_KEYCHAIN_OK" ] && echo yes || echo no); echo SMOKE_KEYCHAIN_BAD=$([ -n "$SMOKE_KEYCHAIN_BAD" ] && echo yes || echo no)'` //nolint:gosec // false-positive credential-name pattern match; this is a presence-check shell script, no credential value appears anywhere in it

// resolveKnownGoodSource reads this daemon's OWN [secrets.op].bootstrap_source
// out of the merged DB+config.toml config — the exact descriptor the
// production op-bootstrap path already resolves successfully — rather than
// hardcoding a Keychain item name into this harness. That keeps the "known
// good" fixture tied to whatever is ACTUALLY configured and working on this
// machine, matching the intent of proving the same known-good path end to
// end, and means the harness doesn't silently rot if that item is ever
// renamed.
func resolveKnownGoodSource(dbPath string) (string, error) {
	d, err := db.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("open db at %s: %w", dbPath, err)
	}
	defer d.Close() //nolint:errcheck
	source := d.Config().Secrets.Op.BootstrapSource
	if source == "" {
		return "", fmt.Errorf("no [secrets.op].bootstrap_source configured on this daemon; cannot exercise the known-good keychain path")
	}
	return source, nil
}

// ensureSecretsBackendREST POSTs the sacrificial backend's base definition
// (name + command only — see the package doc for why env_vars can't travel
// this way). Returns owned=true when this call just created it, owned=false
// when it already existed (409) — mirrors argus-plugin-smoke's
// ensureBashBackend exactly, including the conservative "don't claim
// cleanup ownership of something we didn't create" rule.
func (s *smoke) ensureSecretsBackendREST(name, command string) (bool, error) {
	body := fmt.Sprintf(`{"name":%q,"command":%q}`, name, command)
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

// attachSecretsEnvVars sets the sacrificial backend's FULL definition —
// command AND the two-entry env_vars credential mapping — via a direct
// SQLite write to the same ~/.argus/data.sql file the live daemon reads.
// This is the only way to get env_vars onto a backend from outside the TUI
// Settings screen (see the package doc comment for the REST gap this
// surfaced) and mirrors cmd/argus/tokens.go's own established precedent:
// "the CLI talks to the SQLite database directly (WAL mode allows a writer
// while the daemon holds reads/writes)." A single short-lived open/write/
// close, not a long-held connection racing the daemon.
func attachSecretsEnvVars(dbPath, name, command string, envVars map[string]string) error {
	d, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db at %s: %w", dbPath, err)
	}
	defer d.Close() //nolint:errcheck
	return d.SetBackend(name, config.Backend{Command: command, EnvVars: envVars})
}

// createSmokeTask POSTs a throwaway task under the given backend and project
// (mirrors argus-plugin-smoke's createBashTask). No prompt — the backend's
// command is the whole payload.
func (s *smoke) createSmokeTask(name, backend string) (string, error) {
	body := fmt.Sprintf(`{"name":%q,"project":%q,"backend":%q}`, name, s.project, backend)
	resp, err := s.adminPOST("/api/tasks", body)
	if err != nil {
		return "", err
	}
	var decoded struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(resp, http.StatusCreated, "POST /api/tasks", &decoded); err != nil {
		return "", err
	}
	if decoded.ID == "" {
		return "", fmt.Errorf("daemon returned empty task id")
	}
	return decoded.ID, nil
}

// fetchTaskStatus GETs a task's current status string ("pending",
// "in_progress", "in_review", "complete") via the scope token.
func (s *smoke) fetchTaskStatus(taskID string) (string, error) {
	resp, err := s.scopedGET("/api/tasks/" + taskID)
	if err != nil {
		return "", err
	}
	var decoded struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(resp, http.StatusOK, "GET /api/tasks/"+taskID, &decoded); err != nil {
		return "", err
	}
	return decoded.Status, nil
}

// fetchOutput GETs a task's ANSI-clean captured output via the scope token —
// the same endpoint (and clean=1 param) argus-plugin-smoke's
// fetchTaskOutput uses.
func (s *smoke) fetchOutput(taskID string) (string, error) {
	resp, err := s.scopedGET("/api/tasks/" + taskID + "/output?clean=1")
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

// awaitTaskDone polls GET /api/tasks/{id} until the task leaves
// pending/in_progress or timeout elapses. Our sacrificial command always
// exits 0, so the daemon's transitionTaskOnExit (internal/daemon/daemon.go)
// should land it in StatusComplete specifically — landing in "in_review"
// instead means the process crashed or was treated as an unclean exit,
// which is itself a failure worth surfacing distinctly from a plain timeout.
func (s *smoke) awaitTaskDone(taskID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		status, err := s.fetchTaskStatus(taskID)
		if err != nil {
			return "", err
		}
		last = status
		if status != "pending" && status != "in_progress" {
			return status, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("task %s did not leave status %q within %s", taskID, last, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fetchOutputUntilMarkers polls the task's captured output until both
// expected KEY= lines have appeared or timeout elapses, returning whatever
// was last captured either way — so the caller's precise "=yes"/"=no"
// assertions get real, debuggable output even on a miss, not an empty
// string. The one-liner should complete and flush well under a second; this
// budget is generous headroom, not an expected steady-state latency.
func (s *smoke) fetchOutputUntilMarkers(taskID string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		out, err := s.fetchOutput(taskID)
		if err != nil {
			return "", err
		}
		last = out
		if strings.Contains(out, "SMOKE_KEYCHAIN_OK=") && strings.Contains(out, "SMOKE_KEYCHAIN_BAD=") {
			return out, nil
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// assertOutputContains is the shared assertion for phases 4 and 5: a plain,
// pure string check that always includes the FULL captured output in the
// failure detail (per the task's debuggability requirement) — safe to
// include verbatim because this output only ever contains our own
// echo'd "yes"/"no" presence markers, never a resolved secret value.
func assertOutputContains(output, marker, label string) error {
	if strings.Contains(output, marker) {
		return nil
	}
	return fmt.Errorf("%s: expected output to contain %q; got: %s", label, marker, truncate(output, 2000))
}
