// Command coverfilter strips ignored files from a Go coverage profile and
// prints the recomputed total to stdout. The filtered profile is written to
// the path given by -out (default: stdout).
//
// Usage:
//
//	coverfilter -in coverage.out -out coverage.filtered.out -ignore coverage-ignore.txt
//
// Exit codes:
//
//	0 — success
//	1 — argument or I/O error
//	2 — filtered total fell below -min (when -min is set)
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable body of main. Returns a Unix-style exit code:
// 0 success, 1 argument or I/O error, 2 coverage gate breached.
//
// The flags are parsed from args (no os.Args reads), and stdout/stderr are
// taken as parameters so tests can capture them. main wires up the real
// process streams and exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("coverfilter", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "coverage.out", "input coverage profile")
	out := fs.String("out", "", "output filtered profile (default: stdout); empty also disables write")
	ignore := fs.String("ignore", "coverage-ignore.txt", "path to ignore-pattern file")
	minPct := fs.Float64("min", 0, "fail (exit 2) if filtered coverage is below this percentage")
	quiet := fs.Bool("quiet", false, "suppress the human-readable summary line on stderr")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	patterns, err := readIgnore(*ignore)
	if err != nil {
		fmt.Fprintf(stderr, "coverfilter: read ignore: %v\n", err)
		return 1
	}

	inFile, err := os.Open(*in)
	if err != nil {
		fmt.Fprintf(stderr, "coverfilter: open input: %v\n", err)
		return 1
	}
	defer inFile.Close()

	var w io.Writer = io.Discard
	var outFile *os.File
	switch *out {
	case "":
		// no-op writer
	case "-":
		w = stdout
	default:
		outFile, err = os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "coverfilter: create output: %v\n", err)
			return 1
		}
		defer outFile.Close()
		w = outFile
	}

	stats, err := filter(inFile, w, patterns)
	if err != nil {
		fmt.Fprintf(stderr, "coverfilter: filter: %v\n", err)
		return 1
	}

	pct := stats.percent()
	if !*quiet {
		fmt.Fprintf(stderr, "coverage: %.1f%% of statements (%d/%d covered, %d filtered files)\n",
			pct, stats.covered, stats.total, stats.filteredFiles)
	}
	// Always emit the percentage on stdout so CI can capture it cleanly even
	// when -out=- is also writing the profile to stdout. When -out=- we
	// suppress the pct line on stdout and require callers to read it from
	// stderr instead.
	if *out != "-" {
		fmt.Fprintf(stdout, "%.1f\n", pct)
	}

	if *minPct > 0 && pct < *minPct {
		fmt.Fprintf(stderr, "coverage gate FAILED: %.1f%% < %.1f%% floor\n", pct, *minPct)
		return 2
	}
	return 0
}

// stats summarizes a filtered profile.
type stats struct {
	total, covered int
	filteredFiles  int
}

func (s stats) percent() float64 {
	if s.total == 0 {
		return 0
	}
	return 100.0 * float64(s.covered) / float64(s.total)
}

// filter copies r to w, dropping lines whose file portion matches any pattern,
// and returns aggregate statement counts for the surviving lines.
//
// A coverage profile line looks like:
//
//	github.com/drn/argus/internal/api/server.go:10.20,12.30 4 1
//
// where the trailing two integers are statement count and execution count.
func filter(r io.Reader, w io.Writer, patterns []string) (stats, error) {
	br := bufio.NewReader(r)
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	var s stats
	dropped := map[string]struct{}{}
	first := true
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			keep := true
			if first {
				// First line is always "mode: ..." — keep verbatim.
				first = false
			} else {
				file := fileOf(line)
				if file != "" {
					for _, p := range patterns {
						if strings.Contains(file, p) {
							keep = false
							dropped[p] = struct{}{}
							break
						}
					}
				}
				if keep {
					n, c, ok := parseCounts(line)
					if ok {
						s.total += n
						if c > 0 {
							s.covered += n
						}
					}
				}
			}
			if keep {
				if _, werr := bw.WriteString(line); werr != nil {
					return s, werr
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return s, err
		}
	}
	s.filteredFiles = len(dropped)
	return s, nil
}

// fileOf returns the path segment before the first ':' on a coverage line,
// or "" if the line does not look like a coverage entry.
func fileOf(line string) string {
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return ""
	}
	return line[:i]
}

// parseCounts returns (statementCount, executionCount, ok) for a coverage
// line. ok is false if the line does not have the expected three trailing
// fields (e.g. blank lines, mode header).
func parseCounts(line string) (int, int, bool) {
	line = strings.TrimRight(line, "\n")
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, 0, false
	}
	n, err1 := strconv.Atoi(fields[len(fields)-2])
	c, err2 := strconv.Atoi(fields[len(fields)-1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return n, c, true
}

// readIgnore parses an ignore file. Blank lines and lines starting with '#'
// are skipped; each remaining line is returned with surrounding whitespace
// trimmed. A missing file is not an error — it returns nil and lets the
// caller proceed with no exclusions.
func readIgnore(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

