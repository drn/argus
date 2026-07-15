package keymap

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/testutil"
)

func ev(k tcell.Key, r rune, m tcell.ModMask) *tcell.EventKey {
	return tcell.NewEventKey(k, r, m)
}

// TestDefaultsParse + completeness: every default parses, is allowed in its
// context, and is unique within the context.
func TestDefaultsParseAndUnique(t *testing.T) {
	for _, ctx := range AllContexts {
		seen := map[Binding]Action{}
		for act, spec := range defaultSpecs[ctx] {
			b, err := Parse(spec)
			if err != nil {
				t.Fatalf("%s/%s default %q: %v", ctx, act, spec, err)
			}
			if msg, ok := bindingAllowed(ctx, b); !ok {
				t.Fatalf("%s/%s default %q not allowed: %s", ctx, act, spec, msg)
			}
			if other, dup := seen[b]; dup {
				t.Fatalf("%s: default %q binds %s and %s to the same key", ctx, spec, act, other)
			}
			seen[b] = act
		}
	}
}

// Every action present in contextOrder must have a default + a label, and vice
// versa every default must appear in contextOrder (so help renders it).
func TestInventoryConsistency(t *testing.T) {
	for _, ctx := range AllContexts {
		inOrder := map[Action]bool{}
		for _, act := range contextOrder[ctx] {
			inOrder[act] = true
			if _, ok := defaultSpecs[ctx][act]; !ok {
				t.Errorf("%s: contextOrder lists %s with no default", ctx, act)
			}
			if _, ok := actionLabels[act]; !ok {
				t.Errorf("%s: %s has no label", ctx, act)
			}
		}
		for act := range defaultSpecs[ctx] {
			if !inOrder[act] {
				t.Errorf("%s: default %s missing from contextOrder (won't show in help)", ctx, act)
			}
		}
	}
}

func TestResolve_Defaults(t *testing.T) {
	km := DefaultKeymap()
	tests := []struct {
		ctx  Context
		ev   *tcell.EventKey
		want Action
	}{
		{CtxGlobal, ev(tcell.KeyRune, 'q', 0), ActGlobalQuit},
		{CtxGlobal, ev(tcell.KeyRune, '?', 0), ActGlobalHelp},
		{CtxGlobal, ev(tcell.KeyCtrlF, 0, 0), ActGlobalFork},
		{CtxTaskList, ev(tcell.KeyRune, 'n', 0), ActTaskNew},
		{CtxTaskList, ev(tcell.KeyRune, 'S', 0), ActTaskStatusRev},
		{CtxAgent, ev(tcell.KeyCtrlL, 0, 0), ActAgentLinks},
		{CtxAgent, ev(tcell.KeyCtrlZ, 0, 0), ActAgentZoom},
		{CtxAgent, ev(tcell.KeyUp, 0, tcell.ModCtrl|tcell.ModAlt), ActAgentTaskPrev},
		{CtxAgent, ev(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt), ActAgentPaneLeft},
		{CtxAgent, ev(tcell.KeyDown, 0, tcell.ModShift), ActAgentScrollDown},
		{CtxHeraRail, ev(tcell.KeyRune, 'w', 0), ActHeraSpawn},
		{CtxHeraRail, ev(tcell.KeyCtrlD, 0, 0), ActHeraDelete},
		{CtxDiff, ev(tcell.KeyRune, 's', 0), ActDiffSplit},
		{CtxDiff, ev(tcell.KeyRune, 'f', 0), ActDiffFinder},
		{CtxDiff, ev(tcell.KeyRune, 'o', 0), ActDiffOpen},
		{CtxDiff, ev(tcell.KeyRune, 'e', 0), ActDiffEditor},
		{CtxDiff, ev(tcell.KeyRune, 't', 0), ActDiffTerminal},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			got, ok := km.Resolve(tt.ctx, tt.ev)
			testutil.True(t, ok)
			testutil.Equal(t, got, tt.want)
		})
	}
}

func TestResolve_LooseArrowVsPlain(t *testing.T) {
	km := DefaultKeymap()
	// A plain Up (no modifier) must NOT resolve to the cmd+up action.
	_, ok := km.Resolve(CtxAgent, ev(tcell.KeyUp, 0, 0))
	testutil.False(t, ok)
	// cmd+up (mod-7) resolves; shift+up resolves to a different action.
	prev, ok := km.Resolve(CtxAgent, ev(tcell.KeyUp, 0, tcell.ModCtrl|tcell.ModAlt))
	testutil.True(t, ok)
	testutil.Equal(t, prev, ActAgentTaskPrev)
	scroll, ok := km.Resolve(CtxAgent, ev(tcell.KeyUp, 0, tcell.ModShift))
	testutil.True(t, ok)
	testutil.Equal(t, scroll, ActAgentScrollUp)
}

