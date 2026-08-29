## ADDED Requirements

### Requirement: Configurable shared cache directory redirection

The system SHALL support a project-configurable mapping (`cache_dirs`) from a
target environment-variable name to a subdirectory created under
`~/.argus/cache/`, merged from a global config-level mapping and a
per-project mapping (a per-project entry overrides a shared key and adds any
key the global mapping doesn't define). For every entry in a task's resolved
mapping, the system SHALL create the resolved subdirectory if it does not
already exist and SHALL export `TARGET=<resolved-dir>` on the spawned
agent's environment. This mapping SHALL hold directory paths only, never a
secret value. An entry whose target is empty or contains `=`, or whose
subdirectory is absolute or escapes the cache root via a `..` path segment,
SHALL be skipped (logged, not fatal) rather than exported or used to create a
directory outside the cache root.

#### Scenario: Global cache_dirs entry exported and its directory created

- **WHEN** a command is built for a task whose config defines a global
  `cache_dirs` entry mapping a target env var to a subdirectory name
- **THEN** the spawned agent's environment sets that target to a path under
  `~/.argus/cache/<subdirectory>`, and that directory exists on disk

#### Scenario: Per-project entry overrides a shared key

- **WHEN** a task's project defines a `cache_dirs` entry for the same target
  env var as the global `cache_dirs` mapping, with a different subdirectory
- **THEN** the spawned agent's environment uses the project's subdirectory,
  not the global one

#### Scenario: Per-project entry adds a project-only key

- **WHEN** a task's project defines a `cache_dirs` entry for a target env var
  the global mapping does not define
- **THEN** the spawned agent's environment includes that target, resolved
  under `~/.argus/cache/`

#### Scenario: No cache_dirs configured changes nothing

- **WHEN** neither the global config nor the task's project defines any
  `cache_dirs` entry
- **THEN** no additional cache-directory environment variables are exported,
  beyond the always-forced `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH`

#### Scenario: Invalid entry is skipped, not fatal

- **WHEN** a resolved `cache_dirs` entry has an empty target, a target
  containing `=`, or a subdirectory that is absolute or contains a `..`
  path segment
- **THEN** command construction still succeeds, that entry is not exported,
  and no directory is created for it outside `~/.argus/cache/`
