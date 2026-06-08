package notify

import (
	"sync"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestFocusTracker_InitiallyUnfocused(t *testing.T) {
	ft := NewFocusTracker(nil)
	testutil.Equal(t, ft.IsFocused("task1"), false)
}

func TestFocusTracker_SetAndGet(t *testing.T) {
	ft := NewFocusTracker(nil)
	ft.SetFocused("task1", true)
	testutil.Equal(t, ft.IsFocused("task1"), true)

	ft.SetFocused("task1", false)
	testutil.Equal(t, ft.IsFocused("task1"), false)
}

func TestFocusTracker_IndependentTasks(t *testing.T) {
	ft := NewFocusTracker(nil)
	ft.SetFocused("task1", true)
	testutil.Equal(t, ft.IsFocused("task2"), false)
}

func TestFocusTracker_Concurrent(t *testing.T) {
	ft := NewFocusTracker(nil)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(v bool) {
			defer wg.Done()
			ft.SetFocused("task1", v)
		}(i%2 == 0)
		go func() {
			defer wg.Done()
			ft.IsFocused("task1")
		}()
	}
	wg.Wait()
}

func TestFocusTracker_TransitionCallback_FocusGained(t *testing.T) {
	var events []bool
	var mu sync.Mutex
	cb := func(taskID string, focused bool) {
		mu.Lock()
		events = append(events, focused)
		mu.Unlock()
	}
	ft := NewFocusTracker(cb)
	ft.SetFocused("task1", true)
	mu.Lock()
	got := len(events) == 1 && events[0] == true
	mu.Unlock()
	testutil.Equal(t, got, true)
}

func TestFocusTracker_TransitionCallback_FocusLost(t *testing.T) {
	var events []bool
	var mu sync.Mutex
	cb := func(taskID string, focused bool) {
		mu.Lock()
		events = append(events, focused)
		mu.Unlock()
	}
	ft := NewFocusTracker(cb)
	ft.SetFocused("task1", true)
	ft.SetFocused("task1", false)
	mu.Lock()
	got := len(events) == 2 && events[1] == false
	mu.Unlock()
	testutil.Equal(t, got, true)
}

func TestFocusTracker_NoCallbackOnNoOp(t *testing.T) {
	calls := 0
	cb := func(taskID string, focused bool) { calls++ }
	ft := NewFocusTracker(cb)
	ft.SetFocused("task1", true)
	ft.SetFocused("task1", true) // already true – no transition
	testutil.Equal(t, calls, 1)
	ft.SetFocused("task1", false)
	ft.SetFocused("task1", false) // already false – no transition
	testutil.Equal(t, calls, 2)
}

func TestFocusTracker_NilCallbackSafe(t *testing.T) {
	ft := NewFocusTracker(nil)
	// Should not panic.
	ft.SetFocused("task1", true)
	ft.SetFocused("task1", false)
}
