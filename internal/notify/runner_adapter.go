package notify

// RunnerAdapter wraps any type whose Get method returns a SessionHandle-like
// value (any type that satisfies SessionHandleIface) so it can be used as a
// notify.RunnerIface.
//
// Usage:
//
//	notifier := notify.New(notify.AdaptRunner(myRunner), focusTracker)
type runnerGetAdapter struct {
	fn func(taskID string) SessionHandleIface
}

func (a *runnerGetAdapter) Get(taskID string) SessionHandleIface {
	return a.fn(taskID)
}

// AdaptRunner wraps a function-based lookup (e.g. adapted from an
// *agent.Runner whose Get returns the wider agent.SessionHandle) into a
// notify.RunnerIface. The wrapped function should return nil when no session
// exists for the task ID.
//
//	notify.New(notify.AdaptRunner(func(id string) notify.SessionHandleIface {
//	    return runner.Get(id) // agent.Session satisfies SessionHandleIface
//	}), ft)
func AdaptRunner(fn func(taskID string) SessionHandleIface) RunnerIface {
	return &runnerGetAdapter{fn: fn}
}
