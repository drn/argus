package latencyreport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Capture samples the main goroutine until the caller's deadline. It never
// addresses the daemon: addr must be the TUI's pprof listener.
func Capture(ctx context.Context, client *http.Client, addr string, interval time.Duration) (string, error) {
	if interval <= 0 {
		return "", fmt.Errorf("interval must be positive")
	}
	start := time.Now()
	var runs StallRuns
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/debug/pprof/goroutine?debug=2", nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return "", err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
		_ = resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("pprof status %s", resp.Status)
		}
		if len(body) > 16*1024*1024 {
			return "", fmt.Errorf("goroutine dump exceeds 16 MiB")
		}
		runs.Observe(time.Now(), MainStalled(string(body)))
		select {
		case <-ctx.Done():
			runs.Finish(time.Now())
			return runs.Report(time.Since(start)), nil
		case <-ticker.C:
		}
	}
	runs.Finish(time.Now())
	return runs.Report(time.Since(start)), nil
}
