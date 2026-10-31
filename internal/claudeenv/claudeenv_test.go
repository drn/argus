package claudeenv

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestIsAuthOverride(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"} {
		t.Run(name, func(t *testing.T) { testutil.True(t, IsAuthOverride(name)) })
	}
	testutil.False(t, IsAuthOverride("CLAUDE_CONFIG_DIR"))
}

func TestParentSessionMarkers(t *testing.T) {
	testutil.DeepEqual(t, ParentSessionMarkers, []string{
		"CLAUDECODE",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_EXECPATH",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_SESSION_ATTENDED",
		"CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_PID",
		"CLAUDE_EFFORT",
	})
}

func TestIsParentSessionMarker(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"CLAUDECODE", true},
		{"CLAUDE_CODE_MESSAGING_TOKEN", true},
		{"CLAUDE_CODE_ENABLE_TELEMETRY", false},
		{"CLAUDE_CONFIG_DIR", false},
		{"claudecode", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, IsParentSessionMarker(tc.name), tc.want)
		})
	}
}

func TestForcePersistenceEnv(t *testing.T) {
	testutil.Equal(t, ForcePersistenceEnv, "CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1")
}
