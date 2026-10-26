package config

import "fmt"

// GitConfig controls Git defaults in agent child processes only. These are
// config.toml-only settings; host Git and shared repository config are untouched.
type GitConfig struct {
	AgentCommitSigning string `toml:"agent_commit_signing"`
}

// Validate rejects a misspelled policy instead of silently invoking a signer.
// The zero value is equivalent to inherit for programmatically built configs.
func (g GitConfig) Validate() error {
	switch g.AgentCommitSigning {
	case "", "inherit", "unsigned":
		return nil
	default:
		return fmt.Errorf("git.agent_commit_signing must be inherit or unsigned")
	}
}
