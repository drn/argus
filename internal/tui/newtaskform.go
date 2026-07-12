package tui

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/profiles"
	"github.com/drn/argus/internal/skills"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

const (
	ntFieldProject   = 0
	ntFieldBranch    = 1
	ntFieldBackend   = 2
	ntFieldModel     = 3
	ntFieldProfile   = 4
	ntFieldArchetype = 5
	ntFieldPrompt    = 6
	ntFieldName      = 7
	ntFieldCount     = 8
)

// ntArchetypeNone is the archetype selector's first entry — no archetype, so no
// diligence profile is consulted at spawn (add-diligence-profiles default).
const ntArchetypeNone = "(none)"

// maxPromptLines is the maximum visible lines for the prompt textarea.
const maxPromptLines = 10

// acMaxVisible is the maximum number of autocomplete items shown at once.
const acMaxVisible = 6

// NewTaskForm is a modal form for creating new tasks in the tcell runtime.
type NewTaskForm struct {
	*tview.Box
	title        string // modal title (defaults to " New Task "); set via SetTitle
	projectNames []string
	backendNames []string
	backendIdx   int
	prompt       []rune // raw prompt text
	cursorPos    int    // cursor position in prompt runes
	scrollOffset int    // first visible wrapped line (for scrolling)
	promptWidth  int    // cached inner width from last Draw, used by cursor movement
	focused      int    // 0=project, 1=branch, 2=backend, 3=model, 4=profile, 5=archetype, 6=prompt, 7=name

	// optional task-name field: a single-line input rendered after the prompt.
	// Empty (or whitespace-only) ⇒ the task name is derived from the prompt as
	// before; non-empty ⇒ the entered (trimmed + sanitized) name is used and
	// background auto-naming is suppressed. Mirrors the project/branch inputs.
	nameInput     []rune
	nameCursorPos int

	// model override state. The field is a per-backend cycling selector:
	// modelIdx 0 == "default" (empty model → backend/CLI default), 1..len ==
	// modelOptions[idx-1], len+1 == "custom…" (free text via modelInput).
	// modelOptions is rebuilt for the selected backend (agent.BackendModels)
	// and reset to "default" whenever the backend changes.
	modelOptions   []string
	modelIdx       int
	modelInput     []rune // only meaningful in custom mode
	modelCursorPos int
	done           bool
	canceled       bool
	errMsg         string

	// Diligence-profile + archetype cycling selectors (add-diligence-profiles,
	// forms-and-modals). profileOptions is the cycler's display list: index 0 is
	// the project's BOUND profile (the default), followed by the other valid on-disk
	// profiles (profileValidNames, supplied by the App). Cycling to a name other
	// than the bound default is a per-spawn override (ProfileOverride). The archetype
	// cycler offers ntArchetypeNone + the 13 canonical archetypes, default
	// ntArchetypeNone (Archetype() == ""). hideArchetype suppresses the archetype
	// selector for the new-coordinator prompt (a coordinator is always the
	// `orchestrator` archetype, set programmatically).
	profileValidNames []string
	profileOptions    []string
	profileIdx        int
	archetypeIdx      int
	hideArchetype     bool

	projects map[string]config.Project
	backends map[string]config.Backend

	// project typeahead state
	projInput     []rune   // typed text for project filter
	projCursorPos int      // cursor position in project input
	projACOpen    bool     // whether dropdown is showing
	projACMatches []string // filtered project names
	projACIdx     int      // selected item in dropdown
	projACScroll  int      // scroll offset in dropdown

	// branch typeahead state
	branchInput     []rune   // typed text for branch filter
	branchCursorPos int      // cursor position in branch input
	branchACOpen    bool     // whether dropdown is showing
	branchACAll     []string // all branch options (async-loaded via SetBranchOptions, unlike projACMatches which filters the static projectNames slice)
	branchACMatches []string // filtered branch names
	branchACIdx     int      // selected item in dropdown
	branchACScroll  int      // scroll offset in dropdown
	branchPath      string   // project path for which branches were last loaded

	// OnBranchFocus is called when the branch field needs branches loaded
	// for a project path. The caller should fetch branches in a background
	// goroutine and call SetBranchOptions with the results.
	OnBranchFocus func(path string)

	// autocomplete state
	skills    []skills.SkillItem
	acOpen    bool
	acMatches []skills.SkillItem
	acIdx     int
	acScroll  int
	acStart   int // [start, end) bounds of the token being autocompleted,
	acEnd     int // captured when acOpen transitions to true
}

// NewNewTaskForm creates a new task form with sorted project and backend lists.
func NewNewTaskForm(projects map[string]config.Project, defaultProject string, backends map[string]config.Backend, defaultBackend string) *NewTaskForm {
	// Build sorted project names
	projNames := make([]string, 0, len(projects))
	for name := range projects {
		projNames = append(projNames, name)
	}
	sort.Strings(projNames)

	// Build sorted backend names
	backNames := make([]string, 0, len(backends))
	for name := range backends {
		backNames = append(backNames, name)
	}
	sort.Strings(backNames)

	backIdx := 0
	for i, n := range backNames {
		if n == defaultBackend {
			backIdx = i
			break
		}
	}

	// Pre-fill branch from the default project's config.
	defaultBranch := ""
	if defaultProject != "" {
		if p, ok := projects[defaultProject]; ok {
			defaultBranch = p.Branch
		}
	}

	f := &NewTaskForm{
		Box:             tview.NewBox(),
		title:           " New Task ",
		projectNames:    projNames,
		projInput:       []rune(defaultProject),
		projCursorPos:   len([]rune(defaultProject)),
		branchInput:     []rune(defaultBranch),
		branchCursorPos: len([]rune(defaultBranch)),
		backendNames:    backNames,
		backendIdx:      backIdx,
		focused:         ntFieldPrompt,
		projects:        projects,
		backends:        backends,
	}

	// Load skills for the default project
	f.loadSkills()
	// Populate the model selector for the initially selected backend.
	f.rebuildModelOptions()
	// Seed the profile cycler with the default project's bound profile (the App
	// later supplies the valid on-disk names via SetProfileOptions).
	f.rebuildProfileOptions()
	return f
}

// SetTitle overrides the modal title (e.g. "Spawn worker", "New coordinator"
// when the Hera tab reuses this form). An empty value falls back to " New Task ".
func (f *NewTaskForm) SetTitle(title string) {
	if title == "" {
		title = " New Task "
	}
	f.title = title
}

// Done returns true if the form was submitted.
func (f *NewTaskForm) Done() bool { return f.done }

// Canceled returns true if the form was canceled.
func (f *NewTaskForm) Canceled() bool { return f.canceled }

// resolveProject returns the project name if it exactly matches a known project.
func (f *NewTaskForm) resolveProject() string {
	input := string(f.projInput)
	if _, ok := f.projects[input]; ok {
		return input
	}
	// Case-insensitive fallback
	lower := strings.ToLower(input)
	for _, name := range f.projectNames {
		if strings.ToLower(name) == lower {
			return name
		}
	}
	return ""
}

// Task returns the task from the current form state.
func (f *NewTaskForm) Task() *model.Task {
	proj := f.resolveProject()
	backend := ""
	if f.backendIdx < len(f.backendNames) {
		backend = f.backendNames[f.backendIdx]
	}

	branch := f.resolvedBranch()

	prompt := strings.TrimSpace(string(f.prompt))
	name := f.EnteredName()
	if name == "" {
		name = model.GenerateNameFromPrompt(prompt)
	}
	return &model.Task{
		Name:      name,
		Status:    model.StatusPending,
		Project:   proj,
		Branch:    branch,
		Prompt:    prompt,
		Backend:   backend,
		Model:     f.modelValue(),
		Archetype: f.Archetype(),
		Profile:   f.ProfileOverride(),
	}
}

// EnteredName returns the trimmed, sanitized value of the optional name field,
// or "" when the field is blank (whitespace-only counts as blank). A non-empty
// return is the "user-chosen name" signal: it both names the task and suppresses
// background auto-naming. Sanitization uses the same safe-name path that
// auto-derived task/branch names already pass through.
func (f *NewTaskForm) EnteredName() string {
	trimmed := strings.TrimSpace(string(f.nameInput))
	if trimmed == "" {
		return ""
	}
	return agent.SafeName(trimmed)
}

// currentBackend returns the config.Backend for the currently selected backend.
func (f *NewTaskForm) currentBackend() (config.Backend, bool) {
	if f.backendIdx < 0 || f.backendIdx >= len(f.backendNames) {
		return config.Backend{}, false
	}
	b, ok := f.backends[f.backendNames[f.backendIdx]]
	return b, ok
}

// backendDefaultModel returns the configured default model of the currently
// selected backend, or "" when the backend has none. Surfaced as a hint on the
// "default" model option so the user sees what an empty override resolves to.
func (f *NewTaskForm) backendDefaultModel() string {
	if b, ok := f.currentBackend(); ok {
		return b.Model
	}
	return ""
}

