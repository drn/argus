package main

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/buildid"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/doctor"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/review"
)

// runDoctor implements `argus doctor`: a strictly READ-ONLY diagnostic that
// enumerates every argus binary on disk and in every live process, prints a
// table of their identities, and renders a verdict distinguishing a coherent
// install from restart-needed (same file, older bytes) and path-divergence (the
// daemon symlink and $PATH argus resolve to different files — a restart loops).
//
// It NEVER mutates a symlink, binary, PATH, or process: it only reads files,
// resolves symlinks, shells out to `go env`, and dials the daemon's socket for a
// read-only BootInfo (Connect, never AutoStart — auto-start would spawn a
// daemon). Every gather step is best-effort; a failure degrades that one row to
// "unknown" rather than aborting the command. Exits 0 when healthy, 1 otherwise.
func runDoctor() {
	actors := gatherActors()
	fmt.Print(doctor.Render(actors))
	fmt.Print(doctor.RenderStopHook(gatherStopHookStatus()))
	fmt.Print(doctor.RenderProfileLibrary(gatherProfileLibraryStatus()))
	fmt.Print(doctor.RenderSecretsBootstrap(gatherSecretsBootstrapStatus()))
	fmt.Print(doctor.RenderDevStackOrphans(gatherDevStackOrphans()))
	if doctor.Diagnose(actors).Verdict != doctor.Healthy {
		os.Exit(1)
	}
}

// gatherActors resolves all six binary identities best-effort. The order here is
// the display order in the table.
func gatherActors() []doctor.Actor {
	daemonA, supA := gatherProcesses()
	return []doctor.Actor{
		gatherDisk(doctor.RolePathArgus, pathArgus()),
		gatherDisk(doctor.RoleArgusdTarget, filepath.Join(db.DataDir(), "argusd")),
		gatherDisk(doctor.RoleGoInstall, goInstallTarget()),
		daemonA,
		supA,
		gatherTUI(),
	}
}

// gatherTUI reads this process's own identity: resolved executable path, content
// hash, and VCS build info via the Stage-2 buildid helper.
func gatherTUI() doctor.Actor {
	a := doctor.Actor{Role: doctor.RoleTUI}
	exe, err := os.Executable()
	if err != nil {
		a.Note = "os.Executable failed"
		return a
	}
	if r, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = r
	}
	a.ResolvedPath = exe
	h, err := daemon.BinaryHashFile(exe)
	if err != nil {
		a.Note = "hash failed"
		return a
	}
	a.Hash = h
	a.VCS = buildid.Current()
	a.Resolved = true
	return a
}

// gatherDisk resolves an on-disk binary location: symlink-resolve the path, hash
// the file, and read its VCS build info. An empty or unresolvable path degrades
// to an unknown row.
func gatherDisk(role doctor.Role, path string) doctor.Actor {
	a := doctor.Actor{Role: role}
	if path == "" {
		a.Note = "not found"
		return a
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		a.Note = "not found"
		return a
	}
	a.ResolvedPath = resolved
	h, err := daemon.BinaryHashFile(resolved)
	if err != nil {
		a.Note = "hash failed"
		return a
	}
	a.Hash = h
	a.VCS = vcsFromFile(resolved)
	a.Resolved = true
	return a
}

