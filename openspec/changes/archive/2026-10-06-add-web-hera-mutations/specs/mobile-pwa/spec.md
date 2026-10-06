## MODIFIED Requirements

### Requirement: Hera tab

The Hera ("Projects") tab SHALL no longer be read-only: each orchestrator card and role row SHALL offer an action menu (nuke, hide/unhide, pin/unpin, rename, role status, kanban for top-level orchestrators) mirroring the TUI rail keys. Nuke SHALL require a confirm dialog populated from `nuke-preview`. After any action the roster SHALL reload. Mutation controls MUST be hidden or disabled with an explanatory message when the token lacks permission (403). All rendered names remain HTML-escaped.

#### Scenario: Remove an unbound orchestrator
- **WHEN** the user opens an orchestrator's menu, chooses Nuke, and confirms
- **THEN** the card disappears from the roster after reload
