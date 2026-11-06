// argus-latency reports the opt-in TUI timing log and samples UI stalls.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/drn/argus/internal/latencyreport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: argus-latency report|stalls [flags]")
	}
	switch args[0] {
	case "report":
		fs := flag.NewFlagSet("report", flag.ContinueOnError)
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path := fs.String("log", filepath.Join(home, ".argus", "ux.log"), "UX log path")
		sinceArg := fs.String("since", "", "inclusive RFC3339 start")
		untilArg := fs.String("until", "", "inclusive RFC3339 end")
		last := fs.Duration("last", 0, "look back from now (exclusive with --since)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *last < 0 || (*last > 0 && *sinceArg != "") {
			return fmt.Errorf("use a positive --last or --since")
		}
		var since, until time.Time
		if *sinceArg != "" {
			since, err = time.Parse(time.RFC3339, *sinceArg)
			if err != nil {
				return err
			}
		}
		if *untilArg != "" {
			until, err = time.Parse(time.RFC3339, *untilArg)
			if err != nil {
				return err
			}
		}
		if *last > 0 {
			since = time.Now().Add(-*last)
		}
		if !since.IsZero() && !until.IsZero() && until.Before(since) {
			return fmt.Errorf("--until precedes --since")
		}
		f, err := os.Open(*path)
		if err != nil {
			return err
		}
		defer f.Close()
		samples, err := latencyreport.Parse(f, since, until)
		if err != nil {
			return err
		}
		fmt.Print(latencyreport.Report(samples))
		return nil
	case "stalls":
		fs := flag.NewFlagSet("stalls", flag.ContinueOnError)
		addr := fs.String("addr", "http://127.0.0.1:6061", "TUI pprof URL")
		duration := fs.Duration("duration", 30*time.Second, "capture duration")
		interval := fs.Duration("interval", 5*time.Millisecond, "poll interval")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *duration <= 0 || *interval <= 0 {
			return fmt.Errorf("duration and interval must be positive")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *duration)
		defer cancel()
		report, err := latencyreport.Capture(ctx, &http.Client{Timeout: 5 * time.Second}, *addr, *interval)
		if err != nil {
			return err
		}
		fmt.Println(report)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
