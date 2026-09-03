package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// The session-supervisor's EXECUTED SURFACE, and why it is not the binary hash.
//
// Three processes load the same `argus` file from disk: the TUI, the daemon,
// and the long-lived session-supervisor. `daemon.BinaryHashFile` hashes the
// WHOLE binary, so it reports the supervisor stale on every rebuild — but the
// supervisor executes a small, slow-moving slice of that binary. Measured over
// three months of master: 295 commits, 28 touching anything supervisor-resident,
// 13 touching its PTY/stream core. A whole-binary hash is therefore right about
// one time in ten, and the remedy it points at (a supervisor restart) SIGHUPs
// every running agent.
//
// So staleness is judged on a declared SURFACE VERSION describing the observable
// behavior of the code the supervisor actually runs. Equal surface versions mean
// coherent, whatever the hashes say. The hash stays reported and displayed —
// nothing becomes less inspectable — it just stops being the verdict.
//
// Two components, not one, because the consequences differ in KIND (design D4):
//
//   - SPAWN is read only when a session STARTS (BuildCmd, the sandbox wrapper,
//     skills/routing injection, secret resolution, cache-dir redirection). A
//     mismatch cannot touch a single running agent; the honest verdict is "new
//     agents will spawn with the previous build's config, restart when
//     convenient" — never a mid-incident emergency.
//   - STREAM serves LIVE sessions (the PTY read loop, the ring buffer, the
//     session-log writer, the R/S handlers, exit-info caching). This is the only
//     tier that justifies interrupting agents.
//
// Collapsing them would lose the one fact that decides whether killing 25 agents
// is warranted.
//
// These are HAND-BUMPED constants, deliberately, exactly like ProtocolVersion.
// The attractive alternative — hash the declared sources at build time and inject
// it with `-ldflags -X` — is structurally impossible on the real deploy path
// (design D3): `.iris.toml`'s build is `make install-signed`, whose body is a bare
// `go install ./cmd/argus`, which accepts no ldflags, and that is the exact file
// the daemon runs. The mechanical net is instead SurfaceDigest below, checked by
// a test that runs in CI.
//
// BUMPING RULE: change SupervisorStreamSurface when you change behavior the
// supervisor exhibits toward a session that is ALREADY RUNNING; change
// SupervisorSpawnSurface when you change how a session is CONSTRUCTED. When a
// change is both, bump both. When in genuine doubt, bump STREAM — an unnecessary
// stream bump costs one restart prompt, a missed one costs a silent false
// "coherent", which is the worst outcome this mechanism can produce.
const (
	// SupervisorSpawnSurface names the observable behavior of the spawn stack.
	//
	// History:
	//   - v1: initial declaration (reduce-supervisor-skew-blast-radius, Layer 1).
	SupervisorSpawnSurface = 1

	// SupervisorStreamSurface names the observable behavior of the live-session
	// stream core.
	//
	// History:
	//   - v1: initial declaration (reduce-supervisor-skew-blast-radius, Layer 1).
	SupervisorStreamSurface = 1
)

// SurfaceVersion is a supervisor's declared executed-surface identity: the pair
// of component versions it reports over Hello.
//
// The zero value means "not reported" — a pre-v6 supervisor omits both fields,
// so they decode as 0. That is the additive-protocol feature-detect, and it maps
// to SurfaceUnknown, never to stale (same treatment an empty BinaryHash gets).
type SurfaceVersion struct {
	Spawn  int
	Stream int
}

// CurrentSupervisorSurface is the surface version THIS binary implements.
func CurrentSupervisorSurface() SurfaceVersion {
	return SurfaceVersion{Spawn: SupervisorSpawnSurface, Stream: SupervisorStreamSurface}
}

// Known reports whether a surface version was reported at all. A supervisor
// speaking a protocol older than v6 reports the zero value.
func (v SurfaceVersion) Known() bool { return v.Spawn != 0 || v.Stream != 0 }

// String renders a surface version for logs and the doctor table.
func (v SurfaceVersion) String() string {
	if !v.Known() {
		return "unknown"
	}
	return fmt.Sprintf("spawn=%d stream=%d", v.Spawn, v.Stream)
}

// SurfaceSkew is the tiered supervisor-coherence verdict.
type SurfaceSkew int

