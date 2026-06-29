package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/profiles"
)

// runValidateCommand handles: argus validate <profile-name>
//
// It loads, resolves, and validates a diligence profile from the in-repo
// .argus/profiles/ directory (relative to the current working directory, taking
// precedence) and the per-user ~/.argus/profiles/ library, reporting every
// conformance error or confirming the profile is valid. This affordance is
// operator/documentation tooling only — it is NOT wired into the Go build, CI,
// or any Make gate (specs and profiles are local docs).
func runValidateCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: argus validate <profile-name>")
		os.Exit(2)
	}
	name := args[0]

	cwd, _ := os.Getwd()
	loader := &profiles.Loader{
		RepoDir:    filepath.Join(cwd, ".argus", "profiles"),
		LibraryDir: filepath.Join(db.DataDir(), "profiles"),
	}

	cfg := config.DefaultConfig()
	cfg = config.NewFileLoader(filepath.Join(db.DataDir(), config.FileName)).Apply(cfg)

	os.Exit(runValidate(os.Stdout, loader, cfg, name))
}

// runValidate is the pure, testable core of the validate subcommand: it resolves
// and validates the named profile, writes a human-readable report to w, and
// returns the process exit code (0 = valid, 1 = not found / invalid).
func runValidate(w io.Writer, loader *profiles.Loader, cfg config.Config, name string) int {
	p, errs := loader.ValidateName(name, cfg, agent.KnownModels)
	if p == nil {
		// Resolution failed (not found, or extends cycle); errs holds the cause.
		for _, e := range errs {
			fmt.Fprintf(w, "profile %q: %v\n", name, e)
		}
		return 1
	}
	if len(errs) == 0 {
		fmt.Fprintf(w, "profile %q is valid (source: %s)\n", name, p.Source)
		return 0
	}
	fmt.Fprintf(w, "profile %q has %d error(s) (source: %s):\n", name, len(errs), p.Source)
	for _, e := range errs {
		fmt.Fprintf(w, "  - %v\n", e)
	}
	return 1
}
