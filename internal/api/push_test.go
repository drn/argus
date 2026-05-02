package api

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// fixedNow is a stable wall clock for tests so pushedAt comparisons are
// deterministic. The exact value doesn't matter; only ordering between
// pushedAt and lastInput does.
var fixedNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

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
			fire := shouldFireIdlePush(state, "task-1", tc.isIdle, time.Time{}, fixedNow)
			testutil.Equal(t, fire, false)
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
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow), false)
	// Second observation: still busy. No fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow), false)
	// Third observation: idle. Fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), true)
	// pushedAt was recorded so subsequent calls can suppress.
	if state.pushedAt["t1"].IsZero() {
		t.Fatal("expected pushedAt populated after fire")
	}
}

// TestShouldFireIdlePush_IdleToIdleNoFire verifies that a session that
// stays idle across consecutive ticks fires exactly once (on the first
// transition into idle), not on every subsequent idle-tick.
func TestShouldFireIdlePush_IdleToIdleNoFire(t *testing.T) {
	state := newIdleWatcherState()
	shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow) // busy first
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), true)
	// Stays idle: no further fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), false)
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), false)
}

// TestShouldFireIdlePush_IdleBlipsDoNotRefire is the regression test for the
// original "old idle tasks fire spurious pushes" bug AND the follow-up
// "still way too many notifications" report. A long-idle agent emits
// incidental output (status redraw, heartbeat) which flips IsIdle false→true.
// Before the fix, the per-task throttle expired after 5 minutes and the next
// idle observation re-pushed. After the fix, shouldFireIdlePush requires
// fresh input (lastInputAt > pushedAt) before re-firing — so blips with no
// user reply between them stay silent forever.
func TestShouldFireIdlePush_BlipsAfterPushStaySilent(t *testing.T) {
	state := newIdleWatcherState()
	// First observation: busy.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow), false)
	// Becomes idle for the first time: fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), true)
	// Output blip: !isIdle. No fire on busy transition.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow.Add(time.Minute)), false)
	// Re-idles WITHOUT any input from the user. Suppressed.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow.Add(2*time.Minute)), false)
	// Even hours later, no input → no fire.
	// Another output blip:
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow.Add(time.Hour)), false)
	// Re-idle after that blip:
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow.Add(time.Hour+time.Minute)), false)
}

// TestShouldFireIdlePush_InputAfterPushReArms is the canonical "user replied,
// agent finished follow-up work" flow. After the initial idle push, the user
// sends input (lastInputAt advances), the agent works, and goes idle again.
// The second idle MUST fire — that's the whole point of push notifications.
// The previous 5-minute throttle was incorrectly blocking this case.
func TestShouldFireIdlePush_InputAfterPushReArms(t *testing.T) {
	state := newIdleWatcherState()
	// Cycle 1: busy → idle, fire.
	shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow)
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), true)

	// User sends input. lastInputAt now after the recorded pushedAt.
	inputAt := fixedNow.Add(30 * time.Second)
	// Output blip during follow-up work: not idle.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", false, inputAt, fixedNow.Add(45*time.Second)), false)
	// Agent finishes follow-up: idle. Should fire — input arrived since prior push.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, inputAt, fixedNow.Add(time.Minute)), true)
}

// TestShouldFireIdlePush_InputBeforePushDoesNotReArm guards against an off-
// by-one: input must arrive STRICTLY AFTER the last push (lastInputAt >
// pushedAt) to re-arm. An input timestamp that predates the push (e.g. the
// session's prompt-on-start input) should not let a subsequent blip-idle
// re-fire.
func TestShouldFireIdlePush_InputBeforePushDoesNotReArm(t *testing.T) {
	state := newIdleWatcherState()
	startupInput := fixedNow.Add(-time.Minute)
	shouldFireIdlePush(state, "t1", false, startupInput, fixedNow)
	// First idle fires.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, startupInput, fixedNow), true)

	// Blip: still the same lastInputAt (no new input).
	shouldFireIdlePush(state, "t1", false, startupInput, fixedNow.Add(time.Minute))
	// Re-idle: lastInput predates the push, so no fire.
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, startupInput, fixedNow.Add(2*time.Minute)), false)
}

// TestShouldFireIdlePush_PerTaskIndependent verifies state is keyed by task
// ID so concurrent sessions don't share idle state.
func TestShouldFireIdlePush_PerTaskIndependent(t *testing.T) {
	state := newIdleWatcherState()
	shouldFireIdlePush(state, "t1", false, time.Time{}, fixedNow)
	testutil.Equal(t, shouldFireIdlePush(state, "t2", true, time.Time{}, fixedNow), false)
	testutil.Equal(t, shouldFireIdlePush(state, "t1", true, time.Time{}, fixedNow), true)
	testutil.Equal(t, shouldFireIdlePush(state, "t2", true, time.Time{}, fixedNow), false)
}
