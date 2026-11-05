package tui

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestPTYSyncLoopKeepsResizeWithoutDrawQueue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := New(testDB(t), agent.NewRunner(nil), false)
	sess := newRepaintSession()
	a.agentPane.SetRect(0, 0, 80, 24)
	a.agentPane.SetSession(sess)
	a.agentPane.SetRect(0, 0, 90, 30)
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(90, 30)
	a.agentPane.Draw(sim) // posts a resize; the PTY loop must apply it off-thread
	prior := sess.resizes.Load()
	a.mode = modeAgent
	a.agentState.TaskID = "sync-test"
	synctest.Test(t, func(t *testing.T) {
		a.tickDone = make(chan struct{})
		a.startAgentPTYSyncLoop("sync-test", sess)
		synctest.Wait()
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		testutil.Equal(t, sess.resizes.Load(), prior+1)
		close(a.tickDone)
		synctest.Wait()
		// No tview event loop runs here: any QueueUpdateDraw would strand the
		// loop and prevent this synctest bubble from terminating.
	})
}
