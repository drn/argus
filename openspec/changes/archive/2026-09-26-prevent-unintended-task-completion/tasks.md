## 1. Agent completion instructions

- [x] 1.1 Update the bundled `argus-complete` skill and the MCP `task_complete` description to require an explicit user request and prohibit transport fallback.
- [x] 1.2 Update README wording that currently suggests automatic self-completion; test the shipped skill and tool descriptions.

## 2. TUI completion confirmation

- [x] 2.1 Add a confirmation flow for `in_review` → `complete` via task-list status advance, preserving the task ID at open time and logging confirm/cancel/failure.
- [x] 2.2 Add event-loop smoke coverage for held/repeated `s`, confirm, cancel, and selection stability.
- [x] 2.3 Update keymap help, README Reference, and the task-list gotcha if a non-obvious input/focus invariant emerges.

## 3. Verification

- [x] 3.1 Run targeted TUI, MCP, and skills tests, then `make test` and `make test-cover`.
- [x] 3.2 Archive the OpenSpec change on the branch before merge.
