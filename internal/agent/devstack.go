package agent

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log/slog"
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
var devStackProcessNames = []string{"process-compose", "mysqld", "mysqld_safe", "redis-server", "postgres", "caddy"}

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

// maxPgrepLine caps a single pgrep output line. macOS argv tops out near 1 MiB,
// so 16 MiB is never legitimately exceeded; exceeding it is a scan failure, not
// a silently truncated list.
const maxPgrepLine = 16 << 20

// psCommand returns one process's full command line (the verification seam;
// tests swap it). The default is disabled under `go test` so a test can never
// read a real PID's argv by accident.
var psCommand = func(pid int) (string, error) {
	if isTestBinary() {
		return "", errors.New("ps disabled in test binary")
	}
	return realPsCommand(pid)
}

// realPsCommand reads pid's own argv via ps. A nonexistent pid yields ("", nil).
func realPsCommand(pid int) (string, error) {
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(bytes.TrimSpace(out)) == 0 {
			return "", nil // no such process
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ScanDevStackProcesses lists every currently running process that looks
// like part of a devbox dev stack, with the worktree path parsed out of its
// command line. Returns a nil slice with a nil error when pgrep runs
// successfully but finds nothing (pgrep's own "no matches" exit code 1) —
// that's the overwhelmingly common case on any machine without a devbox dev
// stack running, not a failure. A non-nil error means the scan itself could
// not run or its output could not be read in full (pgrep missing, an
// unexpected pgrep failure, an over-long line) — callers treat that as
// "unknown," not "none found."
//
// The result is a CANDIDATE list: pgrep output is line-based but argv can
// contain raw newlines (an agent's multi-line prompt), so a line can be a
// phantom. Anything acted on must be re-verified per PID (verifyDevStackProc).
func ScanDevStackProcesses() ([]DevStackProc, error) {
	out, err := pgrepOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	return parsePgrepOutput(out)
}

// parsePgrepOutput parses `pgrep -fl` output ("<pid> <command line>" per
// line) into DevStackProc entries. Lines with no discoverable worktree path,
// or whose executable is not a known dev-stack binary, are skipped. A read
// error (e.g. a line over maxPgrepLine) is returned rather than truncating
// the list.
func parsePgrepOutput(out []byte) ([]DevStackProc, error) {
	var procs []DevStackProc
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), maxPgrepLine)
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
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading pgrep output: %w", err)
	}
	return procs, nil
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
// EXECUTES: the basename of argv[0], or the command a `devbox run` wrapper
// launches (devboxInnerCommand). A bare substring match is not enough:
// `pgrep -f` also matches processes that merely mention a binary name in
// their arguments (an agent whose prompt says "mysqld" and names a worktree),
// and the finished-stack sweeper would then SIGTERM a live agent. supervisor
// is true for process-compose, mysqld_safe (which respawns mysqld) and any
// devbox wrapper. `sh -c ...` wrappers are deliberately NOT matched: their
// child (the real binary) matches directly and is signaled on its own.
func classifyDevStackCmdline(cmdline string) (name string, supervisor bool) {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return "", false
	}
	exe := execBaseName(fields[0])
	if exe == "devbox" {
		if inner := devboxInnerCommand(fields[1:]); inner != "" {
			return inner, true
		}
		return "", false
	}
	if isDevStackBinary(exe) {
		return exe, exe == "process-compose" || exe == "mysqld_safe"
	}
	return "", false
}