// rebuildModelOptions repopulates the model selector for the currently selected
// backend (agent.BackendModels: the backend's configured Models override, else
// the built-in KnownModels list) and resets the selection to "default",
// clearing any typed custom value. Called on construction and whenever the
// backend selector changes.
func (f *NewTaskForm) rebuildModelOptions() {
	if b, ok := f.currentBackend(); ok {
		f.modelOptions = agent.BackendModels(b)
	} else {
		f.modelOptions = nil
	}
	f.modelIdx = 0
	f.modelInput = nil
	f.modelCursorPos = 0
}

// modelEntryCount is the number of cycle positions: "default" + each model +
// "custom…".
func (f *NewTaskForm) modelEntryCount() int { return len(f.modelOptions) + 2 }

// modelIsCustom reports whether the model selector is on the "custom…" entry
// (the final position), where the free-text modelInput applies.
func (f *NewTaskForm) modelIsCustom() bool { return f.modelIdx == len(f.modelOptions)+1 }

// modelValue resolves the selected model to the task's Model value: empty for
// "default", the chosen model for a listed entry, the trimmed typed text for
// "custom…".
func (f *NewTaskForm) modelValue() string {
	if f.modelIsCustom() {
		return strings.TrimSpace(string(f.modelInput))
	}
	if f.modelIdx >= 1 && f.modelIdx <= len(f.modelOptions) {
		return f.modelOptions[f.modelIdx-1]
	}
	return ""
}

// modelDisplayLabel returns the selector's current display value: "default",
// a model name, or "custom…".
func (f *NewTaskForm) modelDisplayLabel() string {
	if f.modelIsCustom() {
		return "custom…"
	}
	if f.modelIdx >= 1 && f.modelIdx <= len(f.modelOptions) {
		return f.modelOptions[f.modelIdx-1]
	}
	return "default"
}

// boundProfile returns the diligence profile NAME bound to the currently
// selected project (config.Project.ResolveProfileName: the explicit binding, else
// "default"). The cycler defaults to this so the operator sees what the project
// would resolve.
func (f *NewTaskForm) boundProfile() string {
	if p, ok := f.projects[f.resolveProject()]; ok {
		return p.ResolveProfileName()
	}
	return "default"
}

// rebuildProfileOptions rebuilds the profile cycler: index 0 is the current
// project's bound profile (the default position), followed by the other valid
// on-disk profile names (deduped). Resets the selection to the bound default —
// called on construction, on SetProfileOptions, and on a project change.
func (f *NewTaskForm) rebuildProfileOptions() {
	bound := f.boundProfile()
	opts := []string{bound}
	seen := map[string]bool{bound: true}
	for _, n := range f.profileValidNames {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		opts = append(opts, n)
	}
	f.profileOptions = opts
	f.profileIdx = 0
}

// SetProfileOptions supplies the valid on-disk profile names (the App filters via
// profiles.Loader.ValidateName) for the per-spawn override cycler, then rebuilds
// the option list pre-positioned on the project's bound default.
func (f *NewTaskForm) SetProfileOptions(valid []string) {
	f.profileValidNames = valid
	f.rebuildProfileOptions()
}

// SetHideArchetype suppresses the archetype selector (the new-coordinator prompt
// — a coordinator is always the `orchestrator` archetype, set programmatically).
func (f *NewTaskForm) SetHideArchetype(hide bool) { f.hideArchetype = hide }

// profileDisplayLabel is the cycler's current display value.
func (f *NewTaskForm) profileDisplayLabel() string {
	if f.profileIdx >= 0 && f.profileIdx < len(f.profileOptions) {
		return f.profileOptions[f.profileIdx]
	}
	return f.boundProfile()
}

// ProfileOverride returns the per-spawn profile override the operator selected —
// the chosen name when it differs from the project's bound default, else "" (no
// override). Passed to the spawn caller (forms-and-modals requirement).
func (f *NewTaskForm) ProfileOverride() string {
	sel := f.profileDisplayLabel()
	if sel == f.boundProfile() {
		return ""
	}
	return sel
}

// ntArchetypeOptions is the archetype cycler's display list: "(none)" + the 13
// canonical archetypes. Built fresh each call (callers must not mutate).
func ntArchetypeOptions() []string {
	return append([]string{ntArchetypeNone}, profiles.CanonicalArchetypes...)
}

// Archetype returns the selected archetype, or "" when on "(none)" — the value
// that rides the submitted task (Task().Archetype). Empty means no profile is
// consulted at spawn.
func (f *NewTaskForm) Archetype() string {
	if f.archetypeIdx <= 0 || f.archetypeIdx > len(profiles.CanonicalArchetypes) {
		return ""
	}
	return profiles.CanonicalArchetypes[f.archetypeIdx-1]
}

// resolvedBranch returns the branch to use: the typed text if non-empty,
// otherwise falls back to the project's configured default branch.
func (f *NewTaskForm) resolvedBranch() string {
	branch := strings.TrimSpace(string(f.branchInput))
	if branch != "" {
		return branch
	}
	proj := f.resolveProject()
	if proj != "" {
		if p, ok := f.projects[proj]; ok {
			return p.Branch
		}
	}
	return ""
}

// SelectedProject returns the selected project name.
func (f *NewTaskForm) SelectedProject() string {
	return f.resolveProject()
}

// SetError sets an error message to display on the form and resets the
// done flag so the form remains open for the user to retry.
func (f *NewTaskForm) SetError(msg string) {
	f.errMsg = msg
	f.done = false
}

// selectedProjectPath returns the filesystem path of the currently selected project.
func (f *NewTaskForm) selectedProjectPath() string {
	proj := f.resolveProject()
	if proj == "" {
		return ""
	}
	if p, ok := f.projects[proj]; ok {
		return p.Path
	}
	return ""
}

// acTrigger returns the autocomplete trigger character for the selected backend:
// "$" for codex (codex's native skill prefix), "/" for everything else.
//
// Pi is intentionally bucketed with claude under "/" because pi ships
// slash-prefixed prompt templates (`/<name>` expansions, per pi.dev docs),
// matching claude's slash-skill UX. Pi's `@<file>` include prefix is a
// separate, runtime-only feature — pi reads files itself when it sees `@`,
// so Argus does not need to autocomplete `@`-prefixed tokens here.
func (f *NewTaskForm) acTrigger() string {
	if len(f.backendNames) > 0 && f.backendIdx < len(f.backendNames) {
		if b, ok := f.backends[f.backendNames[f.backendIdx]]; ok && agent.IsCodexBackend(b.Command) {
			return "$"
		}
	}
	return "/"
}

// updateProjectAC recomputes the project autocomplete matches based on the current input.
func (f *NewTaskForm) updateProjectAC() {
	input := strings.ToLower(string(f.projInput))
	f.projACMatches = nil
	for _, name := range f.projectNames {
		if input == "" || strings.Contains(strings.ToLower(name), input) {
			f.projACMatches = append(f.projACMatches, name)
		}
	}
	f.projACOpen = len(f.projACMatches) > 0
	if f.projACIdx >= len(f.projACMatches) {
		f.projACIdx = 0
		f.projACScroll = 0
	}
}

// projACMoveDown moves the project autocomplete cursor down one item (wraps).
func (f *NewTaskForm) projACMoveDown() {
	if len(f.projACMatches) == 0 {
		return
	}
	f.projACIdx = (f.projACIdx + 1) % len(f.projACMatches)
	if f.projACIdx == 0 {
		f.projACScroll = 0
	} else if f.projACIdx >= f.projACScroll+acMaxVisible {
		f.projACScroll = f.projACIdx - acMaxVisible + 1
	}
}

// projACMoveUp moves the project autocomplete cursor up one item (wraps).
func (f *NewTaskForm) projACMoveUp() {
	if len(f.projACMatches) == 0 {
		return
	}
	if f.projACIdx == 0 {
		f.projACIdx = len(f.projACMatches) - 1
		if f.projACIdx >= acMaxVisible {
			f.projACScroll = f.projACIdx - acMaxVisible + 1
		}
	} else {
		f.projACIdx--
		if f.projACIdx < f.projACScroll {
			f.projACScroll = f.projACIdx
		}
	}
}

// projACAccept selects the current autocomplete match and closes the dropdown.
func (f *NewTaskForm) projACAccept() {
	if len(f.projACMatches) == 0 {
		return
	}
	name := f.projACMatches[f.projACIdx]
	f.projInput = []rune(name)
	f.projCursorPos = len(f.projInput)
	f.projACOpen = false
	f.onProjectChanged()
}

// onProjectChanged resets the branch field to the new project's default
// branch and triggers async branch loading.
func (f *NewTaskForm) onProjectChanged() {
	f.loadSkills()
	proj := f.resolveProject()
	defaultBranch := ""
	if proj != "" {
		if p, ok := f.projects[proj]; ok {
			defaultBranch = p.Branch
		}
	}
	f.branchInput = []rune(defaultBranch)
	f.branchCursorPos = len(f.branchInput)
	f.branchACAll = nil
	f.branchACMatches = nil
	f.branchACOpen = false
	f.branchPath = "" // clear so maybeLoadBranches reloads even for the same project
	// Re-default the profile cycler to the new project's bound profile.
	f.rebuildProfileOptions()
	f.maybeLoadBranches()
}