// gatherProcesses dials the daemon (read-only, never auto-starting one) and
// pulls the daemon's and the relayed supervisor's identity out of BootInfo. A
// missing daemon leaves both rows unknown without failing. A pre-v3 supervisor
// (empty hash) is reported present-but-unknown, never stale.
func gatherProcesses() (daemonA, supA doctor.Actor) {
	daemonA = doctor.Actor{Role: doctor.RoleDaemon, Note: "no daemon"}
	supA = doctor.Actor{Role: doctor.RoleSupervisor, Note: "no daemon"}

	c, err := dclient.Connect(daemon.DefaultSocketPath())
	if err != nil {
		return daemonA, supA
	}
	defer c.Close() //nolint:errcheck // read-only diagnostic

	info, err := c.BootInfo()
	if err != nil {
		daemonA.Note = "BootInfo failed"
		supA.Note = "BootInfo failed"
		return daemonA, supA
	}

	// Daemon: BinaryPath is already boot-resolved; re-resolve defensively so its
	// path compares apples-to-apples against the disk rows. An empty hash means a
	// pre-BinaryHash daemon — present but unknown for the verdict.
	dp := resolvePath(info.BinaryPath)
	if info.BinaryHash == "" {
		daemonA = doctor.Actor{Role: doctor.RoleDaemon, ResolvedPath: dp, Resolved: false, Note: "old daemon (hash unknown)"}
	} else {
		daemonA = doctor.Actor{Role: doctor.RoleDaemon, ResolvedPath: dp, Hash: info.BinaryHash, VCS: info.VCS, Resolved: true}
	}

	// Supervisor: relayed via BootInfo. Not present ⇒ in-process runner. Present
	// but empty hash ⇒ old protocol, unknown (never stale).
	switch {
	case !info.SupervisorPresent:
		supA = doctor.Actor{Role: doctor.RoleSupervisor, Resolved: false, Note: "no supervisor (in-process runner)"}
	case info.SupervisorHash == "":
		supA = doctor.Actor{Role: doctor.RoleSupervisor, ResolvedPath: resolvePath(info.SupervisorPath), Resolved: false, Note: "old protocol (hash unknown)"}
	default:
		supA = doctor.Actor{Role: doctor.RoleSupervisor, ResolvedPath: resolvePath(info.SupervisorPath), Hash: info.SupervisorHash, VCS: info.SupervisorVCS, Resolved: true}
	}
	return daemonA, supA
}

// pathArgus resolves the `argus` binary on $PATH. Empty when not found.
func pathArgus() string {
	p, err := exec.LookPath("argus")
	if err != nil {
		return ""
	}
	return p
}

// goInstallTarget returns $(go env GOPATH)/bin/argus, or "" when `go` is absent
// or GOPATH cannot be determined. Best-effort — a failure just yields an unknown
// row.
func goInstallTarget() string {
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	gopath := string(trimNewline(out))
	if gopath == "" {
		return ""
	}
	return filepath.Join(gopath, "bin", "argus")
}

// resolvePath symlink-resolves p best-effort, returning p unchanged on failure.
func resolvePath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// vcsFromFile reads an on-disk binary's VCS build info (commit SHA + dirty flag)
// via debug/buildinfo. Display-only; a blank result (binary built outside a git
// tree, or unreadable) is fine — the content hash remains the gating signal.
func vcsFromFile(path string) buildid.VCS {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return buildid.VCS{}
	}
	var v buildid.VCS
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			v.Revision = s.Value
		case "vcs.modified":
			v.Modified = s.Value == "true"
		}
	}
	return v
}

// trimNewline drops a single trailing newline (and CR) from `go env` output.
func trimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// gatherStopHookStatus reads the user's global Claude Code settings and
// classifies whether `argus coord-hook` is registered as a Stop hook
// (detect-missing-coord-hook). Independent of the binary-coherence actors
// above — never affects runDoctor's exit code.
func gatherStopHookStatus() doctor.StopHookStatus {
	cmds, err := readStopHookCommands(claudeSettingsPath())
	return doctor.DiagnoseStopHook(cmds, err)
}

// claudeSettingsPath resolves ~/.claude/settings.json. Empty when the home
// directory can't be determined, which readStopHookCommands then reports as
// a read error (degrading the status to unknown, never a false negative).
func claudeSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// gatherProfileLibraryStatus classifies the per-user diligence-profile
// library (~/.argus/profiles/) — global library only; no repo-local
// .argus/profiles/ and no per-project binding, both out of scope for this
// check (add-doctor-profile-check).
func gatherProfileLibraryStatus() doctor.ProfileLibraryStatus {
	cfg := config.DefaultConfig()
	cfg = config.NewFileLoader(filepath.Join(db.DataDir(), config.FileName)).Apply(cfg)
	return diagnoseProfileLibraryAt(filepath.Join(db.DataDir(), "profiles"), cfg)
}

