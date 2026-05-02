package api

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestShouldFireIdlePush_FirstObservationSuppressed verifies that the very
// first observation of a session — regardless of idle state — never fires.
// Without this guard, an already-idle session entering the watcher's view
// (e.g. watcher freshly started with running sessions present) would always
// trip the !wasIdle check, since the zero-value of idleNow[id] is false.
func TestShouldFireIdlePush_FirstObservationSuppressed(t *testing.T) {
	tests := []struct {
		name   string
		isIdle bool
	}{
		{"first observation idle", true},
		{"first observation busy", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := newIdleWatcherState()
			fire := shouldFireIdlePush(state, "task-1", tc.isIdle)
			testutil.Equal(t, fire, false)
			// State must still be recorded so subsequent ticks know the
			// previous observation.
			testutil.Equal(t, state.seenBefore["task-1"], true)
			testutil.Equal(t, state.idleNow["task-1"], tc.isIdle)
		})
	}
}

// TestShouldFireIdlePush_BusyToIdleFires verifies the canonical "agent
// finished" path: observed busy, then idle → fire push.
func TestShouldFireIdlePush_BusyToIdleFires(t *testing.T) {
	state := newIdleWatcherState()
	// First observation: busy. No fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false), false)
	// Second observation: still busy. No fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false), false)
	// Third observation: idle. Fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), true)
}

// TestShouldFireIdlePush_IdleToIdleNoFire verifies that a session that
// stays idle across consecutive ticks fires exactly once (on the first
// transition into idle), not on every subsequent idle-tick.
func TestShouldFireIdlePush_IdleToIdleNoFire(t *testing.T) {
	state := newIdleWatcherState()
	// Busy first so we get past the first-observation guard.
	shouldFireIdlePush(state, "t1", false)
	// Transition to idle: fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), true)
	// Stays idle: no further fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), false)
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), false)
}

// TestShouldFireIdlePush_IdleBlipsDoNotRefire is the regression test for the
// "old idle tasks fire spurious pushes" bug. After firing once on the real
// idle transition, a long-idle agent emitting incidental output (status
// redraw, heartbeat) flips IsIdle back to false then to true again. Before
// the fix, the watcher reset the per-task throttle on the busy transition,
// so the next idle observation would push again — leading to repeat
// notifications for tasks the user already saw. After the fix, the
// transition still fires (relying on the push manager's 5-minute throttle
// to suppress repeats), but we no longer actively bust the throttle.
//
// The test asserts that shouldFireIdlePush itself does NOT call ResetThrottle
// (it has no access to a Manager). The end-to-end suppression of spurious
// pushes is owned by Manager.Notify's existing throttle logic.
func TestShouldFireIdlePush_IdleBlipsStillTransitionButThrottleSurvives(t *testing.T) {
	state := newIdleWatcherState()
	// First observation: busy.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false), false)
	// Becomes idle for the first time: fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), true)
	// Output blip: !isIdle. No fire on busy transition.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false), false)
	// Re-idles: shouldFireIdlePush still returns true because it's a real
	// transition. The throttle in Manager.Notify is the actual suppressor —
	// see TestNotify_* in internal/push/push_test.go for that behavior.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), true)
}

// TestShouldFireIdlePush_PerTaskIndependent verifies state is keyed by task
// ID so concurrent sessions don't share idle state.
func TestShouldFireIdlePush_PerTaskIndependent(t *testing.T) {
	state := newIdleWatcherState()
	// t1 first observation busy
	shouldFireIdlePush(state, "t1", false)
	// t2 first observation idle (suppressed)
	testutil.Equal(t, shouldFireIdlePush(state, "t2", true), false)
	// t1 transitions to idle: fires (independent of t2)
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true), true)
	// t2 stays idle: no fire
	testutil.Equal(t, shouldFireIdlePush(state, "t2", true), false)
}