// visibleField returns the next/previous focusable field from start in direction
// dir (+1 / -1), skipping the archetype field when it is hidden (new-coordinator
// prompt). All other fields are always visible.
func (f *NewTaskForm) visibleField(start, dir int) int {
	n := start
	for i := 0; i < ntFieldCount; i++ {
		n = (n + dir + ntFieldCount) % ntFieldCount
		if n == ntFieldArchetype && f.hideArchetype {
			continue
		}
		return n
	}
	return start
}

// SetBranchOptions populates the branch autocomplete options. Called from a
// background goroutine via QueueUpdateDraw after fetching branches.
func (f *NewTaskForm) SetBranchOptions(options []string) {
	f.branchACAll = options
	f.updateBranchAC()
}

// maybeLoadBranches fires OnBranchFocus when the project path has changed
// since the last load.
func (f *NewTaskForm) maybeLoadBranches() {
	path := f.selectedProjectPath()
	if path == "" || path == f.branchPath || f.OnBranchFocus == nil {
		return
	}
	f.branchPath = path
	f.OnBranchFocus(path)
}

// updateBranchAC recomputes the branch autocomplete matches based on current input.
func (f *NewTaskForm) updateBranchAC() {
	input := strings.ToLower(string(f.branchInput))
	f.branchACMatches = nil
	for _, name := range f.branchACAll {
		if input == "" || strings.Contains(strings.ToLower(name), input) {
			f.branchACMatches = append(f.branchACMatches, name)
		}
	}
	f.branchACOpen = len(f.branchACMatches) > 0 && f.focused == ntFieldBranch
	if f.branchACIdx >= len(f.branchACMatches) {
		f.branchACIdx = 0
		f.branchACScroll = 0
	}
}

// branchACMoveDown moves the branch autocomplete cursor down one item (wraps).
func (f *NewTaskForm) branchACMoveDown() {
	if len(f.branchACMatches) == 0 {
		return
	}
	f.branchACIdx = (f.branchACIdx + 1) % len(f.branchACMatches)
	if f.branchACIdx == 0 {
		f.branchACScroll = 0
	} else if f.branchACIdx >= f.branchACScroll+acMaxVisible {
		f.branchACScroll = f.branchACIdx - acMaxVisible + 1
	}
}

// branchACMoveUp moves the branch autocomplete cursor up one item (wraps).
func (f *NewTaskForm) branchACMoveUp() {
	if len(f.branchACMatches) == 0 {
		return
	}
	if f.branchACIdx == 0 {
		f.branchACIdx = len(f.branchACMatches) - 1
		if f.branchACIdx >= acMaxVisible {
			f.branchACScroll = f.branchACIdx - acMaxVisible + 1
		}
	} else {
		f.branchACIdx--
		if f.branchACIdx < f.branchACScroll {
			f.branchACScroll = f.branchACIdx
		}
	}
}

// branchACAccept selects the current autocomplete match and closes the dropdown.
func (f *NewTaskForm) branchACAccept() {
	if len(f.branchACMatches) == 0 {
		return
	}
	name := f.branchACMatches[f.branchACIdx]
	f.branchInput = []rune(name)
	f.branchCursorPos = len(f.branchInput)
	f.branchACOpen = false
}

// loadSkills scans skill directories for the currently selected project.
func (f *NewTaskForm) loadSkills() {
	var extraDirs []string
	if pp := f.selectedProjectPath(); pp != "" {
		extraDirs = []string{filepath.Join(pp, ".claude", "skills")}
	}
	f.skills = skills.LoadSkills(extraDirs)
}

// promptTokenBounds returns the [start, end) rune indices of the
// space-delimited token containing the cursor position.
func (f *NewTaskForm) promptTokenBounds() (int, int) {
	start := f.cursorPos
	for start > 0 && f.prompt[start-1] != ' ' {
		start--
	}
	end := f.cursorPos
	for end < len(f.prompt) && f.prompt[end] != ' ' {
		end++
	}
	return start, end
}

// updateAutocomplete recomputes the autocomplete matches based on the token
// at the cursor position. Autocomplete is active when the current
// space-delimited token starts with the trigger character ("/" for claude,
// "$" for codex). Triggering works anywhere in the prompt, not just at the
// start.
func (f *NewTaskForm) updateAutocomplete() {
	trigger := f.acTrigger()
	start, end := f.promptTokenBounds()
	token := string(f.prompt[start:end])
	if !strings.HasPrefix(token, trigger) {
		f.closeAC()
		return
	}
	filter := token[len(trigger):]
	f.acMatches = skills.FilterSkills(f.skills, filter)
	if len(f.acMatches) == 0 {
		f.closeAC()
		return
	}
	f.acOpen = true
	f.acStart = start
	f.acEnd = end
	if f.acIdx >= len(f.acMatches) {
		f.acIdx = 0
		f.acScroll = 0
	}
}

// closeAC closes the autocomplete dropdown and resets its navigation state.
// Having a single closer means acIdx/acScroll don't leak across open cycles.
func (f *NewTaskForm) closeAC() {
	f.acOpen = false
	f.acIdx = 0
	f.acScroll = 0
}

// acAccept replaces the token captured when the dropdown opened with the
// selected skill and appends a trailing space. Closes the dropdown.
func (f *NewTaskForm) acAccept() {
	if !f.acOpen || len(f.acMatches) == 0 {
		return
	}
	start, end := f.acStart, f.acEnd
	replacement := []rune(f.acTrigger() + f.acMatches[f.acIdx].Name + " ")
	newPrompt := make([]rune, 0, len(f.prompt)-(end-start)+len(replacement))
	newPrompt = append(newPrompt, f.prompt[:start]...)
	newPrompt = append(newPrompt, replacement...)
	newPrompt = append(newPrompt, f.prompt[end:]...)
	f.prompt = newPrompt
	f.cursorPos = start + len(replacement)
	f.closeAC()
}

// acMoveDown moves the autocomplete cursor down one item (wraps around).
func (f *NewTaskForm) acMoveDown() {
	if len(f.acMatches) == 0 {
		return
	}
	f.acIdx = (f.acIdx + 1) % len(f.acMatches)
	if f.acIdx == 0 {
		f.acScroll = 0
	} else if f.acIdx >= f.acScroll+acMaxVisible {
		f.acScroll = f.acIdx - acMaxVisible + 1
	}
}

// acMoveUp moves the autocomplete cursor up one item (wraps around).
func (f *NewTaskForm) acMoveUp() {
	if len(f.acMatches) == 0 {
		return
	}
	if f.acIdx == 0 {
		f.acIdx = len(f.acMatches) - 1
		if f.acIdx >= acMaxVisible {
			f.acScroll = f.acIdx - acMaxVisible + 1
		}
	} else {
		f.acIdx--
		if f.acIdx < f.acScroll {
			f.acScroll = f.acIdx
		}
	}
}

// PasteHandler handles bracketed paste events, inserting the entire pasted
// text at the cursor position in a single operation instead of per-character.
func (f *NewTaskForm) PasteHandler() func(pastedText string, setFocus func(p tview.Primitive)) {
	return f.WrapPasteHandler(func(pastedText string, setFocus func(p tview.Primitive)) {
		f.errMsg = ""
		runes := []rune(pastedText)
		if len(runes) == 0 {
			return
		}
		switch f.focused {
		case ntFieldProject:
			newInput := make([]rune, 0, len(f.projInput)+len(runes))
			newInput = append(newInput, f.projInput[:f.projCursorPos]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, f.projInput[f.projCursorPos:]...)
			f.projInput = newInput
			f.projCursorPos += len(runes)
			f.updateProjectAC()
		case ntFieldBranch:
			newInput := make([]rune, 0, len(f.branchInput)+len(runes))
			newInput = append(newInput, f.branchInput[:f.branchCursorPos]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, f.branchInput[f.branchCursorPos:]...)
			f.branchInput = newInput
			f.branchCursorPos += len(runes)
			f.updateBranchAC()
		case ntFieldModel:
			// Only the custom… free-text entry accepts paste; otherwise the
			// model field is a selector and ignores pasted text (mirroring the
			// backend selector's paste no-op).
			if !f.modelIsCustom() {
				return
			}
			newInput := make([]rune, 0, len(f.modelInput)+len(runes))
			newInput = append(newInput, f.modelInput[:f.modelCursorPos]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, f.modelInput[f.modelCursorPos:]...)
			f.modelInput = newInput
			f.modelCursorPos += len(runes)
		case ntFieldPrompt:
			newPrompt := make([]rune, 0, len(f.prompt)+len(runes))
			newPrompt = append(newPrompt, f.prompt[:f.cursorPos]...)
			newPrompt = append(newPrompt, runes...)
			newPrompt = append(newPrompt, f.prompt[f.cursorPos:]...)
			f.prompt = newPrompt
			f.cursorPos += len(runes)
			f.updateAutocomplete()
		case ntFieldName:
			newInput := make([]rune, 0, len(f.nameInput)+len(runes))
			newInput = append(newInput, f.nameInput[:f.nameCursorPos]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, f.nameInput[f.nameCursorPos:]...)
			f.nameInput = newInput
			f.nameCursorPos += len(runes)
			// ntFieldBackend / ntFieldProfile / ntFieldArchetype: selector fields
			// have no case and ignore pasted text (no free-text entry).
		}
	})
}

