// Package backendtier implements the probe-kind registry backing the
// backend-tier-routing capability: per-provider usage probes (Codex today;
// Claude's existing probe lives in internal/usagebudget and is reused as-is,
// not duplicated here) and their fail-open, in-memory caches.
package backendtier

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/uxlog"
)

const (
	codexProbeTimeout = 45 * time.Second
	codexPtyRows      = 24
	codexPtyCols      = 100

	// CacheMaxAge mirrors internal/usagebudget's CacheMaxAge: both the
	// in-memory cached reading and a rollout file's own mtime are trusted for
	// at most this long before being treated as stale/unknown.
	CacheMaxAge = time.Hour

	// codexStartupSettle/codexMessageSettle/codexStatusSettle bound the PTY
	// fallback's fixed waits. codex's interactive TUI has no machine-readable
	// "ready" signal, unlike claude's `-- /usage` one-shot form, so the
	// fallback probe has to wait fixed durations rather than watch for exit.
	codexStartupSettle = 3 * time.Second
	codexMessageSettle = 5 * time.Second
	codexStatusSettle  = 2 * time.Second
)

// Reading is the latest parsed Codex usage state.
type Reading struct {
	Percentage   float64
	ResetAt      time.Time
	LastProbedAt time.Time
}

type codexCacheState struct {
	mu      sync.RWMutex
	reading Reading
	ok      bool
}

var codexCache codexCacheState

var (
	codexRolloutProbeRunner = rolloutReading
	codexPTYProbeRunner     = runCodexPTYProbe
	codexNowFunc            = time.Now
	codexCacheSnapshotHook  func()
	codexHomeDirFunc        = os.UserHomeDir
)

// Probe runs one best-effort Codex usage probe and updates the cache when a
// reading is available. It first tries the free rollout-file read
// (rolloutReading), falling back to the costed PTY /status probe only when no
// sufficiently fresh rollout reading exists. Failures are logged and leave
// the existing cache untouched; callers do not need to treat the returned
// error as fatal.
func Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return nil
	}

	probedAt := codexNowFunc()

	reading, ok := codexRolloutProbeRunner()
	if !ok {
		probeCtx, cancel := context.WithTimeout(ctx, codexProbeTimeout)
		defer cancel()

		raw, err := codexPTYProbeRunner(probeCtx)
		if err != nil {
			if !errors.Is(probeCtx.Err(), context.Canceled) {
				uxlog.Log("[backendtier] codex PTY probe failed: %v", err)
			}
			return nil
		}
		parsed, perr := parseCodexStatusOutput(renderCodexStatusOutput(raw))
		if perr != nil {
			uxlog.Log("[backendtier] codex PTY probe parse failed: %v", perr)
			return nil
		}
		reading, ok = parsed, true
	}
	if !ok {
		return nil
	}

	reading.LastProbedAt = probedAt
	storeCodexReading(reading)
	uxlog.Log("[backendtier] codex probe updated: percentage=%.2f reset_at=%s", reading.Percentage, reading.ResetAt.Format(time.RFC3339))
	return nil
}

// CachedCodexPct returns the most recently probed Codex usage percentage and
// whether that reading is present and not stale. It never triggers a live
// probe, so it is safe to call synchronously from the tier resolver.
func CachedCodexPct() (float64, bool) {
	reading, ok := codexSnapshot()
	if !ok {
		return 0, false
	}
	return reading.Percentage, true
}

func storeCodexReading(reading Reading) {
	codexCache.mu.Lock()
	defer codexCache.mu.Unlock()
	codexCache.reading = reading
	codexCache.ok = true
}

func codexSnapshot() (Reading, bool) {
	if codexCacheSnapshotHook != nil {
		codexCacheSnapshotHook()
	}
	codexCache.mu.RLock()
	defer codexCache.mu.RUnlock()
	if !codexCache.ok {
		return Reading{}, false
	}
	reading := codexCache.reading
	if codexNowFunc().Sub(reading.LastProbedAt) > CacheMaxAge {
		return Reading{}, false
	}
	return reading, true
}

// rolloutReading returns the Codex rate-limits reading parsed from the most
// recently modified rollout file, and whether one was found that is both
// present and fresh enough (mtime within CacheMaxAge) to trust.
func rolloutReading() (Reading, bool) {
	path, modTime, err := latestRolloutFile()
	if err != nil {
		return Reading{}, false
	}
	if codexNowFunc().Sub(modTime) > CacheMaxAge {
		return Reading{}, false
	}
	return parseLatestRateLimits(path)
}

var rolloutFileRe = regexp.MustCompile(`^rollout-.*\.jsonl$`)

