## ADDED Requirements

### Requirement: Active-viewer registry governs PTY size

A session SHALL maintain a registry of active viewers keyed by a stable viewer ID,
each entry carrying the viewer's requested `(cols, rows)`. The session SHALL expose
`SetViewerSize(id, cols, rows)` to add or update a viewer and `RemoveViewer(id)` to
drop one. The effective PTY size SHALL be computed as the per-dimension minimum
(`min` of cols, `min` of rows) over all currently registered active viewers, and
the session SHALL apply that effective size to the live PTY whenever the registry
changes. Registering, updating, or removing a viewer such that the effective `min`
is unchanged SHALL NOT resize the PTY. When the registry has no active viewers, the
session SHALL keep its last applied size and SHALL NOT resize toward zero.

A viewer counts as active only while it is focused/visible: a viewer that is
connected but hidden SHALL release its registry entry (or be marked inactive) so it
does not constrain the effective size.

#### Scenario: Smallest active viewer wins
- **WHEN** two active viewers are registered at 180×50 and 80×24
- **THEN** the PTY is sized to 80×24

#### Scenario: Removing the smallest viewer grows the PTY back
- **WHEN** the 80×24 viewer is removed and only the 180×50 viewer remains active
- **THEN** the PTY is resized up to 180×50

#### Scenario: Re-registering at an unchanged minimum does not resize
- **WHEN** a viewer is added or updated and the recomputed per-dimension minimum equals the current PTY size
- **THEN** no resize is issued and no SIGWINCH is sent

#### Scenario: Hidden viewer releases its claim
- **WHEN** an active viewer becomes hidden and releases its entry
- **THEN** it no longer constrains the minimum and the effective size is recomputed over the remaining active viewers

#### Scenario: No active viewers keeps the last size
- **WHEN** the last active viewer is removed
- **THEN** the PTY keeps its last applied dimensions rather than resizing to zero

## MODIFIED Requirements

### Requirement: PTY sizing and resize

A session SHALL start with the requested PTY dimensions, falling back to a default
size when zero is supplied. The live PTY size SHALL be derived solely from the
active-viewer registry (the per-dimension minimum over active viewers); viewers
SHALL influence size only through `SetViewerSize`/`RemoveViewer`, not by setting an
absolute size directly. After the process has exited, applying a size SHALL be a
no-op success and SHALL preserve the last size the agent actually rendered for. The
system SHALL expose both the current size and the immutable initial start size, and
SHALL persist the session's size so a dead session's output can be re-emulated at
the width it was formatted for.

#### Scenario: Zero size falls back to default
- **WHEN** a session is started with zero rows or cols
- **THEN** the PTY is sized to the default dimensions

#### Scenario: Live size reflects the active-viewer minimum
- **WHEN** the active-viewer registry changes such that the per-dimension minimum differs from the current size
- **THEN** the reported current size reflects the new minimum

#### Scenario: Initial size is immutable across resize
- **WHEN** a session is resized after start
- **THEN** the reported initial size still equals the start dimensions

#### Scenario: Resize after exit is a safe no-op
- **WHEN** a size is applied after the process has exited and the PTY is closed
- **THEN** it returns success without error
