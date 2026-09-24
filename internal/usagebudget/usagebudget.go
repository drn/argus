package usagebudget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	xvt "github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/uxlog"
)

const (
	probeTimeout = 45 * time.Second
	ptyRows      = 24
	ptyCols      = 100

	// CacheMaxAge bounds how long threshold routing trusts a reading. The probe
	// cadence is currently 30 minutes, so one hour permits a missed tick without
	// letting old pressure steer spawns indefinitely.
	CacheMaxAge = time.Hour
)

// Reading is the latest parsed Claude weekly usage state.
type Reading struct {
	Percentage   float64
	ResetAt      time.Time
	LastProbedAt time.Time
}

type cacheState struct {
	mu      sync.RWMutex
	reading Reading
	ok      bool
}

var usageCache cacheState

var (
	probeRunner       = runClaudeUsageProbe
	nowFunc           = time.Now
	cacheSnapshotHook func()
)

// Probe runs one best-effort Claude /usage probe and updates the cache when the
// rendered output parses. Failures are logged and leave the existing cache
// untouched; callers do not need to treat the returned error as fatal.
func Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	raw, err := probeRunner(probeCtx)
	if err != nil {
		if !errors.Is(probeCtx.Err(), context.Canceled) {
			uxlog.Log("[usagebudget] probe failed: %v", err)
		}
		return nil
	}

	probedAt := nowFunc()
	reading, err := parseUsageOutput(renderUsageOutput(raw), probedAt)
	if err != nil {
		uxlog.Log("[usagebudget] parse failed: %v", err)
		return nil
	}
	reading.LastProbedAt = probedAt
	storeReading(reading)
	uxlog.Log("[usagebudget] probe updated: percentage=%.2f reset_at=%s", reading.Percentage, reading.ResetAt.Format(time.RFC3339))
	return nil
}

// ResolveWorkerBackend returns the budget-aware backend override for a Hera
// worker/freelance spawn. An explicit backend wins immediately and does not read
// the usage cache.
func ResolveWorkerBackend(explicit string, cfg config.Config) string {
	if explicit != "" {
		return explicit
	}

	wb := cfg.Hera.WorkerBudget
	fallback := strings.TrimSpace(wb.FallbackBackend)
	if fallback == "" {
		fallback = config.DefaultWorkerBudgetFallbackBackend
	}
	if _, ok := cfg.Backends[fallback]; !ok {
		return ""
	}
	if wb.Enabled {
		return fallback
	}
	if wb.ThresholdPct <= 0 {
		return ""
	}

	reading, ok := snapshot()
	if !ok {
		return ""
	}
	if reading.Percentage >= float64(wb.ThresholdPct) {
		return fallback
	}
	return ""
}

// CachedClaudePct returns the most recently probed Claude weekly usage
// percentage and whether that reading is present and not stale. It never
// triggers a live probe, so it is safe to call synchronously from
// internal/backendtier's tier-list resolver. Does not affect
// ResolveWorkerBackend's own behavior.
func CachedClaudePct() (float64, bool) {
	reading, ok := snapshot()
	if !ok {
		return 0, false
	}
	return reading.Percentage, true
}

func storeReading(reading Reading) {
	usageCache.mu.Lock()
	defer usageCache.mu.Unlock()
	usageCache.reading = reading
	usageCache.ok = true
}

func snapshot() (Reading, bool) {
	if cacheSnapshotHook != nil {
		cacheSnapshotHook()
	}
	usageCache.mu.RLock()
	defer usageCache.mu.RUnlock()
	if !usageCache.ok {
		return Reading{}, false
	}
	reading := usageCache.reading
	if nowFunc().Sub(reading.LastProbedAt) > CacheMaxAge {
		return Reading{}, false
	}
	return reading, true
}

func runClaudeUsageProbe(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "claude", "--", "/usage")
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
	)

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: ptyRows, Cols: ptyCols})
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

	waitErr := cmd.Wait()
	_ = ptmx.Close()
	readErr := <-readDone
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if waitErr != nil {
		return buf.Bytes(), waitErr
	}
	_ = readErr
	return buf.Bytes(), nil
}

var renderState struct {
	mu   sync.Mutex
	emu  *xvt.SafeEmulator
	cols int
	rows int
}

func renderUsageOutput(raw []byte) string {
	stripped := normalizeText(sanitize.StripANSI(string(raw)))
	rendered := renderPTY(raw)
	if rendered == "" {
		return stripped
	}
	if stripped == "" {
		return rendered
	}
	return rendered + "\n" + stripped
}