// InputHandler handles key events for the form.
func (f *NewTaskForm) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return f.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		// Clear error on any keypress
		f.errMsg = ""

		// Global form keys
		switch event.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlQ:
			if f.acOpen || f.projACOpen || f.branchACOpen { // two-step: first press closes autocomplete, second cancels form
				f.closeAC()
				f.projACOpen = false
				f.branchACOpen = false
				return
			}
			f.canceled = true
			return
		case tcell.KeyTab:
			// In the prompt field, tab accepts an open skill autocomplete
			// without advancing focus — so the user can keep typing.
			if f.focused == ntFieldPrompt && f.acOpen && len(f.acMatches) > 0 {
				f.acAccept()
				return
			}
			// Accept any open autocomplete before advancing field
			if f.projACOpen && len(f.projACMatches) > 0 {
				f.projACAccept()
			}
			if f.branchACOpen && len(f.branchACMatches) > 0 {
				f.branchACAccept()
			}
			f.closeAC()
			f.projACOpen = false
			f.branchACOpen = false
			prev := f.focused
			f.focused = f.visibleField(f.focused, +1)
			if prev == ntFieldProject && f.focused == ntFieldBranch {
				f.maybeLoadBranches()
			}
			return
		case tcell.KeyBacktab:
			// In the prompt field, shift-tab accepts an open skill autocomplete
			// without changing focus — mirrors Tab's behavior for consistency.
			if f.focused == ntFieldPrompt && f.acOpen && len(f.acMatches) > 0 {
				f.acAccept()
				return
			}
			// Accept any open autocomplete before retreating field
			if f.projACOpen && len(f.projACMatches) > 0 {
				f.projACAccept()
			}
			if f.branchACOpen && len(f.branchACMatches) > 0 {
				f.branchACAccept()
			}
			f.closeAC()
			f.projACOpen = false
			f.branchACOpen = false
			f.focused = f.visibleField(f.focused, -1)
			if f.focused == ntFieldBranch {
				f.maybeLoadBranches()
			}
			return
		}

		switch f.focused {
		case ntFieldProject:
			f.handleProjectKey(event)
		case ntFieldBranch:
			f.handleBranchKey(event)
		case ntFieldBackend:
			f.handleSelectorKey(event, &f.backendIdx, len(f.backendNames))
		case ntFieldModel:
			f.handleModelKey(event)
		case ntFieldProfile:
			f.handleProfileKey(event)
		case ntFieldArchetype:
			f.handleArchetypeKey(event)
		case ntFieldPrompt:
			f.handlePromptKey(event)
		case ntFieldName:
			f.handleNameKey(event)
		}
	})
}

// handleProjectKey handles key events when the project typeahead field is focused.
func (f *NewTaskForm) handleProjectKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		if f.projACOpen && len(f.projACMatches) > 0 {
			f.projACAccept()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldBranch
		f.onProjectChanged()
		return
	case tcell.KeyDown:
		if f.projACOpen {
			f.projACMoveDown()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldBranch
		f.onProjectChanged()
		return
	case tcell.KeyUp:
		if f.projACOpen {
			f.projACMoveUp()
			return
		}
		f.projACOpen = false
		f.focused = ntFieldPrompt
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			f.projInput, f.projCursorPos = widget.DeleteWordLeft(f.projInput, f.projCursorPos)
			f.updateProjectAC()
			return
		}
		if f.projCursorPos > 0 {
			f.projInput = append(f.projInput[:f.projCursorPos-1], f.projInput[f.projCursorPos:]...)
			f.projCursorPos--
			f.updateProjectAC()
		}
		return
	case tcell.KeyCtrlW:
		f.projInput, f.projCursorPos = widget.DeleteWordLeft(f.projInput, f.projCursorPos)
		f.updateProjectAC()
		return
	case tcell.KeyDelete:
		if f.projCursorPos < len(f.projInput) {
			f.projInput = append(f.projInput[:f.projCursorPos], f.projInput[f.projCursorPos+1:]...)
			f.updateProjectAC()
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			f.projCursorPos = widget.WordLeftPos(f.projInput, f.projCursorPos)
			return
		}
		if f.projCursorPos > 0 {
			f.projCursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			f.projCursorPos = widget.WordRightPos(f.projInput, f.projCursorPos)
			return
		}
		if f.projCursorPos < len(f.projInput) {
			f.projCursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.projCursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.projCursorPos = len(f.projInput)
		return
	case tcell.KeyCtrlU:
		f.projInput = f.projInput[f.projCursorPos:]
		f.projCursorPos = 0
		f.updateProjectAC()
		return
	case tcell.KeyCtrlK:
		f.projInput = f.projInput[:f.projCursorPos]
		f.updateProjectAC()
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.projCursorPos = widget.WordLeftPos(f.projInput, f.projCursorPos)
			case 'f', 'F':
				f.projCursorPos = widget.WordRightPos(f.projInput, f.projCursorPos)
			case 'd', 'D':
				f.projInput, f.projCursorPos = widget.DeleteWordRight(f.projInput, f.projCursorPos)
				f.updateProjectAC()
			}
			return
		}
		f.projInput = append(f.projInput[:f.projCursorPos], append([]rune{r}, f.projInput[f.projCursorPos:]...)...)
		f.projCursorPos++
		f.updateProjectAC()
		return
	}
}

// handleBranchKey handles key events when the branch typeahead field is focused.
func (f *NewTaskForm) handleBranchKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		if f.branchACOpen && len(f.branchACMatches) > 0 {
			f.branchACAccept()
			return
		}
		f.branchACOpen = false
		f.focused = ntFieldBackend
		return
	case tcell.KeyDown:
		if f.branchACOpen {
			f.branchACMoveDown()
			return
		}
		f.branchACOpen = false
		f.focused = ntFieldBackend
		return
	case tcell.KeyUp:
		if f.branchACOpen {
			f.branchACMoveUp()
			return
		}
		f.branchACOpen = false
		f.focused = ntFieldProject
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			f.branchInput, f.branchCursorPos = widget.DeleteWordLeft(f.branchInput, f.branchCursorPos)
			f.updateBranchAC()
			return
		}
		if f.branchCursorPos > 0 {
			f.branchInput = append(f.branchInput[:f.branchCursorPos-1], f.branchInput[f.branchCursorPos:]...)
			f.branchCursorPos--
			f.updateBranchAC()
		}
		return
	case tcell.KeyCtrlW:
		f.branchInput, f.branchCursorPos = widget.DeleteWordLeft(f.branchInput, f.branchCursorPos)
		f.updateBranchAC()
		return
	case tcell.KeyDelete:
		if f.branchCursorPos < len(f.branchInput) {
			f.branchInput = append(f.branchInput[:f.branchCursorPos], f.branchInput[f.branchCursorPos+1:]...)
			f.updateBranchAC()
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			f.branchCursorPos = widget.WordLeftPos(f.branchInput, f.branchCursorPos)
			return
		}
		if f.branchCursorPos > 0 {
			f.branchCursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			f.branchCursorPos = widget.WordRightPos(f.branchInput, f.branchCursorPos)
			return
		}
		if f.branchCursorPos < len(f.branchInput) {
			f.branchCursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.branchCursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.branchCursorPos = len(f.branchInput)
		return
	case tcell.KeyCtrlU:
		f.branchInput = f.branchInput[f.branchCursorPos:]
		f.branchCursorPos = 0
		f.updateBranchAC()
		return
	case tcell.KeyCtrlK:
		f.branchInput = f.branchInput[:f.branchCursorPos]
		f.updateBranchAC()
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.branchCursorPos = widget.WordLeftPos(f.branchInput, f.branchCursorPos)
			case 'f', 'F':
				f.branchCursorPos = widget.WordRightPos(f.branchInput, f.branchCursorPos)
			case 'd', 'D':
				f.branchInput, f.branchCursorPos = widget.DeleteWordRight(f.branchInput, f.branchCursorPos)
				f.updateBranchAC()
			}
			return
		}
		f.branchInput = append(f.branchInput[:f.branchCursorPos], append([]rune{r}, f.branchInput[f.branchCursorPos:]...)...)
		f.branchCursorPos++
		f.updateBranchAC()
		return
	}
}

