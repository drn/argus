package views

import (
	"errors"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func newRegistry(t *testing.T) *Registry {
	t.Helper()
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })
	return New(database)
}

func TestRegister_Persists(t *testing.T) {
	r := newRegistry(t)

	v, err := r.Register("plugin-ludwig", "Ludwig", "ctrl+l", "ws://127.0.0.1:5111/view")
	testutil.NoError(t, err)
	testutil.Equal(t, v.Scope, "plugin-ludwig")
	testutil.Equal(t, v.Title, "Ludwig")
	testutil.Equal(t, v.Hotkey, "ctrl+l")
	testutil.Equal(t, v.CallbackURL, "ws://127.0.0.1:5111/view")
	if v.ID <= 0 {
		t.Fatalf("expected positive ID, got %d", v.ID)
	}
}

func TestRegister_RequiresTitle(t *testing.T) {
	r := newRegistry(t)

	_, err := r.Register("plugin-ludwig", "  ", "ctrl+l", "ws://127.0.0.1:5111/view")
	if !errors.Is(err, ErrTitleRequired) {
		t.Fatalf("expected ErrTitleRequired, got %v", err)
	}
}

func TestRegister_RequiresCallbackURL(t *testing.T) {
	r := newRegistry(t)

	_, err := r.Register("plugin-ludwig", "Ludwig", "ctrl+l", "")
	if !errors.Is(err, ErrCallbackURLRequired) {
		t.Fatalf("expected ErrCallbackURLRequired, got %v", err)
	}
}

func TestRegister_DuplicateRejected(t *testing.T) {
	r := newRegistry(t)

	_, err := r.Register("plugin-ludwig", "Ludwig", "ctrl+l", "ws://a")
	testutil.NoError(t, err)

	_, err = r.Register("plugin-ludwig", "Ludwig", "ctrl+l", "ws://b")
	if !errors.Is(err, ErrViewExists) {
		t.Fatalf("expected ErrViewExists, got %v", err)
	}
}

func TestRegister_DifferentScopesSameTitleOK(t *testing.T) {
	r := newRegistry(t)

	_, err := r.Register("plugin-a", "Dashboard", "ctrl+d", "ws://a")
	testutil.NoError(t, err)
	_, err = r.Register("plugin-b", "Dashboard", "ctrl+e", "ws://b")
	testutil.NoError(t, err)

	all := r.List()
	testutil.Equal(t, len(all), 2)
}

func TestGet_HitAndMiss(t *testing.T) {
	r := newRegistry(t)

	_, err := r.Register("plugin-ludwig", "Ludwig", "ctrl+l", "ws://a")
	testutil.NoError(t, err)

	got, ok := r.Get("plugin-ludwig", "Ludwig")
	if !ok {
		t.Fatal("expected hit")
	}
	testutil.Equal(t, got.CallbackURL, "ws://a")

	_, ok = r.Get("plugin-ludwig", "Missing")
	if ok {
		t.Fatal("expected miss")
	}
	_, ok = r.Get("other", "Ludwig")
	if ok {
		t.Fatal("expected miss for wrong scope")
	}
}

func TestList_OrderedByID(t *testing.T) {
	r := newRegistry(t)

	_, _ = r.Register("scope-z", "Zeta", "", "ws://1")
	_, _ = r.Register("scope-a", "Alpha", "", "ws://2")
	_, _ = r.Register("scope-m", "Mike", "", "ws://3")

	all := r.List()
	testutil.Equal(t, len(all), 3)
	testutil.Equal(t, all[0].Title, "Zeta")
	testutil.Equal(t, all[1].Title, "Alpha")
	testutil.Equal(t, all[2].Title, "Mike")
}

func TestUnregister_RemovesOne(t *testing.T) {
	r := newRegistry(t)

	_, _ = r.Register("plugin-ludwig", "A", "", "ws://1")
	_, _ = r.Register("plugin-ludwig", "B", "", "ws://2")

	testutil.NoError(t, r.Unregister("plugin-ludwig", "A"))

	_, ok := r.Get("plugin-ludwig", "A")
	if ok {
		t.Fatal("A should be gone")
	}
	_, ok = r.Get("plugin-ludwig", "B")
	if !ok {
		t.Fatal("B should remain")
	}
}

