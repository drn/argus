## Why

Coordinator updates for Aaron scroll away and disappear across recycles. A curated board keeps decisions and FYIs current until they are settled.

## What changes

- Add durable, editable, role-owned human board items and coordinator-only MCP maintenance tools.
- Expose board items and unread counts through REST and coordinator recovery context.
- Show the board beneath the Hera roster with selection-time landing, mailbox indicators, reply and dismissal.
- Add a default-on UX setting and coordinator maintenance instructions.

## Capabilities

### New capabilities

- `coordinator-board`: Durable human attention board and its MCP, REST, recovery and TUI surfaces.

### Modified capabilities

- `hera-view`: The coordinator details region alternates its lower panel between the plan DAG and the human board, while retaining the roster above it.

## Impact

SQLite, Hera services and model, MCP, REST, TUI keymap/help, config, recycle context, builtin Hera guidance. No deployment or merge is part of this change.
