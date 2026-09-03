// Package doctor holds the pure, I/O-free core of the `argus doctor` command:
// the binary-coherence verdict and its human-readable rendering.
//
// argus runs as three cooperating processes that each load a binary from disk —
// the TUI (`argus` on $PATH), the daemon (launched from ~/.argus/argusd), and
// the session-supervisor. `go install` can update one of these files while the
// others keep running the old bytes (restart-needed) or, worse, leave the
// daemon symlink pointing at a DIFFERENT file than $PATH argus (path-divergence)
// — where a plain restart just relaunches the divergent binary and loops.
//
// Diagnose takes the already-gathered identities (the I/O of resolving paths,
// hashing files, and querying the daemon lives in cmd/argus) and returns the
// verdict plus the exact remediation. Keeping it pure makes every branch
// unit-testable without spawning processes.
//
// The gating signal is the SHA-256 content hash for every actor EXCEPT the
// session-supervisor, which is judged on its declared executed-surface version
// instead (reduce-supervisor-skew-blast-radius). The supervisor is the always-
// parent PTY owner, so restarting it SIGHUPs every running agent — and ~9 in 10
// builds change nothing it executes while changing the whole-binary hash, which
// made a fleet-killing remedy fire on a signal that was right one time in ten.
// A supervisor whose surface matches is coherent however its hash reads, so this
// command now exits 0 in cases where it used to exit 1 (design D5). Both hashes
// and both surface versions stay in the printed table. VCS info (commit SHA +
// dirty flag) remains display-only and never gates.
package doctor

import (
	"fmt"
	"strings"

	"github.com/drn/argus/internal/buildid"
)

// Role identifies which of the six binary identities an Actor represents: three
// on-disk locations that anchor divergence detection and three live processes.
type Role int

const (
	RolePathArgus    Role = iota // the `argus` binary resolved on $PATH
	RoleArgusdTarget             // the ~/.argus/argusd symlink target (what the daemon launches from)
	RoleGoInstall                // the $(go env GOPATH)/bin/argus target
	RoleDaemon                   // the running daemon process (from BootInfo)
	RoleSupervisor               // the running session-supervisor process (relayed via BootInfo)
	RoleTUI                      // this process / os.Executable
)

// String returns the table label for a role.
func (r Role) String() string {
	switch r {
	case RolePathArgus:
		return "PATH argus"
	case RoleArgusdTarget:
		return "~/.argus/argusd"
	case RoleGoInstall:
		return "go install target"
	case RoleDaemon:
		return "daemon"
	case RoleSupervisor:
		return "supervisor"
	case RoleTUI:
		return "TUI (this binary)"
	default:
		return fmt.Sprintf("role(%d)", int(r))
	}
}

// isStaleCandidate reports whether a role names a long-lived process that can be
// running older bytes than the on-disk binary (the daemon and supervisor). The
// TUI/self is never a stale candidate — `argus doctor` just launched from the
// current on-disk binary, so it is the "current" reference, not the offender.
func (r Role) isStaleCandidate() bool {
	return r == RoleDaemon || r == RoleSupervisor
}

// Actor is one gathered binary identity row. A row that could not be resolved
// (missing symlink, no daemon, pre-v3 supervisor with an empty hash) has
// Resolved=false and is excluded from the verdict — it degrades to "unknown"
// and never aborts the command or produces a false positive.
type Actor struct {
	Role         Role
	ResolvedPath string      // fully symlink-resolved path; "" when unresolved
	Hash         string      // SHA-256 content hash; "" when unhashed/unknown
	VCS          buildid.VCS // display-only commit SHA + dirty flag; blank outside a git tree
	Resolved     bool        // false ⇒ unknown row, excluded from the verdict
	Note         string      // optional human note, e.g. "no supervisor", "old protocol"

	// The declared EXECUTED-SURFACE version, as plain ints so this package stays
	// pure (it must not drag internal/daemon's dependency tree in). Meaningful on
	// two rows only: RoleSupervisor carries what the LIVE supervisor reports, and
	// RoleTUI carries what THIS build implements — the TUI is already this
	// package's "current" reference. Both zero elsewhere, and zero on a
	// supervisor too old to report one.
	//
	// This, not Hash, is what decides supervisor staleness: ~9 in 10 builds change
	// nothing the supervisor runs yet change the whole-binary hash, and the remedy
	// that hash pointed at costs every running agent. Both hashes stay in the
	// table — only the pass/fail line moved (design D5).
	SpawnSurface  int // spawn-surface component (0 ⇒ unknown / not applicable)
	StreamSurface int // stream-surface component (0 ⇒ unknown / not applicable)
}

