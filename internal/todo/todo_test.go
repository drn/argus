package todo

import (
	"context"
	"testing"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

type fakeBackend struct{}

func (fakeBackend) Create(context.Context, CreateInput) (Item, error)         { return Item{}, nil }
func (fakeBackend) List(context.Context) ([]Item, error)                      { return nil, nil }
func (fakeBackend) Update(context.Context, string, UpdateInput) (Item, error) { return Item{}, nil }
func (fakeBackend) Complete(context.Context, string) (Item, error)            { return Item{}, nil }
func (fakeBackend) Delete(context.Context, string) error                      { return nil }

// resetRegistry lets each test start from a clean factories map without
// depending on package init() side effects from other test files/packages.
func resetRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	saved := factories
	factories = map[string]Factory{}
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		factories = saved
		mu.Unlock()
	})
}

func TestRegisterAndGet(t *testing.T) {
	resetRegistry(t)
	Register("fake", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })

	b, err := Get("fake", config.TodoConfig{})
	testutil.NoError(t, err)
	if b == nil {
		t.Fatal("expected a non-nil backend")
	}
}

func TestGet_EmptyName(t *testing.T) {
	resetRegistry(t)
	_, err := Get("", config.TodoConfig{})
	if err == nil {
		t.Fatal("expected an error for an empty backend name")
	}
}

func TestGet_UnknownName(t *testing.T) {
	resetRegistry(t)
	Register("fake", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })
	_, err := Get("bogus", config.TodoConfig{})
	if err == nil {
		t.Fatal("expected an error for an unregistered backend name")
	}
	testutil.Contains(t, err.Error(), "bogus")
}

func TestRegister_DuplicatePanics(t *testing.T) {
	resetRegistry(t)
	Register("fake", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected Register to panic on a duplicate name")
		}
	}()
	Register("fake", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })
}

func TestRegistered_SortedNames(t *testing.T) {
	resetRegistry(t)
	Register("zeta", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })
	Register("alpha", func(config.TodoConfig) (Backend, error) { return fakeBackend{}, nil })

	testutil.DeepEqual(t, Registered(), []string{"alpha", "zeta"})
}
