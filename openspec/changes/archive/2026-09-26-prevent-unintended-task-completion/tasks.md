## 1. Agent completion instructions

- [x] 1.1 Update the bundled `argus-complete` skill and the MCP `task_complete` description to require an explicit user request and prohibit transport fallback.
- [x] 1.2 Update README wording that currently suggests automatic self-completion; test the shipped skill and tool descriptions.

## 2. Verification

- [x] 2.1 Run targeted MCP and skills tests, then `make test` and `make test-cover`.
- [x] 2.2 Archive the OpenSpec change on the branch before merge.
