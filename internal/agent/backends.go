package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/drn/argus/internal/backendkind"
	"github.com/drn/argus/internal/config"
	injectantigravity "github.com/drn/argus/internal/inject/antigravity"
	injectcursor "github.com/drn/argus/internal/inject/cursor"
	injectopencode "github.com/drn/argus/internal/inject/opencode"
	"github.com/drn/argus/internal/mcplimits"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/routing"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/uxlog"
)

// ModelDelivery is how a family receives the resolved model at launch.
type ModelDelivery int

const (
	// ModelNone injects nothing; the CLI uses its own default.
	ModelNone ModelDelivery = iota
	// ModelFlag appends `--model <id>`.
	ModelFlag
	// ModelInlineConfig merges the model into OPENCODE_CONFIG_CONTENT.
	ModelInlineConfig
)

// SkillDelivery is how a family discovers Argus's builtin skills.
type SkillDelivery int

const (
	SkillsNone SkillDelivery = iota
	// SkillsAddDir appends `--add-dir <workspace root>` (Claude loads
	// .claude/skills from an added dir).
	SkillsAddDir
	// SkillsFlag appends `--skill <skills dir>` (Pi).
	SkillsFlag
	// SkillsInlineConfig merges the skills dir into OPENCODE_CONFIG_CONTENT.
	SkillsInlineConfig
	// SkillsCodexHome points CODEX_HOME at an Argus-only overlay.
	SkillsCodexHome
	// SkillsGlobal materializes the skills dir per launch; the family's
	// InjectGlobal registers it in the CLI's global config at daemon startup.
	SkillsGlobal
)

// RoutingDelivery is how a family receives Argus's builtin routing content.
type RoutingDelivery int

const (
	RoutingNone RoutingDelivery = iota
	// RoutingSystemPromptFile appends `--append-system-prompt-file <path>`.
	RoutingSystemPromptFile
	// RoutingAppendFlag appends `--append-system-prompt <path>` (Pi).
	RoutingAppendFlag
	// RoutingInlineConfig adds the materialized file to the child-only inline
	// config's `instructions` array (opencode).
	RoutingInlineConfig
	// RoutingCodexHome is delivered as the Argus-only CODEX_HOME overlay's
	// AGENTS.md, written alongside the overlay's skills (Codex).
	RoutingCodexHome
	// RoutingGlobalRules is a rules file in the CLI's global config, written
	// by the family's InjectGlobal (Antigravity).
	RoutingGlobalRules
)

// SandboxGrant is a HOME-relative SBPL write grant a family needs to persist
// its own auth and session state.
type SandboxGrant struct {
	// HomeRel is appended to $HOME, e.g. "/.gemini".
	HomeRel string
	// Prefix emits a (prefix ...) rule instead of (subpath ...), for files
	// written via atomic-rename siblings.
	Prefix bool
	// Comment is emitted above the rule in the generated profile.
	Comment string
}

