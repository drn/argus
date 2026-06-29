package agent

import (
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func TestKnownModels(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{"claude bare", "claude", []string{"opus", "sonnet", "haiku"}},
		{"claude abs path", "/usr/local/bin/claude", []string{"opus", "sonnet", "haiku"}},
		{"codex with flags", "codex --dangerously-bypass-approvals-and-sandbox", []string{"gpt-5-codex", "gpt-5"}},
		{"pi is empty", "pi", nil},
		{"opencode is empty (custom-only)", "opencode", nil},
		{"custom is empty", "bash", nil},
		{"empty command", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, KnownModels(tc.command), tc.want)
		})
	}
}

func TestKnownModels_ReturnsFreshSlice(t *testing.T) {
	a := KnownModels("claude")
	a[0] = "mutated"
	b := KnownModels("claude")
	testutil.Equal(t, b[0], "opus") // not affected by the prior mutation
}

func TestBackendModels(t *testing.T) {
	t.Run("falls back to built-in when no override", func(t *testing.T) {
		testutil.DeepEqual(t, BackendModels(config.Backend{Command: "claude"}), []string{"opus", "sonnet", "haiku"})
	})
	t.Run("override wins when non-empty", func(t *testing.T) {
		got := BackendModels(config.Backend{Command: "claude", Models: []string{"x", "y"}})
		testutil.DeepEqual(t, got, []string{"x", "y"})
	})
	t.Run("empty override falls back", func(t *testing.T) {
		testutil.DeepEqual(t, BackendModels(config.Backend{Command: "codex", Models: []string{}}), []string{"gpt-5-codex", "gpt-5"})
	})
	t.Run("override slice is copied", func(t *testing.T) {
		src := []string{"a", "b"}
		got := BackendModels(config.Backend{Command: "claude", Models: src})
		got[0] = "mutated"
		testutil.Equal(t, src[0], "a") // caller's slice untouched
	})
}
