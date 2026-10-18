package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/drn/argus/internal/uxlog"
	"github.com/shirou/gopsutil/v4/process"
)

const rerenderDescendantGrace = 250 * time.Millisecond

type rerenderProcess struct {
	pid, parent int32
	started     int64
	shared      bool
}

func isSharedClaudeDaemon(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	executable := filepath.Base(argv[0])
	// Native installs may exec the versioned binary behind the claude symlink.
	native := executable == "claude" || strings.HasSuffix(filepath.ToSlash(filepath.Dir(argv[0])), "/claude/versions")
	if native {
		return argv[1] == "daemon"
	}
	// npm installs launch the official CLI through Node.
	return len(argv) >= 3 && (executable == "node" || executable == "nodejs") &&
		strings.HasSuffix(filepath.ToSlash(argv[1]), "/@anthropic-ai/claude-code/cli.js") && argv[2] == "daemon"
}

// Creation times bind the captured ancestry to a process instance, even after
// killing its parent causes reparenting. No process-group signal can spill into
// another task, and setsid does not hide an ordinary parent/child relationship.
func rerenderProcessSnapshot(ctx context.Context) ([]rerenderProcess, error) {
	return rerenderProcessSnapshotWithArgv(ctx, (*process.Process).CmdlineSliceWithContext)
}

func rerenderProcessSnapshotWithArgv(ctx context.Context, readArgv func(*process.Process, context.Context) ([]string, error)) ([]rerenderProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pids, err := process.PidsWithContext(ctx)
	if err != nil {
		return nil, err
	}
	var nodes []rerenderProcess
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := process.NewProcessWithContext(ctx, pid)
		if err != nil {
			continue
		}
		started, err := p.CreateTimeWithContext(ctx)
		if err != nil || started <= 0 {
			continue
		}
		parent, err := p.PpidWithContext(ctx)
		if err == nil {
			argv, argsErr := readArgv(p, ctx)
			if argsErr != nil || len(argv) == 0 {
				uxlog.Log("[rerender] argv inspection skipped pid=%d err=%v", pid, argsErr)
				slog.Warn("[rerender] argv inspection skipped", "pid", pid, "err", argsErr)
				continue // Unknown ownership also disconnects this subtree.
			}
			nodes = append(nodes, rerenderProcess{pid: pid, parent: parent, started: started, shared: isSharedClaudeDaemon(argv)})
		}
	}
	return nodes, nil
}

func selectRerenderDescendants(nodes []rerenderProcess, root int) []rerenderProcess {
	if root <= 1 {
		return nil
	}
	children := make(map[int32][]rerenderProcess)
	for _, n := range nodes {
		if n.pid > 1 {
			children[n.parent] = append(children[n.parent], n)
		}
	}
	// The root PID comes from os.Process; compare as int instead of narrowing it.
	var roots []rerenderProcess
	for parent, group := range children {
		if int(parent) == root {
			roots = group
			break
		}
	}
	seen := map[int32]bool{}
	var selected []rerenderProcess
	for len(roots) > 0 {
		n := roots[0]
		roots = roots[1:]
		if seen[n.pid] || int(n.pid) == root {
			continue
		}
		seen[n.pid] = true
		if n.shared {
			uxlog.Log("[rerender] preserving shared Claude daemon pid=%d", n.pid)
			slog.Info("[rerender] preserving shared Claude daemon", "pid", n.pid)
			continue // Hosted background sessions belong to other tasks too.
		}
		selected = append(selected, n)
		roots = append(roots, children[n.pid]...)
	}
	// Children first gives waiting parents an opportunity to reap them.
	for l, r := 0, len(selected)-1; l < r; l, r = l+1, r-1 {
		selected[l], selected[r] = selected[r], selected[l]
	}
	return selected
}

func signalRerenderDescendant(n rerenderProcess, sig syscall.Signal) (bool, error) {
	p, err := process.NewProcess(n.pid)
	if err != nil {
		if errors.Is(err, process.ErrorProcessNotRunning) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	started, err := p.CreateTime()
	if err != nil {
		return false, err
	}
	if started != n.started {
		return false, nil
	}
	err = p.SendSignal(sig)
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return err == nil, err
}

type rerenderReapStats struct{ matched, term, killed, reaped, failed int }

func reapRerenderDescendants(nodes []rerenderProcess, signal func(rerenderProcess, syscall.Signal) (bool, error), wait func(time.Duration)) rerenderReapStats {
	stats := rerenderReapStats{matched: len(nodes)}
	if len(nodes) == 0 {
		return stats
	}
	for _, n := range nodes {
		if sent, err := signal(n, syscall.SIGTERM); err != nil {
			stats.failed++
			slog.Warn("[rerender] descendant TERM failed", "pid", n.pid, "err", err)
		} else if sent {
			stats.term++
		}
	}
	wait(rerenderDescendantGrace)
	for _, n := range nodes {
		if sent, err := signal(n, syscall.SIGKILL); err != nil {
			stats.failed++
			slog.Warn("[rerender] descendant KILL failed", "pid", n.pid, "err", err)
		} else {
			stats.reaped++
			if sent {
				stats.killed++
			}
		}
	}
	return stats
}

func (s *Session) escalateRerender(grace time.Duration) {
	select {
	case <-s.processExited:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	nodes, err := rerenderProcessSnapshot(ctx)
	if err != nil {
		uxlog.Log("[rerender] descendant inspection failed task=%s err=%v", s.TaskID, err)
		slog.Warn("[rerender] descendant inspection failed", "task", s.TaskID, "err", err) //nolint:gosec // Structured slog attributes are escaped by the handler.
	}
	select {
	case <-s.processExited:
		return
	default:
	}
	// Commit descendant cleanup only if the original process was force-stopped.
	if err := s.Signal(syscall.SIGKILL); err != nil {
		uxlog.Log("[rerender] escalation skipped task=%s err=%v", s.TaskID, err)
		slog.Info("[rerender] escalation skipped", "task", s.TaskID, "err", err) //nolint:gosec // Structured slog attributes are escaped by the handler.
		return
	}
	uxlog.Log("[rerender] SIGKILL task=%s after=%s", s.TaskID, grace)
	slog.Info("[rerender] SIGKILL after grace", "task", s.TaskID, "grace", grace) //nolint:gosec // Structured slog attributes are escaped by the handler.
	stats := reapRerenderDescendants(selectRerenderDescendants(nodes, s.PID()), signalRerenderDescendant, time.Sleep)
	if err != nil {
		stats.failed++
	}
	uxlog.Log("[rerender] descendants reaped task=%s count=%d matched=%d term=%d killed=%d errors=%d", s.TaskID, stats.reaped, stats.matched, stats.term, stats.killed, stats.failed)
	slog.Info("[rerender] descendants reaped", "task", s.TaskID, "count", stats.reaped, "matched", stats.matched, "term", stats.term, "killed", stats.killed, "errors", stats.failed)
}
