package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// TestSession_StopConcurrentWithPanePoll reproduces the second crash
// hypothesis: a Hera pane's off-thread poller (HeraPage.SyncPanes, called from
// both the 1s tick goroutine and the 100ms spinner goroutine — see
// gotchas/hera-view.md) keeps calling Alive()/Resize()/RecentOutput()/
// WriteInput() on a session while heraReclaimAndArchiveTask concurrently calls
// Stop() on the SAME session from the tview main goroutine during a bulk
// cascade-nuke. Run under `-race -count=1`, this confirms whether that
// interleaving produces a nil-deref or data race. It does not: Session.Stop
// only signals the process (never nils/frees buf, writers, or ptmx), and
// waitLoop closes ptmx under s.mu with every consumer (Resize) checking
// ptmxClosed under the same lock before touching it.
func TestSession_StopConcurrentWithPanePoll(t *testing.T) {
	r := NewRunner(nil)
	cfg := config.Config{
		Defaults: config.Defaults{Backend: "test"},
		Backends: map[string]config.Backend{
			"test": {Command: "sleep 5", PromptFlag: ""},
		},
		Projects: make(map[string]config.Project),
	}

	task := &model.Task{ID: "poll-race", Name: "poll-race", Worktree: t.TempDir()}
	sess, err := r.Start(task, cfg, 24, 80, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Simulates SyncPanes/SyncPTYSize + forwardKey's pane-poll pattern:
	// Alive() gate, Resize(), RecentOutput(), WriteInput() — all reachable
	// from the tick/spinner goroutines with no recover today.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if sess.Alive() {
				sess.Resize(30, 100) //nolint:errcheck
			}
			sess.RecentOutput()
			sess.RecentOutputTail(64)
			sess.WriteInput([]byte("x"), agentview.OriginUser) //nolint:errcheck
		}
	}()

	// Stop concurrently, from a separate goroutine, mirroring
	// heraReclaimAndArchiveTask racing the pane poller during a cascade nuke.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		if err := r.Stop("poll-race"); err != nil {
			t.Logf("stop: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()
}
