package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDB_AddPushSubscription(t *testing.T) {
	t.Run("inserts new subscription", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddPushSubscription(PushSubscription{
			Label:    "iPhone",
			Endpoint: "https://push.example/abc",
			P256dh:   "key1",
			Auth:     "auth1",
		})
		testutil.NoError(t, err)
		if id <= 0 {
			t.Errorf("expected positive id, got %d", id)
		}
	})

	t.Run("upserts on endpoint conflict", func(t *testing.T) {
		d := testDB(t)
		first, err := d.AddPushSubscription(PushSubscription{
			Label:    "iPhone",
			Endpoint: "https://push.example/abc",
			P256dh:   "key1",
			Auth:     "auth1",
		})
		testutil.NoError(t, err)

		// Same endpoint, different label/keys — should overwrite, not add.
		_, err = d.AddPushSubscription(PushSubscription{
			Label:    "iPhone-renamed",
			Endpoint: "https://push.example/abc",
			P256dh:   "key2",
			Auth:     "auth2",
		})
		testutil.NoError(t, err)

		subs, err := d.PushSubscriptions()
		testutil.NoError(t, err)
		testutil.Equal(t, len(subs), 1)
		testutil.Equal(t, subs[0].ID, first)
		testutil.Equal(t, subs[0].Label, "iPhone-renamed")
		testutil.Equal(t, subs[0].P256dh, "key2")
		testutil.Equal(t, subs[0].Auth, "auth2")
	})
}

func TestDB_PushSubscriptions(t *testing.T) {
	t.Run("returns empty slice when none registered", func(t *testing.T) {
		d := testDB(t)
		subs, err := d.PushSubscriptions()
		testutil.NoError(t, err)
		testutil.Equal(t, len(subs), 0)
	})

	t.Run("returns all registered subscriptions", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddPushSubscription(PushSubscription{
			Label:    "A",
			Endpoint: "https://push.example/a",
			P256dh:   "k", Auth: "u",
		})
		testutil.NoError(t, err)
		_, err = d.AddPushSubscription(PushSubscription{
			Label:    "B",
			Endpoint: "https://push.example/b",
			P256dh:   "k", Auth: "u",
		})
		testutil.NoError(t, err)

		subs, err := d.PushSubscriptions()
		testutil.NoError(t, err)
		testutil.Equal(t, len(subs), 2)
		// Ordered by id ASC.
		testutil.Equal(t, subs[0].Label, "A")
		testutil.Equal(t, subs[1].Label, "B")
		if subs[0].CreatedAt.IsZero() {
			t.Error("CreatedAt should be populated")
		}
	})
}

func TestDB_DeletePushSubscription(t *testing.T) {
	t.Run("removes subscription by id", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddPushSubscription(PushSubscription{
			Endpoint: "https://push.example/x",
			P256dh:   "k", Auth: "u",
		})
		testutil.NoError(t, err)

		testutil.NoError(t, d.DeletePushSubscription(id))

		subs, err := d.PushSubscriptions()
		testutil.NoError(t, err)
		testutil.Equal(t, len(subs), 0)
	})

	t.Run("returns error when id not found", func(t *testing.T) {
		d := testDB(t)
		err := d.DeletePushSubscription(9999)
		testutil.Error(t, err)
	})
}

func TestDB_DeletePushSubscriptionByEndpoint(t *testing.T) {
	t.Run("removes by endpoint", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddPushSubscription(PushSubscription{
			Endpoint: "https://push.example/keep",
			P256dh:   "k", Auth: "u",
		})
		testutil.NoError(t, err)
		_, err = d.AddPushSubscription(PushSubscription{
			Endpoint: "https://push.example/drop",
			P256dh:   "k", Auth: "u",
		})
		testutil.NoError(t, err)

		testutil.NoError(t, d.DeletePushSubscriptionByEndpoint("https://push.example/drop"))

		subs, err := d.PushSubscriptions()
		testutil.NoError(t, err)
		testutil.Equal(t, len(subs), 1)
		testutil.Equal(t, subs[0].Endpoint, "https://push.example/keep")
	})

	t.Run("no-op for unknown endpoint", func(t *testing.T) {
		d := testDB(t)
		// Should not error even when endpoint is absent.
		testutil.NoError(t, d.DeletePushSubscriptionByEndpoint("https://nope.example"))
	})
}

func TestDB_GetConfigValue(t *testing.T) {
	t.Run("returns empty string when key missing", func(t *testing.T) {
		d := testDB(t)
		v, err := d.GetConfigValue("missing.key")
		testutil.NoError(t, err)
		testutil.Equal(t, v, "")
	})

	t.Run("returns set value", func(t *testing.T) {
		d := testDB(t)
		testutil.NoError(t, d.SetConfigValue("ui.theme", "dark"))
		v, err := d.GetConfigValue("ui.theme")
		testutil.NoError(t, err)
		testutil.Equal(t, v, "dark")
	})
}
