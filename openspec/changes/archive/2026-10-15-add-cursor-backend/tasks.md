## 1. Research
- [x] 1.1 Install/inspect `cursor-agent` 2026.10.14: flags, chat storage, MCP config files
- [ ] 1.2 After `agent login`: confirm chat dir layout, `~/.cursor` write set, auth location (sandbox grants)

## 2. Implementation
- [x] 2.1 `backendkind.Cursor` + spec (binaries `cursor-agent`, `agent`) + tests
- [x] 2.2 `internal/agent/cursor.go` capture + fixture tests
- [x] 2.3 `internal/inject/cursor` MCP injector + tests
- [x] 2.4 `Family` entry (LaunchFlag, permission flags, MCP approve flag, routing prefix, sandbox grant); default config backend
- [x] 2.5 `TestBuildCmd_Cursor`; fix hardcoded backend-count/order test assumptions

## 3. Finish
- [x] 3.1 mcp/hera backend text, README backend table, `context/knowledge/backends.md`, gotchas
- [x] 3.2 Supervisor spawn surface bump + digest
- [x] 3.3 Archive this change into base specs
- [ ] 3.4 live verify (create task, exit, capture, resume, MCP tools visible)
