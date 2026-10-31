// Package wispr drives the Wispr Flow dictation app: it starts and stops
// recording through its URL scheme and reads finished transcripts from its
// local history database. Both interfaces are undocumented (v1.6.1034).
package wispr

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

const (
	startURL = "wispr-flow://start-hands-free"
	stopURL  = "wispr-flow://stop-hands-free"

	defaultPoll = 250 * time.Millisecond
)

// Driver controls Wispr Flow. The zero value is usable apart from DBPath.
type Driver struct {
	DBPath string
	Open   func(ctx context.Context, url string) error
	Poll   time.Duration
}

// DefaultDBPath returns Wispr Flow's history database location.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support", "Wispr Flow", "flow.sqlite")
}

func (d *Driver) StartRecording(ctx context.Context) error {
	return d.open(ctx, startURL)
}

func (d *Driver) StopRecording(ctx context.Context) error {
	return d.open(ctx, stopURL)
}

func (d *Driver) open(ctx context.Context, url string) error {
	fn := d.Open
	if fn == nil {
		fn = openURL
	}
	if err := fn(ctx, url); err != nil {
		uxlog.Log("[wispr] open %s failed: %v", url, err)
		return fmt.Errorf("wispr: open %s: %w", url, err)
	}
	uxlog.Log("[wispr] opened %s", url)
	return nil
}

func openURL(ctx context.Context, url string) error {
	if out, err := exec.CommandContext(ctx, "open", "-g", url).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func (d *Driver) poll() time.Duration {
	if d.Poll > 0 {
		return d.Poll
	}
	return defaultPoll
}

func (d *Driver) dbPath() string {
	if d.DBPath != "" {
		return d.DBPath
	}
	return DefaultDBPath()
}
