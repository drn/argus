// Package skew evaluates binary skew between the TUI's own build and the
// long-lived processes it talks to: the daemon and the session-supervisor.
//
// It is the pure decision core, extracted from cmd/argus so BOTH the startup
// path and the TUI's running tick can re-run the same evaluation
// (reduce-supervisor-skew-blast-radius). Before this, evaluateSkew ran exactly
// once in main(), so a TUI left running for days across several `go install`s
// never re-checked — skew was discovered whenever the operator happened to
// relaunch, which is precisely why it kept surfacing mid-incident.
//
// The two processes are judged on DIFFERENT signals, on purpose:
//
//   - The DAEMON is judged on the whole-binary content hash, unchanged. It owns
//     coordination, not PTYs, and bouncing it re-attaches to live sessions, so a
//     false positive costs a cheap restart.
//   - The SUPERVISOR is judged on its declared EXECUTED SURFACE
//     (daemon.SurfaceVersion), not the hash. It is the always-parent PTY owner:
//     restarting it SIGHUPs every running agent. ~9 in 10 builds change nothing
//     it runs while changing the whole-binary hash, so hash-based staleness cried
//     wolf nine times out of ten for a remedy that costs a fleet of agents.
//
// Detection is best-effort throughout: any failed step (RPC error, missing
// binary, hash/stat failure) yields "not stale", so a benign local error never
// nags the operator into a needless restart.
package skew

import (
	"os"
	"path/filepath"
	"time"

	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/daemon"
	"github.com/drn/argus/internal/uxlog"
)

// BootInfoProvider is the subset of the daemon client's API an evaluation needs.
// Narrowing to an interface lets tests drive the full wiring (os.Executable →
// BinaryHashFile → the decisions) against the test binary with a fake response,
// without a live daemon.
type BootInfoProvider interface {
	BootInfo() (daemon.BootInfoResp, error)
}

// Result carries one skew evaluation: whether the daemon runs a different binary
// than the TUI, the TIERED supervisor verdict, and each process's rich display
// identity (commit SHA + dirty flag + resolved path, short content-hash fallback)
// for the modal and the doctor table.
type Result struct {
	DaemonStale bool

	// Supervisor is the tiered verdict — coherent / unknown / spawn-stale /
	// stream-stale. Only the stream tier justifies interrupting agents.
	Supervisor daemon.SurfaceSkew

	DaemonIdentity     string
	SupervisorIdentity string
}

// SupervisorStale reports whether the supervisor is genuinely behind. An
// unknown (pre-v6) supervisor is deliberately NOT stale.
func (r Result) SupervisorStale() bool { return r.Supervisor.Stale() }

// NeedsBlockingPrompt reports whether this evaluation warrants the blocking
// startup modal.
//
// A stale daemon does (the restart is cheap and does not touch agents), and so
// does a STREAM-surface supervisor mismatch (live sessions are genuinely
// affected). A spawn-only mismatch does NOT: it cannot affect a single running
// agent, so blocking the operator on it is the false alarm this change exists to
// remove — it takes the transient status-bar notice instead (design D6).
func (r Result) NeedsBlockingPrompt() bool {
	return r.DaemonStale || r.Supervisor.AffectsLiveSessions()
}

// Notice renders the one-line status-bar text for a non-empty verdict, or "" when
// there is nothing to say. Used for post-startup discoveries and for a spawn-only
// mismatch at startup.
func (r Result) Notice() string {
	switch {
	case r.DaemonStale && r.Supervisor.Stale():
		return "Daemon and supervisor are behind this build — " + r.Supervisor.Consequence()
	case r.DaemonStale:
		return "Daemon is running an older build — restart it (agents are unaffected)"
	case r.Supervisor.Stale():
		return "Supervisor is running an older build — " + r.Supervisor.Consequence()
	default:
		return ""
	}
}

