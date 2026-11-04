package daemon

import (
	"testing"

	"github.com/drn/argus/internal/mcp"
	"github.com/drn/argus/internal/testutil"
)

func TestHeadlessInputFromMCP(t *testing.T) {
	got := headlessInputFromMCP(mcp.TaskCreateInput{
		Name:             "n",
		Prompt:           "p",
		Project:          "proj",
		Model:            "opus",
		AutoName:         true,
		BaseBranch:       "argus/base",
		Account:          "work",
		InheritedAccount: true,
		SandboxOverride:  "enabled",
	})
	want := HeadlessInput{
		Name:             "n",
		Prompt:           "p",
		Project:          "proj",
		Model:            "opus",
		AutoName:         true,
		BaseBranch:       "argus/base",
		Account:          "work",
		InheritedAccount: true,
		SandboxOverride:  "enabled",
	}
	testutil.DeepEqual(t, got, want)
}
