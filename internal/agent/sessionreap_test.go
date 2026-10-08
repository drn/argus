package agent

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/sessiontag"
	"github.com/drn/argus/internal/testutil"
)

// fakeProcTable is an in-memory tagged-process table: SIGTERM removes pids in
// termKills, SIGKILL removes anything.
type fakeProcTable struct {
	mu        sync.Mutex
	procs     map[int]string
	termKills map[int]bool
	signals   []string
	alive     map[int]bool
	sleeps    []time.Duration
	listErr   error
}

func (f *fakeProcTable) hooks() *reapHooks {
	return &reapHooks{
		enabled: true,
		list: func() (map[int]string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.listErr != nil {
				return nil, f.listErr
			}
			out := map[int]string{}
			for k, v := range f.procs {
				out[k] = v
			}
			return out, nil
		},
		signal: func(pid int, sig syscall.Signal) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, ok := f.procs[pid]; !ok {
				return syscall.ESRCH
			}
			f.signals = append(f.signals, sig.String())
			if sig == syscall.SIGKILL || f.termKills[pid] {
				delete(f.procs, pid)
			}
			return nil
		},
		alive: func(pid int) bool { return f.alive[pid] },
		sleep: func(d time.Duration) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sleeps = append(f.sleeps, d)
		},
	}
}

func setReapHooks(t *testing.T, h *reapHooks) {
	t.Helper()
	prev := currentReapHooks.Load()
	currentReapHooks.Store(h)
	t.Cleanup(func() { currentReapHooks.Store(prev) })
}

func sorted(p []int) []int { slices.Sort(p); return p }

func TestReapSessionTag(t *testing.T) {
	self := os.Getpid()
	t.Run("terms matching tag only, escalates survivors", func(t *testing.T) {
		f := &fakeProcTable{
			procs:     map[int]string{10: "1-a", 11: "1-a", 12: "1-b", self: "1-a"},
			termKills: map[int]bool{10: true},
		}
		setReapHooks(t, f.hooks())
		termed, killed := reapSessionTag("t1", "1-a")
		testutil.DeepEqual(t, sorted(termed), []int{10, 11})
		testutil.DeepEqual(t, killed, []int{11})
		_, otherAlive := f.procs[12]
		testutil.True(t, otherAlive) // replacement session's tag untouched
		_, selfAlive := f.procs[self]
		testutil.True(t, selfAlive) // never signals itself
		testutil.DeepEqual(t, f.sleeps, []time.Duration{sessionReapGrace, sessionReapKillAfter})
	})
	t.Run("all exit on SIGTERM: no SIGKILL", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{10: "1-a"}, termKills: map[int]bool{10: true}}
		setReapHooks(t, f.hooks())
		termed, killed := reapSessionTag("t1", "1-a")
		testutil.DeepEqual(t, termed, []int{10})
		testutil.Equal(t, len(killed), 0)
	})
	t.Run("nothing tagged", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{12: "1-b"}}
		setReapHooks(t, f.hooks())
		termed, killed := reapSessionTag("t1", "1-a")
		testutil.Equal(t, len(termed)+len(killed), 0)
		testutil.Equal(t, len(f.signals), 0)
	})
	t.Run("list error is swallowed", func(t *testing.T) {
		f := &fakeProcTable{listErr: errors.New("boom")}
		setReapHooks(t, f.hooks())
		termed, killed := reapSessionTag("t1", "1-a")
		testutil.Equal(t, len(termed)+len(killed), 0)
	})
	t.Run("empty tag is a no-op", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{10: ""}}
		setReapHooks(t, f.hooks())
		reapSessionTag("t1", "")
		testutil.Equal(t, len(f.sleeps), 0)
	})
	t.Run("disabled is a no-op", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{10: "1-a"}}
		h := f.hooks()
		h.enabled = false
		setReapHooks(t, h)
		reapSessionTag("t1", "1-a")
		testutil.Equal(t, len(f.signals), 0)
	})
	t.Run("pid vanished between scan and signal", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{10: "1-a"}}
		h := f.hooks()
		h.signal = func(int, syscall.Signal) error { return syscall.ESRCH }
		setReapHooks(t, h)
		termed, _ := reapSessionTag("t1", "1-a")
		testutil.Equal(t, len(termed), 0)
	})
	t.Run("other signal error is logged and skipped", func(t *testing.T) {
		f := &fakeProcTable{procs: map[int]string{10: "1-a"}}
		h := f.hooks()
		h.signal = func(int, syscall.Signal) error { return syscall.EPERM }
		setReapHooks(t, h)
		termed, killed := reapSessionTag("t1", "1-a")
		testutil.Equal(t, len(termed)+len(killed), 0)
	})
}

