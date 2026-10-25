package agent

import "strings"

// BridgeGeneration identifies one process launch, independently of the
// transcript (which can survive resume) or mod boot (which changes on reload).
func (s *Session) BridgeGeneration() string {
	if s == nil || s.Cmd == nil {
		return ""
	}
	for i := len(s.Cmd.Env) - 1; i >= 0; i-- {
		entry := s.Cmd.Env[i]
		if v, ok := strings.CutPrefix(entry, "ARGUS_BRIDGE_GENERATION="); ok {
			return v
		}
	}
	return ""
}

// SessionBridgeGeneration supports older supervisors and test handles by
// returning empty when the optional launch-identity capability is absent.
func SessionBridgeGeneration(s SessionHandle) string {
	if v, ok := s.(interface{ BridgeGeneration() string }); ok {
		return v.BridgeGeneration()
	}
	return ""
}
