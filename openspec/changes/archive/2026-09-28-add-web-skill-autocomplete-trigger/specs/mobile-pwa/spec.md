# Mobile PWA

## ADDED Requirements

### Requirement: Backend-conditional skill autocomplete trigger

The web New Task prompt and the agent-view compose bar SHALL offer skill
autocomplete when the whitespace-delimited token at the cursor begins with
the backend-specific trigger character, using `$` for Codex backends (a
command whose first word's basename is `codex`) and `/` for all other
backends. Each input SHALL resolve its own trigger independently from its own
backend context (the New Task prompt from the selected `#create-backend`
option; the compose bar from the currently open task's backend) so the two
inputs can show different triggers at the same time. Accepting a suggestion
SHALL replace the triggering token with the trigger plus the skill name and a
trailing space. Changing the New Task form's selected backend while its
dropdown is open SHALL close the dropdown rather than leave it keyed to the
old trigger character.

#### Scenario: Codex task compose bar opens on `$`

- **WHEN** the user, composing a message to a Codex-backed task in the
  agent-view compose bar, types `$p`
- **THEN** the autocomplete dropdown opens with skills whose name contains
  `p`

#### Scenario: Claude task compose bar ignores `$`

- **WHEN** the user, composing a message to a Claude-backed task, types `$p`
- **THEN** the autocomplete dropdown does not open

#### Scenario: Claude task compose bar still opens on `/`

- **WHEN** the user, composing a message to a Claude-backed task, types `/re`
- **THEN** the autocomplete dropdown opens with skills whose name contains
  `re`

#### Scenario: New Task prompt follows the selected backend

- **WHEN** the user selects Codex in the New Task form's backend dropdown and
  types `$p` in the prompt
- **THEN** the autocomplete dropdown opens with skills whose name contains
  `p`

#### Scenario: Switching backend closes a stale dropdown

- **WHEN** the New Task form's autocomplete dropdown is open for one backend's
  trigger and the user changes the selected backend
- **THEN** the dropdown closes
