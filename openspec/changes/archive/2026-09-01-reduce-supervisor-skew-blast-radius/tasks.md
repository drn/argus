# Tasks

**Scope: Layer 1 only.** Layer 2 (moving the spawn stack out of the supervisor) and Layer 3
(generation-scoped supervisors with drain) stay documented follow-ons in `proposal.md` — not
this change. Open Question 1 was answered "Layer 1 alone, then re-measure"; Layer 1's own
telemetry (how often each surface component actually bumps) is the evidence for whether Layer 2
earns its `ProtocolVersion` bump. Open Question 3 is moot while Layer 2 is out of scope — secret
resolution is not touched.

## 1. Declare the surface version

- [x] 1.1 Add `internal/daemon/surface.go`: `SupervisorSpawnSurface` / `SupervisorStreamSurface`
      hand-bumped constants next to `ProtocolVersion`, a `SurfaceVersion` value type with a
      `Known()` feature-detect, and `CurrentSupervisorSurface()`.
- [x] 1.2 Declare the supervisor-resident path manifests (`SupervisorSpawnPaths`,
      `SupervisorStreamPaths`) in the same file, so the declaration and the constant it guards
      are one artifact.
- [x] 1.3 Add `CompareSupervisorSurface` returning the tiered verdict
      (coherent / unknown / spawn-stale / stream-stale), with stream outranking spawn.

## 2. Mechanical drift guard (the CI net)

- [x] 2.1 Add `SurfaceDigest`: a SHA-256 over the declared manifest's file contents, computed
      from the repo tree (test-time only — D3 rules out a build-time fingerprint on the real
      deploy path).
- [x] 2.2 Record `SpawnSurfaceDigest` / `StreamSurfaceDigest` alongside the constants.
- [x] 2.3 Add `internal/daemon/surface_test.go` failing whenever a declared path's content no
      longer matches its recorded digest, with a failure message that states the judgment call
      the author must now make (bump the component, or record the digest and declare the change
      unobservable). `go test ./...` is a CI step, so this is a real PR gate.
- [x] 2.4 Assert the two manifests are disjoint, non-empty, and name files that exist.

## 3. Report it over the wire

- [x] 3.1 Add `HelloResp.SpawnSurface` / `.StreamSurface`; bump `ProtocolVersion` 5→6 with a
      history entry. (The proposal guessed no bump was needed; the wire contract in `types.go`
      says bump on any new optional field, and v3 set the precedent by adding `BinaryHash` the
      same way. Confirmed against the code, as briefed — recorded in `design.md`.)
- [x] 3.2 Report both components from `supervisorRPC.Hello`.
- [x] 3.3 Add `BootInfoResp.SupervisorSpawnSurface` / `.SupervisorStreamSurface` and relay them
      from `RPCService.BootInfo` alongside the existing hash relay.

## 4. Judge skew on the surface, not the whole-binary hash

- [x] 4.1 Extract the pure skew core out of `cmd/argus/main.go` into `internal/skew` so the TUI
      can re-run it too (§6). Daemon staleness stays hash-based and unchanged.
- [x] 4.2 Re-key the supervisor decision onto the surface version: equal surfaces ⇒ coherent
      whatever the hashes say; an unreported surface ⇒ unknown, never stale.
- [x] 4.3 Carry the tier out of the evaluation so callers can state the consequence.

## 5. `argus doctor`

- [x] 5.1 Add a surface tri-state to `doctor.Actor` and gather it in `cmd/argus/doctor.go`.
- [x] 5.2 A supervisor whose surface is coherent no longer produces `RESTART NEEDED` on a bare
      hash mismatch (D5 — this is the exit-code change, approved).
- [x] 5.3 Tier the remediation text: spawn-only says new sessions only; stream says live
      sessions are affected.
- [x] 5.4 Keep both hashes AND both surface versions visible in the rendered table.

## 6. Re-evaluate continuously

- [x] 6.1 Re-run the evaluation on the TUI's existing tick (off the UI thread, alongside the
      daemon health check) rather than only at startup.
- [x] 6.2 Surface a post-startup discovery through the existing status-bar notice, never a
      modal (D6), and only once per distinct verdict.
- [x] 6.3 At startup, keep the blocking modal for a stale daemon and for a stream-surface
      mismatch; a spawn-only mismatch takes the status-bar notice instead (D6).

## 7. Verify and document

- [x] 7.1 Tests for every new branch: surface comparison, tiering, wire relay, doctor verdict,
      TUI re-evaluation and notice.
- [x] 7.2 Document the gotchas in `context/knowledge/gotchas/daemon-rpc.md` and update
      `context/knowledge/index.md`.
- [x] 7.3 Keep `design.md`'s Open Questions current with what shifted during implementation.
- [x] 7.4 `make pre-pr` green.
- [x] 7.5 Archive this change into `openspec/specs/binary-coherence/` before merge, including
      MODIFIED deltas for the two base requirements this supersedes ("Binary identity display and
      staleness signal" — the decision is no longer hash-based for the supervisor; "Startup
      binary-skew detection and prompt" — a spawn-only mismatch no longer blocks).
