// Command docsize enforces line-count caps on the always-loaded context docs
// so they don't silently regrow. CLAUDE.md is pulled into every agent session
// that works in this repo, so an unbounded doc is an unbounded per-session
// context tax. Run via `make docs-check`; wired into `make pre-pr` and CI.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// guard caps one doc's line count. Raising a cap is a deliberate one-line edit
// here — that is the point: growth must be a conscious decision, not drift.
// Keep docs dense and push detail into context/knowledge/gotchas/* instead.
type guard struct {
	path string
	max  int
}

var guards = []guard{
	{path: "CLAUDE.md", max: 150},
}

type result struct {
	guard
	lines int
	err   error
}

func (r result) over() bool { return r.err == nil && r.lines > r.max }

func countLines(r io.Reader) int {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		n++
	}
	return n
}

// check evaluates every guard, returning per-doc results and an overall ok
// (false if any doc is over cap or unreadable).
func check(guards []guard) (results []result, ok bool) {
	ok = true
	for _, g := range guards {
		res := result{guard: g}
		f, err := os.Open(g.path)
		if err != nil {
			res.err = err
			ok = false
		} else {
			res.lines = countLines(f)
			f.Close()
			if res.over() {
				ok = false
			}
		}
		results = append(results, res)
	}
	return results, ok
}

func report(w io.Writer, results []result) {
	for _, r := range results {
		switch {
		case r.err != nil:
			fmt.Fprintf(w, "%-28s    ERR  %v\n", r.path, r.err)
		case r.over():
			fmt.Fprintf(w, "%-28s %4d / %4d  OVER\n", r.path, r.lines, r.max)
		default:
			fmt.Fprintf(w, "%-28s %4d / %4d  ok\n", r.path, r.lines, r.max)
		}
	}
}

func main() {
	results, ok := check(guards)
	report(os.Stdout, results)
	if !ok {
		fmt.Fprintln(os.Stderr, "\ndocsize: a guarded doc exceeds its line cap.")
		fmt.Fprintln(os.Stderr, "Trim it — move detail into context/knowledge/gotchas/* —")
		fmt.Fprintln(os.Stderr, "or consciously raise the cap in scripts/docsize/main.go.")
		os.Exit(1)
	}
}