func renderPTY(raw []byte) (out string) {
	defer func() {
		if rec := recover(); rec != nil {
			uxlog.Log("[usagebudget] recovered from emulator panic: %v", rec)
			out = ""
		}
	}()

	renderState.mu.Lock()
	defer renderState.mu.Unlock()

	if renderState.emu == nil {
		renderState.emu = xvt.NewSafeEmulator(ptyCols, ptyRows)
		renderState.cols = ptyCols
		renderState.rows = ptyRows
		go io.Copy(io.Discard, renderState.emu) //nolint:errcheck
	} else {
		if _, err := renderState.emu.Write([]byte("\x1bc")); err != nil {
			uxlog.Log("[usagebudget] emulator reset failed: %v", err)
			return ""
		}
		if renderState.cols != ptyCols || renderState.rows != ptyRows {
			renderState.emu.Resize(ptyCols, ptyRows)
			renderState.cols = ptyCols
			renderState.rows = ptyRows
		}
	}

	if _, err := renderState.emu.Write(raw); err != nil {
		uxlog.Log("[usagebudget] emulator write failed: %v", err)
		return ""
	}
	return normalizeText(renderState.emu.String())
}

func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", " ")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return s
}

var percentageRe = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*%`)

var resetRe = regexp.MustCompile(`(?i)\bResets\s+(?:on\s+)?(.+?)\s+at\s+([0-9]{1,2}(?::[0-9]{2})?\s*(?:AM|PM|am|pm)?|[0-9]{1,2}:[0-9]{2})\s*\(([^)]+)\)`)

func parseUsageOutput(output string, base time.Time) (Reading, error) {
	var pct float64
	foundPct := false
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "current week") || !strings.Contains(lower, "all models") {
			continue
		}
		match := percentageRe.FindStringSubmatch(line)
		if len(match) < 2 {
			continue
		}
		parsed, err := strconv.ParseFloat(match[1], 64)
		if err != nil {
			return Reading{}, fmt.Errorf("parse percentage %q: %w", match[1], err)
		}
		pct = parsed
		foundPct = true
		break
	}
	if !foundPct {
		return Reading{}, errors.New("weekly percentage not found")
	}

	match := resetRe.FindStringSubmatch(output)
	if len(match) < 4 {
		return Reading{}, errors.New("reset timestamp not found")
	}
	resetAt, err := parseResetTimestamp(match[1], match[2], match[3], base)
	if err != nil {
		return Reading{}, err
	}
	return Reading{Percentage: pct, ResetAt: resetAt}, nil
}

var (
	ordinalDayRe = regexp.MustCompile(`\b([0-9]{1,2})(?:st|nd|rd|th)\b`)
	yearRe       = regexp.MustCompile(`\b[0-9]{4}\b`)
)

func parseResetTimestamp(datePart, timePart, zonePart string, base time.Time) (time.Time, error) {
	datePart = cleanDatePart(datePart)
	timePart = strings.ToUpper(strings.Join(strings.Fields(timePart), " "))
	loc := locationForZone(zonePart)
	hasYear := yearRe.MatchString(datePart)

	input := datePart + " " + timePart
	if !hasYear {
		input = fmt.Sprintf("%s %d %s", datePart, base.In(loc).Year(), timePart)
	}

	layouts := []string{
		"Jan 2 2006 3:04 PM",
		"January 2 2006 3:04 PM",
		"Jan 2 2006 3 PM",
		"January 2 2006 3 PM",
		"Jan 2 2006 15:04",
		"January 2 2006 15:04",
		"2006-01-02 3:04 PM",
		"2006-01-02 15:04",
	}
	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, input, loc)
		if err == nil {
			if !hasYear {
				baseInLoc := base.In(loc)
				if parsed.Before(baseInLoc.Add(-24 * time.Hour)) {
					parsed = parsed.AddDate(1, 0, 0)
				}
			}
			return parsed, nil
		}
		lastErr = err
	}
	return time.Time{}, fmt.Errorf("parse reset timestamp %q (%s): %w", input, zonePart, lastErr)
}

func cleanDatePart(s string) string {
	s = sanitize.StripANSI(s)
	s = strings.ReplaceAll(s, ",", " ")
	s = ordinalDayRe.ReplaceAllString(s, "$1")
	s = strings.Join(strings.Fields(s), " ")
	parts := strings.Fields(s)
	if len(parts) > 1 && isWeekday(parts[0]) {
		s = strings.Join(parts[1:], " ")
	}
	return s
}

func isWeekday(s string) bool {
	switch strings.ToLower(strings.TrimSuffix(s, ".")) {
	case "mon", "monday", "tue", "tues", "tuesday", "wed", "wednesday",
		"thu", "thur", "thurs", "thursday", "fri", "friday",
		"sat", "saturday", "sun", "sunday":
		return true
	default:
		return false
	}
}

func locationForZone(zone string) *time.Location {
	switch strings.ToUpper(strings.TrimSpace(zone)) {
	case "UTC", "GMT":
		return time.UTC
	case "PST", "PDT":
		if loc, err := time.LoadLocation("America/Los_Angeles"); err == nil {
			return loc
		}
	case "MST", "MDT":
		if loc, err := time.LoadLocation("America/Denver"); err == nil {
			return loc
		}
	case "CST", "CDT":
		if loc, err := time.LoadLocation("America/Chicago"); err == nil {
			return loc
		}
	case "EST", "EDT":
		if loc, err := time.LoadLocation("America/New_York"); err == nil {
			return loc
		}
	}
	return time.Local
}