// Evaluate fetches the daemon's BootInfo once and evaluates daemon + supervisor
// skew against the TUI's own on-disk binary.
//
// checkDaemon gates the DAEMON decision only (the startup caller passes
// preExisting): a daemon the TUI just auto-started forks the current binary and
// can never be stale, so checking it would only produce false alarms. The
// SUPERVISOR decision is ALWAYS evaluated when a supervisor is present — it is a
// long-lived process the TUI did not fork, so it can be stale even on the
// auto-start path.
func Evaluate(client BootInfoProvider, checkDaemon bool) Result {
	var res Result
	info, err := client.BootInfo()
	if err != nil {
		uxlog.Log("[skew] BootInfo failed: %v", err)
		return res
	}
	exe, err := os.Executable()
	if err != nil {
		return res
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}

	// Read the TUI binary's identity. The hash is still needed for the DAEMON
	// decision and for display; the mtime fallback serves only a pre-BinaryHash
	// daemon. The supervisor decision no longer needs it at all.
	var (
		tuiHash     string
		tuiHashErr  bool
		tuiMtime    time.Time
		tuiMtimeErr bool
	)
	if info.BinaryHash != "" || (info.SupervisorPresent && info.SupervisorHash != "") {
		tuiHash, err = daemon.BinaryHashFile(exe)
		tuiHashErr = err != nil
	}
	if info.BinaryHash == "" {
		if st, serr := os.Stat(exe); serr == nil {
			tuiMtime = st.ModTime()
		} else {
			tuiMtimeErr = true
		}
	}

	if checkDaemon && DaemonStale(info, tuiHash, tuiHashErr, tuiMtime, tuiMtimeErr) {
		res.DaemonStale = true
		if info.BinaryHash != "" {
			uxlog.Log("[skew] daemon binary stale: daemon hash=%s tui hash=%s",
				ShortHash(info.BinaryHash), ShortHash(tuiHash))
		} else {
			uxlog.Log("[skew] daemon binary stale: daemon mtime=%s tui mtime=%s",
				info.BinaryMtime.Format(time.RFC3339), tuiMtime.Format(time.RFC3339))
		}
	}

	res.Supervisor = SupervisorVerdict(info, tuiHash, tuiHashErr)
	if res.Supervisor != daemon.SurfaceCoherent {
		uxlog.Log("[skew] supervisor %s: reported surface=%s this build=%s (binary hash=%s tui hash=%s)",
			res.Supervisor, SupervisorSurface(info), daemon.CurrentSupervisorSurface(),
			ShortHash(info.SupervisorHash), ShortHash(tuiHash))
	}

	res.DaemonIdentity = FormatIdentity(info.VCS, info.BinaryHash, info.BinaryPath)
	if info.SupervisorPresent {
		res.SupervisorIdentity = FormatIdentity(info.SupervisorVCS, info.SupervisorHash, info.SupervisorPath)
	}
	return res
}

// SupervisorSurface reads the supervisor's reported surface version out of a
// BootInfo response. The zero value means a pre-v6 supervisor reported none.
func SupervisorSurface(info daemon.BootInfoResp) daemon.SurfaceVersion {
	return daemon.SurfaceVersion{
		Spawn:  info.SupervisorSpawnSurface,
		Stream: info.SupervisorStreamSurface,
	}
}

// SupervisorVerdict is the pure supervisor verdict over a BootInfo response.
//
// It keys on the EXECUTED SURFACE, not the binary hash: a supervisor whose whole-
// binary hash differs but whose surface version matches is COHERENT, because the
// build changed nothing it runs. That is the ~90% case, and reporting it as skew
// is what trained the operator to distrust the signal. No supervisor at all is
// likewise coherent — nothing can be stale.
//
// A supervisor that reports NO surface version (pre-v6) is never stale on that
// basis alone — the missing field is the additive-protocol feature-detect, not
// evidence. But the whole-binary hash remains its FALLBACK signal, exactly as
// before surface versions existed, so a genuine skew during the one-release
// transition window is not silently dropped; that case reports as
// SurfaceLegacyStale, whose tier is unknowable and therefore treated as the
// stricter one. An unreadable hash on either side degrades to unknown, never to
// a false stale.
func SupervisorVerdict(info daemon.BootInfoResp, tuiHash string, tuiHashErr bool) daemon.SurfaceSkew {
	if !info.SupervisorPresent {
		return daemon.SurfaceCoherent
	}
	if reported := SupervisorSurface(info); reported.Known() {
		return daemon.CompareSupervisorSurface(reported)
	}
	if info.SupervisorHash == "" || tuiHashErr || tuiHash == "" {
		return daemon.SurfaceUnknown
	}
	if tuiHash != info.SupervisorHash {
		return daemon.SurfaceLegacyStale
	}
	return daemon.SurfaceUnknown
}

// DaemonStale decides daemon staleness from its boot identity and the TUI
// binary's already-read current identity. Split out from the I/O so every branch
// (hash match/mismatch, hash-read failure, mtime fallback, mtime-read failure) is
// unit-testable without a live client or a real executable.
//
// The signal is a content hash, NOT mtime: `go install` rewrites the binary
// (bumping its mtime) on every run even when the deterministic build is
// byte-identical, which flagged the daemon stale on every launch for habitual
// reinstallers. A read failure of the TUI's own binary yields "not stale" — a
// benign local error must never nag the user into a needless restart.
func DaemonStale(info daemon.BootInfoResp, tuiHash string, tuiHashErr bool, tuiMtime time.Time, tuiMtimeErr bool) bool {
	if info.BinaryHash != "" {
		if tuiHashErr {
			return false
		}
		return tuiHash != info.BinaryHash
	}
	// Fallback: pre-BinaryHash daemon. Compare mtime.
	if info.BinaryMtime.IsZero() || tuiMtimeErr {
		return false // older daemon without BootInfo, or stat failed.
	}
	return !tuiMtime.Equal(info.BinaryMtime)
}

// FormatIdentity renders a process's display identity: the commit SHA + dirty
// flag when VCS build info is present, else the short content hash, then the
// resolved path. Mirrors internal/doctor.displayIdentity. Display-only — it never
// participates in any staleness decision.
func FormatIdentity(v buildid.VCS, hash, path string) string {
	var ident string
	switch {
	case v.Present():
		ident = ShortHash(v.Revision)
		if v.Modified {
			ident += " (dirty)"
		}
	case hash != "":
		ident = "sha:" + ShortHash(hash)
	default:
		ident = "unknown"
	}
	if path != "" {
		return ident + " @ " + path
	}
	return ident
}

// ShortHash truncates a hex digest for compact logging and display.
func ShortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
