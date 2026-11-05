package agent

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// devStackProcessNames are the executables a devbox-managed dev stack
// (started by an agent session running `devbox services up` inside a
// worktree, entirely outside argus's own code) is made of, per the
// confirmed worktree-orphaning investigation. See
// context/knowledge/gotchas/worktree.md.
var devStackProcessNames = []string{"process-compose", "mysqld", "redis-server", "postgres", "caddy"}

// DevStackProc is one running process identified as part of a devbox dev
// stack, with the worktree path parsed out of its command line.
type DevStackProc struct {
	PID          int
	Name         string
	WorktreePath string
	// Supervisor is true for processes that can respawn the others
	// (process-compose, or a `devbox run --` wrapper). stopDevStackProcs
	// signals these before the services they supervise so a restart policy
	// cannot race the kill.
	Supervisor bool
}

// pgrepOutput is the exec seam; tests swap it to avoid shelling out to a
// real pgrep. Mirrors the cmdFactory seam in internal/claudeagents.
//
// The full-command-line-output flag differs between BSD pgrep (macOS) and
// GNU procps pgrep (Linux) even though both accept `-f` identically for
// MATCHING against the full argument list: BSD's `-l` prints the full args
// when combined with `-f`, but GNU's `-l` prints only the short process
// name — `-a` is GNU's flag for full-command-line output, which on BSD
// means something unrelated ("include process ancestors in the match
// list"). Using the wrong flag per platform silently degrades every parsed
// line to just a bare name with no worktree path, which parsePgrepOutput
// then filters out entirely — caught via a real CI failure on Linux, not
// locally on macOS, and confirmed against a real Ubuntu container before
// fixing (see fix-devstack-orphaning PR #933 review).
var pgrepOutput = func() ([]byte, error) {
	fullOutputFlag := "-l"
	if runtime.GOOS == "linux" {
		fullOutputFlag = "-a"
	}
	return exec.Command("pgrep", "-f", fullOutputFlag, strings.Join(devStackProcessNames, "|")).Output()
}

// signalPID is the signal-sending seam; tests swap it to observe calls
// without sending a real signal to an arbitrary PID.
var signalPID = func(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

// devStackGracePeriod bounds how long stopDevStackFor waits after SIGTERM
// before verifying and SIGKILLing stragglers. Short: this runs inline in
// every worktree-removal path (all already backgrounded off the UI/request
// thread, which already expect worktree cleanup to take "seconds" per the
// existing call-site comments), but still shouldn't stall a bulk
// delete/prune for long per task.
var devStackGracePeriod = 5 * time.Second

// worktreePathPattern matches a `.../.argus/worktrees/<project>/<task>` or
// `.../.claude/worktrees/<project>/<task>` path segment embedded in a
// command line, stopping at the task-level directory so a process
// referencing a file nested deeper inside the worktree (e.g.
// process-compose's `-f <worktree>/.devbox/virtenv/redis/process-compose.yaml`)
// still resolves to the worktree root — the exact path RemoveWorktree
// deletes, not the deeper file. The prefix excludes `=` and `:` (in addition
// to whitespace) so a flag glued directly to the path with no space —
// `--datadir=<path>` (mysqld), `unixsocket:<path>` (redis-server) — doesn't
// get swept into the captured path.
var worktreePathPattern = regexp.MustCompile(`([^\s=:]*/\.(?:argus|claude)/worktrees/[^/\s]+/[^/\s]+)`)

// ScanDevStackProcesses lists every currently running process that looks
// like part of a devbox dev stack, with the worktree path parsed out of its
// command line. Returns a nil slice with a nil error when pgrep runs
// successfully but finds nothing (pgrep's own "no matches" exit code 1) —
// that's the overwhelmingly common case on any machine without a devbox dev
// stack running, not a failure. A non-nil error means the scan itself could
// not run (pgrep missing, or an unexpected pgrep failure) — callers treat
// that as "unknown," not "none found."
func ScanDevStackProcesses() ([]DevStackProc, error) {
	out, err := pgrepOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parsePgrepOutput(out), nil
}

// parsePgrepOutput parses `pgrep -fl` output ("<pid> <command line>" per
// line) into DevStackProc entries. Lines with no discoverable worktree path,
// or that don't match a known dev-stack process name, are skipped.
func parsePgrepOutput(out []byte) []DevStackProc {
	var procs []DevStackProc
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, " ", 2)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		cmdline := fields[1]
		wt := extractWorktreePath(cmdline)
		if wt == "" {
			continue
		}
		name, supervisor := classifyDevStackCmdline(cmdline)
		if name == "" {
			continue
		}
		procs = append(procs, DevStackProc{PID: pid, Name: name, WorktreePath: wt, Supervisor: supervisor})
	}
	return procs
}

// extractWorktreePath returns the worktree root embedded in cmdline, or ""
// if none is found.
func extractWorktreePath(cmdline string) string {
	m := worktreePathPattern.FindStringSubmatch(cmdline)
	if m == nil {
		return ""
	}
	return filepath.Clean(m[1])
}

// processNameFromCmdline returns which known dev-stack binary cmdline
// belongs to, or "" if none match.
func processNameFromCmdline(cmdline string) string {
	name, _ := classifyDevStackCmdline(cmdline)
	return name
}

