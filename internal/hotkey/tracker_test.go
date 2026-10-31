package hotkey

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

type step struct {
	kind  string // "flags", "key", "tick"
	flags uint64
	at    time.Duration
}

func runSteps(t *testing.T, combo string, steps []step) []Event {
	t.Helper()
	var got []Event
	tr := NewTracker(mustCombo(t, combo), func(e Event) { got = append(got, e) })
	base := time.Unix(1_700_000_000, 0)
	for _, s := range steps {
		now := base.Add(s.at)
		switch s.kind {
		case "flags":
			tr.FlagsChanged(s.flags, now)
		case "key":
			tr.KeyDown(now)
		case "tick":
			tr.Tick(now)
		default:
			t.Fatalf("bad step kind %q", s.kind)
		}
	}
	return got
}

func TestTracker(t *testing.T) {
	ms := time.Millisecond
	rOpt := FlagOption | FlagRightOption
	shift := FlagShift | FlagLeftShift
	flags := func(f uint64, at time.Duration) step { return step{"flags", f, at} }
	key := func(at time.Duration) step { return step{kind: "key", at: at} }
	tick := func(at time.Duration) step { return step{kind: "tick", at: at} }

	for _, tc := range []struct {
		name  string
		combo string
		steps []step
		want  []Event
	}{
		{"hold and release", "right_option", []step{flags(rOpt, 0), tick(50 * ms), tick(200 * ms), flags(0, 2000*ms)}, []Event{Began, Ended}},
		{"tick before delay does nothing", "right_option", []step{flags(rOpt, 0), tick(199 * ms)}, nil},
		{"tap shorter than delay", "right_option", []step{flags(rOpt, 0), tick(100 * ms), flags(0, 150*ms)}, nil},
		{"release after delay without tick", "right_option", []step{flags(rOpt, 0), flags(0, 300*ms)}, []Event{Began, Ended}},
		{"key while armed cancels", "right_option", []step{flags(rOpt, 0), key(50 * ms), tick(300 * ms), flags(0, 400*ms)}, []Event{Cancelled}},
		{"key after delay without tick begins first", "right_option", []step{flags(rOpt, 0), key(250 * ms), flags(0, 400*ms)}, []Event{Began, Ended}},
		{"key while active ignored", "right_option", []step{flags(rOpt, 0), tick(200 * ms), key(300 * ms), flags(0, 500*ms)}, []Event{Began, Ended}},
		{"extra modifier while armed cancels", "right_option", []step{flags(rOpt, 0), flags(rOpt|shift, 50*ms), tick(300 * ms)}, []Event{Cancelled}},
		{"blocked until combo released", "right_option", []step{flags(rOpt, 0), flags(rOpt|shift, 50*ms), flags(rOpt, 100*ms), tick(400 * ms), flags(0, 500*ms)}, []Event{Cancelled}},
		{"re-arms after blocked release", "right_option", []step{flags(rOpt, 0), key(10 * ms), flags(0, 50*ms), flags(rOpt, 100*ms), tick(300 * ms), flags(0, 400*ms)}, []Event{Cancelled, Began, Ended}},
		{"extra modifier while active ends and blocks", "right_option", []step{flags(rOpt, 0), tick(200 * ms), flags(rOpt|shift, 300*ms), flags(rOpt, 400*ms), tick(800 * ms), flags(0, 900*ms)}, []Event{Began, Ended}},
		{"other modifier first never arms", "right_option", []step{flags(shift, 0), flags(rOpt|shift, 10*ms), tick(300 * ms), flags(rOpt, 350*ms), tick(700 * ms)}, nil},
		{"key while idle ignored", "right_option", []step{key(0), flags(rOpt, 10*ms), tick(210 * ms), flags(0, 300*ms)}, []Event{Began, Ended}},
		{"repeated matching flags keep arm time", "right_option", []step{flags(rOpt, 0), flags(rOpt, 150*ms), tick(200 * ms), flags(rOpt, 250*ms), flags(0, 300*ms)}, []Event{Began, Ended}},
		{"partial release of two-key combo while armed", "ctrl+shift", []step{flags(FlagControl|FlagShift, 0), flags(FlagControl, 50*ms), tick(300 * ms)}, nil},
		{"partial release of two-key combo while active", "ctrl+shift", []step{flags(FlagControl|FlagShift, 0), tick(250 * ms), flags(FlagShift, 300*ms)}, []Event{Began, Ended}},
		{"two holds", "right_option", []step{flags(rOpt, 0), tick(200 * ms), flags(0, 300*ms), flags(rOpt, 400*ms), tick(600 * ms), flags(0, 700*ms)}, []Event{Began, Ended, Began, Ended}},
		{"blocked ignores keys", "right_option", []step{flags(rOpt, 0), key(10 * ms), key(20 * ms), tick(500 * ms)}, []Event{Cancelled}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.DeepEqual(t, runSteps(t, tc.combo, tc.steps), tc.want)
		})
	}
}

func TestTracker_StateAccessors(t *testing.T) {
	tr := NewTracker(mustCombo(t, "right_option"), nil)
	tr.SetArmDelay(10 * time.Millisecond)
	base := time.Unix(0, 0)
	testutil.False(t, tr.Armed())
	tr.FlagsChanged(FlagOption|FlagRightOption, base)
	testutil.True(t, tr.Armed())
	testutil.False(t, tr.Active())
	tr.Tick(base.Add(10 * time.Millisecond))
	testutil.False(t, tr.Armed())
	testutil.True(t, tr.Active())
	tr.FlagsChanged(0, base.Add(20*time.Millisecond))
	testutil.False(t, tr.Active())
}

func TestTracker_ConcurrentInputs(t *testing.T) {
	var mu sync.Mutex
	var got []Event
	tr := NewTracker(mustCombo(t, "right_option"), func(e Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})
	base := time.Unix(0, 0)
	tr.FlagsChanged(FlagOption|FlagRightOption, base)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Tick(base.Add(time.Second))
		}()
	}
	tr.FlagsChanged(0, base.Add(2*time.Second))
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	testutil.DeepEqual(t, got, []Event{Began, Ended})
}

func TestEventString(t *testing.T) {
	for _, tc := range []struct {
		e    Event
		want string
	}{{Began, "began"}, {Ended, "ended"}, {Cancelled, "cancelled"}, {Event(0), "unknown"}} {
		t.Run(tc.want, func(t *testing.T) {
			testutil.Equal(t, tc.e.String(), tc.want)
		})
	}
}