// diagnoseProfileLibraryAt lists dir and validates every *.toml profile file
// found in it against cfg, classifying the result via
// doctor.DiagnoseProfileLibrary. Takes an explicit dir/cfg (rather than
// resolving $HOME itself) so it's testable against a temp library without
// touching the real ~/.argus/profiles/ or config.toml.
func diagnoseProfileLibraryAt(dir string, cfg config.Config) doctor.ProfileLibraryStatus {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return doctor.DiagnoseProfileLibrary(nil, os.IsNotExist(err), err)
	}

	loader := &profiles.Loader{LibraryDir: dir}
	var validNames []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".toml")
		if p, errs := loader.ValidateName(name, cfg, agent.KnownModels, review.NewValidator(cfg)); p != nil && len(errs) == 0 {
			validNames = append(validNames, name)
		}
	}
	return doctor.DiagnoseProfileLibrary(validNames, false, nil)
}

// gatherSecretsBootstrapStatus loads config.toml from disk and classifies
// the [secrets.op] bootstrap-source resolution tri-state
// (add-secrets-resolver-registry) — independent of the binary-coherence
// actors above; never affects runDoctor's exit code.
func gatherSecretsBootstrapStatus() doctor.SecretsBootstrapStatus {
	cfg := config.DefaultConfig()
	cfg = config.NewFileLoader(filepath.Join(db.DataDir(), config.FileName)).Apply(cfg)
	return secretsBootstrapStatusFor(cfg)
}

// secretsBootstrapStatusFor classifies cfg's [secrets.op] bootstrap-source
// resolution status via a single resolve-and-discard through the Stage 3
// registry tri-state query (agent.QueryOpBootstrapStatus), translated into
// doctor's own SecretsBootstrapStatus via doctor.DiagnoseSecretsBootstrap.
// Takes an explicit cfg (rather than resolving $HOME/config.toml itself) so
// it's testable without touching the real ~/.argus/config.toml — mirrors
// diagnoseProfileLibraryAt's explicit-input testability pattern.
func secretsBootstrapStatusFor(cfg config.Config) doctor.SecretsBootstrapStatus {
	switch agent.QueryOpBootstrapStatus(cfg.Secrets) {
	case agent.OpBootstrapResolved:
		return doctor.DiagnoseSecretsBootstrap(true, true)
	case agent.OpBootstrapNotResolved:
		return doctor.DiagnoseSecretsBootstrap(true, false)
	default:
		return doctor.DiagnoseSecretsBootstrap(false, false)
	}
}

// gatherDevStackOrphans scans the process table for dev-stack processes
// (mysqld/redis-server/postgres/caddy/process-compose) and classifies any
// whose embedded worktree path no longer exists on disk. Read-only: never
// signals or otherwise mutates a reported process.
func gatherDevStackOrphans() (doctor.DevStackOrphanStatus, []doctor.DevStackOrphan) {
	procs, err := agent.ScanDevStackProcesses()
	return diagnoseDevStackOrphansFrom(procs, err, agent.DirExists)
}

// diagnoseDevStackOrphansFrom classifies already-scanned dev-stack processes
// against an injectable existence check (rather than calling os.Stat itself),
// so it's testable without touching the real process table or filesystem.
func diagnoseDevStackOrphansFrom(procs []agent.DevStackProc, scanErr error, exists func(string) bool) (doctor.DevStackOrphanStatus, []doctor.DevStackOrphan) {
	if scanErr != nil {
		return doctor.DiagnoseDevStackOrphans(nil, scanErr)
	}
	var orphans []doctor.DevStackOrphan
	for _, p := range procs {
		if !exists(p.WorktreePath) {
			orphans = append(orphans, doctor.DevStackOrphan{PID: p.PID, Name: p.Name, WorktreePath: p.WorktreePath})
		}
	}
	return doctor.DiagnoseDevStackOrphans(orphans, nil)
}

// readStopHookCommands reads and parses the Claude Code settings file at
// path, returning every hooks.Stop[].hooks[].command entry. Takes an
// explicit path (rather than resolving $HOME itself) so it's testable
// against a temp file without touching the real ~/.claude/settings.json.
func readStopHookCommands(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Hooks struct {
			Stop []struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"Stop"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	var cmds []string
	for _, group := range parsed.Hooks.Stop {
		for _, h := range group.Hooks {
			cmds = append(cmds, h.Command)
		}
	}
	return cmds, nil
}
