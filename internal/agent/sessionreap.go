package agent

import (
	"errors"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/drn/argus/internal/sessiontag"
	"github.com/drn/argus/internal/uxlog"
)

// Session-tag descendant reaping (fix-playwright-orphans). Every spawned
// session carries a per-spawn sessiontag in its env; every descendant inherits
// it — including Playwright browsers, which launch detached (setsid) and so
// escape the session's process group, and processes reparented to launchd
// after Claude Code's Bash tool timed out their shell. Argus's own SIGTERM
// reaches only the session's root PID, so on exit we find and kill whatever
// still carries the tag. See context/knowledge/gotchas/daemon-rpc.md.

const (
	// sessionReapGrace lets graceful shutdown (Playwright MCP closing Chrome
	// on stdin EOF) finish before we start signalling.
	sessionReapGrace = 2 * time.Second
	// sessionReapKillAfter is the SIGTERM → SIGKILL escalation window.
	sessionReapKillAfter = 3 * time.Second
)

// reapHooks are the reaper's test seams. They live behind an atomic pointer
// because session-exit goroutines from unrelated tests read them
// asynchronously; a plain package-var swap would be a data race. enabled is
// false under `go test` so sessions spawned by other tests never scan the
// host's process table; reaper tests install fakes via setReapHooks.
type reapHooks struct {
	enabled bool
	list    func() (map[int]string, error)
	signal  func(pid int, sig syscall.Signal) error
	alive   func(pid int) bool
	sleep   func(time.Duration)
}

var currentReapHooks atomic.Pointer[reapHooks]

func init() {
	currentReapHooks.Store(&reapHooks{
		enabled: !isTestBinary(),
		list:    listTaggedProcs,
		signal:  func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		alive:   processAlive,
		sleep:   time.Sleep,
	})
}

// processAlive reports whether pid exists (EPERM means it exists but is not
// ours to signal — still alive).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// reapSessionTag kills every process still carrying tag after a session has
// exited. Blocking (grace + escalation window); callers run it in a goroutine.
// Returns the pids sent SIGTERM and SIGKILL, for tests.
func reapSessionTag(taskID, tag string) (termed, killed []int) {
	h := currentReapHooks.Load()
	if !h.enabled || tag == "" {
		return nil, nil
	}
	h.sleep(sessionReapGrace)
	return reapMatching(h, "task="+taskID, func(t string) bool { return t == tag })
}

// SweepOrphanedSessionProcs reaps tagged processes whose owning argus process
// (the tag's owner PID) is no longer alive — the leak left when a supervisor
// or in-process runner died with live sessions, so its per-session reapers
// never ran. Safe to call from any startup: tags owned by a live runner (this
// one, another supervisor, the test server, a TUI fallback) are skipped.
// Blocking; call in a goroutine.
func SweepOrphanedSessionProcs() (termed, killed []int) {
	h := currentReapHooks.Load()
	if !h.enabled {
		return nil, nil
	}
	self := os.Getpid()
	return reapMatching(h, "startup-sweep", func(t string) bool {
		owner, ok := sessiontag.OwnerPID(t)
		return ok && owner != self && !h.alive(owner)
	})
}

// reapMatching SIGTERMs every tagged process whose tag satisfies match, waits
// sessionReapKillAfter, re-scans (never trusting remembered pids, which may
// have been recycled), and SIGKILLs survivors. Never signals this process.
func reapMatching(h *reapHooks, label string, match func(tag string) bool) (termed, killed []int) {
	find := func() []int {
		procs, err := h.list()
		if err != nil {
			uxlog.Log("[sessionreap] %s: list processes failed: %v", label, err)
			return nil
		}
		self := os.Getpid()
		var pids []int
		for pid, tag := range procs {
			if pid != self && match(tag) {
				pids = append(pids, pid)
			}
		}
		return pids
	}
	signal := func(pids []int, sig syscall.Signal) []int {
		var sent []int
		for _, pid := range pids {
			if err := h.signal(pid, sig); err != nil {
				if !errors.Is(err, syscall.ESRCH) {
					uxlog.Log("[sessionreap] %s: signal %v pid=%d failed: %v", label, sig, pid, err)
				}
				continue
			}
			sent = append(sent, pid)
		}
		return sent
	}

	pids := find()
	if len(pids) == 0 {
		uxlog.Log("[sessionreap] %s: no leftover tagged processes", label)
		return nil, nil
	}
	termed = signal(pids, syscall.SIGTERM)
	uxlog.Log("[sessionreap] %s: SIGTERM %d leftover process(es) %v", label, len(termed), termed)
	h.sleep(sessionReapKillAfter)
	if survivors := find(); len(survivors) > 0 {
		killed = signal(survivors, syscall.SIGKILL)
		uxlog.Log("[sessionreap] %s: SIGKILL %d survivor(s) %v", label, len(killed), killed)
	}
	return termed, killed
}