// handleNameKey handles key events when the optional name field is focused. It
// is a plain single-line text input (no autocomplete), mirroring the branch
// field's editing keys. Enter submits the form (the name is the last field, so
// the user can fill it and submit) using the same guard as the prompt's Enter;
// Up/Down move focus within the cycle (prompt above, project below).
func (f *NewTaskForm) handleNameKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		if f.resolveProject() == "" {
			f.errMsg = "Unknown project"
			return
		}
		if len(f.prompt) > 0 {
			f.done = true
		}
		return
	case tcell.KeyDown:
		f.focused = ntFieldProject
		return
	case tcell.KeyUp:
		f.focused = ntFieldPrompt
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			f.nameInput, f.nameCursorPos = widget.DeleteWordLeft(f.nameInput, f.nameCursorPos)
			return
		}
		if f.nameCursorPos > 0 {
			f.nameInput = append(f.nameInput[:f.nameCursorPos-1], f.nameInput[f.nameCursorPos:]...)
			f.nameCursorPos--
		}
		return
	case tcell.KeyCtrlW:
		f.nameInput, f.nameCursorPos = widget.DeleteWordLeft(f.nameInput, f.nameCursorPos)
		return
	case tcell.KeyDelete:
		if hasAlt {
			f.nameInput, f.nameCursorPos = widget.DeleteWordRight(f.nameInput, f.nameCursorPos)
			return
		}
		if f.nameCursorPos < len(f.nameInput) {
			f.nameInput = append(f.nameInput[:f.nameCursorPos], f.nameInput[f.nameCursorPos+1:]...)
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			f.nameCursorPos = widget.WordLeftPos(f.nameInput, f.nameCursorPos)
			return
		}
		if f.nameCursorPos > 0 {
			f.nameCursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			f.nameCursorPos = widget.WordRightPos(f.nameInput, f.nameCursorPos)
			return
		}
		if f.nameCursorPos < len(f.nameInput) {
			f.nameCursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.nameCursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.nameCursorPos = len(f.nameInput)
		return
	case tcell.KeyCtrlU:
		f.nameInput = f.nameInput[f.nameCursorPos:]
		f.nameCursorPos = 0
		return
	case tcell.KeyCtrlK:
		f.nameInput = f.nameInput[:f.nameCursorPos]
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.nameCursorPos = widget.WordLeftPos(f.nameInput, f.nameCursorPos)
			case 'f', 'F':
				f.nameCursorPos = widget.WordRightPos(f.nameInput, f.nameCursorPos)
			case 'd', 'D':
				f.nameInput, f.nameCursorPos = widget.DeleteWordRight(f.nameInput, f.nameCursorPos)
			}
			return
		}
		f.nameInput = append(f.nameInput[:f.nameCursorPos], append([]rune{r}, f.nameInput[f.nameCursorPos:]...)...)
		f.nameCursorPos++
		return
	}
}

// handleModelKey handles key events when the model field is focused. The field
// is a per-backend cycling selector: left/right cycle "default" → known models
// → "custom…" (always, even in custom mode — left/right are the cycle, not
// cursor movement). Up/down move field focus. When the selector is on "custom…"
// the runes/edit keys flow into the free-text modelInput so a model the list
// doesn't name can still be typed; cursor positioning within that text is
// Home/End-only (left/right belong to the selector).
func (f *NewTaskForm) handleModelKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEnter, tcell.KeyDown:
		f.focused = ntFieldProfile
		return
	case tcell.KeyUp:
		f.focused = ntFieldBackend
		return
	case tcell.KeyLeft:
		count := f.modelEntryCount()
		f.modelIdx = (f.modelIdx - 1 + count) % count
		return
	case tcell.KeyRight:
		count := f.modelEntryCount()
		f.modelIdx = (f.modelIdx + 1) % count
		return
	}

	// Remaining keys only act in custom mode (the free-text escape hatch).
	if !f.modelIsCustom() {
		return
	}
	f.handleModelCustomKey(event)
}

// handleProfileKey handles the profile cycling selector: left/right cycle the
// project's bound profile + the other valid on-disk profiles; up/down move field
// focus (down skips the archetype field to the prompt when it is hidden).
func (f *NewTaskForm) handleProfileKey(event *tcell.EventKey) {
	switch event.Key() {
	case tcell.KeyEnter, tcell.KeyDown:
		f.focused = f.visibleField(ntFieldProfile, +1)
	case tcell.KeyUp:
		f.focused = ntFieldModel
	case tcell.KeyLeft:
		if n := len(f.profileOptions); n > 0 {
			f.profileIdx = (f.profileIdx - 1 + n) % n
		}
	case tcell.KeyRight:
		if n := len(f.profileOptions); n > 0 {
			f.profileIdx = (f.profileIdx + 1) % n
		}
	}
}

// handleArchetypeKey handles the archetype cycling selector: left/right cycle
// "(none)" + the 13 canonical archetypes; up/down move field focus.
func (f *NewTaskForm) handleArchetypeKey(event *tcell.EventKey) {
	count := len(profiles.CanonicalArchetypes) + 1 // "(none)" + canonical
	switch event.Key() {
	case tcell.KeyEnter, tcell.KeyDown:
		f.focused = ntFieldPrompt
	case tcell.KeyUp:
		f.focused = ntFieldProfile
	case tcell.KeyLeft:
		f.archetypeIdx = (f.archetypeIdx - 1 + count) % count
	case tcell.KeyRight:
		f.archetypeIdx = (f.archetypeIdx + 1) % count
	}
}

// handleModelCustomKey edits the free-text modelInput while the "custom…" entry
// is selected. Left/right are intentionally absent here — they cycle the
// selector (see handleModelKey); positioning is Home/End plus the word/line
// kill keys.
func (f *NewTaskForm) handleModelCustomKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			f.modelInput, f.modelCursorPos = widget.DeleteWordLeft(f.modelInput, f.modelCursorPos)
			return
		}
		if f.modelCursorPos > 0 {
			f.modelInput = append(f.modelInput[:f.modelCursorPos-1], f.modelInput[f.modelCursorPos:]...)
			f.modelCursorPos--
		}
		return
	case tcell.KeyCtrlW:
		f.modelInput, f.modelCursorPos = widget.DeleteWordLeft(f.modelInput, f.modelCursorPos)
		return
	case tcell.KeyDelete:
		if hasAlt {
			f.modelInput, f.modelCursorPos = widget.DeleteWordRight(f.modelInput, f.modelCursorPos)
			return
		}
		if f.modelCursorPos < len(f.modelInput) {
			f.modelInput = append(f.modelInput[:f.modelCursorPos], f.modelInput[f.modelCursorPos+1:]...)
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.modelCursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.modelCursorPos = len(f.modelInput)
		return
	case tcell.KeyCtrlU:
		f.modelInput = f.modelInput[f.modelCursorPos:]
		f.modelCursorPos = 0
		return
	case tcell.KeyCtrlK:
		f.modelInput = f.modelInput[:f.modelCursorPos]
		return
	case tcell.KeyRune:
		r := event.Rune()
		if hasAlt {
			switch r {
			case 'd', 'D':
				f.modelInput, f.modelCursorPos = widget.DeleteWordRight(f.modelInput, f.modelCursorPos)
			}
			return
		}
		f.modelInput = append(f.modelInput[:f.modelCursorPos], append([]rune{r}, f.modelInput[f.modelCursorPos:]...)...)
		f.modelCursorPos++
		return
	}
}

func (f *NewTaskForm) handleSelectorKey(event *tcell.EventKey, idx *int, count int) {
	if count == 0 {
		return
	}
	switch event.Key() {
	case tcell.KeyLeft:
		*idx = (*idx - 1 + count) % count
		if idx == &f.backendIdx {
			f.updateAutocomplete()
			f.rebuildModelOptions()
		}
	case tcell.KeyRight:
		*idx = (*idx + 1) % count
		if idx == &f.backendIdx {
			f.updateAutocomplete()
			f.rebuildModelOptions()
		}
	case tcell.KeyDown, tcell.KeyEnter:
		if f.focused < ntFieldPrompt {
			f.focused++
		}
	case tcell.KeyUp:
		if f.focused > ntFieldProject {
			f.focused--
		}
	}
}

