package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestDB_UpsertPluginMCPTool(t *testing.T) {
	t.Run("insert + read back", func(t *testing.T) {
		d := testDB(t)
		tool := &PluginMCPTool{
			Name:         "ludwig_decision_add",
			Scope:        "ludwig",
			Description:  "Record a decision.",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			CallbackURL:  "http://127.0.0.1:9991/mcp/decision",
			AuthHeader:   "Bearer plugin-secret",
			RegisteredAt: time.Unix(1700000000, 0).UTC(),
			LastSeenAt:   time.Unix(1700000010, 0).UTC(),
		}
		testutil.NoError(t, d.UpsertPluginMCPTool(tool))

		got, err := d.GetPluginMCPTool("ludwig_decision_add")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil tool")
		}
		testutil.Equal(t, got.Name, "ludwig_decision_add")
		testutil.Equal(t, got.Scope, "ludwig")
		testutil.Equal(t, got.Description, "Record a decision.")
		testutil.Equal(t, string(got.InputSchema), `{"type":"object"}`)
		testutil.Equal(t, got.CallbackURL, "http://127.0.0.1:9991/mcp/decision")
		testutil.Equal(t, got.AuthHeader, "Bearer plugin-secret")
		testutil.Equal(t, got.RegisteredAt.Unix(), int64(1700000000))
		testutil.Equal(t, got.LastSeenAt.Unix(), int64(1700000010))
	})

	t.Run("upsert by name overwrites mutable fields, preserves registered_at on input default", func(t *testing.T) {
		d := testDB(t)
		first := &PluginMCPTool{
			Name:         "ludwig_one",
			Scope:        "ludwig",
			Description:  "v1",
			InputSchema:  json.RawMessage(`{"a":1}`),
			CallbackURL:  "http://127.0.0.1/v1",
			RegisteredAt: time.Unix(1700000000, 0).UTC(),
			LastSeenAt:   time.Unix(1700000000, 0).UTC(),
		}
		testutil.NoError(t, d.UpsertPluginMCPTool(first))

		second := &PluginMCPTool{
			Name:         "ludwig_one",
			Scope:        "ludwig",
			Description:  "v2",
			InputSchema:  json.RawMessage(`{"a":2}`),
			CallbackURL:  "http://127.0.0.1/v2",
			AuthHeader:   "Bearer different",
			RegisteredAt: time.Unix(1700000000, 0).UTC(),
			LastSeenAt:   time.Unix(1700000099, 0).UTC(),
		}
		testutil.NoError(t, d.UpsertPluginMCPTool(second))

		got, err := d.GetPluginMCPTool("ludwig_one")
		testutil.NoError(t, err)
		testutil.Equal(t, got.Description, "v2")
		testutil.Equal(t, string(got.InputSchema), `{"a":2}`)
		testutil.Equal(t, got.CallbackURL, "http://127.0.0.1/v2")
		testutil.Equal(t, got.AuthHeader, "Bearer different")
		testutil.Equal(t, got.RegisteredAt.Unix(), int64(1700000000))
		testutil.Equal(t, got.LastSeenAt.Unix(), int64(1700000099))
	})
}

func TestDB_UpsertPluginMCPTool_NilInput(t *testing.T) {
	d := testDB(t)
	err := d.UpsertPluginMCPTool(nil)
	testutil.Error(t, err)
}

func TestDB_UpsertPluginMCPTool_DefaultsInputSchema(t *testing.T) {
	// A row inserted with a nil InputSchema must round-trip as "{}" so
	// downstream tools/list responses don't emit `null` for the schema.
	d := testDB(t)
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "alpha_nilschema", Scope: "alpha", CallbackURL: "http://x",
		RegisteredAt: time.Unix(1, 0), LastSeenAt: time.Unix(1, 0),
		// InputSchema: nil (zero value)
	}))
	got, err := d.GetPluginMCPTool("alpha_nilschema")
	testutil.NoError(t, err)
	testutil.Equal(t, string(got.InputSchema), "{}")
}

