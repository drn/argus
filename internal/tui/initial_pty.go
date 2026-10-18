package tui

import (
	"sync"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
)

const initialPTYHeartbeat = 30 * time.Second

// One short-lived worker serializes the newest layout. No SQL or widget read
// runs while its mutex is held; a slow DB cannot hold up a Draw callback.
type initialPTYPublisher struct {
	mu      sync.Mutex
	wanted  db.InitialPTYLayout
	running bool
	done    chan struct{}
}

func (p *initialPTYPublisher) queue(layout db.InitialPTYLayout, save func(db.InitialPTYLayout) error) {
	if !layout.Active.Valid() {
		return
	}
	p.mu.Lock()
	previous := p.wanted
	previous.ReportedAt = layout.ReportedAt
	age := layout.ReportedAt.Sub(p.wanted.ReportedAt)
	if previous == layout && age >= 0 && age < initialPTYHeartbeat {
		p.mu.Unlock()
		return
	}
	p.wanted = layout
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.done = make(chan struct{})
	p.mu.Unlock()
	go func() {
		for {
			p.mu.Lock()
			current := p.wanted
			p.mu.Unlock()
			if err := save(current); err != nil {
				uxlog.Log("[spawn-size] publication failed: %v", err)
			} else {
				uxlog.Log("[spawn-size] published active=%dx%d coord=%d worker=%d", current.Active.Cols, current.Active.Rows, current.Coordinator.Cols, current.Worker.Cols)
			}
			p.mu.Lock()
			if p.wanted == current {
				p.running = false
				close(p.done)
				p.mu.Unlock()
				return
			}
			p.mu.Unlock()
		}
	}()
}

func (a *App) publishInitialPTYLayout(screen tcell.Screen) {
	d, local := a.db.(*db.DB)
	if !local {
		return
	}
	a.mu.Lock()
	mode := a.mode
	onHera := mode == modeTaskList && a.header.ActiveTab() == widget.TabHera
	a.mu.Unlock()
	if mode != modeTaskList && mode != modeAgent {
		return
	}
	w, h := screen.Size()
	rows, cols := ptySizeFromHostTerm(w, h, nil)
	if mode == modeAgent {
		_, _, pw, ph := a.agentPane.GetInnerRect()
		rows, cols = ptySizeFromPaneRect(pw, ph)
	}
	now := time.Now()
	if a.nowFn != nil {
		now = a.nowFn()
	}
	layout := db.InitialPTYLayout{ReportedAt: now, Active: db.PTYSize{Rows: rows, Cols: cols}}
	// Header and status bar each occupy one row. Hidden tview pages retain
	// stale rectangles, so derive role geometry from the current screen.
	r, coord, worker := hera.SplitLaunchPaneSizes(w, h-2)
	layout.Coordinator = db.PTYSize{Rows: r, Cols: coord}
	layout.Worker = db.PTYSize{Rows: r, Cols: worker}
	if onHera {
		layout.Active = layout.Worker
	}
	a.initialPTYPublisher.queue(layout, d.PublishInitialPTYLayout)
}
