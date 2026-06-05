# Forms and Modals

## Purpose

This capability covers the keyboard-driven modal overlays the TUI presents for creating and editing tasks, projects, and schedules, for renaming tasks, for bulk-importing git repositories, and for picking AppleEvent allowlist entries and links. Each modal is a centered overlay that collects input, reports a terminal outcome (submitted or canceled) to its host, and exposes the collected values. Every modal that accepts typed text must also accept bracketed paste so multi-character clipboard content is inserted as a single operation rather than being silently dropped.

## Requirements

### Requirement: Modal outcome reporting

Every form and picker modal SHALL expose its terminal state so the host can tell whether the user submitted or dismissed it, and a freshly constructed modal SHALL report neither outcome until the user acts.

#### Scenario: Newly constructed modal is neither done nor canceled

- **WHEN** a form or picker modal is constructed
- **THEN** it reports that it is neither submitted nor canceled

#### Scenario: Escape or Ctrl+Q dismisses a modal

- **WHEN** the user presses Escape or Ctrl+Q on a form or picker modal (and no autocomplete dropdown is consuming the key)
- **THEN** the modal reports that it was canceled

### Requirement: New-task form submission

The new-task form SHALL produce a task only when a known project is selected and the prompt is non-empty, and SHALL surface an error and remain open otherwise. The submitted task SHALL carry the resolved project, the resolved branch, the selected backend, the trimmed prompt, an auto-generated name derived from the prompt, and pending status.

#### Scenario: Submit with unknown project

- **WHEN** the user submits the prompt field while the project text matches no known project
- **THEN** the form sets an "Unknown project" error and does not mark itself done

#### Scenario: Submit with empty prompt

- **WHEN** the project resolves to a known project but the prompt is empty
- **THEN** the form does not mark itself done

#### Scenario: Successful submission

- **WHEN** the project resolves and the prompt is non-empty and the user submits
- **THEN** the form marks itself done and yields a pending task with the resolved project, resolved branch, selected backend, and trimmed prompt

#### Scenario: Branch falls back to project default

- **WHEN** the branch field is left empty and the resolved project has a configured default branch
- **THEN** the resolved branch is the project's configured default branch

### Requirement: Project typeahead resolution

The new-task form SHALL resolve a typed project name to a known project by exact match or case-insensitive match, and SHALL resolve to no project when the text matches nothing. Changing the project SHALL reset the branch field to the new project's default branch and trigger a fresh branch load.

#### Scenario: Case-insensitive project match

- **WHEN** the typed project text differs only in letter case from a known project name
- **THEN** the form resolves it to that known project

#### Scenario: Unrecognized project text

- **WHEN** the typed project text matches no known project name
- **THEN** the form resolves to no project

#### Scenario: Project change resets branch

- **WHEN** the selected project changes
- **THEN** the branch input is replaced with the new project's default branch and the branch options are reloaded

### Requirement: Skill autocomplete in the prompt

The new-task form SHALL offer skill autocomplete when the space-delimited token at the cursor begins with the backend-specific trigger character, using "$" for Codex backends and "/" for all others. Accepting a suggestion SHALL replace the triggering token with the trigger plus the skill name and a trailing space; pressing Tab in the prompt while the dropdown is open SHALL accept the suggestion without advancing focus.

#### Scenario: Trigger character selects autocomplete prefix

- **WHEN** the prompt cursor sits within a token starting with "/" on a non-Codex backend (or "$" on a Codex backend) that matches at least one skill
- **THEN** the autocomplete dropdown opens with the matching skills

#### Scenario: Accept replaces the token

- **WHEN** the user accepts a skill suggestion
- **THEN** the triggering token is replaced with the trigger character, the skill name, and a trailing space

#### Scenario: Tab accepts without advancing focus

- **WHEN** the prompt field is focused, the skill dropdown is open, and the user presses Tab
- **THEN** the suggestion is accepted and focus stays on the prompt field

### Requirement: Project form result normalization

The project form SHALL return the entered project name and a project config whose path is trimmed, tilde-expanded, and absolutized, and whose branch is the selected option when a branch selector is active or the typed text otherwise. In edit mode the name field SHALL be read-only and the form SHALL skip it during navigation.

#### Scenario: Path is absolutized in the result

- **WHEN** the form is submitted with a non-empty path entry
- **THEN** the returned project path is tilde-expanded and converted to an absolute path

#### Scenario: Branch selector value wins

- **WHEN** branch options have been loaded and a selection is made
- **THEN** the returned branch is the selected branch option

#### Scenario: Name is immutable in edit mode

- **WHEN** the form is in edit mode and the user types into or pastes onto the name field
- **THEN** the name value is unchanged

### Requirement: Project form branch loading

The project form SHALL render the branch field as a left/right selector once branch options are supplied and as a free-text input otherwise. It SHALL request branch options when focus reaches the branch field and the path has changed since the last load, and SHALL pre-select the option matching the current branch value.

#### Scenario: Branch options trigger selector mode

- **WHEN** branch options are supplied to the form
- **THEN** the branch field renders as a left/right selector and pre-selects the option equal to the current branch value when one matches

#### Scenario: No loader keeps text input

- **WHEN** no branch loader is configured and focus reaches the branch field
- **THEN** the branch field remains a free-text input that records typed characters into the result

#### Scenario: Cycling selector options

- **WHEN** the branch selector is focused and the user presses left or right
- **THEN** the selected branch index cycles through the options and wraps at the ends

### Requirement: Quick-add directory scan and selection

The quick-add form SHALL operate in two phases: a directory-input phase that triggers a background scan on submit, and a selection phase that lists discovered git repositories with toggleable checkboxes. Submitting the directory phase with empty input SHALL surface an error; submitting the selection phase with nothing selected SHALL surface an error; an empty scan result SHALL surface a "no new repos" error and remain in the directory phase.