// Family declares everything Argus does differently per backend CLI. Each
// supported CLI has exactly one entry in families; unrecognized commands use
// customFamily. See .agents/skills/add-backend/SKILL.md for what each field
// means when adding a backend.
type Family struct {
	backendkind.Spec

	// PinsSessionID: the CLI accepts a start-time `--session-id <id>`, so the
	// task's ID is minted before launch. False means capture-style: the ID is
	// recovered post-exit via Capture.
	PinsSessionID bool
	// ResumeFlag is appended as `<flag> <id>` on resume when the task has an
	// ID. Ignored when ResumeCmd is set.
	ResumeFlag string
	// ResumeCmd, when set, replaces the whole command on resume (Codex's
	// `codex resume <id>` subcommand shape).
	ResumeCmd func(mcpFlag, modelFlag, sessionID string) string

	// Capture recovers the session ID after exit; nil captures nothing.
	Capture func(task *model.Task, cfg config.Config) (string, error)
	// ResumeAvailable checks the exact recorded conversation before killing or
	// resuming it. Nil means this family has no transcript-availability gate.
	ResumeAvailable func(task *model.Task, cfg config.Config) (bool, error)
	// RecaptureEveryExit: the CLI can switch conversations in-session
	// (Claude /clear, agy /new), so the ID is re-captured on every exit and
	// refreshed at resume. False captures once, while SessionID is empty.
	RecaptureEveryExit bool
	// SessionSwitcher: the Claude session list/switch endpoints apply.
	SessionSwitcher bool
	// BridgeMod: Claude Code mods can be loaded into this CLI's sessions, so
	// BuildCmd appends `--plugin-dir <argus-bridge mod dir>` (when [bridge] is
	// enabled). Mods run only inside Claude Code; every other family keeps
	// screen-reading as its only state source.
	BridgeMod bool

	// KnownModels is the curated model list for pickers; nil means
	// custom-only (any typed id is accepted).
	KnownModels []string
	ModelDelivery
	SkillDelivery
	RoutingDelivery

	// DefaultPromptFlag delivers the prompt when the backend config has no
	// PromptFlag. Empty means `-- <prompt>` (or positional, see below).
	DefaultPromptFlag string
	// PromptPositional passes the prompt without a `--` separator.
	PromptPositional bool

	// LaunchFlag is inserted right after the binary unless already present
	// before any `--` separator (OpenCode's --auto).
	LaunchFlag string
	// MCPFlag returns the per-launch flag wiring Argus's MCP server, or nil
	// when the CLI only reads MCP servers from global config (see InjectGlobal).
	MCPFlag func(url string) string
	// PermissionFlags maps defaults.permission_mode to CLI flags; nil injects
	// nothing. Skipped when the command already contains any
	// PermissionFlagTokens.
	PermissionFlags      func(mode string) string
	PermissionFlagTokens []string

	// Prelaunch runs before the fork (Pi's ollama warmup); nil skips.
	Prelaunch func(ctx context.Context, task *model.Task, backend config.Backend) error
	// StartTimeout overrides the daemon client's Start RPC budget when
	// Prelaunch can legitimately take longer than a normal round trip.
	StartTimeout time.Duration

	// InjectGlobal writes Argus's MCP server (and, for SkillsGlobal, the
	// skills dir) into the CLI's global config — at daemon startup
	// (launching=false) and before each launch of this family
	// (launching=true, which itself proves the CLI is installed). Must be
	// idempotent and preserve unrelated keys and the operator's own
	// enable/disable choice.
	InjectGlobal func(port int, launching bool) error

	// SandboxGrants are the HOME-relative write grants the CLI needs.
	SandboxGrants []SandboxGrant
	// SandboxGrantsLaunchOnly emits SandboxGrants only into this family's own
	// launches rather than every sandboxed task's profile, for grants that
	// would let another backend's agent tamper with config an unsandboxed CLI
	// later trusts (agy's ~/.gemini/config MCP registrations).
	SandboxGrantsLaunchOnly bool
	// PrecreateSandboxGrants creates every non-Prefix grant dir (unsandboxed)
	// before a sandboxed launch, for a CLI whose grant dirs sit under a parent
	// the sandbox can't write (agy's ~/.gemini/...) and may not exist yet.
	PrecreateSandboxGrants bool
}

func mustSpec(k backendkind.Kind) backendkind.Spec {
	s, ok := backendkind.Lookup(k)
	if !ok {
		panic("backendkind: no spec for " + string(k))
	}
	return s
}

func claudeMCPFlag(url string) string {
	mcpConfig, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{
		"argus": map[string]any{"type": "http", "url": url, "timeout": mcplimits.ClientTimeoutMilliseconds},
	}})
	return " --mcp-config " + shellQuote(string(mcpConfig))
}