// classifyDevStackCmdline identifies the dev-stack binary a command line
// EXECUTES: the basename of argv[0], or of the first token after `devbox run
// --` when argv[0] is devbox. A bare substring match is not enough: `pgrep -f`
// also matches processes that merely mention a binary name in their
// arguments (an agent whose prompt says "mysqld" and names a worktree), and
// the finished-stack sweeper would then SIGTERM a live agent. supervisor is
// true for process-compose and for a devbox wrapper, which can respawn the
// services they run.
func classifyDevStackCmdline(cmdline string) (name string, supervisor bool) {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return "", false
	}
	exe := execBaseName(fields[0])
	if exe == "devbox" {
		for i := 1; i+1 < len(fields); i++ {
			if fields[i] == "--" {
				if inner := execBaseName(fields[i+1]); isDevStackBinary(inner) {
					return inner, true
				}
				return "", false
			}
		}
		return "", false
	}
	if isDevStackBinary(exe) {
		return exe, exe == "process-compose"
	}
	return "", false
}

// execBaseName returns the basename of an executable path, undoing the Nix
// wrapper naming convention (".mysqld-wrapped" -> "mysqld").
func execBaseName(path string) string {
	b := filepath.Base(path)
	b = strings.TrimPrefix(b, ".")
	return strings.TrimSuffix(b, "-wrapped")
}

func isDevStackBinary(name string) bool {
	for _, n := range devStackProcessNames {
		if name == n {
			return true
		}
	}
	return false
}

// devStackSupervisorGrace bounds the wait after the supervisor-only SIGTERM
// round, before the remaining services are signaled directly.
var devStackSupervisorGrace = 2 * time.Second

// StopDevStack is the exported entry for callers outside this package (the
// daemon's finished-task dev-stack sweeper). Same behavior as the
// removal-time teardown.
func StopDevStack(worktreePath string) { stopDevStackFor(worktreePath) }

// stopDevStackFor stops any dev-stack process (mysqld/redis-server/postgres/
// caddy/process-compose) whose command line embeds worktreePath. Meant to
// run before that worktree's files are removed, and by the finished-task
// sweeper. Best-effort throughout: logs via uxlog, never returns an error.
//
// Order: (1) SIGTERM supervisors only (process-compose, `devbox run --`
// wrappers) and wait briefly, so a `restart: "always"` policy cannot respawn
// a service we are about to kill; (2) rescan and SIGTERM every remaining
// process DIRECTLY — the supervisor-to-children shutdown cascade is not
// reliable (a live investigation found a sibling redis-server and caddy
// survive SIGTERM to their process-compose parent while mysqld did not);
// (3) after a grace period SIGKILL anything still running, supervisors first.
//
// Matching requires worktreePath to be the exact extracted worktree root of
// a process's command line, not a bare substring — guards against a
// sibling worktree whose name is a prefix of this one (e.g. "Sherlock/3b"
// vs "Sherlock/3b-more"), the same class of bug documented in
// gotchas/worktree.md's firstKnownDescendant note.
func stopDevStackFor(worktreePath string) {
	clean := filepath.Clean(worktreePath)

	found := scanForWorktree(clean)
	if len(found) == 0 {
		return
	}

	if sups := supervisorsOf(found); len(sups) > 0 {
		uxlog.Log("[worktree] stopDevStack: SIGTERM to %d supervisor(s) first for %q", len(sups), clean)
		for _, p := range sups {
			signalDevStackProc(p, syscall.SIGTERM)
		}
		time.Sleep(devStackSupervisorGrace)
		found = scanForWorktree(clean)
		if len(found) == 0 {
			return
		}
	}

	uxlog.Log("[worktree] stopDevStack: found %d dev-stack process(es) for %q, sending SIGTERM", len(found), clean)
	for _, p := range found {
		signalDevStackProc(p, syscall.SIGTERM)
	}

	time.Sleep(devStackGracePeriod)

	remaining := scanForWorktree(clean)
	if len(remaining) == 0 {
		return
	}
	uxlog.Log("[worktree] stopDevStack: %d process(es) survived SIGTERM for %q, sending SIGKILL", len(remaining), clean)
	sups := supervisorsOf(remaining)
	for _, p := range sups {
		signalDevStackProc(p, syscall.SIGKILL)
	}
	for _, p := range remaining {
		if !p.Supervisor {
			signalDevStackProc(p, syscall.SIGKILL)
		}
	}
}

func supervisorsOf(procs []DevStackProc) []DevStackProc {
	var out []DevStackProc
	for _, p := range procs {
		if p.Supervisor {
			out = append(out, p)
		}
	}
	return out
}

// scanForWorktree returns every currently running dev-stack process whose
// extracted worktree path exactly matches worktreePath (already cleaned by
// the caller). A scan failure is logged and treated as "found nothing" —
// stopDevStackFor never blocks worktree removal on it.
func scanForWorktree(worktreePath string) []DevStackProc {
	procs, err := ScanDevStackProcesses()
	if err != nil {
		uxlog.Log("[worktree] stopDevStack: scan unavailable: %v", err)
		return nil
	}
	var out []DevStackProc
	for _, p := range procs {
		if p.WorktreePath == worktreePath {
			out = append(out, p)
		}
	}
	return out
}

func signalDevStackProc(p DevStackProc, sig syscall.Signal) {
	if err := signalPID(p.PID, sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		uxlog.Log("[worktree] stopDevStack: signal %v to pid %d (%s) failed: %v", sig, p.PID, p.Name, err)
		return
	}
	uxlog.Log("[worktree] stopDevStack: sent %v to pid %d (%s, %s)", sig, p.PID, p.Name, p.WorktreePath)
}