const (
	// SurfaceCoherent: the supervisor runs the same executed surface as this
	// build. Its binary hash may well differ — that is the ~90% case this whole
	// mechanism exists to stop reporting as skew.
	SurfaceCoherent SurfaceSkew = iota
	// SurfaceUnknown: the supervisor reports no surface version (pre-v6). Present
	// but unidentifiable — NEVER treated as stale on that basis alone.
	SurfaceUnknown
	// SurfaceSpawnStale: only the spawn component differs. Running agents are
	// untouched; sessions started from now on use the previous build's spawn
	// configuration.
	SurfaceSpawnStale
	// SurfaceStreamStale: the stream component differs (possibly along with
	// spawn). Live sessions are affected — the only tier that justifies
	// interrupting agents.
	SurfaceStreamStale
	// SurfaceLegacyStale: a supervisor too old to report a surface version at
	// all, whose whole-binary hash nonetheless differs from this build's.
	//
	// The MISSING surface version is never itself evidence of staleness — that is
	// the additive-protocol feature-detect. But the hash remains the fallback
	// signal for a pre-v6 supervisor, exactly as it was before surface versions
	// existed, so a genuine skew in the one-release transition window is not
	// silently dropped. The tier is unknowable here, so it is treated as the
	// stricter one.
	SurfaceLegacyStale
)

// Stale reports whether a verdict means the supervisor is genuinely behind.
// Unknown is deliberately NOT stale.
func (s SurfaceSkew) Stale() bool {
	return s == SurfaceSpawnStale || s == SurfaceStreamStale || s == SurfaceLegacyStale
}

// AffectsLiveSessions reports whether a verdict means running agents are on
// affected code — the only condition under which interrupting them is warranted.
func (s SurfaceSkew) AffectsLiveSessions() bool {
	return s == SurfaceStreamStale || s == SurfaceLegacyStale
}

// String returns a short label for logs.
func (s SurfaceSkew) String() string {
	switch s {
	case SurfaceCoherent:
		return "coherent"
	case SurfaceUnknown:
		return "unknown"
	case SurfaceSpawnStale:
		return "spawn-stale"
	case SurfaceStreamStale:
		return "stream-stale"
	case SurfaceLegacyStale:
		return "legacy-stale"
	default:
		return fmt.Sprintf("surfaceskew(%d)", int(s))
	}
}

// Consequence renders what a verdict COSTS, in the operator's terms. This is the
// point of tiering: the verdict has to say whether interrupting agents is
// warranted, so nobody has to reverse-engineer a diff mid-incident.
func (s SurfaceSkew) Consequence() string {
	switch s {
	case SurfaceSpawnStale:
		return "running agents are unaffected; newly started sessions will use the previous build's spawn config — restart when convenient"
	case SurfaceStreamStale:
		return "live sessions are affected — a supervisor restart is warranted, and it interrupts every running agent"
	case SurfaceLegacyStale:
		return "supervisor predates surface-version reporting and its binary differs — the tier is unknowable, so it is treated as if live sessions are affected"
	case SurfaceUnknown:
		return "supervisor surface version unknown (older protocol) — reported as unknown, never stale"
	default:
		return "supervisor runs the same executed surface as this build"
	}
}

// Headline is Consequence compressed to a single short clause, for surfaces too
// narrow for the full sentence (the skew modal's 72-column body).
func (s SurfaceSkew) Headline() string {
	switch s {
	case SurfaceSpawnStale:
		return "spawn config only — running agents are unaffected"
	case SurfaceStreamStale:
		return "live sessions are affected"
	case SurfaceLegacyStale:
		return "older protocol — extent unknown, assume live sessions"
	case SurfaceUnknown:
		return "surface version unknown"
	default:
		return "same executed surface"
	}
}

// CompareSupervisorSurface classifies a supervisor's reported surface against
// this build's. Stream outranks spawn: when both differ the verdict is
// stream-stale, because that is the strictly larger consequence.
func CompareSupervisorSurface(reported SurfaceVersion) SurfaceSkew {
	if !reported.Known() {
		return SurfaceUnknown
	}
	cur := CurrentSupervisorSurface()
	switch {
	case reported.Stream != cur.Stream:
		return SurfaceStreamStale
	case reported.Spawn != cur.Spawn:
		return SurfaceSpawnStale
	default:
		return SurfaceCoherent
	}
}