func codexSessionsRoot() (string, error) {
	home, err := codexHomeDirFunc()
	if err != nil {
		return "", fmt.Errorf("codex sessions dir: home dir: %w", err)
	}
	return filepath.Join(home, ".codex", "sessions"), nil
}

// latestRolloutFile walks the Codex sessions tree (~/.codex/sessions/YYYY/MM/DD/)
// and returns the most recently modified rollout-*.jsonl file.
func latestRolloutFile() (string, time.Time, error) {
	root, err := codexSessionsRoot()
	if err != nil {
		return "", time.Time{}, err
	}
	if _, err := os.Stat(root); err != nil {
		return "", time.Time{}, err
	}

	var (
		latestPath string
		latestMod  time.Time
	)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !rolloutFileRe.MatchString(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestPath = path
		}
		return nil
	})
	if walkErr != nil {
		return "", time.Time{}, walkErr
	}
	if latestPath == "" {
		return "", time.Time{}, fmt.Errorf("no codex rollout files found under %s", root)
	}
	return latestPath, latestMod, nil
}

// codexRolloutRecord is the minimal shape of one Codex rollout JSONL line
// needed to reach its rate_limits object; every other field is ignored.
type codexRolloutRecord struct {
	Payload struct {
		RateLimits *codexRateLimits `json:"rate_limits"`
	} `json:"payload"`
}

type codexRateLimits struct {
	Primary   *codexRateWindow `json:"primary"`
	Secondary *codexRateWindow `json:"secondary"`
}

type codexRateWindow struct {
	UsedPercent float64 `json:"used_percent"`
	ResetsAt    int64   `json:"resets_at"`
}

// parseLatestRateLimits tail-scans path for the last JSONL record carrying a
// rate_limits object and returns its reading. Malformed lines are skipped,
// not fatal, since a rollout file can be mid-write by a live codex session.
func parseLatestRateLimits(path string) (Reading, bool) {
	f, err := os.Open(path)
	if err != nil {
		uxlog.Log("[backendtier] codex rollout open failed: %v", err)
		return Reading{}, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		found   bool
		pct     float64
		resetAt time.Time
	)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec codexRolloutRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		p, r, ok := worstCodexWindow(rec.Payload.RateLimits)
		if !ok {
			continue
		}
		pct, resetAt, found = p, r, true
	}
	if err := scanner.Err(); err != nil {
		uxlog.Log("[backendtier] codex rollout scan failed: %v", err)
	}
	if !found {
		return Reading{}, false
	}
	return Reading{Percentage: pct, ResetAt: resetAt}, true
}

// worstCodexWindow picks the more exhausted of Codex's two independent
// rate-limit windows (5h "primary", weekly "secondary"): a tier should be
// treated as capped if either window is exhausted, not only the weekly one.
func worstCodexWindow(rl *codexRateLimits) (float64, time.Time, bool) {
	if rl == nil {
		return 0, time.Time{}, false
	}
	var (
		pct     float64
		resetAt time.Time
		found   bool
	)
	if rl.Primary != nil {
		pct = rl.Primary.UsedPercent
		resetAt = time.Unix(rl.Primary.ResetsAt, 0)
		found = true
	}
	if rl.Secondary != nil && (!found || rl.Secondary.UsedPercent > pct) {
		pct = rl.Secondary.UsedPercent
		resetAt = time.Unix(rl.Secondary.ResetsAt, 0)
		found = true
	}
	return pct, resetAt, found
}

// runCodexPTYProbe spawns a headless codex session, sends one minimal message
// to reach a state where rate-limit data is populated, then requests /status
// and returns the raw PTY output for parseCodexStatusOutput. Never call this
// from a test; internal/testutil convention forbids spawning the real codex
// binary, so it is exercised only via the codexPTYProbeRunner injection seam.
func runCodexPTYProbe(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "codex")
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: codexPtyRows, Cols: codexPtyCols})
	if err != nil {
		return nil, err
	}
	defer ptmx.Close()

	var buf bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&buf, ptmx)
		readDone <- copyErr
	}()

	kill := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = ptmx.Close()
		<-readDone
	}

	if waitOrDone(ctx, codexStartupSettle) {
		kill()
		return buf.Bytes(), ctx.Err()
	}
	if _, err := ptmx.Write([]byte("hi\r")); err != nil {
		kill()
		return buf.Bytes(), err
	}
	if waitOrDone(ctx, codexMessageSettle) {
		kill()
		return buf.Bytes(), ctx.Err()
	}
	if _, err := ptmx.Write([]byte("/status\r")); err != nil {
		kill()
		return buf.Bytes(), err
	}
	waitOrDone(ctx, codexStatusSettle)

	kill()
	return buf.Bytes(), nil
}

