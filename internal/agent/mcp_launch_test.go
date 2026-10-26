package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drn/argus/internal/mcplimits"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestBuildCmd_TaskScopedMCP(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CODEX_SQLITE_HOME", "")
	t.Setenv("MCP_TOOL_TIMEOUT", "60000")
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
		{"claude resume disabled", "claude", true, 0, ""},
		{"codex resume disabled", "codex", true, 0, ""},
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
			timeoutEnv := ""
			for _, entry := range cmd.Environ() {
				if strings.HasPrefix(entry, "MCP_TOOL_TIMEOUT=") {
					timeoutEnv = strings.TrimPrefix(entry, "MCP_TOOL_TIMEOUT=")
				}
			}
			if tc.want == "" {
				testutil.Equal(t, timeoutEnv, "60000")
				testutil.Equal(t, strings.Contains(command, "mcp_servers.argus"), false)
				testutil.Equal(t, strings.Contains(command, "--mcp-config"), false)
				return
			}
			testutil.Equal(t, strings.Contains(command, tc.want), true)
			testutil.Equal(t, strings.Contains(command, "http://localhost:7743/mcp"), true)
			if tc.backend == "claude" {
				_, rest, ok := strings.Cut(command, "--mcp-config '")
				testutil.Equal(t, ok, true)
				raw, _, ok := strings.Cut(rest, "'")
				testutil.Equal(t, ok, true)
				var cfg struct {
					Servers map[string]struct {
						Timeout int `json:"timeout"`
					} `json:"mcpServers"`
				}
				testutil.NoError(t, json.Unmarshal([]byte(raw), &cfg))
				testutil.Equal(t, cfg.Servers["argus"].Timeout, 150000)
				testutil.Equal(t, cfg.Servers["argus"].Timeout > mcplimits.HeraInboxTimeoutSeconds*1000, true)
				testutil.Equal(t, timeoutEnv, "150000")
			} else {
				_, rest, ok := strings.Cut(command, "-c 'mcp_servers.argus.tool_timeout_sec=")
				testutil.Equal(t, ok, true)
				value, _, ok := strings.Cut(rest, "'")
				testutil.Equal(t, ok, true)
				testutil.Equal(t, value, "150")
				testutil.Equal(t, timeoutEnv, "60000")
			}
			if tc.backend == "codex" && tc.resume {
				testutil.Equal(t, strings.Contains(command, " resume "), true)
			}
		})
	}
}
