// Package latencyreport reads the optional TUI timing log and summarizes UI stalls.
package latencyreport

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

var metrics = []string{"total_ms", "queue_ms", "arrive_ms", "paint_ms", "stall_ms", "quiet_ms"}

// Sample holds identifiers and timings only; input content is never collected.
type Sample struct {
	Pane          string
	Values        map[string]float64
	Quiet, NoEcho bool
}

// Parse reads uxlog's local timestamps, with inclusive optional bounds.
// Incomplete/malformed lines (including a rotation boundary) are skipped.
func Parse(r io.Reader, since, until time.Time) ([]Sample, error) {
	var samples []Sample
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		idx := strings.Index(line, " [typing] ")
		if idx < 0 {
			continue
		}
		stamp, err := time.ParseInLocation("2006/01/02 15:04:05.000", line[:idx], time.Local)
		if err != nil || (!since.IsZero() && stamp.Before(since)) || (!until.IsZero() && stamp.After(until)) {
			continue
		}
		fields := make(map[string]string)
		for _, word := range strings.Fields(line[idx+len(" [typing] "):]) {
			if key, value, ok := strings.Cut(word, "="); ok {
				fields[key] = value
			}
		}
		if fields["pane"] == "" {
			continue
		}
		s := Sample{Pane: fields["pane"], NoEcho: fields["outcome"] == "no-echo", Values: make(map[string]float64)}
		valid := true
		if !s.NoEcho {
			for _, metric := range metrics {
				v, err := strconv.ParseFloat(fields[metric], 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
					valid = false
					break
				}
				s.Values[metric] = v
			}
			if fields["overlap"] != "0" && fields["overlap"] != "1" {
				valid = false
			}
		}
		if !valid {
			continue
		}
		s.Quiet = s.Values["quiet_ms"] >= 50 && fields["overlap"] == "0"
		samples = append(samples, s)
	}
	return samples, scanner.Err()
}

// Percentile uses the nearest-rank definition. An empty population returns zero.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := slices.Clone(values)
	slices.Sort(v)
	return v[max(0, min(len(v)-1, int(math.Ceil(p*float64(len(v))))-1))]
}

// Report prints all and quiet populations separately, without pooling panes.
func Report(samples []Sample) string {
	panes := make(map[string][]Sample)
	for _, s := range samples {
		panes[s.Pane] = append(panes[s.Pane], s)
	}
	names := make([]string, 0, len(panes))
	for name := range panes {
		names = append(names, name)
	}
	slices.Sort(names)
	var out strings.Builder
	out.WriteString("pane population metric n p50 p90 p99 max\n")
	for _, name := range names {
		noEcho := 0
		for _, s := range panes[name] {
			if s.NoEcho {
				noEcho++
			}
		}
		fmt.Fprintf(&out, "%s no-echo=%d\n", name, noEcho)
		for _, population := range []string{"all", "quiet"} {
			for _, metric := range metrics {
				var values []float64
				for _, s := range panes[name] {
					if !s.NoEcho && (population == "all" || s.Quiet) {
						values = append(values, s.Values[metric])
					}
				}
				fmt.Fprintf(&out, "%s %s %s %d %.3f %.3f %.3f %.3f\n", name, population, metric, len(values), Percentile(values, .5), Percentile(values, .9), Percentile(values, .99), Percentile(values, 1))
			}
		}
	}
	return out.String()
}

// MainStalled restricts matches to goroutine 1, the tview event loop.
func MainStalled(dump string) bool {
	for _, stack := range strings.Split(dump, "\n\n") {
		if strings.HasPrefix(stack, "goroutine 1 [") {
			return strings.Contains(stack, ".onTick") || strings.Contains(stack, ".refreshTasksWithIDs")
		}
	}
	return false
}

// StallRuns accumulates contiguous positive observations. Dump sampling adds
// overhead and boundaries are uncertain by one polling interval.
type StallRuns struct {
	Runs  []float64
	start time.Time
}

// Observe records one timestamped sample.
func (s *StallRuns) Observe(at time.Time, stalled bool) {
	if stalled && s.start.IsZero() {
		s.start = at
	}
	if !stalled {
		s.Finish(at)
	}
}

// Finish closes a run still active at the end of the capture.
func (s *StallRuns) Finish(at time.Time) {
	if !s.start.IsZero() {
		s.Runs = append(s.Runs, float64(at.Sub(s.start))/float64(time.Millisecond))
		s.start = time.Time{}
	}
}

// Report summarizes stalls over the actual elapsed window.
func (s *StallRuns) Report(elapsed time.Duration) string {
	var total float64
	for _, run := range s.Runs {
		total += run
	}
	pct := float64(0)
	if elapsed > 0 {
		pct = total / (float64(elapsed) / float64(time.Millisecond)) * 100
	}
	return fmt.Sprintf("count=%d median_ms=%.3f p90_ms=%.3f max_ms=%.3f stalled_pct=%.2f", len(s.Runs), Percentile(s.Runs, .5), Percentile(s.Runs, .9), Percentile(s.Runs, 1), pct)
}
