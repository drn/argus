package config

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestAgentCommitSigningConfig(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		invalid          bool
	}{
		{"absent", "", "inherit", false},
		{"inherit", "[git]\nagent_commit_signing = 'inherit'", "inherit", false},
		{"unsigned", "[git]\nagent_commit_signing = 'unsigned'", "unsigned", false},
		{"invalid", "[git]\nagent_commit_signing = 'off'", "inherit", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := NewFileLoader(writeFile(t, tc.body))
			base := DefaultConfig()
			got := loader.Apply(base)
			if tc.invalid {
				testutil.Contains(t, loader.Err().Error(), "git.agent_commit_signing")
				testutil.Contains(t, loader.Err().Error(), "inherit or unsigned")
			} else {
				testutil.NoError(t, loader.Err())
			}
			testutil.Equal(t, got.Git.AgentCommitSigning, tc.want)
			testutil.Equal(t, base.Git.AgentCommitSigning, "inherit")
		})
	}
}
