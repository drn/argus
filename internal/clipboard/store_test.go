package clipboard

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestStoreSetGetClear(t *testing.T) {
	t.Run("get on empty store", func(t *testing.T) {
		s := New()
		text, ok := s.Get("task1")
		testutil.Equal(t, ok, false)
		testutil.Equal(t, text, "")
	})

	t.Run("set then get", func(t *testing.T) {
		s := New()
		testutil.NoError(t, s.Set("task1", "hello"))
		text, ok := s.Get("task1")
		testutil.Equal(t, ok, true)
		testutil.Equal(t, text, "hello")
	})

	t.Run("clear removes entry", func(t *testing.T) {
		s := New()
		testutil.NoError(t, s.Set("task1", "hello"))
		s.Clear("task1")
		text, ok := s.Get("task1")
		testutil.Equal(t, ok, false)
		testutil.Equal(t, text, "")
	})

	t.Run("last-write-wins", func(t *testing.T) {
		s := New()
		testutil.NoError(t, s.Set("task1", "first"))
		testutil.NoError(t, s.Set("task1", "second"))
		text, _ := s.Get("task1")
		testutil.Equal(t, text, "second")
	})

	t.Run("per-task isolation", func(t *testing.T) {
		s := New()
		testutil.NoError(t, s.Set("task1", "one"))
		testutil.NoError(t, s.Set("task2", "two"))
		t1, _ := s.Get("task1")
		t2, _ := s.Get("task2")
		testutil.Equal(t, t1, "one")
		testutil.Equal(t, t2, "two")
	})

	t.Run("empty taskID is rejected silently", func(t *testing.T) {
		s := New()
		testutil.NoError(t, s.Set("", "hi"))
		_, ok := s.Get("")
		testutil.Equal(t, ok, false)
	})

	t.Run("clear on missing entry is a no-op", func(t *testing.T) {
		s := New()
		s.Clear("task1") // must not panic
	})
}

func TestStoreTooLarge(t *testing.T) {
	s := New()
	big := strings.Repeat("a", MaxTextSize+1)
	err := s.Set("task1", big)
	if err == nil {
		t.Fatal("expected ErrTooLarge, got nil")
	}
	var tooLarge *ErrTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("expected *ErrTooLarge, got %T", err)
	}
	_, ok := s.Get("task1")
	testutil.Equal(t, ok, false)

	t.Run("exactly MaxTextSize is allowed", func(t *testing.T) {
		s := New()
		ok := strings.Repeat("a", MaxTextSize)
		testutil.NoError(t, s.Set("task1", ok))
	})
}

func TestStoreTTLExpiry(t *testing.T) {
	s := NewWithTTL(time.Hour)
	current := time.Now()
	s.now = func() time.Time { return current }

	testutil.NoError(t, s.Set("task1", "hello"))
	text, ok := s.Get("task1")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, text, "hello")

	// Advance past TTL.
	current = current.Add(2 * time.Hour)
	_, ok = s.Get("task1")
	testutil.Equal(t, ok, false)

	t.Run("Prune removes expired", func(t *testing.T) {
		s := NewWithTTL(time.Hour)
		current := time.Now()
		s.now = func() time.Time { return current }
		testutil.NoError(t, s.Set("task1", "x"))
		current = current.Add(2 * time.Hour)
		s.Prune()
		_, ok := s.Get("task1")
		testutil.Equal(t, ok, false)
	})
}

func TestStoreSubscribe(t *testing.T) {
	t.Run("notifies on set", func(t *testing.T) {
		s := New()
		var got []string
		var mu sync.Mutex
		unsub := s.Subscribe("task1", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, text)
		})
		defer unsub()

		testutil.NoError(t, s.Set("task1", "first"))
		testutil.NoError(t, s.Set("task1", "second"))

		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, got, []string{"first", "second"})
	})

	t.Run("notifies on clear with empty string", func(t *testing.T) {
		s := New()
		var got []string
		var mu sync.Mutex
		unsub := s.Subscribe("task1", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, text)
		})
		defer unsub()

		testutil.NoError(t, s.Set("task1", "hello"))
		s.Clear("task1")

		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, got, []string{"hello", ""})
	})

	t.Run("clear without prior set is silent", func(t *testing.T) {
		s := New()
		var calls int32
		unsub := s.Subscribe("task1", func(text string) {
			atomic.AddInt32(&calls, 1)
		})
		defer unsub()

		s.Clear("task1")
		testutil.Equal(t, atomic.LoadInt32(&calls), int32(0))
	})

	t.Run("unsubscribe stops delivery", func(t *testing.T) {
		s := New()
		var calls int32
		unsub := s.Subscribe("task1", func(text string) {
			atomic.AddInt32(&calls, 1)
		})

		testutil.NoError(t, s.Set("task1", "first"))
		unsub()
		testutil.NoError(t, s.Set("task1", "second"))

		testutil.Equal(t, atomic.LoadInt32(&calls), int32(1))
	})

	t.Run("per-task isolation of subscribers", func(t *testing.T) {
		s := New()
		var got1, got2 []string
		var mu sync.Mutex
		s.Subscribe("task1", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got1 = append(got1, text)
		})
		s.Subscribe("task2", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got2 = append(got2, text)
		})

		testutil.NoError(t, s.Set("task1", "one"))
		testutil.NoError(t, s.Set("task2", "two"))

		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, got1, []string{"one"})
		testutil.DeepEqual(t, got2, []string{"two"})
	})

	t.Run("expiry via Get notifies subscribers", func(t *testing.T) {
		s := NewWithTTL(time.Hour)
		current := time.Now()
		s.now = func() time.Time { return current }
		var got []string
		var mu sync.Mutex
		s.Subscribe("task1", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, text)
		})

		testutil.NoError(t, s.Set("task1", "hi"))
		current = current.Add(2 * time.Hour)
		_, ok := s.Get("task1")
		testutil.Equal(t, ok, false)

		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, got, []string{"hi", ""})
	})

	t.Run("expiry via Prune notifies subscribers", func(t *testing.T) {
		s := NewWithTTL(time.Hour)
		current := time.Now()
		s.now = func() time.Time { return current }
		var got []string
		var mu sync.Mutex
		s.Subscribe("task1", func(text string) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, text)
		})

		testutil.NoError(t, s.Set("task1", "hi"))
		current = current.Add(2 * time.Hour)
		s.Prune()

		mu.Lock()
		defer mu.Unlock()
		testutil.DeepEqual(t, got, []string{"hi", ""})
	})
}

func TestStoreConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent test in short mode")
	}
	s := New()
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			s.Set("task1", "x") //nolint:errcheck
			s.Set("task2", "y") //nolint:errcheck
		}()
		go func() {
			defer wg.Done()
			s.Get("task1")
			s.Get("task2")
		}()
		go func() {
			defer wg.Done()
			unsub := s.Subscribe("task1", func(string) {})
			unsub()
			s.Clear("task1")
		}()
	}
	wg.Wait()
}