func codexMCPFlag(url string) string {
	return " -c " + shellQuote("mcp_servers.argus.url="+strconv.Quote(url)) +
		" -c " + shellQuote("mcp_servers.argus.tool_timeout_sec="+strconv.Itoa(mcplimits.ClientTimeoutSeconds))
}

func codexResumeCommand(mcpFlag, modelFlag, sessionID string) string {
	// Flags must precede the positional session-id argument.
	return "codex" + mcpFlag + codexResumeFlags + modelFlag + " " + shellQuote(sessionID)
}

func captureClaude(task *model.Task, cfg config.Config) (string, error) {
	dir, err := ClaudeConfigDirForTask(task, cfg)
	if err != nil {
		return "", err
	}
	return CaptureClaudeSessionIDIn(dir, task.Worktree)
}

func captureCodex(task *model.Task, cfg config.Config) (string, error) {
	home, err := CodexHomeForTask(task, cfg)
	if err != nil {
		return "", err
	}
	return CaptureCodexSessionIDIn(home, task.Worktree)
}

func capturePi(task *model.Task, _ config.Config) (string, error) {
	return CapturePiSessionID(task.Worktree)
}

func captureOpencode(task *model.Task, _ config.Config) (string, error) {
	return CaptureOpencodeSessionID(task.Worktree)
}

func captureAntigravity(task *model.Task, _ config.Config) (string, error) {
	return CaptureAntigravitySessionID(task.Worktree)
}

func captureCursor(task *model.Task, _ config.Config) (string, error) {
	return CaptureCursorSessionID(task.Worktree)
}

// cursorPermissionFlags maps Argus's permission modes onto cursor-agent's
// --force / --plan. Cursor has no accept-edits mode (it uses an allowlist).
func cursorPermissionFlags(mode string) string {
	switch mode {
	case config.PermissionModePlan, config.PermissionModeBypassAllow:
		return "--plan"
	case config.PermissionModeBypassActive:
		return "--force"
	default:
		return ""
	}
}

// cursorInstalledFn reports whether Cursor's CLI is present, so the daemon
// never writes ~/.cursor for a user who doesn't run it. A package var for tests.
var cursorInstalledFn = func() bool {
	for _, bin := range backendkind.SpecOf("cursor-agent").Binaries {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".local", "share", "cursor-agent"))
	return err == nil
}

func injectCursorGlobal(port int, launching bool) error {
	if !launching && !cursorInstalledFn() {
		slog.Info("inject cursor skipped: cursor-agent not installed")
		return nil
	}
	return injectcursor.InjectGlobal(port)
}

// antigravityPermissionFlags maps Argus's permission modes onto agy's
// --mode / --dangerously-skip-permissions. agy has no "plan with bypass
// reachable" mode, so bypass-allow degrades to plan.
func antigravityPermissionFlags(mode string) string {
	switch mode {
	case config.PermissionModeAcceptEdits:
		return "--mode accept-edits"
	case config.PermissionModePlan, config.PermissionModeBypassAllow:
		return "--mode plan"
	case config.PermissionModeBypassActive:
		return "--dangerously-skip-permissions"
	default:
		return ""
	}
}

// injectOpencodeGlobal runs only at daemon startup, as before the registry:
// a per-launch rewrite would add a lost-update race across parallel opencode
// spawns for no gain, since opencode is registered whenever the daemon starts.
func injectOpencodeGlobal(port int, launching bool) error {
	if launching {
		return nil
	}
	if dir, err := skills.ManagedSkillsDir(); err == nil {
		// Older builds put the skills dir in the global config; launches now
		// deliver it child-only via OPENCODE_CONFIG_CONTENT. A cleanup failure
		// must not block MCP registration.
		if rerr := injectopencode.RemoveManagedSkillsGlobal(dir); rerr != nil {
			slog.Error("remove old global opencode skill path", "err", rerr)
		}
	}
	return injectopencode.InjectGlobal(port, "")
}

