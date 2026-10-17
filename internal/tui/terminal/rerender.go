package terminal

import "github.com/drn/argus/internal/uxlog"

// SetRerenderingForTask ignores late outcomes for a pane that has rebound.
func (tp *TerminalPane) SetRerenderingForTask(taskID string, active bool) {
	tp.setRerenderStatus(taskID, active, false)
}

func (tp *TerminalPane) SetRerenderPendingForTask(taskID string) {
	tp.setRerenderStatus(taskID, true, true)
}

func (tp *TerminalPane) setRerenderStatus(taskID string, active, pending bool) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.taskID != taskID || tp.rerendering == active && tp.rerenderPending == pending {
		return
	}
	tp.rerendering = active
	tp.rerenderPending = pending
	uxlog.Log("[terminalpane] rerender status task=%s active=%t", taskID, active)
}

func (tp *TerminalPane) RerenderStatus() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if !tp.rerendering {
		return ""
	}
	if tp.rerenderPending {
		return "Re-render pending"
	}
	return "Re-rendering…"
}

// Rerendering reports whether this pane has an outstanding width repair.
func (tp *TerminalPane) Rerendering() bool {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.rerendering
}