func TestResolve_Unbound(t *testing.T) {
	km := DefaultKeymap()
	_, ok := km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'z', 0))
	testutil.False(t, ok)
}

func TestBuild_OverrideApplied(t *testing.T) {
	kb := config.Keybindings{TaskList: map[string]string{"new": "x"}}
	km, warns := Build(kb)
	testutil.Equal(t, len(warns), 0)
	// New key fires.
	got, ok := km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'x', 0))
	testutil.True(t, ok)
	testutil.Equal(t, got, ActTaskNew)
	// Old key no longer maps to new-task.
	_, ok = km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'n', 0))
	testutil.False(t, ok)
}

func TestBuild_InvalidKeyspecKeepsDefault(t *testing.T) {
	kb := config.Keybindings{TaskList: map[string]string{"new": "ctrl+/"}}
	km, warns := Build(kb)
	testutil.Equal(t, len(warns), 1)
	testutil.Contains(t, warns[0].Message, "invalid keyspec")
	got, ok := km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'n', 0))
	testutil.True(t, ok)
	testutil.Equal(t, got, ActTaskNew)
}

func TestBuild_AgentBareRuneRejected(t *testing.T) {
	kb := config.Keybindings{Agent: map[string]string{"links": "z"}}
	km, warns := Build(kb)
	testutil.Equal(t, len(warns), 1)
	testutil.Contains(t, warns[0].Message, "modifier")
	// Default ctrl+l survives; bare z does nothing in agent ctx.
	got, ok := km.Resolve(CtxAgent, ev(tcell.KeyCtrlL, 0, 0))
	testutil.True(t, ok)
	testutil.Equal(t, got, ActAgentLinks)
	_, ok = km.Resolve(CtxAgent, ev(tcell.KeyRune, 'z', 0))
	testutil.False(t, ok)
}

func TestBuild_StructuralKeyRejected(t *testing.T) {
	for _, spec := range []string{"enter", "esc", "tab", "backtab"} {
		kb := config.Keybindings{TaskList: map[string]string{"new": spec}}
		_, warns := Build(kb)
		if len(warns) != 1 {
			t.Fatalf("spec %q: want 1 warning, got %d", spec, len(warns))
		}
		testutil.Contains(t, warns[0].Message, "reserved")
	}
}

func TestBuild_PlainNavKeyRejected(t *testing.T) {
	// Plain arrows AND plain page/home/end keys are reserved everywhere, because
	// the fast-path lookup strips modifiers (a plain binding would also capture
	// the cmd/shift variant).
	for _, spec := range []string{"up", "down", "pgdn", "pgup", "home", "end"} {
		kb := config.Keybindings{Global: map[string]string{"tab_tasks": spec}}
		_, warns := Build(kb)
		if len(warns) != 1 {
			t.Fatalf("spec %q: want 1 warning, got %d", spec, len(warns))
		}
		testutil.Contains(t, warns[0].Message, "navigation key")
	}
}

func TestBuild_ReservedContextKeyRejected(t *testing.T) {
	// `q` is structural (exit) in the diff view; `h`/`l` are structural focus in
	// settings. Binding an action onto them would be silently shadowed, so reject.
	cases := []struct {
		kb   config.Keybindings
		ctx  Context
		dflt Binding
	}{
		{config.Keybindings{Diff: map[string]string{"split": "q"}}, CtxDiff, Binding{Key: tcell.KeyRune, Rune: 's'}},
		{config.Keybindings{Settings: map[string]string{"edit": "h"}}, CtxSettings, Binding{Key: tcell.KeyRune, Rune: 'e'}},
		{config.Keybindings{Settings: map[string]string{"edit": "l"}}, CtxSettings, Binding{Key: tcell.KeyRune, Rune: 'e'}},
	}
	for _, c := range cases {
		km, warns := Build(c.kb)
		testutil.Equal(t, len(warns), 1)
		testutil.Contains(t, warns[0].Message, "reserved as structural")
		// Default survived.
		if c.ctx == CtxDiff {
			got, _ := km.Resolve(CtxDiff, ev(tcell.KeyRune, 's', 0))
			testutil.Equal(t, got, ActDiffSplit)
		} else {
			got, _ := km.Resolve(CtxSettings, ev(tcell.KeyRune, 'e', 0))
			testutil.Equal(t, got, ActSettingsEdit)
		}
	}
}