#### Scenario: Empty directory input rejected

- **WHEN** the user submits the directory phase with an empty path
- **THEN** the form sets an error prompting for a directory and does not start a scan

#### Scenario: Scan results advance to selection

- **WHEN** a background scan returns one or more repositories
- **THEN** the form moves to the selection phase listing those repositories with all pre-selected

#### Scenario: Submitting with no selection rejected

- **WHEN** the user submits the selection phase with no repository selected
- **THEN** the form sets a "no repos selected" error and does not mark itself done

#### Scenario: Select-all and deselect-all

- **WHEN** the user presses the select-all or deselect-all key in the selection phase
- **THEN** every listed repository becomes selected or deselected respectively

### Requirement: Schedule form values

The schedule form SHALL collect a name, project, backend, cron schedule, prompt, and enabled flag, defaulting the schedule to a daily expression and the enabled flag to on for new schedules. Project, backend, and enabled SHALL be cycling selectors; submission SHALL be available via Ctrl+S or by acting on the final selector, and the returned schedule SHALL trim the name and schedule fields while preserving the prompt verbatim.

#### Scenario: New schedule defaults

- **WHEN** a new schedule form is constructed
- **THEN** the schedule field defaults to a daily expression and the enabled flag is on

#### Scenario: Ctrl+S submits

- **WHEN** the user presses Ctrl+S on the schedule form
- **THEN** the form marks itself done

#### Scenario: Selector fields cycle

- **WHEN** a selector field (project, backend, or enabled) is focused and the user presses left or right
- **THEN** the corresponding selection cycles and wraps

### Requirement: Rename task form

The rename-task form SHALL be a single text field pre-populated with the current name and with the cursor at the end. Pressing Enter SHALL mark it done; the host can clear the done flag to keep it open after a rejected rename, and the form SHALL expose the current name text.

#### Scenario: Pre-populated name

- **WHEN** the rename form is constructed with an existing name
- **THEN** the field contains that name and the cursor is positioned at the end

#### Scenario: Enter submits the new name

- **WHEN** the user edits the field and presses Enter
- **THEN** the form marks itself done and exposes the edited name

### Requirement: Bracketed paste support

Every modal that accepts typed text SHALL implement a paste handler that inserts the entire pasted text at the cursor in a single operation. Paste SHALL be ignored on selector fields and on read-only fields, and filter inputs that must stay single-line SHALL strip embedded newline and carriage-return characters from pasted text.

#### Scenario: Paste inserts at the cursor

- **WHEN** text is pasted into a focused text field
- **THEN** the entire pasted text is inserted at the cursor position and the cursor advances past it

#### Scenario: Paste ignored on a selector or read-only field

- **WHEN** text is pasted while a selector field or a read-only field is focused
- **THEN** the field value is unchanged

#### Scenario: Single-line filter strips newlines

- **WHEN** multi-line text is pasted into a single-line filter input
- **THEN** newline and carriage-return characters are dropped and the remaining characters are appended

### Requirement: AppleEvents allowlist picker

The AppleEvents picker SHALL present a filterable, multi-select list of scriptable apps, pre-populated from a project's existing allowlist, with selections that persist across filter changes. When the trimmed filter is a syntactically valid dotted bundle identifier that matches no scanned app and is not already selected, the picker SHALL offer a synthetic "Add custom" row for it. The confirmed result SHALL be the selected bundle identifiers in a deterministic sorted order.

#### Scenario: Selection survives filtering

- **WHEN** a row is toggled selected and the filter is then changed to hide it
- **THEN** the bundle identifier remains in the selected set

#### Scenario: Custom bundle id suggestion

- **WHEN** the filter is a valid dotted bundle identifier matching no scanned app and not already selected
- **THEN** the picker appends a synthetic "Add custom" row that can be toggled

#### Scenario: Result is sorted

- **WHEN** the user confirms the picker
- **THEN** the result lists the selected bundle identifiers in sorted order

### Requirement: Fuzzy link picker

The link picker SHALL present a filterable list of links matched by fuzzy subsequence matching against each link's URL and label, and SHALL report a selection only when a link is highlighted and confirmed. Confirming with no matches SHALL not produce a selection.

#### Scenario: Fuzzy filter narrows the list

- **WHEN** the user types a query
- **THEN** the visible list is limited to links whose URL or label contains the query characters in order, case-insensitively

#### Scenario: Confirm with matches selects

- **WHEN** the user presses Enter while at least one link matches the current query
- **THEN** the picker reports a selection and exposes the highlighted link

#### Scenario: Confirm with no matches does nothing

- **WHEN** the user presses Enter while no link matches the current query
- **THEN** the picker does not report a selection

### Requirement: Directory autocomplete

Path and directory input fields SHALL offer directory completions resolved to absolute paths, expanding a leading tilde and excluding hidden directories. The dropdown SHALL open only when completions exist, SHALL not appear when the input already exactly matches the sole completion, and Tab SHALL trigger then accept a completion in a single keystroke when the dropdown is closed.

#### Scenario: Completions are absolute and exclude hidden dirs

- **WHEN** completions are computed for a directory input
- **THEN** the matches are absolute paths and directories whose names begin with a dot are excluded

#### Scenario: Tab triggers and accepts

- **WHEN** the user presses Tab on a path field with the dropdown closed and completions are available
- **THEN** the autocomplete is opened and the top match is accepted into the field

#### Scenario: No dropdown for exact sole match

- **WHEN** the input already exactly equals the only available completion
- **THEN** no completion dropdown is shown
