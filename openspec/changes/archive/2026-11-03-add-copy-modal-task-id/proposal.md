# Add task ID to the copy-choice modal

## Why
The task-list `c` menu could copy a task's name or prompt but not its ID, which is the handle used by MCP tools, the REST API and logs.

## What Changes
- The copy-choice modal always offers an **ID** choice, listed after Name and Prompt. Selecting it copies the task ID.

## Non-Goals
- Web and macOS clients have no copy-choice menu equivalent; any ID-copy affordance there is a named follow-up.
