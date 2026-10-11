## 1. Research
- [x] 1.1 Install `agy` 1.3.3 (cask artifact, checksum-verified) and record flags, subcommands, storage layout, MCP/skills/rules docs

## 2. Registry refactor (no behavior change)
- [x] 2.1 `internal/backendkind` (Kind, Of, Lookup, All) + tests
- [x] 2.2 `agent.Family` registry + `FamilyOf`; IsXBackend become wrappers
- [x] 2.3 BuildCmd, CaptureSessionID, NeedsSessionRecapture, RefreshResumeSessionID, KnownModels, profileBackendName read the registry
- [x] 2.4 create.go, prelaunch.go, daemon client start timeout, config.AccountSupports, TUI (pin gate, switcher gate, capture log, account tool, skill prefix), api/claudesessions read the registry
- [x] 2.5 Sandbox backend grants generated from the registry; daemon global injection iterates families
- [x] 2.6 Existing test suite passes unchanged

## 3. Antigravity
- [x] 3.1 Family entry + default config backend
- [x] 3.2 `CaptureAntigravitySessionID` + fixture tests
- [x] 3.3 `internal/inject/antigravity` MCP + skills manifest injection + tests
- [x] 3.4 BuildCmd tests: new/resume/no-ID/permission modes/model/prompt flag default/routing prefix

## 4. Frontends
- [x] 4.1 REST backend listing gains kind/label/skill_prefix/session_switcher
- [x] 4.2 Web SPA uses them (bump SW_VERSION)
- [x] 4.3 macOS decodes them; account tool + switcher eligibility use kind

## 5. Docs + skill
- [x] 5.1 `.agents/skills/add-backend/SKILL.md` + `context/knowledge/backends.md`
- [x] 5.2 README Reference backend table, gotchas entry, knowledge index
- [x] 5.3 Supervisor spawn surface bump + digest re-record
- [x] 5.4 Archive this change into base specs
- [x] 5.5 `make pre-pr` clean
