package hera

import (
	"fmt"
	"maps"
	"reflect"
	"time"

	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// Off-thread rail rebuild (reduce-hera-rail-rebuild-stalls).
//
// A tick-driven rebuild used to run BuildModel, tier annotation and the PR-meta
// read on the tview goroutine, ~100ms every time ANY process committed ANY
// write (PRAGMA data_version is database-wide). The tick path now:
//
//  1. snapshots its inputs on the tview goroutine (takeSnapshot): value copies
//     of the task list, clones of the four runtime maps, the data-version
//     fingerprint and the current generation;
//  2. builds from that snapshot ONLY on one background goroutine
//     (buildFromSnapshot) — no *model.Task, runtime map or page field is read;
//  3. queues a publish back onto the tview goroutine, which drops it if the
//     generation moved (stale), drops it if the built model equals the
//     published one (equal), and otherwise runs the same SetModel +
//     SetPRMeta + applySelection sequence as the synchronous path.
//
// Every field below the "UI-owned" line is read and written ONLY on the tview
// goroutine; the build goroutine touches nothing but its rebuildSnapshot.

// asyncStats counts async-path outcomes. UI-goroutine-owned; read by tests.
type asyncStats struct {
	builds     int // background builds launched
	published  int // builds whose result replaced the rail's model
	staleDrops int // results dropped: generation moved while the build ran
	equalDrops int // results dropped: identical to the published model
	reruns     int // follow-up builds launched because a change arrived mid-build
	errors     int // builds that failed (gate left open for retry)
}

// gateState is the input state a rebuild is based on — exactly what
// shouldRebuild compares the live page against.
type gateState struct {
	fp              int64
	hasFP           bool
	needsInput      map[string]bool
	sessionIdle     map[string]bool
	sessionRunning  map[string]bool
	sustainedActive map[string]bool
}

// rebuildSnapshot is everything one build is allowed to read. It is created on
// the tview goroutine and never mutated afterwards.
type rebuildSnapshot struct {
	gen          uint64
	gate         gateState
	reader       heramodel.HeraReader
	tasks        []*model.Task // private value copies (nil unless tasksKnown)
	tasksKnown   bool
	tierResolver func(*heramodel.RoleView)
	// tierResolverFactory is invoked ONCE per build, on whichever goroutine runs
	// buildFromSnapshot; the closure it returns (and any memo inside it) is
	// confined to that build, so it needs no synchronization.
	tierResolverFactory func() (annotate func(*heramodel.RoleView), done func())

	// Last published state, for the equality drop. Replaced, never mutated, so
	// reading them from the build goroutine is safe; the generation check at
	// publish proves they are still the current ones.
	prevPristine *heramodel.Model
	prevPRMeta   map[string]map[string]string
}

// rebuildResult is what a build hands back to the tview goroutine.
type rebuildResult struct {
	model    heramodel.Model // given to rail.SetModel (the rail may stamp it)
	pristine heramodel.Model // untouched clone kept for the next equality compare
	prMeta   map[string]map[string]string
	prOK     bool // prMeta was read successfully
	equal    bool // identical to the previously published state
	err      error
	buildDur time.Duration
}

// SetUIQueue wires the function that runs a closure on the tview goroutine (the
// App passes tapp.QueueUpdateDraw). With none wired — unit tests, a bare
// New() — every rebuild stays synchronous and inline, exactly as before.
func (p *HeraPage) SetUIQueue(fn func(func())) { p.uiQueue = fn }

// AsyncEnabled reports whether tick-driven rebuilds build off the tview
// goroutine (a UI queue is wired). Exposed so the App can pin its Run()-time
// wiring in a test.
func (p *HeraPage) AsyncEnabled() bool { return p.uiQueue != nil }

// AsyncStats reports how many background builds were launched and how many
// published a new model. tview goroutine only; for the App's smoke tests.
func (p *HeraPage) AsyncStats() (builds, published int) {
	return p.asyncStats.builds, p.asyncStats.published
}

// captureGate snapshots the state a rebuild started now would be based on.
func (p *HeraPage) captureGate() gateState {
	fp, ok := p.dbFingerprint()
	return gateState{
		fp:              fp,
		hasFP:           ok,
		needsInput:      maps.Clone(p.needsInput),
		sessionIdle:     maps.Clone(p.sessionIdle),
		sessionRunning:  maps.Clone(p.sessionRunning),
		sustainedActive: maps.Clone(p.sustainedActive),
	}
}

// takeSnapshot builds the immutable input for one rebuild. tview goroutine only.
func (p *HeraPage) takeSnapshot() *rebuildSnapshot {
	s := &rebuildSnapshot{
		gen:                 p.gen,
		gate:                p.captureGate(),
		reader:              p.reader,
		tasksKnown:          p.tasksKnown,
		tierResolver:        p.tierResolver,
		tierResolverFactory: p.tierResolverFactory,
		prevPristine:        p.lastPristine,
		prevPRMeta:          p.prMeta,
	}
	if p.tasksKnown {
		s.tasks = make([]*model.Task, len(p.tasks))
		for i, t := range p.tasks {
			if t == nil {
				continue
			}
			cp := *t // model.Task holds no slice/map/pointer fields: a value copy is a deep copy
			s.tasks[i] = &cp
		}
	}
	return s
}

// buildFromSnapshot runs the full model projection. It may run on any
// goroutine: it reads only s (and the mutex-guarded reader).
func (p *HeraPage) buildFromSnapshot(s *rebuildSnapshot) rebuildResult {
	start := time.Now()
	reader := s.reader
	if reader != nil && s.tasksKnown {
		reader = &tasksReader{HeraReader: reader, tasks: s.tasks}
	}
	m, err := heramodel.BuildModel(reader, s.gate.needsInput, s.gate.sessionIdle, s.gate.sessionRunning, s.gate.sustainedActive)
	if err != nil {
		return rebuildResult{err: err, buildDur: time.Since(start)}
	}
	// Tier annotation reads config and disk; it runs here, off the Draw path.
	if s.tierResolverFactory != nil {
		annotate, done := s.tierResolverFactory()
		m.AnnotateRoles(annotate)
		if done != nil {
			done()
		}
	} else if s.tierResolver != nil {
		m.AnnotateRoles(s.tierResolver)
	}
	res := rebuildResult{model: m}
	if s.reader != nil {
		if pr, perr := s.reader.ListMetaByNamespace("pr"); perr == nil {
			res.prMeta, res.prOK = pr, true
		}
	}
	res.pristine = m.Clone()
	res.equal = s.prevPristine != nil &&
		(!res.prOK || reflect.DeepEqual(s.prevPRMeta, res.prMeta)) &&
		reflect.DeepEqual(*s.prevPristine, res.pristine)
	res.buildDur = time.Since(start)
	return res
}

// applyResult installs a built model on the rail. tview goroutine only. This is
// the SAME sequence the synchronous path always ran, so selection, fold state,
// sticky reveal and the needs-input re-arm bookkeeping (BUG-069/070/071) are
// untouched — only when the input arrives changed.
func (p *HeraPage) applyResult(res rebuildResult) {
	p.rail.SetModel(res.model)
	pristine := res.pristine
	p.lastPristine = &pristine
	if res.prOK {
		p.prMeta = res.prMeta
	}
	p.rail.SetPRMeta(p.prMeta)
	// SetModel rebuilt the model's backing arrays, so the prior Selection
	// pointers are stale — re-derive and rebind (task IDs usually unchanged, so
	// bindPane is a no-op and the emulators are preserved).
	p.applySelection()
}

// rebuildSync is the inline path: forced refreshes, remote mode, and any page
// with no UI queue wired. It supersedes any build in flight.
func (p *HeraPage) rebuildSync() {
	start := time.Now()
	p.gen++
	p.rerun = false // this rebuild covers whatever a queued follow-up wanted
	snap := p.takeSnapshot()
	res := p.buildFromSnapshot(snap)
	if res.err != nil {
		uxlog.Log("[hera-view] rail refresh failed: %v", res.err)
		return
	}
	p.applyResult(res)
	p.markRebuiltFrom(snap.gate)
	m := res.model
	uxlog.Log("[hera-view] rail refreshed: pinned=%d active=%d archived=%d freelance=%d (remote=%v) took=%s",
		len(m.Pinned), len(m.Active), len(m.Archived), len(m.Freelance), p.remote, time.Since(start).Round(time.Microsecond))
}

// launchBuild snapshots and starts one background build. tview goroutine only.
func (p *HeraPage) launchBuild() {
	snap := p.takeSnapshot()
	// The gate now describes the state the most recent build is based on, so a
	// write landing DURING the build differs from it and queues a follow-up,
	// and a quiescent tick during the build queues nothing.
	p.markRebuiltFrom(snap.gate)
	p.building = true
	p.asyncStats.builds++
	uxlog.Log("[hera-view] rail build start: gen=%d fingerprint=%d", snap.gen, snap.gate.fp)

	build := p.buildFn
	if build == nil {
		build = p.buildFromSnapshot
	}
	queue := p.uiQueue
	go func() {
		res := safeBuild(build, snap)
		queue(func() { p.publish(snap, res) })
	}()
}

// publish is the build's completion, run on the tview goroutine.
func (p *HeraPage) publish(snap *rebuildSnapshot, res rebuildResult) {
	p.building = false
	switch {
	case snap.gen != p.gen:
		p.asyncStats.staleDrops++
		uxlog.Log("[hera-view] rail publish dropped: stale gen=%d current=%d build=%s", snap.gen, p.gen, res.buildDur.Round(time.Microsecond))
	case res.err != nil:
		p.asyncStats.errors++
		p.fpKnown = false // retry on the next opportunity
		uxlog.Log("[hera-view] rail build failed: %v", res.err)
	case res.equal:
		p.asyncStats.equalDrops++
		uxlog.Log("[hera-view] rail publish dropped: model unchanged build=%s", res.buildDur.Round(time.Microsecond))
	default:
		start := time.Now()
		p.applyResult(res)
		p.asyncStats.published++
		m := res.model
		uxlog.Log("[hera-view] rail refreshed: pinned=%d active=%d archived=%d freelance=%d (remote=%v) took=%s async=true build=%s ui=%s",
			len(m.Pinned), len(m.Active), len(m.Archived), len(m.Freelance), p.remote,
			(res.buildDur + time.Since(start)).Round(time.Microsecond),
			res.buildDur.Round(time.Microsecond), time.Since(start).Round(time.Microsecond))
	}
	if p.rerun {
		p.rerun = false
		p.asyncStats.reruns++
		uxlog.Log("[hera-view] rail follow-up build: change arrived during the previous build")
		p.launchBuild()
	}
}

// safeBuild runs a build and turns a panic into an error result. The build runs
// on its own goroutine, where an unrecovered panic would take the whole TUI
// down (on the tview goroutine it would not have been any safer, but the other
// hera background work is panic-guarded — BUG-062 — and a failed rail refresh
// must degrade to "retry next tick", not "crash").
func safeBuild(build func(*rebuildSnapshot) rebuildResult, snap *rebuildSnapshot) (res rebuildResult) {
	defer func() {
		if r := recover(); r != nil {
			res = rebuildResult{err: fmt.Errorf("rail build panicked: %v", r)}
		}
	}()
	return build(snap)
}