// waitOrDone waits for d or ctx cancellation, whichever comes first, and
// reports whether ctx was the reason it returned.
func waitOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}

var codexRenderState struct {
	mu   sync.Mutex
	emu  *xvt.SafeEmulator
	cols int
	rows int
}

func renderCodexStatusOutput(raw []byte) string {
	stripped := normalizeCodexText(sanitize.StripANSI(string(raw)))
	rendered := renderCodexPTY(raw)
	if rendered == "" {
		return stripped
	}
	if stripped == "" {
		return rendered
	}
	return rendered + "\n" + stripped
}

func renderCodexPTY(raw []byte) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			uxlog.Log("[backendtier] recovered from emulator panic: %v", rec)
			out = ""
		}
	}()

	codexRenderState.mu.Lock()
	defer codexRenderState.mu.Unlock()

	if codexRenderState.emu == nil {
		codexRenderState.emu = xvt.NewSafeEmulator(codexPtyCols, codexPtyRows)
		codexRenderState.cols = codexPtyCols
		codexRenderState.rows = codexPtyRows
		go io.Copy(io.Discard, codexRenderState.emu) //nolint:errcheck
	} else {
		if _, err := codexRenderState.emu.Write([]byte("\x1bc")); err != nil {
			uxlog.Log("[backendtier] emulator reset failed: %v", err)
			return ""
		}
		if codexRenderState.cols != codexPtyCols || codexRenderState.rows != codexPtyRows {
			codexRenderState.emu.Resize(codexPtyCols, codexPtyRows)
			codexRenderState.cols = codexPtyCols
			codexRenderState.rows = codexPtyRows
		}
	}

	if _, err := codexRenderState.emu.Write(raw); err != nil {
		uxlog.Log("[backendtier] emulator write failed: %v", err)
		return ""
	}
	return normalizeCodexText(codexRenderState.emu.String())
}

func normalizeCodexText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", " ")
	s = strings.ReplaceAll(s, " ", " ")
	return s
}

// codexStatusPercentRe requires a nearby "used" so a stray unrelated
// percentage on the same screen (e.g. a progress bar) isn't mistaken for a
// rate-limit reading.
var codexStatusPercentRe = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*%\s*used`)

// codexStatusResetRe matches codex's relative "resets in" phrasing, e.g.
// "resets in 3h 20m" or "resets in 4d".
var codexStatusResetRe = regexp.MustCompile(`(?i)resets?\s+in\s+((?:[0-9]+d\s*)?(?:[0-9]+h\s*)?(?:[0-9]+m\s*)?)`)

// parseCodexStatusOutput extracts the worst-case usage percentage rendered by
// codex's /status screen. The exact upstream format is not yet stable
// (openai/codex#15281 was open as of this writing), so this is deliberately
// defensive: any unrecognized shape is a parse failure, never a panic, and
// reset info is best-effort (a percentage with no parseable reset still
// succeeds with a zero-value ResetAt).
func parseCodexStatusOutput(output string) (Reading, error) {
	var (
		pct   float64
		found bool
	)
	for _, match := range codexStatusPercentRe.FindAllStringSubmatch(output, -1) {
		var v float64
		if _, err := fmt.Sscanf(match[1], "%f", &v); err != nil {
			continue
		}
		if !found || v > pct {
			pct = v
			found = true
		}
	}
	if !found {
		return Reading{}, errors.New("codex status: no usage percentage found")
	}

	reading := Reading{Percentage: pct}
	if match := codexStatusResetRe.FindStringSubmatch(output); len(match) == 2 {
		if d, err := parseCodexResetDuration(match[1]); err == nil {
			reading.ResetAt = codexNowFunc().Add(d)
		}
	}
	return reading, nil
}

var codexDurationPartRe = regexp.MustCompile(`(?i)([0-9]+)\s*(d|h|m)`)

func parseCodexResetDuration(s string) (time.Duration, error) {
	parts := codexDurationPartRe.FindAllStringSubmatch(s, -1)
	if len(parts) == 0 {
		return 0, fmt.Errorf("parse codex reset duration %q: no components", s)
	}
	var total time.Duration
	for _, part := range parts {
		var n int
		if _, err := fmt.Sscanf(part[1], "%d", &n); err != nil {
			return 0, fmt.Errorf("parse codex reset duration %q: %w", s, err)
		}
		switch strings.ToLower(part[2]) {
		case "d":
			total += time.Duration(n) * 24 * time.Hour
		case "h":
			total += time.Duration(n) * time.Hour
		case "m":
			total += time.Duration(n) * time.Minute
		}
	}
	return total, nil
}
