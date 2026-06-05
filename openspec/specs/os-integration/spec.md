# OS Integration

## Purpose

This capability provides macOS-specific operating-system integration for Argus: managing the launchd LaunchAgent that auto-starts the argus daemon at user login, and discovering installed macOS applications (including their AppleScript scriptability) so the user can pick CFBundleIdentifiers for the per-project AppleEvents allowlist. On non-macOS platforms LaunchAgent management is unavailable and degrades to safe no-ops.

## Requirements

### Requirement: Platform availability gating

LaunchAgent management SHALL be reported as available only on macOS (darwin). On every non-darwin platform, availability SHALL be reported as false and the operations SHALL degrade to safe no-ops or unsupported errors rather than attempting launchd operations.

#### Scenario: macOS reports available

- **WHEN** availability is queried on darwin
- **THEN** it SHALL report true

#### Scenario: Non-darwin reports unavailable

- **WHEN** availability is queried on a non-darwin platform
- **THEN** it SHALL report false

#### Scenario: Operations are unsupported off darwin

- **WHEN** install, uninstall, plist-path resolution, or daemon-exe resolution is invoked on a non-darwin platform
- **THEN** each SHALL return an error that matches the sentinel unsupported error (identifiable via errors.Is), and no operation SHALL panic

#### Scenario: Status carries an explanatory reason off darwin

- **WHEN** the LaunchAgent status is queried on a non-darwin platform
- **THEN** the status SHALL report not-installed and not-loaded, an empty plist path, and a non-empty reason explaining the platform restriction

### Requirement: LaunchAgent plist location

On macOS, the LaunchAgent plist SHALL live at a fixed, user-scoped path derived from the user's home directory, using the canonical job label as its filename.

#### Scenario: Plist path under the user LaunchAgents directory

- **WHEN** the plist path is resolved on macOS
- **THEN** it SHALL be `<home>/Library/LaunchAgents/com.drn.argus.daemon.plist`

### Requirement: Daemon executable resolution and symlink stability

On macOS, resolving the daemon executable for the plist SHALL produce a stable `~/.argus/argusd` symlink that points at the running binary, so that the daemon appears with a friendly process name. The symlink creation SHALL be idempotent and SHALL never fail the caller: if the symlink cannot be created, the resolved binary path SHALL be returned unchanged.

#### Scenario: Resolve returns the stable symlink path

- **WHEN** the daemon executable is resolved on macOS
- **THEN** the returned path SHALL be the `~/.argus/argusd` symlink and that symlink SHALL resolve to a reachable target

#### Scenario: Symlink creation is idempotent

- **WHEN** the daemon symlink is ensured twice for the same target executable
- **THEN** the second call SHALL return the same symlink path and SHALL NOT recreate the existing correct symlink

#### Scenario: Symlink failure falls back to the binary path

- **WHEN** the daemon symlink cannot be created
- **THEN** the original executable path SHALL be returned unchanged so a working plist can still be written

#### Scenario: Symlink passthrough off darwin

- **WHEN** the daemon symlink is ensured on a non-darwin platform
- **THEN** the input path SHALL be returned unchanged

### Requirement: Plist content contract

The rendered LaunchAgent plist SHALL be well-formed XML that instructs launchd to run the daemon at load, restart it only on unclean exit, and execute it with a PATH that includes user-specific install locations. All path and user-supplied values SHALL be XML-escaped so special characters cannot produce a malformed plist.

#### Scenario: Plist declares the daemon launch program

- **WHEN** the plist is rendered for a given daemon executable path
- **THEN** it SHALL declare the canonical job label and program arguments that invoke the executable with `daemon` and `start`

#### Scenario: Plist enables run-at-load and crash-only restart

- **WHEN** the plist is rendered
- **THEN** it SHALL set run-at-load to true and configure keep-alive to restart only on unsuccessful exit (so a clean daemon stop is honored)

#### Scenario: PATH bakes in user install locations in priority order

- **WHEN** the plist is rendered for home `/Users/me`
- **THEN** the PATH environment value SHALL be exactly `/Users/me/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin` so user-installed tools are found before system directories

#### Scenario: Special characters are XML-escaped

- **WHEN** the plist is rendered with paths or home values containing `<`, `>`, `&`, or `"`
- **THEN** those characters SHALL appear escaped and the resulting document SHALL parse as well-formed XML

### Requirement: LaunchAgent status reporting

On macOS, status reporting SHALL determine whether the plist file exists on disk and whether launchd recognizes the job as loaded. The loaded check is best-effort and SHALL report not-loaded when the launchd query fails.

#### Scenario: Not installed and not loaded

- **WHEN** the plist file does not exist and launchd reports the label is not loaded
- **THEN** status SHALL report not-installed and not-loaded, with a plist path still populated

#### Scenario: Installed and loaded

- **WHEN** the plist file exists on disk and launchd reports the label is loaded
- **THEN** status SHALL report installed and loaded

### Requirement: LaunchAgent installation

On macOS, installation SHALL require a non-empty daemon executable path, create the required directories, write the rendered plist, and bootstrap the job into the user's launchd domain. If the job is already loaded, it SHALL be booted out first so the new plist takes effect. Failures from launchd SHALL be surfaced as errors that include the launchd output.

#### Scenario: Empty executable path is rejected

- **WHEN** installation is invoked with an empty daemon executable path
- **THEN** it SHALL return an error and SHALL NOT write a plist

#### Scenario: Fresh install writes plist and bootstraps

