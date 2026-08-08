# Tasks: add-op-secret-resolver

**Design doc:** `openspec/changes/add-op-secret-resolver/design.md`

**Status: NOT approved for implementation.** This proposal (`proposal.md` +
`design.md` + the delta specs) is awaiting Aaron's sign-off per this repo's
CLAUDE.md ("Get approval before implementing"). Nothing below has been
started; no code exists yet. Once approved, work through these groups with
TDD (failing tests first, per group) and run `make pre-pr` clean before
archiving.

## 1. Config schema

- [ ] 1.1 Write failing tests for `config.DefaultConfig()`: `Secrets.Resolver`
      defaults to `"env"`; `Secrets.Op` is the zero value (empty
      `ReferenceTemplate`/`Command`, zero `TimeoutSeconds`).
      (`specs/config-management/spec.md` → "Default secret-resolver
      configuration")
- [ ] 1.2 Write failing tests for `config.toml` round-trip: an explicit
      `[secrets]` / `[secrets.op]` table overrides the default; an absent
      `[secrets]` table parses as the default (BurntSushi zero-value
      behavior).
- [ ] 1.3 Add `Secrets SecretsConfig` (`toml:"secrets"`) to `config.Config` and
      the `SecretsConfig`/`OpResolverConfig` types to
      `internal/config/config.go`, per design.md's "Data model changes"
      section (doc comments included — mapping/config only, no secret value,
      no default vault/item). Seed `Secrets: SecretsConfig{Resolver: "env"}`
      in `DefaultConfig()`.

## 2. Resolver selection + op resolver (agent)

**Depends on:** Stage 1

- [ ] 2.1 Write failing tests for resolver selection (`resolverFor` or
      equivalent, `internal/agent/secret_test.go`): `"env"`/empty/unrecognized
      mode selects the existing package resolver; `"op"` mode with an empty
      template selects the package resolver + logs a degrade notice; `"op"`
      mode with an unresolvable command selects the package resolver + logs a
      degrade notice; `"op"` mode with a valid template + resolvable command
      returns a distinct op-backed resolver. (`specs/agent-execution/spec.md`
      → "Configurable secret-resolver mode")
- [ ] 2.2 Write failing tests for the op resolver's invocation shape, mocking
      `op` via a test-only executable/stub (never a real 1Password call):
      `{source}` substitution into the reference template; the exact
      `op read --no-newline <ref>` argv; trailing-newline trim on success;
      timeout enforcement (a stub that sleeps past `TimeoutSeconds`);
      `cmd.Stdin` is never set/inherited. (`specs/agent-execution/spec.md` →
      "op secret-resolver behavior")
- [ ] 2.3 Write failing tests for op-resolver failure handling: non-zero exit,
      timeout, and empty-stdout all resolve `("", false)`; the existing
      `BuildCmd` unresolved-source warning still fires; exactly one
      additional diagnostic log line appears, containing the source
      descriptor and a size-capped first line of stderr, and asserting by
      substring that it never contains a resolved value, the expanded
      reference, or stdout.
- [ ] 2.4 Implement resolver selection in `internal/agent/secret.go`: a
      function taking `config.SecretsConfig` (or the equivalent) that returns
      a `SecretResolver`, implementing D-DEGRADE's ordered checks exactly.
      Leave `secretResolver` / `SetSecretResolver` / `envSecretResolver`
      untouched.
- [ ] 2.5 Implement the op resolver closure in `internal/agent/secret.go`:
      template substitution, `exec.CommandContext` with the configured
      timeout (default 5s when zero), `--no-newline`, stdout trim, stderr
      capture capped to one line/~200 bytes for the failure diagnostic, nil
      stdin.
- [ ] 2.6 Wire `BuildCmd` (`internal/agent/agent.go`, the existing `EnvVars`
      merge loop at the tail of the function) to call the Stage 2.4 selector
      with the `cfg.Secrets` it already receives as a parameter, in place of
      the current direct reference to the package `secretResolver` variable.
      No other change to the merge loop or its existing warning line.

## 3. Docs

**Depends on:** Stage 1, Stage 2

- [ ] 3.1 Add a `[secrets]` / `[secrets.op]` entry to README's Reference
      appendix, adjacent to the existing `[backends.<name>]` table: keys,
      types, defaults, the `{source}` substitution rule, and the daemon-env
      authentication precondition (op itself must already be able to
      authenticate from the daemon's own process environment — argus does not
      manage that credential).
- [ ] 3.2 Add a gotcha note to `context/knowledge/gotchas/misc.md` (extending
      the existing "Backend credential env mapping (env_vars)" section or a
      new adjacent section): config.toml-only scope, the fail-open degrade
      rule (D-DEGRADE), `--no-newline` + trim, the timeout rationale, and the
      explicit non-goal that argus never bootstraps the daemon's own
      authentication to `op`.

## 4. Review, verification, and archive

**Depends on:** Stage 1, Stage 2, Stage 3

- [ ] 4.1 `make pre-pr` clean.
- [ ] 4.2 Run `/hera-review` (or the project's standard review pass) against
      the implementation vs. the deltas above; address findings.
- [ ] 4.3 Archive this change in the same PR per this repo's CLAUDE.md: merge
      the delta requirements into `openspec/specs/agent-execution/spec.md` and
      `openspec/specs/config-management/spec.md`, move this folder to
      `openspec/changes/archive/<YYYY-MM-DD>-add-op-secret-resolver/`.
- [ ] 4.4 Open the PR via `mcp__argus__iris_gh_pr_create` (never bare `gh pr
      create` from inside an argus sandbox).
