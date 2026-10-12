package backendkind

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestOf(t *testing.T) {
	cases := []struct {
		command string
		want    Kind
	}{
		{"claude", Claude},
		{"claude --dangerously-skip-permissions", Claude},
		{"/usr/local/bin/claude", Claude},
		{"codex --dangerously-bypass-approvals-and-sandbox", Codex},
		{"pi", Pi},
		{"opencode --auto", Opencode},
		{"agy", Antigravity},
		{"/opt/homebrew/bin/agy --model x", Antigravity},
		{"antigravity", Custom},
		{"claude-wrapper", Custom},
		{"bash -c 'sleep 1'", Custom},
		{"", Custom},
		{"   ", Custom},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			testutil.Equal(t, Of(tc.command), tc.want)
		})
	}
}

func TestSpecOf(t *testing.T) {
	t.Run("codex skill prefix", func(t *testing.T) {
		testutil.Equal(t, SpecOf("codex").SkillPrefix, "$")
	})
	t.Run("antigravity label", func(t *testing.T) {
		testutil.Equal(t, SpecOf("agy").Label, "Antigravity")
	})
	t.Run("custom defaults", func(t *testing.T) {
		s := SpecOf("mything")
		testutil.Equal(t, s.Kind, Custom)
		testutil.Equal(t, s.SkillPrefix, "/")
		testutil.Equal(t, s.Label, "")
	})
}

func TestLookup(t *testing.T) {
	t.Run("known", func(t *testing.T) {
		s, ok := Lookup(Opencode)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, s.Label, "OpenCode")
	})
	t.Run("custom", func(t *testing.T) {
		s, ok := Lookup(Custom)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, s.Kind, Custom)
	})
	t.Run("unknown", func(t *testing.T) {
		_, ok := Lookup("nope")
		testutil.Equal(t, ok, false)
	})
}

func TestAll(t *testing.T) {
	all := All()
	testutil.Equal(t, len(all), 5)
	all[0].Label = "mutated"
	testutil.Equal(t, All()[0].Label, "Claude Code")
	seen := map[string]bool{}
	for _, s := range all {
		for _, b := range s.Binaries {
			testutil.Equal(t, seen[b], false)
			seen[b] = true
		}
	}
}

func TestBinary(t *testing.T) {
	testutil.Equal(t, Binary("/a/b/agy --x"), "agy")
	testutil.Equal(t, Binary(""), "")
}
