package settings

import (
	"errors"
	"sync"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// mustParse is a test helper — returns a valid Section for the form-section
// shape used throughout the test cases below.
func mustParse(t *testing.T, scope, title string) Section {
	t.Helper()
	raw := []byte(`{"title":"` + title + `","callback_url":"http://127.0.0.1/save","fields":[{"key":"k","label":"l","type":"bool","default":false}]}`)
	s, err := ParseSection(scope, raw)
	testutil.NoError(t, err)
	return s
}

func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewRegistry()
	testutil.Equal(t, r.Len(), 0)

	err := r.Register(mustParse(t, "a", "Bravo"))
	testutil.NoError(t, err)
	err = r.Register(mustParse(t, "a", "Alpha"))
	testutil.NoError(t, err)
	err = r.Register(mustParse(t, "b", "Bravo"))
	testutil.NoError(t, err)

	testutil.Equal(t, r.Len(), 3)

	got := r.List()
	testutil.Equal(t, len(got), 3)
	// Sorted by title, then scope.
	testutil.Equal(t, got[0].Title, "Alpha")
	testutil.Equal(t, got[1].Title, "Bravo")
	testutil.Equal(t, got[1].Scope, "a")
	testutil.Equal(t, got[2].Title, "Bravo")
	testutil.Equal(t, got[2].Scope, "b")
}

func TestRegistry_RegisterReplacesSameKey(t *testing.T) {
	r := NewRegistry()
	testutil.NoError(t, r.Register(mustParse(t, "scope", "Title")))
	testutil.NoError(t, r.Register(mustParse(t, "scope", "Title")))
	testutil.Equal(t, r.Len(), 1)
}

func TestRegistry_RegisterRejectsInvalid(t *testing.T) {
	r := NewRegistry()
	// Empty scope.
	err := r.Register(Section{Title: "x", Type: TypeForm, CallbackURL: "http://x", Spec: &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldBool}}}})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("expected ErrInvalidScope, got %v", err)
	}
	// Empty title.
	err = r.Register(Section{Scope: "s", Type: TypeForm, CallbackURL: "http://x", Spec: &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldBool}}}})
	if !errors.Is(err, ErrInvalidTitle) {
		t.Fatalf("expected ErrInvalidTitle, got %v", err)
	}
	// Wrong type.
	err = r.Register(Section{Scope: "s", Title: "t", Type: "wat", CallbackURL: "http://x"})
	if !errors.Is(err, ErrInvalidType) {
		t.Fatalf("expected ErrInvalidType, got %v", err)
	}
	// Missing callback.
	err = r.Register(Section{Scope: "s", Title: "t", Type: TypeForm, Spec: &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldBool}}}})
	if !errors.Is(err, ErrMissingCallbackURL) {
		t.Fatalf("expected ErrMissingCallbackURL, got %v", err)
	}
	// Empty form.
	err = r.Register(Section{Scope: "s", Title: "t", Type: TypeForm, CallbackURL: "http://x", Spec: &FormSpec{}})
	if !errors.Is(err, ErrEmptyForm) {
		t.Fatalf("expected ErrEmptyForm, got %v", err)
	}
	// Nil spec.
	err = r.Register(Section{Scope: "s", Title: "t", Type: TypeForm, CallbackURL: "http://x"})
	if !errors.Is(err, ErrEmptyForm) {
		t.Fatalf("expected ErrEmptyForm, got %v", err)
	}
	testutil.Equal(t, r.Len(), 0)
}

func TestRegistry_RegisterAcceptsStream(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Section{Scope: "ludwig", Title: "Live", Type: TypeStream, CallbackURL: "ws://x"})
	testutil.NoError(t, err)
	testutil.Equal(t, r.Len(), 1)
	got, ok := r.Get("ludwig", "Live")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Type, TypeStream)

	// Stream + fields rejected.
	err = r.Register(Section{
		Scope: "ludwig", Title: "Bad", Type: TypeStream, CallbackURL: "ws://x",
		Spec: &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldBool}}},
	})
	if !errors.Is(err, ErrStreamHasFields) {
		t.Fatalf("expected ErrStreamHasFields, got %v", err)
	}
}

