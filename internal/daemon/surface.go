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
	//   - v2: BuildCmd force-exports the resolved [secrets.op] bootstrap
	//     credential into every spawned session's env (fix-agent-secret-bootstrap).
	//   - v3: Codex-backend spawn gained CLAUDE.md/routing prompt-prefix and
	//     builtin-skill materialization into $CODEX_HOME/skills
	//     (add-nonclaude-context-parity); EnsureCodexSkills's stale-removal
	//     sweep there is now gated on a positive per-directory ownership
	//     marker instead of mere absence-from-known-set, so it no longer
	//     deletes Codex's own .system/ content or user-installed skills.
	//   - v4: ResolveSandboxConfig gained a per-task SandboxOverride
	//     ("enabled"/"disabled") as a fourth precedence tier, read at spawn
	//     time and taking precedence over both the project and global sandbox
	//     settings (add-task-sandbox-override).
	//   - v5: diligence-profile model selection became backend-family-aware,
	//     so sessions spawned through arbitrarily named Claude/Codex/Pi/OpenCode
	//     backend instances use that family's configured archetype model.
	//   - v6: ResolveBackend gained a tiered backend-routing precedence tier
	//     (add-tiered-backend-routing), consulted between explicit task/project
	//     backend and the single configured default — a configured tier list can
	//     now change which backend a session with no explicit backend spawns
	//     with, from one probe tick to the next.
	//   - v7: KnownModels' curated Codex model list was refreshed from the
	//     retired gpt-5-codex/gpt-5 identifiers to the current Codex CLI
	//     lineup (using-default-codex-model) — a session spawned with an
	//     explicit or profile-resolved Codex model now validates against, and
	//     injects, different --model values than the previous build.
	//   - v8: Codex sessions use an Argus-only CODEX_HOME with builtin skills,
	//     preserving normal Codex configuration and state via links.
	//   - v9: Pi and OpenCode sessions receive Argus skills through per-process
	//     discovery hooks; OpenCode no longer needs a global skills entry.
	//   - v10: BuildCmd adds --auto when launching OpenCode, including stored
	//     commands and resumed sessions.
	//   - v11: task launches pass the actual Argus MCP listener to Claude and
	//     Codex as process-scoped CLI config, including supervisor starts; an
	//     unresolved credential mapping clears any inherited target variable.
	//   - v12: OpenCode model delivery moved from the unsupported top-level
	//     --model flag to child-only OPENCODE_CONFIG_CONTENT, merged with the
	//     session-scoped skills path.
	//   - v13: sandboxed sessions export PLAYWRIGHT_MCP_SANDBOX=false so the
	//     Playwright MCP's Chrome can launch inside the sandbox-exec profile.
	//   - v14: CreateAndStart's task-creation-time backend stamp now resolves
	//     through the SAME project/tier/default precedence chain
	//     ResolveBackend uses, instead of blindly stamping cfg.Defaults.Backend
	//     (fix-backend-routing-semantics) — a project-level Backend override
	//     or a configured [backend_routing] tier list can now actually change
	//     which backend a freshly created task spawns with, where previously
	//     neither was ever reachable. Hera coordinator/sub-coordinator spawn is
	//     the deliberate exception: it always forces a Claude-capable backend
	//     regardless of this resolution (resolveCoordinatorBackend) and the old
	//     [hera.worker_budget] one-way Claude→codex fallback for hera worker
	//     spawn was retired in favor of the same shared tier list.
	//   - v15: per-task accounts (add-agent-accounts). A Claude task on an
	//     explicit account spawns with CLAUDE_CONFIG_DIR=<account dir> (the dir
	//     bootstrapped with inherited links and a one-time settings.json copy),
	//     that dir granted in the sandbox, and inherited ANTHROPIC_API_KEY /
	//     ANTHROPIC_AUTH_TOKEN / CLAUDE_CODE_OAUTH_TOKEN stripped; every spawn
	//     drops an inherited CLAUDE_CONFIG_DIR. A Codex task on an explicit
	//     account builds its Argus CODEX_HOME overlay from the account's
	//     codex_home (CODEX_SQLITE_HOME pointed there too), grants that home in
	//     the sandbox, and strips inherited OPENAI_API_KEY / CODEX_API_KEY. The settings.json seed also omits login-bound keys (forceLogin*, aws*, otel helper), inherit entry names match case-insensitively, and account dirs inside ~/.ssh, ~/.argus, ~/.aws, ~/.gnupg, ~/.kube or ~/Library are rejected. An
	//     unknown stored account now refuses to spawn.
	SupervisorSpawnSurface = 15

	// SupervisorStreamSurface names the observable behavior of the live-session
	// stream core.
	//
	// History:
	//   - v1: initial declaration (reduce-supervisor-skew-blast-radius, Layer 1).
	//   - v2: the PTY session answers startup OSC 10/11 color queries before a
	//     renderer attaches, so Codex can highlight its composer immediately.
	//   - v3: rerender and coordinator recycle carry the live MCP port through
	//     the supervisor RPC so their replacement sessions keep Argus tools.
	//   - v4: sessionCore.StartSession now sets StartResp.Ambiguous when the
	//     runner's own Start call (itself a second RPC hop to the supervisor
	//     in P4 supervisor mode) failed with agent.ErrStartAmbiguous, so the
	//     distinction survives across that hop instead of being flattened
	//     into a plain error string (fix-ambiguous-start-timeout-unwind). A
	//     stale supervisor built before this change never sets the field, so
	//     a daemon on the new build talking to an old supervisor silently
	//     loses the distinction again until the supervisor is bounced.
	//   - v5: Runner.Start(resume) on a Claude backend stops the Claude Code
	//     background session holding the task's own conversation before
	//     launching, so a resume no longer dies on Claude's "session is
	//     running in the background" guard (reap-background-session-on-resume).
	//     A stale supervisor lacks the pre-launch reap, so the resume still
	//     fails there until the supervisor is bounced.
	//   - v6: the Claude background-session reap (Runner.Stop's orphan reap and
	//     the pre-resume reap) runs `claude agents`/`claude stop` under the
	//     session's own CLAUDE_CONFIG_DIR, since Claude scopes that registry per
	//     config dir (add-agent-accounts). A stale supervisor reaps only the
	//     default account's registry, missing explicit-account orphans.
	//   - v7: every spawn carries a per-spawn ARGUS_SESSION_TAG in its env, and
	//     on any session exit the runner reaps leftover processes still
	//     carrying that tag (detached Playwright browsers, shells orphaned to
	//     launchd); the supervisor sweeps dead-owner tags at startup
	//     (fix-playwright-orphans). A stale supervisor neither tags nor reaps,
	//     so those processes keep leaking until it is bounced.
	SupervisorStreamSurface = 7
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
	"internal/agent/agent.go",             // BuildCmd: argv, env, dir, cache-dir redirection
	"internal/agent/claudeaccount.go",     // per-task account dir bootstrap, env hygiene, sandbox grant
	"internal/agent/codexaccount.go",      // per-task Codex home, env hygiene, sandbox grant
	"internal/config/accounts.go",         // account → config dir resolution BuildCmd reads
	"internal/agent/nonclaude_context.go", // OpenCode child-only skills config content
	"internal/agent/prelaunch.go",         // backend prelaunch (pi/ollama) run before the fork
	"internal/agent/routing_prompt.go",    // --append-system-prompt-file routing injection
	"internal/agent/sandbox.go",           // the sandbox-exec wrapper the command is wrapped in
	"internal/agent/secret.go",            // point-of-use secret resolution inside BuildCmd
	"internal/agent/secretregistry.go",    // the resolver registry BuildCmd resolves through
	"internal/skills/builtin.go",          // builtin skills materialized at spawn time
	"internal/skills/skills.go",           // --add-dir skill provisioning
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
	"internal/agent/bgsessionreap.go",       // Claude background-session reap on Stop and before resume
	"internal/agent/ringbuffer.go",          // the ring the sole readLoop tees into
	"internal/claudeagents/claudeagents.go", // `claude agents`/`claude stop` under the session's config dir
	"internal/agent/runner.go",              // live session map, pendingRestart, Stop/StopAll, KickRerender
	"internal/agent/session.go",             // the single readLoop: PTY read → ring + writers, session log
	"internal/agent/terminal_color.go",      // PTY startup OSC color-query replies and duplicate filtering
	"internal/agent/sessionsize.go",         // the PTY-size sidecar session.go writes on resize
	"internal/daemon/sessioncore.go",        // the R/S handlers both daemon and supervisor mount
	"internal/daemon/supervisor.go",         // the supervisor process itself: Hello, exit caching, serve loop
	"internal/agent/sessionreap.go",         // session-tag descendant reaper on exit + startup sweep
	"internal/agent/procenv.go",             // tagged-process env parsing + ancestor tag inheritance
	"internal/agent/procenv_darwin.go",      // darwin tagged-process enumerator
	"internal/sessiontag/sessiontag.go",     // the per-spawn tag StartSession stamps into the env
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
	SpawnSurfaceDigest  = "283ef66f1c834091d8591f312ee99083879c0c23d411d003af17ab6f96e774ee"
	StreamSurfaceDigest = "2373b2b803c7f87193027e3664d79206157aeabf6537ed0d630544a230bd54c6"
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