// surfaceKnown reports whether this row carries a surface version at all.
func (a Actor) surfaceKnown() bool { return a.SpawnSurface != 0 || a.StreamSurface != 0 }

// surfaceString renders the row's surface version for the table.
func (a Actor) surfaceString() string {
	if !a.surfaceKnown() {
		return "unknown"
	}
	return fmt.Sprintf("spawn=%d stream=%d", a.SpawnSurface, a.StreamSurface)
}

// Verdict is the overall binary-coherence classification.
type Verdict int

const (
	// Healthy: every resolvable actor resolves to the same file with a matching hash.
	Healthy Verdict = iota
	// RestartNeeded: a running process is on older bytes of the SAME file (a rebuild
	// happened). A restart fixes it.
	RestartNeeded
	// PathDivergence: the daemon symlink target and $PATH argus resolve to DIFFERENT
	// files. The real footgun — a plain restart relaunches the divergent binary and loops.
	PathDivergence
)

// String returns the uppercase verdict label used in the printed output.
func (v Verdict) String() string {
	switch v {
	case Healthy:
		return "HEALTHY"
	case RestartNeeded:
		return "RESTART NEEDED"
	case PathDivergence:
		return "PATH DIVERGENCE"
	default:
		return fmt.Sprintf("VERDICT(%d)", int(v))
	}
}

// Result is the pure verdict output: the classification plus the exact
// remediation text (empty when Healthy).
type Result struct {
	Verdict     Verdict
	Remediation string
}

// Diagnose classifies binary coherence from the gathered identities. It is pure
// (no I/O). Priority order matters: path-divergence is checked FIRST because a
// plain restart would loop on it, so it must never be misreported as the
// milder restart-needed.
func Diagnose(actors []Actor) Result {
	byRole := make(map[Role]Actor, len(actors))
	for _, a := range actors {
		byRole[a.Role] = a
	}

	// 1. Path divergence — the footgun. The daemon launches from ~/.argus/argusd
	//    and the TUI from $PATH argus; if those resolve to different files, a
	//    daemon restart relaunches the same divergent binary forever.
	if pa, ok := resolved(byRole, RolePathArgus); ok {
		if at, ok := resolved(byRole, RoleArgusdTarget); ok && at.ResolvedPath != pa.ResolvedPath {
			return Result{PathDivergence, pathDivergenceFix(pa, at)}
		}
	}
	//    Also catch it directly between a live process and the TUI: if a running
	//    daemon/supervisor resolves to a different file than this binary, the
	//    same loop applies even when the disk anchors above were unresolvable.
	if tui, ok := resolved(byRole, RoleTUI); ok {
		for _, role := range []Role{RoleDaemon, RoleSupervisor} {
			if p, ok := resolved(byRole, role); ok && p.ResolvedPath != tui.ResolvedPath {
				return Result{PathDivergence, pathDivergenceFixProc(tui, p)}
			}
		}
	}

	// 2. Supervisor surface skew. Judged BEFORE the hash comparison and
	//    independently of it: the supervisor's executed surface is the verdict
	//    signal now, so a surface mismatch is restart-needed even when no
	//    hash-bearing pair happens to be resolvable, and — see supervisorSurfaceSkew
	//    below — a surface MATCH excuses the supervisor from the hash loop
	//    entirely.
	sup, supOK := resolved(byRole, RoleSupervisor)
	surfaceSkew := supervisorSurfaceSkew(byRole)
	if surfaceSkew != surfaceCoherent && surfaceSkew != surfaceUnknown {
		return Result{RestartNeeded, supervisorSurfaceFix(sup, surfaceSkew)}
	}

	// 3. Restart needed — a process on old bytes of the SAME file. Any two
	//    resolved actors that share a resolved path but differ in content hash
	//    prove a rebuild landed while a process kept the old image. (Two on-disk
	//    anchors at one path can never differ, so a hit always involves a process.)
	//
	//    The supervisor is EXEMPT from this loop once its surface is known to
	//    match: a build that changes nothing it executes is not skew, whatever the
	//    whole-binary hashes say. That exemption is the entire point of the
	//    change, and it is why `argus doctor` now exits 0 in cases where it used
	//    to exit 1 (design D5). A supervisor whose surface is UNKNOWN (pre-v6)
	//    stays in the loop, so the hash remains its fallback signal.
	supExempt := supOK && surfaceSkew == surfaceCoherent
	res := resolvedList(actors)
	for i := range res {
		for j := i + 1; j < len(res); j++ {
			a, b := res[i], res[j]
			if supExempt && (a.Role == RoleSupervisor || b.Role == RoleSupervisor) {
				continue
			}
			if a.ResolvedPath != "" && a.ResolvedPath == b.ResolvedPath &&
				a.Hash != "" && b.Hash != "" && a.Hash != b.Hash {
				return Result{RestartNeeded, restartFix(a, b)}
			}
		}
	}

	return Result{Verdict: Healthy}
}

