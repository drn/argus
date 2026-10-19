## Why

In the Tasks-tab agent view, `Cmd+Up` / `Cmd+Down` (previous / next task) walked the raw task slice, ignoring the `H` hide-hera-managed toggle, the `/` filter, and the list's display order. With hera-managed tasks hidden, the chord still landed on hidden hera workers and coordinators.

## What Changes

- Previous / next task in the agent view walks the same visible order the task list renders (Pinned, then Active projects alphabetically, then Archive), with the filter and the hide-hera-managed toggle applied. Folded projects stay reachable, as before.
- When the current task is not visible, the chord is a no-op.

## Non-Goals

- Web SPA and macOS app: unaffected. The `H` toggle is TUI-local and neither client reads it; the macOS Cmd+Up/Down walks its own sidebar list.
- The Hera tab's Cmd+Up/Down rail navigation is a separate requirement and is unchanged.
