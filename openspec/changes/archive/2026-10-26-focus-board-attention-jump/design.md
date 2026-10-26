## Context

Board candidates already use exact orchestrator selection through JumpToRow and landOnSelection. That landing focuses the coordinator terminal in details mode. Board input instead requires FocusAgent with detailsMode and showBoard true.

## Goals / Non-Goals

**Goals:** Focus the Board on the first unread item after Ctrl+G, preserving all existing selection and reattach behavior.

**Non-Goals:** Rendering changes, new keybindings, altered read acknowledgment or ordinary selection/refresh behavior. No REST surface changes or web/macOS focus changes are required.

## Decisions

Override focus only after successful exact-row Board landing. Scan the Board items in their existing display order for the first item with Read false and set followCursor. Do not invoke Board expansion or the read callback. Keep landOnSelection unchanged so needs-input-only jumps and reattach semantics remain intact.

## Risks / Trade-offs

- Shared coordinator tasks can select the wrong Board if task lookup is substituted: retain JumpToRow's exact orchestrator targeting.
- Refresh must preserve the plan: limit cursor/focus changes to the explicit Board jump branch.
- Read state must survive landing: verify callbacks and persisted item state, plus Enter acknowledgment, in regression tests.
