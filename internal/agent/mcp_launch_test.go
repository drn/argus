package agent

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestBuildCmd_TaskScopedMCP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend string
		resume  bool
		port    int
		want    string
	}{
		{"claude fresh", "claude", false, 7743, "--mcp-config"},
		{"claude resume", "claude", true, 7743, "--mcp-config"},
		{"codex fresh", "codex", false, 7743, "mcp_servers.argus.url"},
		{"codex resume", "codex", true, 7743, "mcp_servers.argus.url"},
		{"claude disabled", "claude", false, 0, ""},
		{"codex disabled", "codex", false, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.MCPPort = tc.port
			task := &model.Task{ID: "mcp-task", Backend: tc.backend, Worktree: t.TempDir(), SessionID: "a-session"}
			cmd, cleanup, err := BuildCmd(task, cfg, tc.resume)
			testutil.NoError(t, err)
			if cleanup != nil {
				defer cleanup()
			}
			command := cmd.Args[2]
			if tc.want == "" {
				if strings.Contains(command, "mcp_servers.argus") || strings.Contains(command, "--mcp-config") {
					t.Fatalf("unexpected MCP configuration in %q", command)
				}
				return
			}
			testutil.Contains(t, command, tc.want)
			testutil.Contains(t, command, "http://localhost:7743/mcp")
			if tc.backend == "codex" && tc.resume {
				testutil.Contains(t, command, " resume ")
			}
		})
	}
}