// devboxInnerCommand returns the dev-stack binary launched by `devbox run
// [flags] [--] [env] [VAR=val ...] <binary> ...`, or "" when args is not a
// `run` of one. Leading dash tokens, `env` and VAR=val assignments are
// skipped; the first remaining token must be a dev-stack binary.
func devboxInnerCommand(args []string) string {
	i := 0
	for i < len(args) && args[i] != "run" {
		if !strings.HasPrefix(args[i], "-") {
			return "" // another subcommand (services, shell, ...)
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	rest := args[i+1:]
	for len(rest) > 0 {
		t := rest[0]
		if strings.HasPrefix(t, "-") || execBaseName(t) == "env" || isEnvAssignment(t) {
			rest = rest[1:]
			continue
		}
		break
	}
	if len(rest) == 0 {
		return ""
	}
	if inner := execBaseName(rest[0]); isDevStackBinary(inner) {
		return inner
	}
	return ""
}

func isEnvAssignment(t string) bool {
	eq := strings.IndexByte(t, '=')
	return eq > 0 && !strings.ContainsAny(t[:eq], "/")
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

// devStackSleep waits d, returning false if cancel closes first. A var so
// tests can observe ordering without real waits.
var devStackSleep = func(d time.Duration, cancel <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-cancel:
		return false
	}
}

// devStackSupervisorGrace bounds the wait after the supervisor-only SIGTERM
// round, before the remaining services are signaled directly.
var devStackSupervisorGrace = 2 * time.Second

// StopOptions tunes StopDevStack. The zero value is the removal-time
// behavior: short SIGKILL grace, no cancellation, no re-check.
type StopOptions struct {
	// KillGrace is the wait between SIGTERM and SIGKILL (zero: devStackGracePeriod,
	// 5s, chosen for removal when the data is about to be deleted). The
	// finished-task sweeper passes longer: it keeps the worktree, and a later
	// resume reuses the datadir, so databases get time to shut down cleanly.
	KillGrace time.Duration
	// Cancel aborts any grace wait (daemon shutdown). A cancelled stop sends
	// no further signals.
	Cancel <-chan struct{}
	// Proceed, when set, is consulted before each escalation (the direct
	// SIGTERM round and SIGKILL); false abandons the stop. The sweeper uses it
	// to re-read task state so a task resumed mid-stop is not escalated on.
	Proceed func() bool
}

// StopDevStack stops every dev-stack process of worktreePath. See stopDevStackFor.
func StopDevStack(worktreePath string, o StopOptions) { stopDevStack(worktreePath, o) }

// stopDevStackFor stops any dev-stack process (mysqld/redis-server/postgres/
// caddy/process-compose) whose command line embeds worktreePath, with the
// removal-time defaults. Meant to run before that worktree's files are
// removed. Best-effort throughout: logs, never returns an error.
func stopDevStackFor(worktreePath string) { stopDevStack(worktreePath, StopOptions{}) }

// stopDevStack — order: (1) SIGTERM supervisors only (process-compose,
// mysqld_safe, `devbox run` wrappers) and wait briefly, so a `restart:
// "always"` policy cannot respawn a service we are about to kill; (2) rescan
// and SIGTERM every remaining process DIRECTLY — the supervisor-to-children
// shutdown cascade is not reliable (a live investigation found a sibling
// redis-server and caddy survive SIGTERM to their process-compose parent
// while mysqld did not); (3) after KillGrace SIGKILL anything still running,
// supervisors first.
//
// Matching requires worktreePath to be the exact extracted worktree root of
// a process's command line, not a bare substring — guards against a
// sibling worktree whose name is a prefix of this one (e.g. "Sherlock/3b"
// vs "Sherlock/3b-more"), the same class of bug documented in
// gotchas/worktree.md's firstKnownDescendant note. Every PID is re-verified
// against its own live argv immediately before each signal.
func stopDevStack(worktreePath string, o StopOptions) {
	clean := filepath.Clean(worktreePath)
	killGrace := o.KillGrace
	if killGrace <= 0 {
		killGrace = devStackGracePeriod
	}
	proceed := func() bool { return o.Proceed == nil || o.Proceed() }

	found := scanForWorktree(clean)
	if len(found) == 0 {
		return
	}

	if sups := supervisorsOf(found); len(sups) > 0 {
		devStackLog("SIGTERM to %d supervisor(s) first for %q", len(sups), clean)
		for _, p := range sups {
			signalDevStackProc(p, syscall.SIGTERM)
		}
		if !devStackSleep(devStackSupervisorGrace, o.Cancel) {
			devStackLog("cancelled during supervisor grace for %q", clean)
			return
		}
		found = scanForWorktree(clean)
		if len(found) == 0 {
			return
		}
	}

	if !proceed() {
		devStackLog("abandoned before SIGTERM round for %q (no longer eligible)", clean)
		return
	}
	devStackLog("found %d dev-stack process(es) for %q, sending SIGTERM", len(found), clean)
	for _, p := range found {
		signalDevStackProc(p, syscall.SIGTERM)
	}

	if !devStackSleep(killGrace, o.Cancel) {
		devStackLog("cancelled during kill grace for %q", clean)
		return
	}

	remaining := scanForWorktree(clean)
	if len(remaining) == 0 {
		return
	}
	if !proceed() {
		devStackLog("abandoned before SIGKILL for %q (no longer eligible)", clean)
		return
	}
	devStackLog("%d process(es) survived SIGTERM for %q, sending SIGKILL", len(remaining), clean)
	for _, p := range supervisorsOf(remaining) {
		signalDevStackProc(p, syscall.SIGKILL)
	}
	for _, p := range remaining {
		if !p.Supervisor {
			signalDevStackProc(p, syscall.SIGKILL)
		}
	}
}

// devStackLog writes to slog (the only trail in the daemon, which never calls
// uxlog.Init) and uxlog (the TUI process's trail).
func devStackLog(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Info("devstack: " + msg)
	uxlog.Log("[worktree] stopDevStack: %s", msg)
}

func devStackWarn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	slog.Warn("devstack: " + msg)
	uxlog.Log("[worktree] stopDevStack: %s", msg)
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
// stopDevStack never blocks worktree removal on it.
func scanForWorktree(worktreePath string) []DevStackProc {
	procs, err := ScanDevStackProcesses()
	if err != nil {
		devStackWarn("scan unavailable: %v", err)
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

// verifyDevStackProc re-reads p's own live argv and confirms it still IS a
// dev-stack process of the same worktree. pgrep output is line-based but a
// process's argv can contain raw newlines (an agent's multi-line prompt
// quoting a dev-stack command line), so a scanned entry can be a phantom whose
// PID belongs to something else entirely — or the process may be gone and its
// PID reused.
func verifyDevStackProc(p DevStackProc) (bool, string) {
	cmd, err := psCommand(p.PID)
	if err != nil {
		return false, fmt.Sprintf("argv re-read failed: %v", err)
	}
	if strings.TrimSpace(cmd) == "" {
		return false, "process is gone"
	}
	name, _ := classifyDevStackCmdline(cmd)
	if name == "" || name != p.Name {
		return false, fmt.Sprintf("live argv is not a %s dev-stack process", p.Name)
	}
	if extractWorktreePath(cmd) != p.WorktreePath {
		return false, "live argv names a different worktree"
	}
	return true, ""
}

func signalDevStackProc(p DevStackProc, sig syscall.Signal) {
	if ok, why := verifyDevStackProc(p); !ok {
		devStackWarn("NOT signaling %v to pid %d (%s, %s): %s", sig, p.PID, p.Name, p.WorktreePath, why)
		return
	}
	if err := signalPID(p.PID, sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		devStackWarn("signal %v to pid %d (%s) failed: %v", sig, p.PID, p.Name, err)
		return
	}
	devStackLog("sent %v to pid %d (%s, %s)", sig, p.PID, p.Name, p.WorktreePath)
}
