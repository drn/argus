// Package keymap is the single source of truth for argus's own TUI keybindings.
//
// It owns the default bindings (mirroring the historical hardcoded literals),
// parses user keyspec overrides from config.Keybindings, validates them, and
// answers Resolve(context, event) → Action at every dispatch site. It depends
// only on tcell and internal/config so it stays free of import cycles and is
// unit-testable in isolation.
//
// Keyspec grammar (see Parse): zero or more modifiers joined by '+', then a
// base key — a single printable rune or a named key. Modifiers: ctrl/control,
// cmd/opt/alt (the Ctrl+Alt mod-7 convention, only on arrows), shift (only on
// arrows). Examples: "n", "?", "J", "/", "space", "ctrl+l", "cmd+up",
// "shift+down".
//
// Reserved/structural keys are NOT routed through the keymap and cannot be
// rebound: Esc/Enter/Tab/Backtab, Ctrl+C / Ctrl+Q (failsafe), plain navigation
// keys (arrows + pgup/pgdn/home/end), and per-context structural runes (diff q,
// settings h/l) listed in reservedContextKeys. Agent-context bindings must carry
// a modifier so plain typing still reaches the agent PTY.
package keymap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
)

// Binding is a parsed key chord. Key is KeyRune for a printable rune (with Rune
// set), or a named/control key constant otherwise. Mods is non-zero only for
// modified arrows (ModCtrl|ModAlt for cmd, ModShift for shift).
type Binding struct {
	Key  tcell.Key
	Rune rune
	Mods tcell.ModMask
}

// Matches reports whether a tcell key event is this binding. It mirrors how the
// historical literal dispatch inspected events: rune bindings compare the rune
// (shift already baked in); ctrl-letter / plain named keys compare the key only;
// modified arrows use a LOOSE modifier test (ev has the modifier bit set) to
// match the Ctrl+Alt (cmd) and Shift conventions byte-for-byte.
func (b Binding) Matches(ev *tcell.EventKey) bool {
	if b.Key == tcell.KeyRune {
		return ev.Key() == tcell.KeyRune && ev.Rune() == b.Rune
	}
	if ev.Key() != b.Key {
		return false
	}
	switch {
	case b.Mods&(tcell.ModCtrl|tcell.ModAlt) != 0:
		return ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0
	case b.Mods&tcell.ModShift != 0:
		return ev.Modifiers()&tcell.ModShift != 0
	default:
		return true
	}
}

// Warning is a non-fatal problem found while building a keymap (an invalid or
// rejected override). The default binding is always kept; the warning is logged
// by the caller. A bad config never bricks the TUI.
type Warning struct {
	Context Context
	Action  Action
	Message string
}

func (w Warning) String() string {
	return fmt.Sprintf("[keymap] %s/%s: %s", w.Context, w.Action, w.Message)
}

// Keymap holds the resolved bindings per context plus reverse indexes for
// Resolve. It is immutable after Build — rebuild on config change rather than
// mutating in place.
type Keymap struct {
	fwd    map[Context]map[Action]Binding
	rev    map[Context]map[Binding]Action // non-arrow (exact match)
	arrows map[Context][]arrowBind        // modified arrows (loose match)
}

type arrowBind struct {
	b Binding
	a Action
}

// DefaultKeymap returns the built-in bindings with no user overrides.
func DefaultKeymap() *Keymap {
	km, _ := Build(config.Keybindings{})
	return km
}

// Build resolves the default bindings overlaid by the user's config overrides,
// validating each override and returning any non-fatal warnings. Defaults are
// placed first (they are unique within a context); overrides are then applied
// deterministically, each kept only if it parses, is allowed in its context,
// and does not collide with an already-placed binding — otherwise the default
// survives.
func Build(kb config.Keybindings) (*Keymap, []Warning) {
	km := &Keymap{
		fwd:    make(map[Context]map[Action]Binding, len(AllContexts)),
		rev:    make(map[Context]map[Binding]Action, len(AllContexts)),
		arrows: make(map[Context][]arrowBind, len(AllContexts)),
	}
	var warns []Warning

	for _, ctx := range AllContexts {
		km.fwd[ctx] = make(map[Action]Binding)
		km.rev[ctx] = make(map[Binding]Action)

		// 1. Place defaults. They are unique within a context (pinned by a test),
		//    so the `used` set never collides here.
		used := make(map[Binding]Action)
		for act, spec := range defaultSpecs[ctx] {
			b, err := Parse(spec)
			if err != nil {
				// Impossible in a tested build (TestDefaultsParse pins it); skip
				// defensively rather than panic at runtime.
				warns = append(warns, Warning{ctx, act, fmt.Sprintf("default %q failed to parse: %v", spec, err)})
				continue
			}
			km.fwd[ctx][act] = b
			used[b] = act
		}

		// 2. Apply overrides deterministically (sorted by action id).
		ovr := overridesFor(kb, ctx)
		for _, id := range sortedKeys(ovr) {
			spec := strings.TrimSpace(ovr[id])
			if spec == "" {
				continue
			}
			act := Action(string(ctx) + "." + id)
			if _, known := defaultSpecs[ctx][act]; !known {
				// Unknown action id — ignore silently (forward-compatible, like
				// the lenient TOML overlay).
				continue
			}
			b, err := Parse(spec)
			if err != nil {
				warns = append(warns, Warning{ctx, act, fmt.Sprintf("invalid keyspec %q: %v (keeping default)", spec, err)})
				continue
			}
			if msg, ok := bindingAllowed(ctx, b); !ok {
				warns = append(warns, Warning{ctx, act, fmt.Sprintf("%q %s (keeping default)", spec, msg)})
				continue
			}
			cur := km.fwd[ctx][act]
			delete(used, cur)
			if other, taken := used[b]; taken {
				warns = append(warns, Warning{ctx, act, fmt.Sprintf("%q conflicts with %s (keeping default)", spec, other)})
				used[cur] = act // restore the default we tentatively removed
				continue
			}
			km.fwd[ctx][act] = b
			used[b] = act
		}

		// 3. Build reverse indexes from the resolved forward map.
		for act, b := range km.fwd[ctx] {
			if b.Mods != 0 {
				// Modified nav keys need loose-modifier matching at Resolve time.
				km.arrows[ctx] = append(km.arrows[ctx], arrowBind{b, act})
			} else {
				km.rev[ctx][b] = act
			}
		}
	}
	return km, warns
}

