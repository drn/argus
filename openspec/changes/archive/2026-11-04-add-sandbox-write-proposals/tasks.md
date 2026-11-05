## 1. Validation and storage
- [x] 1.1 Tests then `agent.SandboxPathWarning` (denylist, warn-only) alongside `ValidateWritePath`
- [x] 1.2 Tests then `sandbox_proposals` table + db methods (create/merge-pending/list/get/CAS-decide/caps)

## 2. Apply logic
- [x] 2.1 Tests then approve/reject service: scope resolution, dedupe, revalidate on edit, revert-on-failure, config.toml shadow warning

## 3. REST + MCP
- [x] 3.1 Tests then GET routes (any token) and approve/reject (`requireMaster`, 403/409)
- [x] 3.2 Tests then `sandbox_propose_write_paths` tool + test that no decision tool is in `tools/list`
- [x] 3.3 apiclient/apistore methods for remote TUI

## 4. Frontends
- [x] 4.1 TUI Settings > Sandbox proposal rows + System warning (tests, uxlog)
- [x] 4.2 Web Settings panel + `SW_VERSION` bump
- [x] 4.3 macOS: ArgusKit proposals API + SwiftUI panel (`make mac-test`)

## 5. Docs and guidance
- [x] 5.1 Agent-facing builtin guidance: use the proposal tool
- [x] 5.2 README Reference (MCP tool, REST endpoints), `gotchas/sandbox.md` entry
- [x] 5.3 `make pre-pr`; archive this change into base specs before merge