func TestSweepOrphanedSessionProcs(t *testing.T) {
	self := os.Getpid()
	selfTag := sessiontag.NewForOwner(self)
	f := &fakeProcTable{
		procs: map[int]string{
			20: "500-dead", // owner dead → reaped
			21: "600-live", // owner alive → kept
			22: selfTag,    // owned by this runner → kept
			23: "garbage",  // malformed → kept
		},
		termKills: map[int]bool{20: true},
		alive:     map[int]bool{600: true},
	}
	setReapHooks(t, f.hooks())
	termed, killed := SweepOrphanedSessionProcs()
	testutil.DeepEqual(t, termed, []int{20})
	testutil.Equal(t, len(killed), 0)
	testutil.Equal(t, len(f.procs), 3)
	testutil.DeepEqual(t, f.sleeps, []time.Duration{sessionReapKillAfter}) // no grace for a startup sweep

	t.Run("disabled", func(t *testing.T) {
		g := &fakeProcTable{procs: map[int]string{20: "500-dead"}}
		h := g.hooks()
		h.enabled = false
		setReapHooks(t, h)
		termed, _ := SweepOrphanedSessionProcs()
		testutil.Equal(t, len(termed), 0)
	})
}

func TestReapHooks_DisabledUnderGoTest(t *testing.T) {
	testutil.False(t, currentReapHooks.Load().enabled)
}

func procArgs2(argc uint32, execPath string, args, env []string) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, argc)
	b = append(b, execPath...)
	b = append(b, 0, 0, 0)
	for _, a := range args {
		b = append(append(b, a...), 0)
	}
	for _, e := range env {
		b = append(append(b, e...), 0)
	}
	return append(b, 0, 'j', 'u', 'n', 'k', 0)
}

func TestProcArgs2Env(t *testing.T) {
	tag := sessiontag.EnvKey + "=9-x"
	tests := []struct {
		name string
		buf  []byte
		want []string
	}{
		{"normal", procArgs2(2, "/bin/x", []string{"x", "-a"}, []string{"A=1", tag}), []string{"A=1", tag}},
		{"no env", procArgs2(1, "/bin/x", []string{"x"}, nil), nil},
		{"too short", []byte{1, 0}, nil},
		{"no exec path terminator", []byte{1, 0, 0, 0, 'x'}, nil},
		{"truncated argv", append([]byte{5, 0, 0, 0}, "/bin/x\x00x\x00"...), nil},
		{"unterminated last env", append([]byte{0, 0, 0, 0}, "/x\x00A=1"...), []string{"A=1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.DeepEqual(t, procArgs2Env(tt.buf), tt.want)
		})
	}
}

func TestSessionTagFromEnv(t *testing.T) {
	v, ok := sessionTagFromEnv([]string{"A=1", sessiontag.EnvKey + "=", sessiontag.EnvKey + "=3-z"})
	testutil.True(t, ok)
	testutil.Equal(t, v, "3-z")
	_, ok = sessionTagFromEnv([]string{"A=1"})
	testutil.False(t, ok)
}

// TestStartSession_StampsTag asserts the spawned process actually sees the tag
// and it is recorded on the Session.
func TestStartSession_StampsTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := exec.Command("sh", "-c", "printf %s \"$"+sessiontag.EnvKey+"\"")
	cmd.Env = []string{sessiontag.EnvKey + "=inherited-from-parent-agent"}
	sess, err := StartSession("tag-task", cmd, 24, 80)
	testutil.NoError(t, err)
	<-sess.Done()
	owner, ok := sessiontag.OwnerPID(sess.reapTag)
	testutil.True(t, ok)
	testutil.Equal(t, owner, os.Getpid())
	testutil.Contains(t, string(sess.RecentOutput()), sess.reapTag)
}

