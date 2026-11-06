## Why

Tasks currently auto-collapses every project except the focused one. Aaron wants to keep all Tasks projects open until he toggles back, remembering the choice after relaunch.

## What changes

- Add rebindable Tasks action `tasklist.expand_all`, default `F` (free in the Tasks keymap), toggling all projects expanded versus only the focused project expanded.
- Apply it to both the classic Tasks list and optional Tasks-tab rail. Persist as local `ui.tasks_expand_all`, default false, with process-local fallback in remote mode.
- **Change from the approved rail draft:** the draft proposed independent project folds with all active projects initially open. This addition instead makes focus-fold the default in both layouts; `F` keeps every active project open until pressed again. All-open does not permit closing individual project folders.
- Pinned stays open. Archive retains its outer expando; all-open applies to its project folders only when Archive is open.
- Add keymap/help and README Reference rows.

## Capabilities

### New capabilities

- `tasks-project-expansion`: Remembered Tasks-only expansion mode and rebindable action.

### Modified capabilities

None. The new requirements explicitly supersede the optional rail's independent-project-fold policy; base specs will be reconciled to the final policy when archiving both changes.

## Impact

Tasks row building, cursor/focus-fold behavior, local UI-state persistence, help, README Reference, and tests. No REST or TOML import changes.

## Non-goals

- **Named follow-up: Web SPA remembered Tasks project expansion.**
- **Named follow-up: macOS remembered Tasks project expansion.**
