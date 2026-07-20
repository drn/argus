## ADDED Requirements

### Requirement: Diligence-profile library diagnostic

`argus doctor` SHALL additionally report whether the per-user diligence-profile library (`~/.argus/profiles/`) contains at least one valid profile file. This check SHALL be independent of the binary-coherence table and verdict and of the Stop-hook registration diagnostic: it is printed as its own section and SHALL NOT alter `argus doctor`'s exit-code contract, which remains governed solely by the binary-coherence verdict. The check SHALL report library existence only, not any project's profile binding.

The check SHALL report exactly one of three states:

- **Found** — at least one `*.toml` file under `~/.argus/profiles/` passes profile validation.
- **None found** — the directory does not exist, is empty, or every file in it fails validation; the output SHALL include the exact remediation command.
- **Unknown** — the directory could not be listed for a reason other than nonexistence (e.g. a permission error); this SHALL be reported distinctly from "none found" rather than assumed to mean no profiles exist.

#### Scenario: Profile found

- **WHEN** `~/.argus/profiles/` contains at least one file that passes profile validation
- **THEN** `argus doctor` reports the diligence-profile library as found

#### Scenario: No profiles installed

- **WHEN** `~/.argus/profiles/` does not exist or contains no file that passes profile validation
- **THEN** `argus doctor` reports the diligence-profile library as none found and prints the `argus profiles install-defaults` remediation

#### Scenario: Library unreadable degrades to unknown, not a false negative

- **WHEN** `~/.argus/profiles/` exists but cannot be listed (e.g. permission denied)
- **THEN** `argus doctor` reports the diligence-profile-library status as unknown rather than "none found"

#### Scenario: Per-project binding is out of scope

- **WHEN** a project has no profile bound and resolves to a missing `default` profile
- **THEN** this check SHALL NOT report that project as a warning — only the library's own existence is evaluated

#### Scenario: Check does not change the exit-code contract

- **WHEN** no diligence profiles are found but the binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero
