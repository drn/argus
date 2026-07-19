## ADDED Requirements

### Requirement: Install default profile seeds from the Hera settings category

The Hera settings category SHALL offer an action row that installs the embedded default diligence
profile seeds into the per-user profile library. Activating it SHALL mark the action busy, dispatch the
install via a callback, and report back which seed names were newly installed and which were already
present (skipped); a second activation while busy SHALL be ignored. This follows the same in-flight /
async-result shape as the existing Update Argus action.

#### Scenario: Installing into an empty library

- **WHEN** the cursor is on the install-profiles row and the per-user library has none of the seed
  profiles
- **THEN** activating it installs all seeds and the detail pane reports them as installed

#### Scenario: Installing when some seeds already exist

- **WHEN** the per-user library already contains one of the seed names
- **THEN** activating the row installs only the missing seeds and the detail pane reports the existing
  one as already present, without altering its contents

#### Scenario: Busy state ignores re-activation

- **WHEN** the install is in flight and the user activates the row again
- **THEN** the callback does not fire a second time

#### Scenario: Result clears busy state

- **WHEN** an install result is reported back to the view
- **THEN** the busy flag clears and the installed/skipped names are shown in the detail pane
