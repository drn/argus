package mcp

import "github.com/drn/argus/internal/db"

func newProposalForTest(id string) *db.SandboxProposal {
	return &db.SandboxProposal{ID: id, Status: db.SandboxProposalPending}
}