- **WHEN** installation runs and the job is not currently loaded
- **THEN** the plist file SHALL be written containing the daemon path and label, no boot-out SHALL be performed, and exactly one bootstrap into the user's launchd domain SHALL be performed referencing the plist path

#### Scenario: Reinstall boots out the existing job first

- **WHEN** installation runs and the job is already loaded
- **THEN** exactly one boot-out SHALL be performed before exactly one bootstrap

#### Scenario: Bootstrap failure surfaces an error

- **WHEN** the launchd bootstrap step exits non-zero
- **THEN** installation SHALL return an error mentioning the bootstrap failure

### Requirement: LaunchAgent uninstallation

On macOS, uninstallation SHALL boot the job out of launchd if it is currently loaded and remove the plist file. Both steps are best-effort: a missing plist SHALL NOT be treated as an error, and no boot-out SHALL be attempted when the job is not loaded.

#### Scenario: Loaded agent is booted out and plist removed

- **WHEN** uninstallation runs and the job is loaded with a plist present
- **THEN** exactly one boot-out SHALL be performed and the plist file SHALL be removed

#### Scenario: Not-installed uninstall is a no-op

- **WHEN** uninstallation runs and the job is not loaded
- **THEN** it SHALL return no error and SHALL NOT perform a boot-out

### Requirement: Application discovery

Scanning SHALL walk the given application directories (defaulting to the standard macOS app roots when none are supplied), parse each `.app` bundle's Info.plist, and return the apps it can identify. Results SHALL be sorted by lowercase display name, deduplicated by bundle identifier with the first-scanned bundle winning, and SHALL never be nil. Unreadable directories and unparseable or unidentified bundles SHALL be skipped silently so one bad bundle cannot break discovery.

#### Scenario: Bundles parsed and identified

- **WHEN** scanning a directory containing valid `.app` bundles
- **THEN** each parsed bundle SHALL be returned with its display name and bundle identifier, sorted by lowercase name

#### Scenario: Display name fallback order

- **WHEN** a bundle has no display name and no bundle name in its Info.plist
- **THEN** its name SHALL fall back to the `.app` directory basename without the extension

#### Scenario: Bundles without an identifier are skipped

- **WHEN** a bundle's Info.plist has no bundle identifier
- **THEN** that bundle SHALL be omitted from the results

#### Scenario: Duplicate identifiers are deduplicated first-wins

- **WHEN** the same bundle identifier appears in two scanned directories
- **THEN** only the first-scanned bundle SHALL be returned

#### Scenario: Unreadable directories and invalid plists are skipped

- **WHEN** a scan target directory does not exist or a bundle has an unparseable Info.plist
- **THEN** those SHALL be skipped silently and a non-nil result SHALL be returned

### Requirement: Scriptability detection

A scanned app SHALL be flagged scriptable when it declares AppleScript support by any of three independent signals: an `NSAppleScriptEnabled` value that is boolean true or a string of "true"/"yes" (case-insensitive), a non-empty `OSAScriptingDefinition` key, or the presence of any `.sdef` file in the bundle's `Contents/Resources`. The scriptable-only convenience scan SHALL return only apps flagged scriptable.

#### Scenario: Boolean NSAppleScriptEnabled flags scriptable

- **WHEN** a bundle declares `NSAppleScriptEnabled` true (boolean or "true"/"yes" string)
- **THEN** the app SHALL be flagged scriptable

#### Scenario: OSAScriptingDefinition flags scriptable

- **WHEN** a bundle declares a non-empty `OSAScriptingDefinition` key
- **THEN** the app SHALL be flagged scriptable

#### Scenario: A .sdef file flags scriptable

- **WHEN** a bundle has a `.sdef` file in `Contents/Resources` even without a scripting Info.plist key
- **THEN** the app SHALL be flagged scriptable

#### Scenario: No scripting signals means not scriptable

- **WHEN** a bundle declares none of the scripting signals
- **THEN** the app SHALL NOT be flagged scriptable

#### Scenario: Scriptable-only scan filters out non-scriptable apps

- **WHEN** the scriptable-only scan runs over a mix of scriptable and non-scriptable apps
- **THEN** only the scriptable apps SHALL be returned

### Requirement: Bundle identifier validation and filtering helpers

The package SHALL provide helpers for picker UIs: a bundle-identifier validator that accepts only identifiers beginning with an alphanumeric and containing only alphanumerics, dots, and hyphens; a case-insensitive substring filter over app name and bundle identifier; and a stable display format. These let a picker accept manually typed legacy-alias identifiers that no installed app exposes.

#### Scenario: Valid identifiers are accepted

- **WHEN** validating an identifier that starts with an alphanumeric and contains only alphanumerics, dots, and hyphens (e.g. `com.apple.iChat`)
- **THEN** it SHALL be accepted

#### Scenario: Invalid identifiers are rejected

- **WHEN** validating an empty string, an identifier with a leading dot or hyphen, or one containing spaces, quotes, parentheses, slashes, or underscores
- **THEN** it SHALL be rejected

#### Scenario: Text filter matches name or bundle id case-insensitively

- **WHEN** filtering apps by a non-empty query
- **THEN** only apps whose name or bundle identifier contains the query (case-insensitive) SHALL be returned; an empty or whitespace-only query SHALL return the input unchanged

#### Scenario: App display format

- **WHEN** an app with a name and bundle identifier is formatted
- **THEN** the result SHALL be "Name — bundle.id"