func TestBuild_CtrlLetterOverrideHonored(t *testing.T) {
	// A ctrl-letter override in a rune-default context must resolve (the dispatch
	// sites now resolve for all key types, not just runes).
	km, warns := Build(config.Keybindings{TaskList: map[string]string{"new": "ctrl+n"}})
	testutil.Equal(t, len(warns), 0)
	got, ok := km.Resolve(CtxTaskList, ev(tcell.KeyCtrlN, 0, 0))
	testutil.True(t, ok)
	testutil.Equal(t, got, ActTaskNew)
}

func TestBuild_ConflictKeepsDefault(t *testing.T) {
	// Rebind copy onto 'n', which is already new-task's key.
	kb := config.Keybindings{TaskList: map[string]string{"copy": "n"}}
	km, warns := Build(kb)
	testutil.Equal(t, len(warns), 1)
	testutil.Contains(t, warns[0].Message, "conflicts")
	// new-task keeps 'n'; copy keeps its default 'c'.
	got, _ := km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'n', 0))
	testutil.Equal(t, got, ActTaskNew)
	got, _ = km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'c', 0))
	testutil.Equal(t, got, ActTaskCopy)
}

func TestBuild_UnknownActionIgnored(t *testing.T) {
	kb := config.Keybindings{TaskList: map[string]string{"bogus": "x"}}
	_, warns := Build(kb)
	testutil.Equal(t, len(warns), 0)
}

func TestBuild_EmptySpecIgnored(t *testing.T) {
	kb := config.Keybindings{TaskList: map[string]string{"new": "   "}}
	km, warns := Build(kb)
	testutil.Equal(t, len(warns), 0)
	got, _ := km.Resolve(CtxTaskList, ev(tcell.KeyRune, 'n', 0))
	testutil.Equal(t, got, ActTaskNew)
}

func TestWarning_String(t *testing.T) {
	w := Warning{Context: CtxTaskList, Action: ActTaskNew, Message: "boom"}
	testutil.True(t, strings.Contains(w.String(), "tasklist.new"))
}

func TestKeymap_ActionLabel(t *testing.T) {
	km := DefaultKeymap()
	testutil.Equal(t, km.ActionLabel(ActTaskNew), "new task")
	// Unknown action falls back to its id.
	testutil.Equal(t, km.ActionLabel(Action("nope.x")), "nope.x")
}

func TestKeymap_Bindings(t *testing.T) {
	km := DefaultKeymap()
	b := km.Bindings(CtxTaskList)
	got, ok := b[ActTaskNew]
	testutil.True(t, ok)
	testutil.Equal(t, got, Binding{Key: tcell.KeyRune, Rune: 'n'})
}

func TestKeymap_HelpRows(t *testing.T) {
	km := DefaultKeymap()
	rows := km.HelpRows(CtxTaskList)
	testutil.True(t, len(rows) > 0)
	// First row follows contextOrder (ActTaskNew) and carries key + label.
	testutil.Equal(t, rows[0].Key, "n")
	testutil.Equal(t, rows[0].Label, "new task")
	// An override is reflected in the rows.
	km2, _ := Build(config.Keybindings{TaskList: map[string]string{"new": "x"}})
	testutil.Equal(t, km2.HelpRows(CtxTaskList)[0].Key, "x")
}

func TestOverridesFor_AllContexts(t *testing.T) {
	kb := config.Keybindings{
		Global:    map[string]string{"quit": "Q"},
		TaskList:  map[string]string{"new": "x"},
		Agent:     map[string]string{"zoom": "ctrl+w"},
		FilePanel: map[string]string{"open": "O"},
		Diff:      map[string]string{"split": "u"},
		Settings:  map[string]string{"edit": "E"},
		HeraRail:  map[string]string{"spawn_worker": "W"},
	}
	for _, ctx := range AllContexts {
		testutil.NotNil(t, overridesFor(kb, ctx))
	}
	testutil.Nil(t, overridesFor(kb, Context("bogus")))
}

func TestBinding_StringFallback(t *testing.T) {
	// A named special key (no ctrl-letter, no rune) renders its name; an unknown
	// key falls back to key(N).
	testutil.Equal(t, (Binding{Key: tcell.KeyHome}).String(), "home")
	testutil.Contains(t, (Binding{Key: tcell.KeyF5}).String(), "key(")
}