// surfaceVerdict is the supervisor's executed-surface classification, kept local
// to this package so it stays pure (internal/daemon owns the wire-level twin).
type surfaceVerdict int

const (
	surfaceCoherent    surfaceVerdict = iota // reported surface matches this build's
	surfaceUnknown                           // supervisor reports none, or there is no supervisor
	surfaceSpawnStale                        // spawn component differs; running agents unaffected
	surfaceStreamStale                       // stream component differs; live sessions affected
)

// supervisorSurfaceSkew compares the live supervisor's reported surface against
// this build's (carried on the TUI row, which is already this package's "current"
// reference). Anything unreported on either side yields surfaceUnknown — never a
// verdict, so the hash keeps serving as the fallback for an older supervisor.
// Stream outranks spawn: when both differ the consequence is the larger one.
func supervisorSurfaceSkew(byRole map[Role]Actor) surfaceVerdict {
	sup, ok := resolved(byRole, RoleSupervisor)
	if !ok || !sup.surfaceKnown() {
		return surfaceUnknown
	}
	tui, ok := resolved(byRole, RoleTUI)
	if !ok || !tui.surfaceKnown() {
		return surfaceUnknown
	}
	switch {
	case sup.StreamSurface != tui.StreamSurface:
		return surfaceStreamStale
	case sup.SpawnSurface != tui.SpawnSurface:
		return surfaceSpawnStale
	default:
		return surfaceCoherent
	}
}

// supervisorSurfaceFix builds remediation for a surface mismatch, TIERED by what
// it actually costs. That tiering is the point: a spawn-surface change cannot
// reach a single running agent, so telling the operator to restart — and lose 25
// PTYs — would be the same false alarm in a new coat.

func supervisorSurfaceFix(sup Actor, v surfaceVerdict) string {
	var b strings.Builder
	if v == surfaceSpawnStale {
		b.WriteString("The session-supervisor's SPAWN surface is behind this build")
		if sup.ResolvedPath != "" {
			fmt.Fprintf(&b, " (%s)", sup.ResolvedPath)
		}
		b.WriteString(".\n")
		b.WriteString("Already-running agents are UNAFFECTED — the spawn stack is read only when a session starts.\n")
		b.WriteString("Sessions started from now on will use the previous build's spawn configuration\n")
		b.WriteString("(command, sandbox profile, skills injection, secrets, cache dirs).\n")
		b.WriteString("Fix when convenient — the restart interrupts every running agent, so pick your moment:\n")
		b.WriteString("    argus session-supervisor stop   # the daemon auto-restarts it on next need")
		return b.String()
	}
	b.WriteString("The session-supervisor's STREAM surface is behind this build")
	if sup.ResolvedPath != "" {
		fmt.Fprintf(&b, " (%s)", sup.ResolvedPath)
	}
	b.WriteString(".\n")
	b.WriteString("LIVE sessions are affected — the PTY read loop, ring buffer, session log and\n")
	b.WriteString("R/S handlers serving your running agents are the previous build's.\n")
	b.WriteString("Restarting the supervisor INTERRUPTS every running agent — use the skew modal's guarded restart, or if no agents are running:\n")
	b.WriteString("    argus session-supervisor stop   # the daemon auto-restarts it on next need")
	return b.String()
}

// resolved returns the actor for a role only when it is present and resolved.
func resolved(byRole map[Role]Actor, r Role) (Actor, bool) {
	a, ok := byRole[r]
	if !ok || !a.Resolved {
		return Actor{}, false
	}
	return a, true
}

// resolvedList returns only the resolved actors, preserving order.
func resolvedList(actors []Actor) []Actor {
	out := make([]Actor, 0, len(actors))
	for _, a := range actors {
		if a.Resolved {
			out = append(out, a)
		}
	}
	return out
}

