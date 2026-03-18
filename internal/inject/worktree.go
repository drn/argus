package inject

import (
	"log"
	"sync/atomic"

	injectcodex "github.com/drn/argus/internal/inject/codex"
)

// mcpPort holds the actual MCP server port for worktree injection.
// Set by the daemon after the MCP server starts listening.
// Zero means the MCP server is not yet running.
var mcpPort atomic.Int32

// SetMCPPort stores the MCP server port so that InjectWorktreeAll can use it.
func SetMCPPort(port int) {
	mcpPort.Store(int32(port))
}

// MCPPort returns the currently configured MCP server port.
func MCPPort() int {
	return int(mcpPort.Load())
}

// InjectWorktreeAll injects both Claude and Codex MCP configs into the given
// worktree path. Uses the port stored via SetMCPPort. No-op if port is 0.
func InjectWorktreeAll(worktreePath string) {
	port := MCPPort()
	if port == 0 {
		return
	}
	if err := InjectWorktree(worktreePath, port); err != nil {
		log.Printf("inject worktree claude: %v", err)
	}
	if err := injectcodex.InjectWorktree(worktreePath, port); err != nil {
		log.Printf("inject worktree codex: %v", err)
	}
}