func (f *NewTaskForm) handlePromptKey(event *tcell.EventKey) {
	mod := event.Modifiers()
	hasAlt := mod&tcell.ModAlt != 0

	switch event.Key() {
	case tcell.KeyEnter:
		// Select autocomplete suggestion if open
		if f.acOpen && len(f.acMatches) > 0 {
			f.acAccept()
			return
		}
		if f.resolveProject() == "" {
			f.errMsg = "Unknown project"
			return
		}
		if len(f.prompt) > 0 {
			f.done = true
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if hasAlt {
			// Alt+Backspace: delete word left
			f.prompt, f.cursorPos = widget.DeleteWordLeft(f.prompt, f.cursorPos)
			f.updateAutocomplete()
			return
		}
		if f.cursorPos > 0 {
			f.prompt = append(f.prompt[:f.cursorPos-1], f.prompt[f.cursorPos:]...)
			f.cursorPos--
			f.updateAutocomplete()
		}
		return
	case tcell.KeyCtrlW:
		// Ctrl+W: delete word left
		f.prompt, f.cursorPos = widget.DeleteWordLeft(f.prompt, f.cursorPos)
		f.updateAutocomplete()
		return
	case tcell.KeyDelete:
		if hasAlt {
			// Alt+Delete: delete word right
			f.prompt, f.cursorPos = widget.DeleteWordRight(f.prompt, f.cursorPos)
			f.updateAutocomplete()
			return
		}
		if f.cursorPos < len(f.prompt) {
			f.prompt = append(f.prompt[:f.cursorPos], f.prompt[f.cursorPos+1:]...)
			f.updateAutocomplete()
		}
		return
	case tcell.KeyLeft:
		if hasAlt {
			// Alt+Left: jump word left
			f.cursorPos = widget.WordLeftPos(f.prompt, f.cursorPos)
			return
		}
		if f.cursorPos > 0 {
			f.cursorPos--
		}
		return
	case tcell.KeyRight:
		if hasAlt {
			// Alt+Right: jump word right
			f.cursorPos = widget.WordRightPos(f.prompt, f.cursorPos)
			return
		}
		if f.cursorPos < len(f.prompt) {
			f.cursorPos++
		}
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.cursorPos = 0
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		f.cursorPos = len(f.prompt)
		return
	case tcell.KeyCtrlU:
		f.prompt = f.prompt[f.cursorPos:]
		f.cursorPos = 0
		f.updateAutocomplete()
		return
	case tcell.KeyCtrlK:
		f.prompt = f.prompt[:f.cursorPos]
		f.updateAutocomplete()
		return
	case tcell.KeyUp:
		if f.acOpen {
			f.acMoveUp()
			return
		}
		// Move cursor up one wrapped line if possible, otherwise leave prompt field
		// (to the archetype selector, or profile when archetype is hidden).
		if !f.moveCursorUp() {
			f.focused = f.visibleField(ntFieldPrompt, -1)
		}
		return
	case tcell.KeyDown:
		if f.acOpen {
			f.acMoveDown()
			return
		}
		// Move cursor down one wrapped line, or wrap to project if on last line
		w := f.promptInnerW()
		lines := f.wrapPrompt(w)
		line, _ := f.cursorWrappedPos(w)
		if line >= len(lines)-1 {
			// On last line — wrap to project (circular navigation)
			f.focused = ntFieldProject
			return
		}
		f.moveCursorDown()
		return
	case tcell.KeyRune:
		r := event.Rune()
		// Alt+B: jump word left, Alt+F: jump word right, Alt+D: delete word right
		if hasAlt {
			switch r {
			case 'b', 'B':
				f.cursorPos = widget.WordLeftPos(f.prompt, f.cursorPos)
				return
			case 'f', 'F':
				f.cursorPos = widget.WordRightPos(f.prompt, f.cursorPos)
				return
			case 'd', 'D':
				f.prompt, f.cursorPos = widget.DeleteWordRight(f.prompt, f.cursorPos)
				f.updateAutocomplete()
				return
			}
			return // ignore other alt+rune combos
		}
		f.prompt = append(f.prompt[:f.cursorPos], append([]rune{r}, f.prompt[f.cursorPos:]...)...)
		f.cursorPos++
		f.updateAutocomplete()
		return
	}
}

// wrappedLine represents a visual line segment within the prompt rune slice.
type wrappedLine struct {
	start  int // index into f.prompt where this line begins
	length int // number of runes on this line
}

// wrapPrompt splits the prompt runes into visual lines of the given width,
// breaking at word boundaries when possible. A "word boundary" is a space
// character. If a single word exceeds the width, it is hard-broken.
func (f *NewTaskForm) wrapPrompt(width int) []wrappedLine {
	if width <= 0 {
		return nil
	}
	if len(f.prompt) == 0 {
		return []wrappedLine{{0, 0}}
	}
	var lines []wrappedLine
	i := 0
	for i < len(f.prompt) {
		remaining := len(f.prompt) - i
		if remaining <= width {
			// Rest fits on one line
			lines = append(lines, wrappedLine{i, remaining})
			break
		}
		// Find last space within the width to break at
		// (i+width is safe: the remaining <= width guard above handles the boundary case)
		breakAt := -1
		for j := i + width; j > i; j-- {
			if f.prompt[j] == ' ' {
				breakAt = j
				break
			}
		}
		if breakAt <= i {
			// No space found — hard break at width
			lines = append(lines, wrappedLine{i, width})
			i += width
		} else {
			// Break at the space; include the space on this line
			lineLen := breakAt - i + 1
			lines = append(lines, wrappedLine{i, lineLen})
			i = breakAt + 1
		}
	}
	return lines
}

// cursorWrappedPos returns (line index, column) of the cursor within wrapped lines.
func (f *NewTaskForm) cursorWrappedPos(width int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	lines := f.wrapPrompt(width)
	for i, wl := range lines {
		if f.cursorPos >= wl.start && f.cursorPos < wl.start+wl.length {
			return i, f.cursorPos - wl.start
		}
	}
	// Cursor is at the end of the last line
	if len(lines) > 0 {
		last := lines[len(lines)-1]
		return len(lines) - 1, f.cursorPos - last.start
	}
	return 0, 0
}

// promptInnerW returns the cached prompt width from the last Draw call,
// falling back to a reasonable default (min(60, sw-4) - 4 = 52 at 64+ cols).
func (f *NewTaskForm) promptInnerW() int {
	if f.promptWidth > 0 {
		return f.promptWidth
	}
	return 52
}

// moveCursorUp moves the cursor up one wrapped line. Returns false if already on the first line.
func (f *NewTaskForm) moveCursorUp() bool {
	w := f.promptInnerW()
	lines := f.wrapPrompt(w)
	line, col := f.cursorWrappedPos(w)
	if line == 0 {
		return false
	}
	prevLine := lines[line-1]
	newPos := prevLine.start + col
	if col > prevLine.length-1 {
		newPos = prevLine.start + prevLine.length - 1
		if newPos < prevLine.start {
			newPos = prevLine.start
		}
	}
	if newPos > len(f.prompt) {
		newPos = len(f.prompt)
	}
	f.cursorPos = newPos
	return true
}

// moveCursorDown moves the cursor down one wrapped line.
func (f *NewTaskForm) moveCursorDown() {
	w := f.promptInnerW()
	lines := f.wrapPrompt(w)
	line, col := f.cursorWrappedPos(w)
	if line >= len(lines)-1 {
		return
	}
	nextLine := lines[line+1]
	newPos := nextLine.start + col
	endPos := nextLine.start + nextLine.length
	if newPos > endPos {
		newPos = endPos
	}
	if newPos > len(f.prompt) {
		newPos = len(f.prompt)
	}
	f.cursorPos = newPos
}

// ensureCursorVisible adjusts scrollOffset so the cursor line is visible.
func (f *NewTaskForm) ensureCursorVisible(totalLines, visibleLines int) {
	// If all content fits in the visible area, never scroll
	if totalLines <= visibleLines {
		f.scrollOffset = 0
		return
	}
	w := f.promptInnerW()
	curLine, _ := f.cursorWrappedPos(w)
	if curLine < f.scrollOffset {
		f.scrollOffset = curLine
	}
	if curLine >= f.scrollOffset+visibleLines {
		f.scrollOffset = curLine - visibleLines + 1
	}
}

// Draw renders the modal form.
func (f *NewTaskForm) Draw(screen tcell.Screen) {
	f.Box.DrawForSubclass(screen, f)
	sx, sy, sw, sh := f.GetInnerRect()
	if sw <= 0 || sh <= 0 {
		return
	}

	// Modal dimensions
	modalW := min(60, sw-4)
	innerW := modalW - 4
	f.promptWidth = innerW // cache for key handlers
	if modalW < 20 {
		return
	}

	// Compute wrapped prompt lines for dynamic height
	wrappedLines := f.wrapPrompt(innerW)
	promptLines := len(wrappedLines)
	visiblePromptLines := promptLines
	if visiblePromptLines > maxPromptLines {
		visiblePromptLines = maxPromptLines
	}
	if visiblePromptLines < 1 {
		visiblePromptLines = 1
	}

	// Ensure cursor is visible within the scroll window
	f.ensureCursorVisible(promptLines, visiblePromptLines)

	// Autocomplete row count for modal height
	acRows := 0
	if f.acOpen && len(f.acMatches) > 0 {
		acRows = len(f.acMatches)
		if acRows > acMaxVisible {
			acRows = acMaxVisible + 1 // extra row for scroll indicator
		}
	}

	// Project autocomplete row count
	projACRows := 0
	if f.projACOpen && len(f.projACMatches) > 0 {
		projACRows = len(f.projACMatches)
		if projACRows > acMaxVisible {
			projACRows = acMaxVisible + 1
		}
	}

	// Branch autocomplete row count
	branchACRows := 0
	if f.branchACOpen && len(f.branchACMatches) > 0 {
		branchACRows = len(f.branchACMatches)
		if branchACRows > acMaxVisible {
			branchACRows = acMaxVisible + 1
		}
	}

	// Extra selector rows beyond backend/model: profile (always) + archetype
	// (unless hidden for the new-coordinator prompt).
	selectorRows := 1
	if !f.hideArchetype {
		selectorRows++
	}

	// Modal height: border(1) + padding(1) + project(1) + projAC(P) + branch(1) + branchAC(B) + backend(1) + model(1) + profile/archetype(selectorRows) + label(1) + prompt(N) + ac(M) + name(1) + gap(1) + help(1) + padding(1) + border(1)
	modalH := 12 + selectorRows + visiblePromptLines + acRows + projACRows + branchACRows
	if f.errMsg != "" {
		modalH += 2
	}
	if modalH > sh {
		return
	}

	mx := sx + (sw-modalW)/2
	my := sy + (sh-modalH)/2

	// Clear modal area
	modalBG := tcell.ColorDefault
	clearStyle := tcell.StyleDefault.Background(modalBG)
	for row := my; row < my+modalH; row++ {
		for col := mx; col < mx+modalW; col++ {
			screen.SetContent(col, row, ' ', nil, clearStyle)
		}
	}

	// Border
	widget.DrawBorder(screen, mx, my, modalW, modalH, theme.StyleFocusedBorder)

	// Title
	title := f.title
	if title == "" {
		title = " New Task "
	}
	titleX := mx + (modalW-utf8.RuneCountInString(title))/2
	titleStyle := tcell.StyleDefault.Foreground(theme.ColorTitle).Bold(true).Background(modalBG)
	for i, r := range title {
		screen.SetContent(titleX+i, my, r, nil, titleStyle)
	}

	innerX := mx + 2
	row := my + 2

	// Project typeahead
	f.drawProjectField(screen, innerX, row, innerW)
	row++
	if projACRows > 0 {
		f.drawProjectAC(screen, innerX, row, innerW)
		row += projACRows
	}

	// Branch typeahead
	f.drawBranchField(screen, innerX, row, innerW)
	row++
	if branchACRows > 0 {
		f.drawBranchAC(screen, innerX, row, innerW)
		row += branchACRows
	}

	// Backend selector
	f.drawSelector(screen, innerX, row, innerW, "Backend", f.backendNames, f.backendIdx, f.focused == ntFieldBackend)
	row++

	// Model override field
	f.drawModelField(screen, innerX, row, innerW)
	row++

	// Profile selector (per-spawn diligence-profile override; defaults to the
	// project's bound profile).
	f.drawSelector(screen, innerX, row, innerW, "Profile", f.profileOptions, f.profileIdx, f.focused == ntFieldProfile)
	row++

	// Archetype selector — hidden for the new-coordinator prompt.
	if !f.hideArchetype {
		f.drawSelector(screen, innerX, row, innerW, "Archetype", ntArchetypeOptions(), f.archetypeIdx, f.focused == ntFieldArchetype)
		row++
	}

	// Prompt field
	labelStyle := theme.StyleDimmed
	if f.focused == ntFieldPrompt {
		labelStyle = theme.StyleTitle
	}
	widget.DrawText(screen, innerX, row, innerW, "Prompt:", labelStyle)
	row++

	// Prompt input — wrapped across multiple visual lines
	curLine, curCol := f.cursorWrappedPos(innerW)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if f.focused == ntFieldPrompt {
		for vi := 0; vi < visiblePromptLines; vi++ {
			li := vi + f.scrollOffset
			if li >= len(wrappedLines) {
				// Empty line below content
				for col := 0; col < innerW; col++ {
					screen.SetContent(innerX+col, row+vi, ' ', nil, inputEmptyStyle)
				}
				continue
			}
			start := wrappedLines[li].start
			length := wrappedLines[li].length
			for col := 0; col < innerW; col++ {
				var ch rune
				var st tcell.Style
				if col < length {
					ch = f.prompt[start+col]
					st = inputStyle
				} else {
					ch = ' '
					st = inputEmptyStyle
				}
				if li == curLine && col == curCol {
					st = cursorStyle
				}
				screen.SetContent(innerX+col, row+vi, ch, nil, st)
			}
		}
	} else {
		if len(f.prompt) == 0 {
			// Placeholder text with input background
			placeholderStyle := tcell.StyleDefault.Foreground(theme.ColorDimmed).Background(modalBG)
			placeholder := "Prompt for the agent"
			pRunes := []rune(placeholder)
			for col := 0; col < innerW; col++ {
				if col < len(pRunes) {
					screen.SetContent(innerX+col, row, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(innerX+col, row, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			// Render wrapped lines when unfocused too
			unfocusedStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
			for vi := 0; vi < visiblePromptLines; vi++ {
				li := vi + f.scrollOffset
				if li >= len(wrappedLines) {
					break
				}
				start := wrappedLines[li].start
				length := wrappedLines[li].length
				lineStr := string(f.prompt[start : start+length])
				widget.DrawText(screen, innerX, row+vi, innerW, lineStr, unfocusedStyle)
			}
		}
	}
	row += visiblePromptLines

	// Autocomplete dropdown
	if f.acOpen && len(f.acMatches) > 0 {
		f.drawAutocomplete(screen, innerX, row, innerW)
		row += acRows
	}

	// Optional name field (rendered after the prompt + its autocomplete).
	f.drawNameField(screen, innerX, row, innerW)
	row++

	row++ // gap

	// Error message
	if f.errMsg != "" {
		widget.DrawText(screen, innerX, row, innerW, f.errMsg, theme.StyleError)
		row++
	}

	// Help text
	help := "Enter submit  Tab next  Esc cancel"
	widget.DrawText(screen, innerX, row, innerW, help, theme.StyleDimmed)
}

// drawAutocomplete renders the skill suggestion dropdown.
func (f *NewTaskForm) drawAutocomplete(screen tcell.Screen, x, y, w int) {
	end := f.acScroll + acMaxVisible
	if end > len(f.acMatches) {
		end = len(f.acMatches)
	}
	trigger := f.acTrigger()

	// Compute longest skill name for alignment
	maxName := 0
	for i := f.acScroll; i < end; i++ {
		n := utf8.RuneCountInString(trigger + f.acMatches[i].Name)
		if n > maxName {
			maxName = n
		}
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.Color(87))

	for vi, i := 0, f.acScroll; i < end; vi, i = vi+1, i+1 {
		skill := f.acMatches[i]
		isSelected := i == f.acIdx

		indicator := "  "
		if isSelected {
			indicator = "> "
		}

		nameStr := trigger + skill.Name
		plainNameW := utf8.RuneCountInString(nameStr)
		padding := maxName - plainNameW + 2
		if padding < 1 {
			padding = 1
		}

		// Truncate description to fit
		descW := w - utf8.RuneCountInString(indicator) - maxName - 2
		desc := skill.Description
		if descW <= 0 {
			desc = ""
		} else {
			runes := []rune(desc)
			if len(runes) > descW {
				desc = string(runes[:descW-1]) + "…"
			}
		}

		line := indicator + nameStr + strings.Repeat(" ", padding) + desc
		lineRunes := []rune(line)
		for col := 0; col < w && col < len(lineRunes); col++ {
			st := theme.StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			screen.SetContent(x+col, y+vi, lineRunes[col], nil, st)
		}
	}

	// Scroll indicator
	if len(f.acMatches) > acMaxVisible {
		countStr := "  (" + itoa(f.acIdx+1) + "/" + itoa(len(f.acMatches)) + ")"
		widget.DrawText(screen, x, y+end-f.acScroll, w, countStr, theme.StyleDimmed)
	}
}

// drawProjectField renders the project typeahead input field.
func (f *NewTaskForm) drawProjectField(screen tcell.Screen, x, y, w int) {
	focused := f.focused == ntFieldProject
	modalBG := tcell.ColorDefault

	labelStyle := theme.StyleDimmed
	if focused {
		labelStyle = theme.StyleTitle
	}
	label := "Project:"
	labelW := utf8.RuneCountInString(label)
	widget.DrawText(screen, x, y, w, label, labelStyle)

	inputX := x + labelW + 1
	inputW := w - labelW - 1
	if inputW <= 0 {
		return
	}

	inputRow := y
	inputRunes := f.projInput
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if focused {
		for col := 0; col < inputW; col++ {
			var ch rune
			var st tcell.Style
			if col < len(inputRunes) {
				ch = inputRunes[col]
				st = inputStyle
			} else {
				ch = ' '
				st = inputEmptyStyle
			}
			if col == f.projCursorPos {
				st = cursorStyle
			}
			screen.SetContent(inputX+col, inputRow, ch, nil, st)
		}
	} else {
		if len(inputRunes) == 0 {
			placeholderStyle := tcell.StyleDefault.Foreground(theme.ColorDimmed).Background(modalBG)
			placeholder := "Type to search..."
			pRunes := []rune(placeholder)
			for col := 0; col < inputW; col++ {
				if col < len(pRunes) {
					screen.SetContent(inputX+col, inputRow, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(inputX+col, inputRow, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			unfocusedStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
			widget.DrawText(screen, inputX, inputRow, inputW, string(inputRunes), unfocusedStyle)
		}
	}
}

// drawProjectAC renders the project autocomplete dropdown.
func (f *NewTaskForm) drawProjectAC(screen tcell.Screen, x, y, w int) {
	end := f.projACScroll + acMaxVisible
	if end > len(f.projACMatches) {
		end = len(f.projACMatches)
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.Color(87))

	for vi, i := 0, f.projACScroll; i < end; vi, i = vi+1, i+1 {
		name := f.projACMatches[i]
		isSelected := i == f.projACIdx

		indicator := "  "
		if isSelected {
			indicator = "> "
		}

		line := indicator + name
		lineRunes := []rune(line)
		for col := 0; col < w && col < len(lineRunes); col++ {
			st := theme.StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			screen.SetContent(x+col, y+vi, lineRunes[col], nil, st)
		}
	}

	// Scroll indicator
	if len(f.projACMatches) > acMaxVisible {
		countStr := "  (" + itoa(f.projACIdx+1) + "/" + itoa(len(f.projACMatches)) + ")"
		widget.DrawText(screen, x, y+end-f.projACScroll, w, countStr, theme.StyleDimmed)
	}
}

// drawBranchField renders the branch typeahead input field.
func (f *NewTaskForm) drawBranchField(screen tcell.Screen, x, y, w int) {
	focused := f.focused == ntFieldBranch
	modalBG := tcell.ColorDefault

	labelStyle := theme.StyleDimmed
	if focused {
		labelStyle = theme.StyleTitle
	}
	label := "Branch:"
	labelW := utf8.RuneCountInString(label)
	widget.DrawText(screen, x, y, w, label, labelStyle)

	inputX := x + labelW + 1
	inputW := w - labelW - 1
	if inputW <= 0 {
		return
	}

	inputRow := y
	inputRunes := f.branchInput
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if focused {
		for col := 0; col < inputW; col++ {
			var ch rune
			var st tcell.Style
			if col < len(inputRunes) {
				ch = inputRunes[col]
				st = inputStyle
			} else {
				ch = ' '
				st = inputEmptyStyle
			}
			if col == f.branchCursorPos {
				st = cursorStyle
			}
			screen.SetContent(inputX+col, inputRow, ch, nil, st)
		}
	} else {
		if len(inputRunes) == 0 {
			placeholderStyle := tcell.StyleDefault.Foreground(theme.ColorDimmed).Background(modalBG)
			placeholder := "default"
			pRunes := []rune(placeholder)
			for col := 0; col < inputW; col++ {
				if col < len(pRunes) {
					screen.SetContent(inputX+col, inputRow, pRunes[col], nil, placeholderStyle)
				} else {
					screen.SetContent(inputX+col, inputRow, ' ', nil, inputEmptyStyle)
				}
			}
		} else {
			unfocusedStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
			widget.DrawText(screen, inputX, inputRow, inputW, string(inputRunes), unfocusedStyle)
		}
	}
}

// drawNameField renders the optional task-name input. Single-line, mirroring
// the branch field (caret when focused, dim "(optional)" placeholder when empty
// and unfocused). No autocomplete.
func (f *NewTaskForm) drawNameField(screen tcell.Screen, x, y, w int) {
	focused := f.focused == ntFieldName
	modalBG := tcell.ColorDefault

	labelStyle := theme.StyleDimmed
	if focused {
		labelStyle = theme.StyleTitle
	}
	label := "Name:"
	labelW := utf8.RuneCountInString(label)
	widget.DrawText(screen, x, y, w, label, labelStyle)

	inputX := x + labelW + 1
	inputW := w - labelW - 1
	if inputW <= 0 {
		return
	}

	inputRunes := f.nameInput
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if focused {
		for col := range inputW {
			var ch rune
			var st tcell.Style
			if col < len(inputRunes) {
				ch = inputRunes[col]
				st = inputStyle
			} else {
				ch = ' '
				st = inputEmptyStyle
			}
			if col == f.nameCursorPos {
				st = cursorStyle
			}
			screen.SetContent(inputX+col, y, ch, nil, st)
		}
		return
	}

	if len(inputRunes) == 0 {
		placeholderStyle := tcell.StyleDefault.Foreground(theme.ColorDimmed).Background(modalBG)
		placeholder := []rune("(optional)")
		for col := range inputW {
			if col < len(placeholder) {
				screen.SetContent(inputX+col, y, placeholder[col], nil, placeholderStyle)
			} else {
				screen.SetContent(inputX+col, y, ' ', nil, inputEmptyStyle)
			}
		}
		return
	}
	unfocusedStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	widget.DrawText(screen, inputX, y, inputW, string(inputRunes), unfocusedStyle)
}

// drawModelField renders the per-backend model selector. Non-custom entries
// render as a "◀ value ▶" selector (matching the backend selector); the
// "default" entry surfaces the backend's configured default model as a dim
// hint so the user sees what an empty override resolves to. The "custom…" entry
// renders the selector value followed by an inline free-text input for a model
// the list does not name.
func (f *NewTaskForm) drawModelField(screen tcell.Screen, x, y, w int) {
	focused := f.focused == ntFieldModel
	modalBG := tcell.ColorDefault

	labelStyle := theme.StyleDimmed
	if focused {
		labelStyle = theme.StyleTitle
	}
	label := "Model:"
	labelW := utf8.RuneCountInString(label)
	widget.DrawText(screen, x, y, w, label, labelStyle)

	valX := x + labelW + 1
	valW := w - labelW - 1
	if valW <= 0 {
		return
	}

	selector := "◀ " + f.modelDisplayLabel() + " ▶"
	selectorStyle := theme.StyleNormal
	if focused {
		selectorStyle = theme.StyleSelected
	}
	widget.DrawText(screen, valX, y, valW, selector, selectorStyle)
	selectorW := utf8.RuneCountInString(selector)

	if f.modelIsCustom() {
		// Inline free-text input after the selector value.
		inputX := valX + selectorW + 1
		inputW := valW - selectorW - 1
		if inputW > 0 {
			f.drawModelCustomInput(screen, inputX, y, inputW, focused, modalBG)
		}
		return
	}

	// "default" entry: hint at what an empty override resolves to.
	if f.modelIdx == 0 {
		if m := f.backendDefaultModel(); m != "" {
			hint := "→ " + m
			hintX := valX + selectorW + 1
			hintW := valW - selectorW - 1
			if hintW > 0 {
				widget.DrawText(screen, hintX, y, hintW, hint, theme.StyleDimmed)
			}
		}
	}
}

// drawModelCustomInput renders the inline free-text model input shown when the
// "custom…" entry is selected.
func (f *NewTaskForm) drawModelCustomInput(screen tcell.Screen, x, y, w int, focused bool, modalBG tcell.Color) {
	inputRunes := f.modelInput
	inputEmptyStyle := tcell.StyleDefault.Background(modalBG)
	inputStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	cursorStyle := tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.Color252)

	if focused {
		for col := range w {
			var ch rune
			var st tcell.Style
			if col < len(inputRunes) {
				ch = inputRunes[col]
				st = inputStyle
			} else {
				ch = ' '
				st = inputEmptyStyle
			}
			if col == f.modelCursorPos {
				st = cursorStyle
			}
			screen.SetContent(x+col, y, ch, nil, st)
		}
		return
	}

	if len(inputRunes) == 0 {
		placeholderStyle := tcell.StyleDefault.Foreground(theme.ColorDimmed).Background(modalBG)
		placeholder := []rune("type a model")
		for col := range w {
			if col < len(placeholder) {
				screen.SetContent(x+col, y, placeholder[col], nil, placeholderStyle)
			} else {
				screen.SetContent(x+col, y, ' ', nil, inputEmptyStyle)
			}
		}
		return
	}
	unfocusedStyle := tcell.StyleDefault.Foreground(theme.ColorNormal).Background(modalBG)
	widget.DrawText(screen, x, y, w, string(inputRunes), unfocusedStyle)
}

// drawBranchAC renders the branch autocomplete dropdown.
func (f *NewTaskForm) drawBranchAC(screen tcell.Screen, x, y, w int) {
	end := f.branchACScroll + acMaxVisible
	if end > len(f.branchACMatches) {
		end = len(f.branchACMatches)
	}

	selectedStyle := tcell.StyleDefault.Bold(true).Foreground(tcell.Color(87))

	for vi, i := 0, f.branchACScroll; i < end; vi, i = vi+1, i+1 {
		name := f.branchACMatches[i]
		isSelected := i == f.branchACIdx

		indicator := "  "
		if isSelected {
			indicator = "> "
		}

		line := indicator + name
		lineRunes := []rune(line)
		for col := 0; col < w && col < len(lineRunes); col++ {
			st := theme.StyleDimmed
			if isSelected {
				st = selectedStyle
			}
			screen.SetContent(x+col, y+vi, lineRunes[col], nil, st)
		}
	}

	// Scroll indicator
	if len(f.branchACMatches) > acMaxVisible {
		countStr := "  (" + itoa(f.branchACIdx+1) + "/" + itoa(len(f.branchACMatches)) + ")"
		widget.DrawText(screen, x, y+end-f.branchACScroll, w, countStr, theme.StyleDimmed)
	}
}

func (f *NewTaskForm) drawSelector(screen tcell.Screen, x, y, w int, label string, names []string, idx int, focused bool) {
	labelStyle := theme.StyleDimmed
	if focused {
		labelStyle = theme.StyleTitle
	}
	widget.DrawText(screen, x, y, w, label+":", labelStyle)

	if len(names) == 0 {
		widget.DrawText(screen, x+len(label)+2, y, w-len(label)-2, "(none)", theme.StyleDimmed)
		return
	}

	name := names[idx]
	selector := "◀ " + name + " ▶"
	selectorStyle := theme.StyleNormal
	if focused {
		selectorStyle = theme.StyleSelected
	}
	widget.DrawText(screen, x+len(label)+2, y, w-len(label)-2, selector, selectorStyle)

	// Position indicator
	posText := "(" + itoa(idx+1) + "/" + itoa(len(names)) + ")"
	posX := x + w - len(posText)
	if posX > x+len(label)+2+len(selector)+1 {
		widget.DrawText(screen, posX, y, len(posText), posText, theme.StyleDimmed)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