// antigravityInstalledFn reports whether agy is present: any of the family's
// binaries on PATH, or already initialized (~/.gemini/antigravity-cli exists —
// not bare ~/.gemini, which Gemini CLI also owns). A package var so tests can
// force either answer.
var antigravityInstalledFn = func() bool {
	for _, bin := range backendkind.SpecOf("agy").Binaries {
		if _, err := exec.LookPath(bin); err == nil {
			return true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".gemini", "antigravity-cli"))
	return err == nil
}

// injectGlobalAtSpawnFn re-runs a family's InjectGlobal from BuildCmd so a
// CLI installed (or an MCP port changed) after daemon startup still gets
// Argus's registration on its next launch. Inert under `go test` so command
// construction tests never touch the developer's HOME; tests swap the var.
var injectGlobalAtSpawnFn = func(f *Family, port int) {
	if isTestBinary() {
		return
	}
	if err := f.InjectGlobal(port, true); err != nil {
		uxlog.Log("[%s] global config inject at spawn failed (continuing): %v", f.Kind, err)
	}
}

// injectAntigravityGlobal registers Argus in agy's global config. At daemon
// startup it runs only when agy is installed — the daemon must never write
// ~/.gemini for a user who doesn't run agy; before an agy launch the launch
// itself proves installation (and a launchd daemon's PATH may not show agy).
// The skills dir is materialized first so the registered path exists.
func injectAntigravityGlobal(port int, launching bool) error {
	if !launching && !antigravityInstalledFn() {
		slog.Info("inject antigravity skipped: agy not installed")
		return nil
	}
	dir := ""
	if root, err := ensureSessionSkillsFn(); err != nil {
		slog.Error("materialize builtin skills for antigravity", "err", err)
	} else if root != "" {
		dir = skills.BuiltinSkillsDir(root)
	}
	if dir == "" {
		if d, err := skills.ManagedSkillsDir(); err == nil {
			dir = d
		}
	}
	var routingErr error
	if content, err := routing.BuiltinContent(); err != nil {
		routingErr = err
	} else {
		routingErr = injectantigravity.InjectRouting(content)
	}
	if err := injectantigravity.InjectGlobal(port, dir); err != nil {
		return err
	}
	return routingErr
}

