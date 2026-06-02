package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDB_AddAPIToken(t *testing.T) {
	d := testDB(t)
	id, err := d.AddAPIToken("device-1", "abc123hash", "1234")
	testutil.NoError(t, err)
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}
}

func TestDB_APITokens(t *testing.T) {
	t.Run("empty when no tokens", func(t *testing.T) {
		d := testDB(t)
		tokens, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(tokens), 0)
	})

	t.Run("returns inserted tokens", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddAPIToken("a", "hash-a", "0001")
		testutil.NoError(t, err)
		_, err = d.AddAPIToken("b", "hash-b", "0002")
		testutil.NoError(t, err)

		tokens, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(tokens), 2)
		testutil.Equal(t, tokens[0].Label, "a")
		testutil.Equal(t, tokens[0].Last4, "0001")
		testutil.Equal(t, tokens[0].Revoked, false)
		if tokens[0].CreatedAt.IsZero() {
			t.Error("CreatedAt should be populated")
		}
	})
}

func TestDB_FindAPITokenByHash(t *testing.T) {
	t.Run("returns nil when not found", func(t *testing.T) {
		d := testDB(t)
		got, err := d.FindAPITokenByHash("nope")
		testutil.NoError(t, err)
		if got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("returns token and updates last_used", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPIToken("dev", "hash-x", "9999")
		testutil.NoError(t, err)

		got, err := d.FindAPITokenByHash("hash-x")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.ID, id)
		testutil.Equal(t, got.Label, "dev")
		testutil.Equal(t, got.Last4, "9999")
		testutil.Equal(t, got.Revoked, false)

		// Subsequent lookup should populate LastUsed.
		got2, err := d.FindAPITokenByHash("hash-x")
		testutil.NoError(t, err)
		if got2 == nil || got2.LastUsed.IsZero() {
			t.Error("expected LastUsed to be populated on second lookup")
		}
	})

	t.Run("returns nil for revoked token", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPIToken("dev", "hash-rev", "0000")
		testutil.NoError(t, err)
		testutil.NoError(t, d.RevokeAPIToken(id))

		got, err := d.FindAPITokenByHash("hash-rev")
		testutil.NoError(t, err)
		if got != nil {
			t.Errorf("expected nil for revoked, got %+v", got)
		}
	})
}

func TestDB_AddAPITokenWithScope(t *testing.T) {
	t.Run("persists scope on insert", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPITokenWithScope("ludwig token", "ludwig", "hash-1", "1234")
		testutil.NoError(t, err)
		if id <= 0 {
			t.Errorf("expected positive id, got %d", id)
		}

		got, err := d.FindAPITokenByHash("hash-1")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.Scope, "ludwig")
		testutil.Equal(t, got.Label, "ludwig token")
	})

	t.Run("blank scope behaves like device token", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddAPITokenWithScope("iPhone", "", "hash-2", "5678")
		testutil.NoError(t, err)

		got, err := d.FindAPITokenByHash("hash-2")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.Scope, "")
	})

	t.Run("existing AddAPIToken inserts empty scope", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddAPIToken("device-x", "hash-3", "9999")
		testutil.NoError(t, err)

		got, err := d.FindAPITokenByHash("hash-3")
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.Scope, "")
	})

	t.Run("APITokens returns scope alongside other fields", func(t *testing.T) {
		d := testDB(t)
		_, err := d.AddAPITokenWithScope("plugin a", "a", "h-a", "0001")
		testutil.NoError(t, err)
		_, err = d.AddAPIToken("device b", "h-b", "0002")
		testutil.NoError(t, err)

		toks, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(toks), 2)

		byHash := map[string]APIToken{}
		for _, t := range toks {
			byHash[t.Hash] = t
		}
		testutil.Equal(t, byHash["h-a"].Scope, "a")
		testutil.Equal(t, byHash["h-b"].Scope, "")
	})
}

func TestDB_FindAPITokenByID(t *testing.T) {
	t.Run("returns row for valid id", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPITokenWithScope("ludwig", "ludwig", "hash-id", "1111")
		testutil.NoError(t, err)

		got, err := d.FindAPITokenByID(id)
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.ID, id)
		testutil.Equal(t, got.Scope, "ludwig")
		testutil.Equal(t, got.Revoked, false)
	})

	t.Run("returns nil for missing id", func(t *testing.T) {
		d := testDB(t)
		got, err := d.FindAPITokenByID(99999)
		testutil.NoError(t, err)
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})

	t.Run("returns revoked token with Revoked=true", func(t *testing.T) {
		// Unlike FindAPITokenByHash (which hides revoked rows for auth), the
		// by-ID lookup intentionally returns revoked rows so the revoke
		// cascade in handleRevokeToken can read the scope even after revoke.
		d := testDB(t)
		id, err := d.AddAPITokenWithScope("plug", "plug", "hash-rev2", "0000")
		testutil.NoError(t, err)
		testutil.NoError(t, d.RevokeAPIToken(id))

		got, err := d.FindAPITokenByID(id)
		testutil.NoError(t, err)
		if got == nil {
			t.Fatal("expected non-nil token")
		}
		testutil.Equal(t, got.Revoked, true)
		testutil.Equal(t, got.Scope, "plug")
	})

	t.Run("LastUsed populated when non-zero", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPITokenWithScope("plug", "plug", "hash-lu", "0000")
		testutil.NoError(t, err)
		// FindAPITokenByHash bumps LastUsed; trigger it so the by-ID read
		// hits the non-zero branch.
		_, err = d.FindAPITokenByHash("hash-lu")
		testutil.NoError(t, err)

		got, err := d.FindAPITokenByID(id)
		testutil.NoError(t, err)
		if got.LastUsed.IsZero() {
			t.Error("expected LastUsed to be populated")
		}
	})
}

func TestDB_RevokeAPIToken(t *testing.T) {
	t.Run("marks token revoked", func(t *testing.T) {
		d := testDB(t)
		id, err := d.AddAPIToken("dev", "h", "1111")
		testutil.NoError(t, err)
		testutil.NoError(t, d.RevokeAPIToken(id))

		// Idempotent — second revoke succeeds (row affected resets revoked_at, still > 0).
		testutil.NoError(t, d.RevokeAPIToken(id))

		tokens, err := d.APITokens()
		testutil.NoError(t, err)
		testutil.Equal(t, len(tokens), 1)
		testutil.Equal(t, tokens[0].Revoked, true)
	})

	t.Run("returns error for missing id", func(t *testing.T) {
		d := testDB(t)
		err := d.RevokeAPIToken(99999)
		testutil.Error(t, err)
	})
}