// SupervisorSpawnPaths declares every source file whose content the supervisor
// reads when it CONSTRUCTS a session. Repo-relative, slash-separated.
//
// The manifest is a declaration, not a derivation: nothing computes it, and
// keeping it honest is the same judgment ProtocolVersion already asks for. When
// new supervisor-resident code lands in a file not listed here, ADD IT — an
// omission from this list is exactly the silent false-negative the surface
// version is guarded against.
var SupervisorSpawnPaths = []string{
	"internal/agent/agent.go",          // BuildCmd: argv, env, dir, cache-dir redirection
	"internal/agent/prelaunch.go",      // backend prelaunch (pi/ollama) run before the fork
	"internal/agent/routing_prompt.go", // --append-system-prompt-file routing injection
	"internal/agent/sandbox.go",        // the sandbox-exec wrapper the command is wrapped in
	"internal/agent/secret.go",         // point-of-use secret resolution inside BuildCmd
	"internal/agent/secretregistry.go", // the resolver registry BuildCmd resolves through
	"internal/skills/builtin.go",       // builtin skills materialized at spawn time
	"internal/skills/skills.go",        // --add-dir skill provisioning
}

// SupervisorStreamPaths declares every source file whose content the supervisor
// reads while SERVING a live session. Repo-relative, slash-separated.
//
// See SupervisorSpawnPaths for the honesty contract. runner.go lives here rather
// than in the spawn set even though Runner.Start calls BuildCmd: it also owns the
// live session map, the pendingRestart bookkeeping, and Stop/StopAll, so a change
// to it is far more likely to reach a running agent than not. Classifying an
// ambiguous file as STREAM is the safe direction.
var SupervisorStreamPaths = []string{
	"internal/agent/ringbuffer.go",   // the ring the sole readLoop tees into
	"internal/agent/runner.go",       // live session map, pendingRestart, Stop/StopAll, KickRerender
	"internal/agent/session.go",      // the single readLoop: PTY read → ring + writers, session log
	"internal/agent/sessionsize.go",  // the PTY-size sidecar session.go writes on resize
	"internal/daemon/sessioncore.go", // the R/S handlers both daemon and supervisor mount
	"internal/daemon/supervisor.go",  // the supervisor process itself: Hello, exit caching, serve loop
}

// SpawnSurfaceDigest and StreamSurfaceDigest are the recorded SHA-256 of each
// manifest's file contents AS OF the surface-component values above.
//
// They exist so that touching supervisor-resident code cannot be SILENT. The
// guard test recomputes them and fails when they drift, which forces the author
// to make the judgment call explicitly: bump the component (the change is
// observable), or re-record the digest alone (the change is not — a comment, a
// rename, a pure refactor).
//
// Honest about what this does and does not catch: it makes OMISSION mechanical
// to detect, which is design D3's stated goal. It cannot stop a deliberate wrong
// call — re-recording a digest without bumping a genuinely-observable change
// yields a false negative — and no in-tree check can, since any recorded value
// is itself editable. That residual risk is why the bumping rule above says to
// bump STREAM when in doubt, and why doctor keeps both binary hashes visible.
//
// To re-record after an intentional change: run the guard test; its failure
// message prints the computed digest to paste back here.
const (
	SpawnSurfaceDigest  = "6022c1fe2721112c06fb1b789de9eb7e2fad431c96af97310c60969d9639129e"
	StreamSurfaceDigest = "57078648a38b01bad81b795bf6ca431199e4e394460f3a19ca9bf0ee407a785e"
)

// SurfaceDigest computes the SHA-256 over the declared manifest's file contents,
// resolved against root. Path names and lengths are folded in alongside the
// bytes, so a rename or a move of content between two declared files also
// changes the digest.
//
// Test-time only by design: D3 rules out computing a fingerprint at build time on
// the real deploy path, so this never runs in a shipped process — it defines what
// "the declared surface content" MEANS for the guard test.
func SurfaceDigest(root string, paths []string) (string, error) {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)

	h := sha256.New()
	for _, p := range sorted {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p))) //nolint:gosec // p comes from a compile-time manifest
		if err != nil {
			return "", fmt.Errorf("surface digest: %s: %w", p, err)
		}
		// hash.Hash's Write never returns an error, so neither can Fprintf here.
		fmt.Fprintf(h, "%s\n%d\n", p, len(b)) //nolint:errcheck // hash.Hash.Write never errors
		h.Write(b)                            //nolint:errcheck // hash.Hash.Write never errors
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