// TestListTaggedProcs_Live exercises the real per-OS enumerator and the real
// signaller end to end: a detached (setsid) tagged child — the shape of a
// Playwright browser — is found and reaped.
func TestListTaggedProcs_Live(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a real process")
	}
	// A platform binary like /bin/sleep hides its env from KERN_PROCARGS2 on
	// macOS; node (like Playwright's Chromium) does not.
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	tag := sessiontag.New()
	cmd := exec.Command(node, "-e", "setTimeout(() => {}, 60000)")
	cmd.Env = sessiontag.WithTag(nil, tag)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	testutil.NoError(t, cmd.Start())
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	procs, err := listTaggedProcs()
	if err != nil || procs == nil {
		t.Skipf("no process enumerator available here: %v", err)
	}
	testutil.Equal(t, procs[cmd.Process.Pid], tag)

	live := *currentReapHooks.Load()
	live.enabled = true
	live.sleep = func(time.Duration) {}
	setReapHooks(t, &live)
	termed, _ := reapSessionTag("live", tag)
	testutil.DeepEqual(t, termed, []int{cmd.Process.Pid})
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("tagged process survived the reaper")
	}
}

func TestInheritAncestorTags(t *testing.T) {
	ppid := map[int]int{
		10: 1,  // tagged root
		11: 10, // child of tagged → inherits
		12: 11, // grandchild → inherits
		13: 1,  // unrelated orphan, untagged → stays untagged
		14: 15, // cycle with 15, untagged → terminates, untagged
		15: 14,
		16: 99, // parent unknown → untagged
		20: 1,  // differently tagged root
		21: 20,
	}
	tagged := map[int]string{10: "a", 20: "b"}
	inheritAncestorTags(ppid, tagged)
	testutil.DeepEqual(t, tagged, map[int]string{10: "a", 11: "a", 12: "a", 20: "b", 21: "b"})
}

// TestRunner_ExitReapsSessionTag pins the wiring: when a runner session exits,
// the reaper scans for exactly that session's tag (never another session's).
func TestRunner_ExitReapsSessionTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	scanned := make(chan struct{}, 4)
	f := &fakeProcTable{procs: map[int]string{}, termKills: map[int]bool{}}
	h := f.hooks()
	list := h.list
	h.list = func() (map[int]string, error) {
		defer func() { scanned <- struct{}{} }()
		return list()
	}
	setReapHooks(t, h)

	finished := make(chan struct{}, 1)
	r := NewRunner(func(string, error, bool, []byte) { finished <- struct{}{} })
	task := &model.Task{ID: "t-reap", Name: "test", Worktree: t.TempDir()}
	handle, err := r.Start(task, runnerTestConfig(), 24, 80, false)
	testutil.NoError(t, err)
	tag := handle.(*Session).reapTag
	// The leftover "browser" carries this session's tag; a neighbour carries
	// another session's.
	f.mu.Lock()
	f.procs[4242] = tag
	f.procs[4343] = sessiontag.New()
	f.termKills[4242] = true
	f.mu.Unlock()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("session never exited")
	}
	// Two scans: the initial one, then the post-SIGTERM rescan — which only
	// happens after the SIGTERM was sent.
	for range 2 {
		select {
		case <-scanned:
		case <-time.After(5 * time.Second):
			t.Fatal("reaper never ran on session exit")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, leftover := f.procs[4242]
	_, neighbour := f.procs[4343]
	testutil.False(t, leftover)
	testutil.True(t, neighbour)
}

func TestProcessAlive(t *testing.T) {
	testutil.True(t, processAlive(os.Getpid()))
	cmd := exec.Command("true")
	testutil.NoError(t, cmd.Run())
	testutil.False(t, processAlive(cmd.Process.Pid)) // exited and reaped
}