func TestUnregister_NotFound(t *testing.T) {
	r := newRegistry(t)

	err := r.Unregister("plugin-ludwig", "Missing")
	if !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("expected ErrViewNotFound, got %v", err)
	}
}

func TestRevokeScope_CascadeDeletes(t *testing.T) {
	r := newRegistry(t)

	_, _ = r.Register("plugin-ludwig", "A", "", "ws://1")
	_, _ = r.Register("plugin-ludwig", "B", "", "ws://2")
	_, _ = r.Register("other", "C", "", "ws://3")

	testutil.NoError(t, r.RevokeScope("plugin-ludwig"))

	all := r.List()
	testutil.Equal(t, len(all), 1)
	testutil.Equal(t, all[0].Scope, "other")
}

func TestRevokeScope_NoMatchesIsNoOp(t *testing.T) {
	r := newRegistry(t)

	_, _ = r.Register("plugin-ludwig", "A", "", "ws://1")

	testutil.NoError(t, r.RevokeScope("does-not-exist"))

	all := r.List()
	testutil.Equal(t, len(all), 1)
}

func TestRegister_DBClosedError(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	r := New(database)
	_ = database.Close()

	_, err = r.Register("plugin", "Title", "", "ws://x")
	if err == nil {
		t.Fatal("expected DB-closed error")
	}
	if errors.Is(err, ErrViewExists) || errors.Is(err, ErrTitleRequired) || errors.Is(err, ErrCallbackURLRequired) {
		t.Fatalf("expected raw DB error, got sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed-DB hint in %v", err)
	}
}

func TestList_PropagatesDBError(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	r := New(database)
	_ = database.Close()

	all := r.List()
	if all != nil {
		t.Fatalf("expected nil on DB error, got %v", all)
	}
}

func TestGet_PropagatesDBError(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	r := New(database)
	_ = database.Close()

	_, ok := r.Get("scope", "title")
	if ok {
		t.Fatal("expected miss on DB error")
	}
}

func TestUnregister_PropagatesDBError(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	r := New(database)
	_ = database.Close()

	err = r.Unregister("scope", "title")
	if err == nil {
		t.Fatal("expected DB error")
	}
	if errors.Is(err, ErrViewNotFound) {
		t.Fatalf("expected raw DB error, got ErrViewNotFound: %v", err)
	}
}

func TestParseHotkey(t *testing.T) {
	for _, tc := range []struct {
		in       string
		wantKey  int
		wantOK   bool
		wantName string
	}{
		// tcell uses uppercase-letter ASCII for Ctrl+letter constants:
		// KeyCtrlA = 'A' = 65, KeyCtrlL = 'L' = 76, KeyCtrlZ = 'Z' = 90.
		{"ctrl+l", 76, true, "ctrl+l (lowercase)"},
		{"CTRL+L", 76, true, "CTRL+L (uppercase)"},
		{"Ctrl+a", 65, true, "ctrl+a"},
		{"ctrl+z", 90, true, "ctrl+z"},
		{"ctrl+", 0, false, "ctrl+ trailing nothing"},
		{"ctrl+ll", 0, false, "ctrl+ multi-letter"},
		{"ctrl+1", 0, false, "ctrl+digit"},
		{"alt+l", 0, false, "alt unsupported"},
		{"l", 0, false, "missing ctrl+"},
		{"", 0, false, "empty"},
		{"   ", 0, false, "whitespace"},
	} {
		t.Run(tc.wantName, func(t *testing.T) {
			k, ok := ParseHotkey(tc.in)
			testutil.Equal(t, ok, tc.wantOK)
			testutil.Equal(t, int(k), tc.wantKey)
		})
	}
}

func TestRevokeScope_PropagatesDBError(t *testing.T) {
	database, err := db.OpenInMemory()
	testutil.NoError(t, err)
	r := New(database)
	_ = database.Close()

	err = r.RevokeScope("scope")
	if err == nil {
		t.Fatal("expected DB error")
	}
}
