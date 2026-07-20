## Why

Diligence profiles never auto-install (`argus profiles install-defaults` / Settings → Hera is always an explicit, opt-in action), and the one in-product warning for a missing profile — the plan-DAG per-node ⚠ — is deliberately silent for the common case: an unbound project falling back to an absent `default` profile is documented as "silent fail-open — never a warning" (`internal/tui/hera_tiering.go`). A first-time user can register projects and spawn hera workers for a long time with model-tiering completely inert and get no signal anywhere. `argus doctor` already carries the exact shape of check needed — the Stop-hook registration diagnostic is an independent, advisory, tri-state section with no effect on the exit code — so extending that pattern to the profile library closes this blind spot with no new UI surface.

Scope is deliberately narrow: this reports whether any diligence profile files exist at all (the library), not whether any given project is bound to one. Per-project binding is an accepted, unwarned state.

## What Changes

- `argus doctor` gains a third independent section: a diligence-profile-library diagnostic, printed after the Stop-hook section.
- Reports one of three states:
  - **Found** — at least one `*.toml` under `~/.argus/profiles/` (the per-user library) passes validation.
  - **None found** — the directory is missing, empty, or contains files that all fail validation. Prints the fix: `argus profiles install-defaults` or Settings → Hera → "Install Default Profiles".
  - **Unknown** — the directory exists but could not be listed (e.g. a permission error), reported distinctly rather than assumed "none found".
- Explicitly out of scope: per-project profile binding, repo-local `.argus/profiles/`, and the profile's internal validity details beyond pass/fail — this is a library-existence check, not a re-run of `argus validate <name>`.
- Like the Stop-hook section, this check is purely advisory: it never changes `argus doctor`'s exit code, which stays governed solely by the binary-coherence verdict.
- README's "Diagnosing binary skew" section gets one more sentence documenting the new section, mirroring how the Stop-hook line reads today.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `binary-coherence`: `argus doctor` adds a diligence-profile-library diagnostic section (Found / None found / Unknown), independent of the exit-code contract — same shape as the existing Stop-hook registration diagnostic in this capability.

## Impact

- `cmd/argus/doctor.go` — new gather step listing `~/.argus/profiles/` via `internal/profiles.Loader.Discover()` + `ValidateName`, wired into `runDoctor()` alongside the existing `RenderStopHook` call.
- `internal/doctor` package — new status type + render function mirroring `StopHookStatus` / `RenderStopHook`.
- Tests: `internal/doctor` (status classification) and `cmd/argus` (gather step), following the existing Stop-hook test shape.
- `README.md` — one sentence in the "Diagnosing binary skew (`argus doctor`)" section.
- No changes to the exit-code contract, the binary-coherence table, or per-project profile resolution/binding.
