package db

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestPluginViews_AddGetListDelete(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	row, err := d.AddPluginView("scope-a", "Alpha", "ctrl+a", "ws://a")
	testutil.NoError(t, err)
	if row.ID <= 0 {
		t.Fatalf("expected positive ID, got %d", row.ID)
	}
	testutil.Equal(t, row.Scope, "scope-a")
	testutil.Equal(t, row.Title, "Alpha")
	testutil.Equal(t, row.Hotkey, "ctrl+a")
	testutil.Equal(t, row.CallbackURL, "ws://a")
	if row.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be stamped")
	}

	got, err := d.GetPluginView("scope-a", "Alpha")
	testutil.NoError(t, err)
	testutil.Equal(t, got.CallbackURL, "ws://a")
	if got.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to round-trip from db")
	}

	miss, err := d.GetPluginView("scope-a", "Missing")
	testutil.NoError(t, err)
	testutil.Nil(t, miss)

	_, err = d.AddPluginView("scope-b", "Beta", "ctrl+b", "ws://b")
	testutil.NoError(t, err)
	all, err := d.PluginViews()
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 2)
	testutil.Equal(t, all[0].Title, "Alpha")
	testutil.Equal(t, all[1].Title, "Beta")

	ok, err := d.DeletePluginView("scope-a", "Alpha")
	testutil.NoError(t, err)
	testutil.True(t, ok)

	ok, err = d.DeletePluginView("scope-a", "Alpha")
	testutil.NoError(t, err)
	testutil.False(t, ok)
}

func TestPluginViews_UniqueScopeTitleRejected(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, err = d.AddPluginView("scope-a", "Alpha", "", "ws://a")
	testutil.NoError(t, err)

	_, err = d.AddPluginView("scope-a", "Alpha", "", "ws://b")
	if err == nil {
		t.Fatal("expected UNIQUE constraint to reject duplicate")
	}
}

func TestPluginViews_DeleteByScope(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	_, _ = d.AddPluginView("scope-a", "A1", "", "ws://1")
	_, _ = d.AddPluginView("scope-a", "A2", "", "ws://2")
	_, _ = d.AddPluginView("scope-b", "B1", "", "ws://3")

	n, err := d.DeletePluginViewsByScope("scope-a")
	testutil.NoError(t, err)
	testutil.Equal(t, n, int64(2))

	all, err := d.PluginViews()
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 1)
	testutil.Equal(t, all[0].Scope, "scope-b")
}

func TestPluginViews_DeleteByScope_NoMatchReturnsZero(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	n, err := d.DeletePluginViewsByScope("nope")
	testutil.NoError(t, err)
	testutil.Equal(t, n, int64(0))
}

func TestPluginViews_OperationsErrOnClosedDB(t *testing.T) {
	d, err := OpenInMemory()
	testutil.NoError(t, err)
	_ = d.Close()

	if _, err := d.AddPluginView("s", "t", "", "ws://x"); err == nil {
		t.Fatal("expected error on closed DB")
	}
	if _, err := d.GetPluginView("s", "t"); err == nil {
		t.Fatal("expected error on closed DB")
	}
	if _, err := d.PluginViews(); err == nil {
		t.Fatal("expected error on closed DB")
	}
	if _, err := d.DeletePluginView("s", "t"); err == nil {
		t.Fatal("expected error on closed DB")
	}
	if _, err := d.DeletePluginViewsByScope("s"); err == nil {
		t.Fatal("expected error on closed DB")
	}
}
