## MODIFIED Requirements

### Requirement: Seed profiles

The change SHALL ship three example profiles — `default`, `lean`, and `customer_grade` — seeded from the
archetype→model framework (premium for high-leverage/low-verifiability roles, cheap for verifiable
high-volume roles), with Fable treated as absent. These SHALL be embedded into the binary (not read from
a git checkout at runtime), so installing them works identically for a from-source build and a
release-binary install. The system SHALL expose a programmatic `InstallDefaults` affordance that writes
each seed into the per-user profile library when — and only when — no file already exists at that
destination; an existing file SHALL be left untouched (never overwritten) and reported as skipped rather
than silently ignored. Installation SHALL remain an explicit, user-triggered action — the system SHALL
NOT auto-write seeds on daemon startup or any other unattended path.

#### Scenario: Default seed covers all archetypes

- **WHEN** the `default` seed profile is validated
- **THEN** it conforms and provides a model for each canonical archetype

#### Scenario: Lean and customer_grade extend default

- **WHEN** the `lean` and `customer_grade` seed profiles are validated
- **THEN** they conform and express their differences as overrides of `default`

#### Scenario: Embedded seeds validate independently of a git checkout

- **WHEN** a seed profile's embedded bytes are extracted and loaded (no reliance on any file outside the
  binary's embedded data)
- **THEN** it passes `profiles.Validate` the same as when read from a source checkout

#### Scenario: Installing into an empty library writes every seed

- **WHEN** `InstallDefaults` runs against a profiles directory containing none of the seed names
- **THEN** every seed is written and reported as installed

#### Scenario: An existing file is never overwritten

- **WHEN** `InstallDefaults` runs and a seed name already exists at the destination
- **THEN** the existing file's bytes are left unmodified and its name is reported as skipped, not
  installed

#### Scenario: Installation is never automatic

- **WHEN** the daemon starts with no `~/.argus/profiles/` directory present
- **THEN** no seed files are written unless an operator explicitly triggers the install action