var families = []*Family{
	{
		Spec:               mustSpec(backendkind.Claude),
		PinsSessionID:      true,
		ResumeFlag:         "--resume",
		Capture:            captureClaude,
		ResumeAvailable:    claudeResumeAvailable,
		RecaptureEveryExit: true,
		SessionSwitcher:    true,
		BridgeMod:          true,
		KnownModels:        []string{"opus", "sonnet", "haiku", "fable"},
		ModelDelivery:      ModelFlag,
		SkillDelivery:      SkillsAddDir,
		RoutingDelivery:    RoutingSystemPromptFile,
		MCPFlag:            claudeMCPFlag,
		PermissionFlags:    config.PermissionModeFlags,
		PermissionFlagTokens: []string{
			"--permission-mode", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
		},
		SandboxGrants: []SandboxGrant{
			{HomeRel: "/.claude.json", Prefix: true, Comment: "Claude auth file; prefix so write-file-atomic siblings (~/.claude.json.<rand>) persist OAuth tokens."},
			{HomeRel: "/.claude", Comment: "Claude transcripts, settings, and session state."},
		},
	},
	{
		Spec:      mustSpec(backendkind.Codex),
		Capture:   captureCodex,
		ResumeCmd: codexResumeCommand,
		// Keep this list synced with the Codex CLI's own `/model` picker and
		// the seed diligence profiles' archetype.*.models.codex entries.
		KnownModels:     []string{"gpt-6.1-sol", "gpt-6-sol", "gpt-6-astra", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"},
		ModelDelivery:   ModelFlag,
		SkillDelivery:   SkillsCodexHome,
		RoutingDelivery: RoutingCodexHome,
		MCPFlag:         codexMCPFlag,
		SandboxGrants: []SandboxGrant{
			{HomeRel: "/.codex", Comment: "Codex auth, session DB (state_5.sqlite), history, sessions."},
			{HomeRel: "/.local/share/argus/codex-home", Comment: "Argus-only CODEX_HOME overlay carrying builtin skills."},
		},
	},
	{
		Spec:            mustSpec(backendkind.Pi),
		ResumeFlag:      "--session",
		Capture:         capturePi,
		ModelDelivery:   ModelFlag,
		SkillDelivery:   SkillsFlag,
		RoutingDelivery: RoutingAppendFlag,
		// Pi's argv parser does not honor "--" as end-of-flags.
		PromptPositional: true,
		Prelaunch:        piPrelaunch,
		StartTimeout:     PiStartTimeout,
		SandboxGrants: []SandboxGrant{
			{HomeRel: "/.pi", Comment: "Pi session files (~/.pi/agent/sessions) plus auth/config; capture reads them post-exit."},
		},
	},
	{
		Spec:            mustSpec(backendkind.Opencode),
		ResumeFlag:      "--session",
		Capture:         captureOpencode,
		ModelDelivery:   ModelInlineConfig,
		SkillDelivery:   SkillsInlineConfig,
		RoutingDelivery: RoutingInlineConfig,
		LaunchFlag:      "--auto",
		InjectGlobal:    injectOpencodeGlobal,
		// State lives under ~/.local/share/opencode, already granted by the
		// base profile's ~/.local rule.
	},
	{
		Spec:               mustSpec(backendkind.Antigravity),
		ResumeFlag:         "--conversation",
		Capture:            captureAntigravity,
		RecaptureEveryExit: true,
		ModelDelivery:      ModelFlag,
		SkillDelivery:      SkillsGlobal,
		RoutingDelivery:    RoutingGlobalRules,
		// agy reads prompts only from -p/--print, -i/--prompt-interactive, or
		// stdin; a positional or `--` prompt is a hard error.
		DefaultPromptFlag: "--prompt-interactive",
		PermissionFlags:   antigravityPermissionFlags,
		// "--mode " / "--mode=" (plus a trailing "--mode", see
		// HasPermissionFlag) so the check never trips on --model.
		PermissionFlagTokens: []string{"--dangerously-skip-permissions", "--mode ", "--mode="},
		InjectGlobal:         injectAntigravityGlobal,
		// Not all of ~/.gemini: Gemini CLI keeps its own credentials at its
		// root. These are the dirs agy 1.3.3 writes.
		PrecreateSandboxGrants:  true,
		SandboxGrantsLaunchOnly: true,
		SandboxGrants: []SandboxGrant{
			{HomeRel: "/.gemini/antigravity-cli", Comment: "Antigravity CLI logs, conversation DB, settings, and sign-in state."},
			{HomeRel: "/.gemini/config", Comment: "Antigravity global customizations (mcp_config.json, skills.json, projects)."},
			{HomeRel: "/.gemini/jetski", Comment: "Antigravity agent brain/artifact store."},
		},
	},
	{
		Spec:          mustSpec(backendkind.Cursor),
		ResumeFlag:    "--resume",
		Capture:       captureCursor,
		ModelDelivery: ModelFlag,
		// Skips the workspace-trust prompt in a fresh worktree.
		LaunchFlag:      "--trust",
		PermissionFlags: cursorPermissionFlags,
		// "--mode " / "--mode=" so the check never trips on --model.
		PermissionFlagTokens:    []string{"--force", "--yolo", "--plan", "--mode ", "--mode="},
		InjectGlobal:            injectCursorGlobal,
		PrecreateSandboxGrants:  true,
		SandboxGrantsLaunchOnly: true,
		SandboxGrants: []SandboxGrant{
			// Not all of ~/.cursor: mcp.json and cli-config.json (the command
			// allowlist) stay read-only so a sandboxed agent can't rewrite config an
			// unsandboxed Cursor later trusts. Unverified live; widen if launches EPERM.
			{HomeRel: "/.cursor/chats", Comment: "Cursor CLI chat store; capture reads it post-exit."},
			{HomeRel: "/.cursor/projects", Comment: "Cursor CLI per-project state."},
			{HomeRel: "/.cursor/statsig-cache.json", Prefix: true, Comment: "Cursor CLI feature-flag cache (written via atomic-rename siblings)."},
		},
	},
}

// customFamily is any command no family claims: it keeps the historical
// Claude-style session handling (pinned --session-id, --resume) without any
// Claude-only flags, skills, routing, or capture.
var customFamily = &Family{
	Spec:            mustSpec(backendkind.Custom),
	PinsSessionID:   true,
	ResumeFlag:      "--resume",
	SessionSwitcher: true,
}

// FamilyOf returns the family for a backend command; never nil.
func FamilyOf(command string) *Family {
	return FamilyForKind(backendkind.Of(command))
}

// FamilyForKind returns the family for a kind; unknown kinds are custom.
func FamilyForKind(k backendkind.Kind) *Family {
	for _, f := range families {
		if f.Kind == k {
			return f
		}
	}
	return customFamily
}

// Families returns every known (non-custom) family in a stable order.
func Families() []*Family {
	return slices.Clone(families)
}

// IsCustom reports whether the family is the unrecognized-command fallback.
func (f *Family) IsCustom() bool { return f.Kind == backendkind.Custom }

// PromptFlagFor returns the flag that delivers the prompt for a backend:
// its configured PromptFlag, else the family default.
func (f *Family) PromptFlagFor(b config.Backend) string {
	if b.PromptFlag != "" {
		return b.PromptFlag
	}
	return f.DefaultPromptFlag
}

// commandFlagFields returns the command's fields before any `--` separator;
// tokens after it are positional prompt text, not flags.
func commandFlagFields(command string) []string {
	fields := strings.Fields(command)
	if separator := slices.Index(fields, "--"); separator >= 0 {
		fields = fields[:separator]
	}
	return fields
}

// HasPermissionFlag reports whether the command already names one of the
// family's permission flags, in which case the configured permission mode is
// not injected. Matching is a plain substring search anywhere in the command
// (the pre-registry Claude semantics, kept byte-for-byte); a token ending in
// a space or "=" also matches as the command's final word, so a family whose
// flag prefixes another flag (agy's --mode vs --model) declares the
// delimited forms instead of the bare one.
func (f *Family) HasPermissionFlag(command string) bool {
	for _, tok := range f.PermissionFlagTokens {
		if strings.Contains(command, tok) {
			return true
		}
		if bare := strings.TrimRight(tok, " ="); bare != tok && strings.HasSuffix(command, bare) {
			return true
		}
	}
	return false
}

// ensureSandboxGrantDirsFn is replaceable so command-construction tests never
// create directories under the developer's HOME.
var ensureSandboxGrantDirsFn = func(f *Family, sb config.SandboxConfig) {
	if isTestBinary() {
		return
	}
	ensureSandboxGrantDirsIn(f, sb, "")
}

func ensureSandboxGrantDirs(f *Family, sb config.SandboxConfig) { ensureSandboxGrantDirsFn(f, sb) }

// ensureSandboxGrantDirsIn creates the family's non-Prefix grant dirs under
// home ("" = the user's home) when the launch will be sandboxed.
func ensureSandboxGrantDirsIn(f *Family, sb config.SandboxConfig, home string) {
	if !sb.Enabled || !IsSandboxAvailable() || len(f.SandboxGrants) == 0 {
		return
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return
		}
		home = h
	}
	for _, g := range f.SandboxGrants {
		if g.Prefix {
			continue
		}
		dir := filepath.Join(home, filepath.FromSlash(g.HomeRel))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			uxlog.Log("[%s] create sandbox grant dir %s failed (continuing): %v", f.Kind, dir, err)
		}
	}
}
