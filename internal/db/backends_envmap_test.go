package db

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

// The env_vars credential mapping round-trips through SetBackend / Backends.
func TestDB_BackendEnvVarsRoundTrip(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackend("codex", config.Backend{
		Command: "codex",
		EnvVars: map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"},
	}))

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b, ok := backends["codex"]
	if !ok {
		t.Fatal("codex backend missing")
	}
	testutil.Equal(t, len(b.EnvVars), 1)
	testutil.Equal(t, b.EnvVars["OPENAI_API_KEY"], "HERA_OPENAI")

	// Clearing the mapping stores '' and reads back as an empty mapping.
	testutil.NoError(t, d.SetBackend("codex", config.Backend{Command: "codex"}))
	backends, err = d.Backends()
	testutil.NoError(t, err)
	testutil.Equal(t, len(backends["codex"].EnvVars), 0)
}

// The mapping persists ONLY the descriptor — never a secret value. This guards
// the invariant that a secret never enters the DB.
func TestDB_BackendEnvVars_StoresMappingNotValue(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackend("codex", config.Backend{
		Command: "codex",
		EnvVars: map[string]string{"OPENAI_API_KEY": "HERA_OPENAI"},
	}))

	// Read the raw stored column and assert it contains only the descriptor.
	var raw string
	testutil.NoError(t, d.conn.QueryRow(`SELECT env_vars FROM backends WHERE name='codex'`).Scan(&raw))
	testutil.Contains(t, raw, "HERA_OPENAI")
	testutil.Contains(t, raw, "OPENAI_API_KEY")
	// A descriptor is an env-var NAME; it must not look like a key value.
	if strings.Contains(raw, "sk-") {
		t.Fatalf("env_vars column looks like it stored a secret value: %q", raw)
	}
}

// seedDefaults seeds the codex credential mapping on a fresh database.
func TestSeedDefaults_SeedsCodexEnvVars(t *testing.T) {
	d := testDB(t) // testDB seeds defaults on open
	backends, err := d.Backends()
	testutil.NoError(t, err)
	codex, ok := backends["codex"]
	if !ok {
		t.Fatal("codex backend missing after seed")
	}
	testutil.Equal(t, codex.EnvVars["OPENAI_API_KEY"], "HERA_OPENAI")
	// No gemini backend is seeded.
	if _, ok := backends["gemini"]; ok {
		t.Fatal("did not expect a gemini backend to be seeded")
	}
}

// fixupBackends fills the codex mapping on a pre-existing row that lacks one,
// without clobbering a mapping the user has customized.
func TestFixupBackends_EnvVars(t *testing.T) {
	t.Run("fills empty mapping on existing row", func(t *testing.T) {
		d := testDB(t)
		// Simulate a pre-existing codex row with no mapping.
		_, err := d.conn.Exec(`UPDATE backends SET env_vars='' WHERE name='codex'`)
		testutil.NoError(t, err)

		testutil.NoError(t, d.fixupBackends())

		backends, err := d.Backends()
		testutil.NoError(t, err)
		testutil.Equal(t, backends["codex"].EnvVars["OPENAI_API_KEY"], "HERA_OPENAI")
	})

	t.Run("does not clobber a customized mapping", func(t *testing.T) {
		d := testDB(t)
		testutil.NoError(t, d.SetBackend("codex", config.Backend{
			Command: "codex --dangerously-bypass-approvals-and-sandbox",
			EnvVars: map[string]string{"OPENAI_API_KEY": "MY_CUSTOM_SOURCE"},
		}))

		testutil.NoError(t, d.fixupBackends())

		backends, err := d.Backends()
		testutil.NoError(t, err)
		testutil.Equal(t, backends["codex"].EnvVars["OPENAI_API_KEY"], "MY_CUSTOM_SOURCE")
	})
}
