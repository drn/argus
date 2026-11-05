## 1. Research
- [x] 1.1 Inspect `omp` 18.16.0: flags, `--` handling, session layout + dir encoding, MCP/skills discovery, approval modes, state dirs

## 2. Implementation
- [x] 2.1 `backendkind.Omp` + spec (binary `omp`, prefix `/`) + tests
- [x] 2.2 `internal/agent/omp.go`: session-dir encoding, capture, MCP plugin + config overlay materialization, fixture tests
- [x] 2.3 `SkillsConfigOverlay` delivery in BuildCmd; `Family` entry (resume, recapture, permission flags, MCP flag, routing, launch-only sandbox grant); default config backend
- [x] 2.4 `TestBuildCmd_Omp`; fix hardcoded backend-count/order test assumptions

## 3. Finish
- [x] 3.1 mcp/hera backend text, README backend table, `context/knowledge/backends.md`, gotchas
- [x] 3.2 Supervisor spawn surface bump + digest
- [x] 3.3 Archive this change into base specs
- [x] 3.4 `make pre-pr` (all gates green; `vuln` fails only on go1.26.8 stdlib CVEs, advisory in CI)
- [ ] 3.5 Live verify (create task, exit, capture, resume, MCP tools visible)
