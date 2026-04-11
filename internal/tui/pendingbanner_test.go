package tui

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestTerminalPane_PendingState(t *testing.T) {
	tp := NewTerminalPane()

	// Initially not pending.
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Set pending.
	tp.SetPending(true)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, true)
	tp.mu.Unlock()

	// SetPending(false) clears it explicitly.
	tp.SetPending(false)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()

	// Pending is cleared when a real session is set.
	tp.SetPending(true)
	mock := &mockAdapter{alive: true, totalWritten: 100, output: make([]byte, 100)}
	tp.SetSession(mock)
	tp.mu.Lock()
	testutil.Equal(t, tp.pending, false)
	tp.mu.Unlock()
}
