package tui

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
	"github.com/rivo/tview"
)

// Fixed synthetic output workload through the REAL pump, REAL tview draw and a
// SimulationScreen. It is opt-in (ARGUS_PAINT_WORKLOAD=1) because it burns CPU
// on purpose and its absolute numbers are machine-dependent; the before/after
// comparison in openspec/changes/cap-output-repaint-rate/measurements.md was
// produced by running it on both sides of the change.

const (
	workloadSeconds = 4
	workloadWrites  = 50 // writes/s: ~live busy-pane frame rate (38-47fps measured in PR 1)
)

func cpuTime(t *testing.T) time.Duration {
	t.Helper()
	var ru syscall.Rusage
	testutil.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &ru))
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

type workloadResult struct {
	frames       int
	drawBusy     time.Duration // wall time inside the pump's draw (the UI goroutine's work)
	cpu          time.Duration
	echoMs       []float64
	finalPainted bool
}

func runWorkload(t *testing.T) workloadResult {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	a := New(testDB(t), agent.NewRunner(nil), false)
	sess := newRepaintSession()
	a.agentPane.SetTaskID("workload")
	a.agentPane.SetSession(sess)
	tapp, sim, _ := simApp(t)
	sim.SetSize(200, 50)
	a.tapp = tapp
	tapp.SetRoot(tview.NewPages().AddPage("tasks", a.agentPane, true, true), true)
	tapp.SetAfterDrawFunc(a.afterDraw)
	var mu sync.Mutex
	var frames int
	var drawnAt []time.Time
	var drawBusy time.Duration
	drawn := make(chan struct{}) // closed and replaced on every completed draw
	a.startOutputRepaint()
	draw := a.repaint.draw
	a.repaint.draw = func() {
		begin := time.Now()
		draw()
		mu.Lock()
		frames++
		drawBusy += time.Since(begin)
		drawnAt = append(drawnAt, time.Now())
		close(drawn)
		drawn = make(chan struct{})
		mu.Unlock()
	}
	stop := runApp(t, tapp)
	defer func() {
		close(a.tickDone)
		<-a.repaint.stopped
		readUI(t, tapp, a.repaint.close)
		stop()
	}()
	time.Sleep(200 * time.Millisecond) // subscription settles
	mu.Lock()
	frames, drawnAt, drawBusy = 0, nil, 0
	mu.Unlock()

	var res workloadResult
	line := strings.Repeat("streamed agent output line with some width ", 3)
	cpu0 := cpuTime(t)
	start := time.Now()
	next := start
	for i := 0; time.Since(start) < workloadSeconds*time.Second; i++ {
		sess.emit([]byte(fmt.Sprintf("%05d %s\r\n", i, line)))
		if i > 0 && i%(workloadWrites/2) == 0 { // a keystroke echo every ~500ms mid-stream
			a.repaint.noteKey() // the keystroke reaching handleGlobalKey
			emitted := time.Now()
			sess.emit([]byte("ECHO"))
			res.echoMs = append(res.echoMs, waitDrawAfter(&mu, &drawnAt, &drawn, emitted))
		}
		next = next.Add(time.Second / workloadWrites)
		time.Sleep(time.Until(next))
	}
	res.cpu = cpuTime(t) - cpu0
	// No frame may be lost at the end of a burst: the last byte must paint.
	sess.emit([]byte("FINAL-MARKER"))
	last := time.Now()
	waitDrawAfter(&mu, &drawnAt, &drawn, last)
	var b strings.Builder
	readUI(t, tapp, func() {
		cells, w, _ := sim.GetContents()
		for i, c := range cells {
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
			if (i+1)%w == 0 {
				b.WriteByte('\n')
			}
		}
	})
	res.finalPainted = strings.Contains(b.String(), "FINAL-MARKER")
	mu.Lock()
	res.frames = frames
	res.drawBusy = drawBusy
	mu.Unlock()
	return res
}

// waitDrawAfter returns ms from `since` until the first completed draw after
// it, blocking on the draw notification rather than polling.
func waitDrawAfter(mu *sync.Mutex, drawnAt *[]time.Time, drawn *chan struct{}, since time.Time) float64 {
	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		for _, d := range *drawnAt {
			if d.After(since) {
				mu.Unlock()
				return float64(d.Sub(since)) / float64(time.Millisecond)
			}
		}
		ch := *drawn
		mu.Unlock()
		select {
		case <-ch:
		case <-deadline:
			return -1
		}
	}
}

func TestWorkload_OutputRepaintRate(t *testing.T) {
	if os.Getenv("ARGUS_PAINT_WORKLOAD") == "" {
		t.Skip("set ARGUS_PAINT_WORKLOAD=1 to run the repaint workload")
	}
	var runs []workloadResult
	for range 5 {
		runs = append(runs, runWorkload(t))
	}
	for i, r := range runs {
		echo := slices.Clone(r.echoMs)
		slices.Sort(echo)
		t.Logf("run %d: frames=%d (%.1f fps) draw_busy=%.1f%% cpu=%.1f%% of one core echo_ms=%v final_painted=%v",
			i, r.frames, float64(r.frames)/workloadSeconds,
			100*float64(r.drawBusy)/float64(workloadSeconds*time.Second),
			100*float64(r.cpu)/float64(workloadSeconds*time.Second), echo, r.finalPainted)
		testutil.Equal(t, r.finalPainted, true)
	}
}