func TestDB_GetPluginMCPTool_Missing(t *testing.T) {
	d := testDB(t)
	got, err := d.GetPluginMCPTool("missing")
	testutil.NoError(t, err)
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDB_PluginMCPTools(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "ludwig_b", Scope: "ludwig", CallbackURL: "http://x/b",
		InputSchema: json.RawMessage(`{}`), RegisteredAt: time.Unix(2, 0), LastSeenAt: time.Unix(2, 0),
	}))
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "alpha_a", Scope: "alpha", CallbackURL: "http://x/a",
		InputSchema: json.RawMessage(`{}`), RegisteredAt: time.Unix(1, 0), LastSeenAt: time.Unix(1, 0),
	}))

	got, err := d.PluginMCPTools()
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)
	// Stable ORDER BY name.
	testutil.Equal(t, got[0].Name, "alpha_a")
	testutil.Equal(t, got[1].Name, "ludwig_b")
}

func TestDB_DeletePluginMCPTool(t *testing.T) {
	t.Run("removes matching row", func(t *testing.T) {
		d := testDB(t)
		testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
			Name: "ludwig_one", Scope: "ludwig", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
			RegisteredAt: time.Now(), LastSeenAt: time.Now(),
		}))
		removed, err := d.DeletePluginMCPTool("ludwig_one")
		testutil.NoError(t, err)
		testutil.Equal(t, removed, true)

		got, err := d.GetPluginMCPTool("ludwig_one")
		testutil.NoError(t, err)
		if got != nil {
			t.Fatal("expected tool to be gone")
		}
	})

	t.Run("missing name returns false", func(t *testing.T) {
		d := testDB(t)
		removed, err := d.DeletePluginMCPTool("nope")
		testutil.NoError(t, err)
		testutil.Equal(t, removed, false)
	})
}

func TestDB_DeletePluginMCPToolsByScope(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "ludwig_one", Scope: "ludwig", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
		RegisteredAt: time.Now(), LastSeenAt: time.Now(),
	}))
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "ludwig_two", Scope: "ludwig", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
		RegisteredAt: time.Now(), LastSeenAt: time.Now(),
	}))
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "alpha_one", Scope: "alpha", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
		RegisteredAt: time.Now(), LastSeenAt: time.Now(),
	}))

	removed, err := d.DeletePluginMCPToolsByScope("ludwig")
	testutil.NoError(t, err)
	testutil.Equal(t, removed, 2)

	tools, err := d.PluginMCPTools()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tools), 1)
	testutil.Equal(t, tools[0].Name, "alpha_one")
}

func TestDB_DeletePluginMCPToolsIdle(t *testing.T) {
	d := testDB(t)
	fresh := time.Unix(1700000200, 0).UTC()
	stale := time.Unix(1700000000, 0).UTC()

	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "ludwig_old", Scope: "ludwig", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
		RegisteredAt: stale, LastSeenAt: stale,
	}))
	testutil.NoError(t, d.UpsertPluginMCPTool(&PluginMCPTool{
		Name: "ludwig_new", Scope: "ludwig", CallbackURL: "http://x", InputSchema: json.RawMessage(`{}`),
		RegisteredAt: fresh, LastSeenAt: fresh,
	}))

	// Cutoff between stale and fresh — only the stale row should fall.
	cutoff := time.Unix(1700000100, 0).UTC()
	removed, err := d.DeletePluginMCPToolsIdle(cutoff)
	testutil.NoError(t, err)
	testutil.Equal(t, len(removed), 1)
	testutil.Equal(t, removed[0].Name, "ludwig_old")

	tools, err := d.PluginMCPTools()
	testutil.NoError(t, err)
	testutil.Equal(t, len(tools), 1)
	testutil.Equal(t, tools[0].Name, "ludwig_new")
}