func TestRegistry_GetAndUnregister(t *testing.T) {
	r := NewRegistry()
	testutil.NoError(t, r.Register(mustParse(t, "scope", "Title")))

	got, ok := r.Get("scope", "Title")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, got.Scope, "scope")
	testutil.Equal(t, got.Title, "Title")

	_, ok = r.Get("scope", "missing")
	testutil.Equal(t, ok, false)

	testutil.Equal(t, r.Unregister("", ""), false)
	testutil.Equal(t, r.Unregister("scope", "missing"), false)
	testutil.Equal(t, r.Unregister("scope", "Title"), true)
	testutil.Equal(t, r.Len(), 0)
	testutil.Equal(t, r.Unregister("scope", "Title"), false)
}

func TestRegistry_UnregisterScope(t *testing.T) {
	r := NewRegistry()
	testutil.NoError(t, r.Register(mustParse(t, "a", "A1")))
	testutil.NoError(t, r.Register(mustParse(t, "a", "A2")))
	testutil.NoError(t, r.Register(mustParse(t, "b", "B1")))

	testutil.Equal(t, r.UnregisterScope(""), 0)
	testutil.Equal(t, r.UnregisterScope("a"), 2)
	testutil.Equal(t, r.Len(), 1)
	testutil.Equal(t, r.UnregisterScope("a"), 0)
	testutil.Equal(t, r.UnregisterScope("b"), 1)
	testutil.Equal(t, r.Len(), 0)
}

func TestRegistry_ReplaceFiltersInvalid(t *testing.T) {
	good := mustParse(t, "scope", "Good")
	bad := Section{Scope: "scope", Title: "Bad", Type: TypeForm, CallbackURL: ""} // missing callback
	r := NewRegistry()
	n := r.Replace([]Section{good, bad})
	testutil.Equal(t, n, 1)
	testutil.Equal(t, r.Len(), 1)
	_, ok := r.Get("scope", "Good")
	testutil.Equal(t, ok, true)
	_, ok = r.Get("scope", "Bad")
	testutil.Equal(t, ok, false)
}

func TestRegistry_ReplaceRejectsCorruptSpec(t *testing.T) {
	// Duplicate field key inside a manually-built Section should be filtered.
	corrupt := Section{
		Scope:       "s",
		Title:       "t",
		Type:        TypeForm,
		CallbackURL: "http://x",
		Spec: &FormSpec{Fields: []FormField{
			{Key: "k", Label: "a", Type: FieldBool},
			{Key: "k", Label: "b", Type: FieldString},
		}},
	}
	r := NewRegistry()
	n := r.Replace([]Section{corrupt})
	testutil.Equal(t, n, 0)
}

func TestRegistry_ReplaceRejectsMissingFieldAttributes(t *testing.T) {
	r := NewRegistry()
	// Missing field key.
	n := r.Replace([]Section{{
		Scope:       "s",
		Title:       "t",
		Type:        TypeForm,
		CallbackURL: "http://x",
		Spec:        &FormSpec{Fields: []FormField{{Label: "l", Type: FieldBool}}},
	}})
	testutil.Equal(t, n, 0)
	// Missing field label.
	n = r.Replace([]Section{{
		Scope:       "s",
		Title:       "t",
		Type:        TypeForm,
		CallbackURL: "http://x",
		Spec:        &FormSpec{Fields: []FormField{{Key: "k", Type: FieldBool}}},
	}})
	testutil.Equal(t, n, 0)
	// Invalid field type.
	n = r.Replace([]Section{{
		Scope:       "s",
		Title:       "t",
		Type:        TypeForm,
		CallbackURL: "http://x",
		Spec:        &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldType("wat")}}},
	}})
	testutil.Equal(t, n, 0)
	// Enum without options.
	n = r.Replace([]Section{{
		Scope:       "s",
		Title:       "t",
		Type:        TypeForm,
		CallbackURL: "http://x",
		Spec:        &FormSpec{Fields: []FormField{{Key: "k", Label: "l", Type: FieldEnum}}},
	}})
	testutil.Equal(t, n, 0)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	// Stress the RWMutex paths with concurrent reads and writes so the race
	// detector catches a missing lock.
	r := NewRegistry()
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			s := mustParse(t, "scope", "Title")
			_ = r.Register(s)
			_ = r.Unregister(s.Scope, s.Title)
		}(i)
		go func() {
			defer wg.Done()
			_ = r.List()
			_ = r.Len()
		}()
	}
	wg.Wait()
}