// pathDivergenceFix builds remediation naming the two diverging disk anchors.
func pathDivergenceFix(pathArgus, argusd Actor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Your PATH `argus` and the daemon's ~/.argus/argusd symlink resolve to DIFFERENT files:\n")
	fmt.Fprintf(&b, "    PATH argus:      %s\n", pathArgus.ResolvedPath)
	fmt.Fprintf(&b, "    ~/.argus/argusd: %s\n", argusd.ResolvedPath)
	b.WriteString("A plain daemon restart would relaunch the same divergent binary and loop.\n")
	b.WriteString("Fix — rebuild once and re-point the symlink at that build, then restart:\n")
	b.WriteString("    go install ./cmd/argus/   # refresh your PATH argus\n")
	b.WriteString("    argus daemon install      # re-point ~/.argus/argusd at it (macOS)\n")
	b.WriteString("    argus daemon restart")
	return b.String()
}

// pathDivergenceFixProc builds remediation when a live process resolves to a
// different file than this binary.
func pathDivergenceFixProc(tui, proc Actor) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %s is running from a different file than your PATH/TUI argus:\n", proc.Role)
	fmt.Fprintf(&b, "    TUI argus: %s\n", tui.ResolvedPath)
	fmt.Fprintf(&b, "    %s: %s\n", proc.Role, proc.ResolvedPath)
	b.WriteString("A plain restart may relaunch the divergent binary. Rebuild and reinstall so all resolve to one file:\n")
	b.WriteString("    go install ./cmd/argus/\n")
	b.WriteString("    argus daemon install\n")
	b.WriteString("    argus daemon restart")
	return b.String()
}

// restartFix builds remediation for a same-path hash mismatch. It identifies the
// stale process (daemon or supervisor) in the pair and prints the matching
// restart command — flagging the supervisor's agent-interrupting caveat.
func restartFix(a, b Actor) string {
	stale := a
	if b.Role.isStaleCandidate() && !a.Role.isStaleCandidate() {
		stale = b
	}
	var sb strings.Builder
	if stale.Role == RoleSupervisor {
		fmt.Fprintf(&sb, "The session-supervisor is running an older build than the binary on disk at:\n    %s\n", stale.ResolvedPath)
		sb.WriteString("Restarting the supervisor INTERRUPTS every running agent — use the skew modal's guarded restart, or if no agents are running:\n")
		sb.WriteString("    argus session-supervisor stop   # the daemon auto-restarts it on next need")
		return sb.String()
	}
	fmt.Fprintf(&sb, "The daemon is running an older build than the binary on disk at:\n    %s\n", stale.ResolvedPath)
	sb.WriteString("Fix — restart the daemon (running agents are unaffected; they live on the supervisor):\n")
	sb.WriteString("    argus daemon restart")
	return sb.String()
}

// Render builds the full human-readable report: a table of every actor's
// identity, the verdict, and any remediation. Pure — the caller prints the
// returned string.
func Render(actors []Actor) string {
	var b strings.Builder
	b.WriteString("argus doctor — binary coherence\n\n")

	label := func(r Role) string { return r.String() }
	width := 0
	for _, a := range actors {
		if n := len(label(a.Role)); n > width {
			width = n
		}
	}
	for _, a := range actors {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, label(a.Role), displayIdentity(a))
	}

	res := Diagnose(actors)
	fmt.Fprintf(&b, "\nVerdict: %s\n", res.Verdict)
	if res.Remediation != "" {
		b.WriteString("\n")
		b.WriteString(res.Remediation)
		b.WriteString("\n")
	}
	return b.String()
}

// displayIdentity formats one actor for the table: the commit SHA + dirty flag
// when VCS build info is present, else the short content hash, then the resolved
// path. Unresolved rows render as "unknown" plus their note.
func displayIdentity(a Actor) string {
	if !a.Resolved {
		if a.Note != "" {
			return fmt.Sprintf("unknown (%s)", a.Note)
		}
		return "unknown"
	}
	var ident string
	switch {
	case a.VCS.Present():
		ident = shortHash(a.VCS.Revision)
		if a.VCS.Modified {
			ident += " (dirty)"
		}
	case a.Hash != "":
		ident = "sha:" + shortHash(a.Hash)
	default:
		ident = "?"
	}
	if a.ResolvedPath != "" {
		ident = fmt.Sprintf("%s @ %s", ident, a.ResolvedPath)
	}
	// Both hashes AND both surface versions stay visible: the verdict moved to
	// the surface, but nothing became less inspectable (design D5). Only the
	// supervisor and TUI rows carry a surface at all.
	if a.Role == RoleSupervisor || a.Role == RoleTUI {
		ident += "  [surface " + a.surfaceString() + "]"
	}
	return ident
}

// shortHash truncates a hex digest (content hash or commit SHA) to 12 chars for
// compact display, mirroring cmd/argus's shortHash.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
