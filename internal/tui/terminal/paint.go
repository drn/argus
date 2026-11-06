package terminal

import "github.com/drn/argus/internal/app/agentview"

// TakePaint consumes this frame's observation. Call only on the UI goroutine.
// A pane hidden since the last frame returns drawn=false even if still bound.
func (tp *TerminalPane) TakePaint() (session agentview.TerminalAdapter, fed uint64, drawn bool) {
	drawn, tp.painted = tp.painted, false
	return tp.Session(), tp.paintedTotal, drawn
}

// TaskID identifies the bound task without exposing input or output content.
func (tp *TerminalPane) TaskID() string {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return tp.taskID
}