// Resolve returns the action bound to a key event in the given context, or
// ("", false) when nothing is bound (the caller falls through to its structural
// / PTY path). Hot path: O(1) map hit for runes and ctrl-letters; a tiny linear
// scan over the context's modified-arrow bindings for the loose-match cases.
func (m *Keymap) Resolve(ctx Context, ev *tcell.EventKey) (Action, bool) {
	if rev := m.rev[ctx]; rev != nil {
		if a, ok := rev[eventBinding(ev)]; ok {
			return a, true
		}
	}
	for _, ab := range m.arrows[ctx] {
		if ab.b.Matches(ev) {
			return ab.a, true
		}
	}
	return "", false
}

// Bindings returns the resolved Action→Binding map for a context (for help
// rendering). The returned map must not be mutated.
func (m *Keymap) Bindings(ctx Context) map[Action]Binding {
	return m.fwd[ctx]
}

// eventBinding canonicalizes an event for the fast-path reverse lookup. Modifier
// bits are dropped: only mod-less bindings (runes, ctrl-letters, plain named
// keys) live in the rev map; modified arrows are matched loosely via the arrows
// slice, so a cmd+up / shift+up event misses the map here and falls to the scan.
func eventBinding(ev *tcell.EventKey) Binding {
	if ev.Key() == tcell.KeyRune {
		return Binding{Key: tcell.KeyRune, Rune: ev.Rune()}
	}
	return Binding{Key: ev.Key()}
}

// reservedContextKeys are per-context keys that the dispatch site handles with a
// literal branch (so they stay structural and cannot be rebound). An override
// onto one of these would be silently shadowed by the literal, so it is rejected
// at build time instead — the user gets a warning and keeps the default.
var reservedContextKeys = map[Context][]Binding{
	CtxDiff:     {{Key: tcell.KeyRune, Rune: 'q'}},                                  // exit diff
	CtxSettings: {{Key: tcell.KeyRune, Rune: 'h'}, {Key: tcell.KeyRune, Rune: 'l'}}, // focus rail / pane
}

// bindingAllowed enforces the rebinding limits. Returns ("", true) when allowed,
// or (reason, false) to reject (keep the default).
func bindingAllowed(ctx Context, b Binding) (string, bool) {
	// Plain (unmodified) navigation keys are reserved in every context: the
	// fast-path reverse lookup strips modifiers (eventBinding), so a plain
	// binding here would also capture the cmd/shift-modified variant of the same
	// key — e.g. a plain `pgdn` binding would swallow `shift+pgdn` scrollback.
	if isModifiable(b.Key) && b.Mods == 0 {
		return "binds a plain navigation key (reserved)", false
	}
	switch b.Key {
	case tcell.KeyEnter, tcell.KeyEscape, tcell.KeyTab, tcell.KeyBacktab, tcell.KeyCtrlC, tcell.KeyCtrlQ:
		return "binds a reserved structural/failsafe key", false
	}
	for _, rb := range reservedContextKeys[ctx] {
		if b == rb {
			return "binds a key reserved as structural in this context", false
		}
	}
	if ctx == CtxAgent {
		_, isCtrlLetter := ctrlLetters[b.Key]
		if b.Key == tcell.KeyRune || (!isCtrlLetter && b.Mods == 0) {
			return "needs a modifier (ctrl/cmd/shift) in the agent view to avoid stealing PTY input", false
		}
	}
	return "", true
}

func overridesFor(kb config.Keybindings, ctx Context) map[string]string {
	switch ctx {
	case CtxGlobal:
		return kb.Global
	case CtxTaskList:
		return kb.TaskList
	case CtxAgent:
		return kb.Agent
	case CtxFilePnl:
		return kb.FilePanel
	case CtxDiff:
		return kb.Diff
	case CtxSettings:
		return kb.Settings
	case CtxHeraRail:
		return kb.HeraRail
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
