package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
)

const voiceUsage = "usage: argus voice <start|stop|status>"

const inputMonitoringHelp = "open System Settings > Privacy & Security > Input Monitoring, enable argus, then run `argus daemon restart`"

type voiceClient interface {
	VoiceStart() error
	VoiceStop() error
	VoiceStatus() (daemon.VoiceStatusResp, error)
	Close() error
}

var dialVoice = func() (voiceClient, error) {
	c, err := dclient.Connect(daemon.DefaultSocketPath())
	if err != nil {
		return nil, err
	}
	return c, nil
}

// runVoiceCommand handles: argus voice start|stop|status
func runVoiceCommand(args []string) {
	os.Exit(runVoice(args, os.Stdout, os.Stderr, time.Now()))
}

func runVoice(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, voiceUsage)
		return 1
	}
	sub := args[0]
	if sub != "start" && sub != "stop" && sub != "status" {
		_, _ = fmt.Fprintf(stderr, "unknown voice subcommand: %s\n%s\n", sub, voiceUsage)
		return 1
	}

	c, err := dialVoice()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "argus voice: daemon not reachable (%v); start it with `argus daemon start`\n", err)
		return 1
	}
	defer c.Close() //nolint:errcheck // short-lived CLI client; close error is non-actionable

	switch sub {
	case "start":
		if err := c.VoiceStart(); err != nil {
			_, _ = fmt.Fprintf(stderr, "argus voice start: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Recording with Wispr Flow. Run `argus voice stop` to finish.")
	case "stop":
		if err := c.VoiceStop(); err != nil {
			_, _ = fmt.Fprintf(stderr, "argus voice stop: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, "Stopped. The task launches in the background; a notification confirms it.")
	case "status":
		st, err := c.VoiceStatus()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "argus voice status: %v\n", err)
			return 1
		}
		printVoiceStatus(stdout, st, now)
	}
	return 0
}

func printVoiceStatus(w io.Writer, st daemon.VoiceStatusResp, now time.Time) {
	if !st.Available {
		_, _ = fmt.Fprintln(w, "Voice capture is not running in the daemon; restart it with `argus daemon restart`.")
		return
	}
	row := func(label, value string) { _, _ = fmt.Fprintf(w, "%-18s %s\n", label+":", value) }

	switch {
	case !st.HotkeyEnabled:
		row("Hotkey", st.Hotkey+" (disabled; set [voice] enabled = true in ~/.argus/config.toml, then `argus daemon restart`)")
	case st.TapActive:
		row("Hotkey", st.Hotkey+" (active)")
	default:
		row("Hotkey", st.Hotkey+" (enabled but not listening; see ~/.argus/ux.log)")
	}

	if st.InputMonitoringGranted {
		row("Input Monitoring", "granted")
	} else {
		row("Input Monitoring", "not granted; "+inputMonitoringHelp)
	}

	if st.Recording {
		row("Recording", fmt.Sprintf("yes, for %s", now.Sub(st.StartedAt).Round(time.Second)))
	} else {
		row("Recording", "no")
	}
	if st.Processing > 0 {
		row("Processing", fmt.Sprintf("%d transcript(s)", st.Processing))
	}

	if st.LastResult == "" {
		row("Last result", "none yet")
		return
	}
	last := st.LastResult
	if st.LastDetail != "" {
		last += ": " + st.LastDetail
	}
	if st.LastTaskID != "" {
		last += " [task " + st.LastTaskID + "]"
	}
	if !st.LastAt.IsZero() {
		last += fmt.Sprintf(" (%s ago)", now.Sub(st.LastAt).Round(time.Second))
	}
	row("Last result", last)
}
