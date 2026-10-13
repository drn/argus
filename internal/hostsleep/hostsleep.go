// Package hostsleep diagnoses whether the host will sleep out from under a
// running Argus daemon while the remote API / web app is enabled.
package hostsleep

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// Status classifies the host's sleep posture relative to the remote API.
type Status string

const (
	// NotApplicable: the remote API is off, or the host is not macOS. Never a warning.
	NotApplicable Status = "not_applicable"
	// Safe: AC sleep is disabled (pmset sleep 0), so the host stays awake on its own.
	Safe Status = "safe"
	// Held: a caffeinate assertion is bound to the daemon's PID.
	Held Status = "held"
	// AtRisk: the host can idle-sleep and nothing holds it awake; the web app
	// goes unreachable whenever that happens.
	AtRisk Status = "at_risk"
	// Unknown: pmset could not be read or parsed. Reported, never assumed safe or at risk.
	Unknown Status = "unknown"
)

// FixCommands are the two remedies shown wherever AtRisk is reported.
const (
	PermanentFix = "sudo pmset -c sleep 0 displaysleep 10"
	TemporaryFix = "caffeinate -is -w $(cat ~/.argus/daemon.pid)"
)

// Report is the diagnosis plus a user-facing sentence. Message is empty unless
// the status warrants showing something.
type Report struct {
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// Warn reports whether the report should render as a warning.
func (r Report) Warn() bool { return r.Status == AtRisk }

// Inputs are the already-gathered facts Diagnose decides on.
type Inputs struct {
	APIEnabled   bool
	Supported    bool // darwin
	ACSleepMin   int  // minutes; 0 = never; -1 = unknown
	HeldByDaemon bool
	GatherErr    error
}

// Diagnose is pure: the same facts always give the same report.
func Diagnose(in Inputs) Report {
	if !in.APIEnabled || !in.Supported {
		return Report{Status: NotApplicable}
	}
	if in.GatherErr != nil || in.ACSleepMin < 0 {
		return Report{Status: Unknown, Message: "Could not read macOS power settings (pmset); sleep risk unknown."}
	}
	if in.ACSleepMin == 0 {
		return Report{Status: Safe}
	}
	if in.HeldByDaemon {
		return Report{Status: Held, Message: "Held awake by caffeinate while the daemon runs (does not survive a daemon restart or reboot)."}
	}
	return Report{
		Status:  AtRisk,
		Message: fmt.Sprintf("This Mac sleeps after %d min idle on AC, which makes the web app unreachable until it wakes.", in.ACSleepMin),
		Fix:     PermanentFix,
	}
}

var (
	acSleepRe = regexp.MustCompile(`(?m)^\s*sleep\s+(\d+)\b`)
	heldByRe  = regexp.MustCompile(`on behalf of Process ID (\d+)`)
)

// ParseACSleep extracts the AC-power "sleep" minutes from `pmset -g custom`.
func ParseACSleep(out string) (int, bool) {
	_, section, found := strings.Cut(out, "AC Power:")
	if !found {
		return -1, false
	}
	if before, _, ok := strings.Cut(section, "Power:"); ok {
		section = before
	}
	m := acSleepRe.FindStringSubmatch(section)
	if m == nil {
		return -1, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return -1, false
	}
	return n, true
}

// ParseHeldBy reports whether `pmset -g assertions` shows a caffeinate
// assertion created on behalf of pid.
func ParseHeldBy(out string, pid int) bool {
	if pid <= 0 {
		return false
	}
	for line := range strings.SplitSeq(out, "\n") {
		if !strings.Contains(line, "caffeinate asserting on behalf of") {
			continue
		}
		if m := heldByRe.FindStringSubmatch(line); m != nil {
			if got, err := strconv.Atoi(m[1]); err == nil && got == pid {
				return true
			}
		}
	}
	return false
}

// DaemonPID reads the daemon's pid file; 0 when absent or malformed.
func DaemonPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// runPmset is the shell-out seam; tests replace it.
var runPmset = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "pmset", args...).Output()
	return string(out), err
}

var goos = runtime.GOOS

// Check shells out to pmset (3s timeout); UI callers should use Monitor.
func Check(apiEnabled bool, daemonPID int) Report {
	if !apiEnabled || goos != "darwin" {
		return Report{Status: NotApplicable}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	custom, err := runPmset(ctx, "-g", "custom")
	if err != nil {
		return Diagnose(Inputs{APIEnabled: true, Supported: true, GatherErr: err})
	}
	sleepMin, ok := ParseACSleep(custom)
	if !ok {
		return Diagnose(Inputs{APIEnabled: true, Supported: true, ACSleepMin: -1})
	}
	held := false
	if sleepMin != 0 {
		// Assertion read failure degrades to "not held", the conservative direction.
		if asserts, aerr := runPmset(ctx, "-g", "assertions"); aerr == nil {
			held = ParseHeldBy(asserts, daemonPID)
		}
	}
	return Diagnose(Inputs{APIEnabled: true, Supported: true, ACSleepMin: sleepMin, HeldByDaemon: held})
}

// Monitor serves a cached Report without blocking; stale values refresh in the background and a key change discards the cache.
type Monitor struct {
	TTL time.Duration
	// OnUpdate, when set, runs (outside the lock) after a background check lands.
	OnUpdate func()

	mu       sync.Mutex
	report   Report
	at       time.Time
	key      string
	inFlight bool
	check    func(bool, int) Report
	anyOS    bool // test seam: skip the darwin gate
}

// NewMonitorForTest returns a Monitor with an injected check, immediately stale
// and not gated on the host OS, so cross-package tests can drive it.
func NewMonitorForTest(check func(bool, int) Report) *Monitor {
	return &Monitor{TTL: 0, check: check, anyOS: true}
}

// NewMonitor returns a Monitor with a 60s TTL.
func NewMonitor() *Monitor { return &Monitor{TTL: 60 * time.Second, check: Check} }

// Get returns the cached report for (apiEnabled, pid), refreshing in the background when stale.
func (m *Monitor) Get(apiEnabled bool, pid int) Report {
	if !apiEnabled || (goos != "darwin" && !m.anyOS) {
		return Report{Status: NotApplicable}
	}
	key := fmt.Sprintf("%t/%d", apiEnabled, pid)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.key != key {
		m.key, m.report, m.at = key, Report{Status: NotApplicable}, time.Time{}
	}
	if time.Since(m.at) >= m.TTL && !m.inFlight {
		m.inFlight = true
		check := m.check
		go func() {
			r := check(apiEnabled, pid)
			m.mu.Lock()
			stored := m.key == key
			if stored {
				m.report, m.at = r, time.Now()
			}
			m.inFlight = false
			onUpdate := m.OnUpdate
			m.mu.Unlock()
			uxlog.Log("[hostsleep] check status=%s stored=%t", r.Status, stored)
			if onUpdate != nil {
				onUpdate()
			}
		}()
	}
	return m.report
}
