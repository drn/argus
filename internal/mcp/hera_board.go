package mcp

import (
	"encoding/json"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/uxlog"
)

type boardStore interface {
	PostHeraBoardItem(int64, string, string, string) (*db.HeraBoardItem, error)
	UpdateHeraBoardItem(int64, int64, string, string, string) (*db.HeraBoardItem, error)
	ResolveHeraBoardItem(int64, int64, string, string) error
	ListHeraBoardItems(int64, bool) ([]*db.HeraBoardItem, error)
}

func boardToolDefs() []Tool {
	var out []Tool
	for _, verb := range []string{"post", "update", "resolve", "list"} {
		required := []string{"cwd"}
		switch verb {
		case "post":
			required = append(required, "kind", "subject", "body")
		case "update":
			required = append(required, "id", "kind", "subject", "body")
		case "resolve":
			required = append(required, "id")
		}
		out = append(out, Tool{Name: "hera_board_" + verb, Description: "Coordinator-only human attention board: " + verb + " items owned by your coordinator role. Put things needing Aaron's decision or attention on your board instead of only in chat. Rewrite an item when newer information supersedes it; resolve when settled, including after Aaron replies and follow-up concludes. Material edits mark unread. update replaces kind, subject and body; list defaults to open items.", InputSchema: map[string]interface{}{
			"type": "object", "required": required, "properties": map[string]interface{}{
				"cwd": map[string]string{"type": "string"}, "orchestrator": map[string]string{"type": "string"}, "id": map[string]string{"type": "integer"},
				"kind": map[string]interface{}{"type": "string", "enum": []string{"decision", "fyi"}}, "subject": map[string]string{"type": "string"}, "body": map[string]string{"type": "string"}, "note": map[string]string{"type": "string"}, "include_resolved": map[string]string{"type": "boolean"},
			},
		}})
	}
	return out
}

func (s *Server) toolHeraBoard(id interface{}, args json.RawMessage, verb string) *Response {
	if !s.heraEnabled() {
		return toolError(id, "hera not configured")
	}
	var p struct {
		Cwd             string `json:"cwd"`
		Orchestrator    string `json:"orchestrator"`
		ID              int64  `json:"id"`
		Kind            string `json:"kind"`
		Subject         string `json:"subject"`
		Body            string `json:"body"`
		Note            string `json:"note"`
		IncludeResolved bool   `json:"include_resolved"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return toolError(id, "invalid board arguments")
	}
	if p.Cwd == "" {
		return toolError(id, "cwd is required")
	}
	caller, err := s.resolveCallerRole(p.Cwd, p.Orchestrator)
	if err != nil {
		return toolError(id, err.Error())
	}
	if caller.role.Kind != db.HeraKindCoordinator {
		return toolError(id, "board tools require a coordinator binding; report to your coordinator")
	}
	store, ok := s.heraStore.(boardStore)
	if !ok {
		return toolError(id, "board store unavailable")
	}
	var result any
	switch verb {
	case "post":
		result, err = store.PostHeraBoardItem(caller.role.ID, p.Kind, p.Subject, p.Body)
	case "update":
		result, err = store.UpdateHeraBoardItem(caller.role.ID, p.ID, p.Kind, p.Subject, p.Body)
	case "resolve":
		err = store.ResolveHeraBoardItem(caller.role.ID, p.ID, "agent", p.Note)
		result = "resolved"
	case "list":
		result, err = store.ListHeraBoardItems(caller.role.ID, p.IncludeResolved)
	}
	if err != nil {
		uxlog.Log("[board] MCP %s failed role=%d: %v", verb, caller.role.ID, err)
		return toolError(id, err.Error())
	}
	uxlog.Log("[board] MCP %s succeeded role=%d", verb, caller.role.ID)
	raw, err := json.Marshal(result)
	if err != nil {
		return toolError(id, err.Error())
	}
	return toolResult(id, string(raw))
}
