package db

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/kb"
	"github.com/drn/argus/internal/testutil"
)

func TestDB_DeleteBackend(t *testing.T) {
	t.Run("removes existing backend", func(t *testing.T) {
		d := testDB(t)
		testutil.NoError(t, d.SetBackend("custom", config.Backend{Command: "x", PromptFlag: "-p"}))

		testutil.NoError(t, d.DeleteBackend("custom"))

		backends, err := d.Backends()
		testutil.NoError(t, err)
		if _, ok := backends["custom"]; ok {
			t.Error("custom backend should be deleted")
		}
	})

	t.Run("no error when backend missing", func(t *testing.T) {
		d := testDB(t)
		// Deleting a non-existent backend is a no-op (DELETE returns 0 rows).
		testutil.NoError(t, d.DeleteBackend("nonexistent"))
	})
}

func TestDB_KBMetadataMap(t *testing.T) {
	t.Run("empty map for fresh DB", func(t *testing.T) {
		d := testDB(t)
		m, err := d.KBMetadataMap()
		testutil.NoError(t, err)
		testutil.Equal(t, len(m), 0)
	})

	t.Run("returns path → mtime for upserted docs", func(t *testing.T) {
		d := testDB(t)
		mtA := time.Unix(1234567890, 0)
		mtB := time.Unix(1234567999, 0)
		testutil.NoError(t, d.KBUpsert(&kb.Document{
			Path: "notes/a.md", Title: "A", Body: "ba",
			Tier: "hot", ModifiedAt: mtA, IngestedAt: time.Now(), WordCount: 1,
		}))
		testutil.NoError(t, d.KBUpsert(&kb.Document{
			Path: "notes/b.md", Title: "B", Body: "bb",
			Tier: "hot", ModifiedAt: mtB, IngestedAt: time.Now(), WordCount: 1,
		}))

		m, err := d.KBMetadataMap()
		testutil.NoError(t, err)
		testutil.Equal(t, len(m), 2)
		testutil.Equal(t, m["notes/a.md"], mtA.Unix())
		testutil.Equal(t, m["notes/b.md"], mtB.Unix())
	})
}

// Backend default-model column round-trips through SetBackend / Backends.
func TestDB_BackendModelRoundTrip(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetBackend("custom", config.Backend{Command: "claude", PromptFlag: "-p", Model: "opus"}))

	backends, err := d.Backends()
	testutil.NoError(t, err)
	b, ok := backends["custom"]
	if !ok {
		t.Fatal("custom backend missing")
	}
	testutil.Equal(t, b.Command, "claude")
	testutil.Equal(t, b.PromptFlag, "-p")
	testutil.Equal(t, b.Model, "opus")

	// Upsert clears the model when set back to empty.
	testutil.NoError(t, d.SetBackend("custom", config.Backend{Command: "claude"}))
	backends, err = d.Backends()
	testutil.NoError(t, err)
	testutil.Equal(t, backends["custom"].Model, "")
}
