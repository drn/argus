# Design: plugin key surrender

## Context

Argus hosts plugin views: full-screen UI surfaces a plugin registers via `POST /api/plugins/views`, mounted in the TUI as a `tview.Page` and fed over a WebSocket. The plugin streams ANSI frames in; argus forwards keystrokes back out. hera-view (a TUI with rail / coord / agent panes, itself running a Claude PTY) is the motivating plugin.

The operator reported that Ctrl/Cmd-arrow never reaches hera-view: hera wants Ctrl-→/← to move focus across its panes, but the keys never arrive. The framing assumed argus was intercepting those keys for its own navigation.

Investigation of this branch (`argus/plugin-substrate`) shows the real mechanism is narrower:

- **Routing already surrenders.** In `internal/tui/app.go:handleGlobalKey` (around line 1517), when `a.mode == modePluginView`, argus intercepts only `Esc` (calls `deactivatePluginView`) and returns every other key as an event, which tview routes to the focused pane. Argus's own nav bindings (e.g. agent-view `Ctrl|Alt + Left/Right` to move the focus rail, `app.go:1820-1831`) are gated on `modeAgent`, so they do not fire in `modePluginView`.
- **The encoder drops the keys.** The focused pane forwards keystrokes to the plugin by mapping each `tcell.EventKey` to bytes via `eventBytes()`. There are two near-identical copies — `internal/tui/terminalpane/terminalpane.go:289` (full-screen plugin views) and `internal/tui/streampane/streampane.go:266` (settings stream sections) — each a tiny allowlist covering only Rune, Enter, Tab, Backspace, and Escape. Arrows, Ctrl/Shift/Alt-arrow, function keys, Home/End/PgUp/PgDn all fall through to `return nil` and are silently dropped before they ever reach the WebSocket. A third encoder, `app.go:tcellKeyToBytes` (the agent PTY path), is richer but still has no Ctrl-arrow case, so it flattens `Ctrl+Right` to a plain `\x1b[C`.

The operator confirmed the keys reach argus: in the agent view, `Ctrl|Cmd + Right` selects the Files rail today, which only works because tcell delivers the modified arrow and argus binds it. So the key is delivered and forwardable; it is the pane encoder that throws it away.

A second, related problem: the bottom status bar (`internal/tui/widget/statusbar.go`) chooses its hints purely from the active tab (Tasks / DAG / Settings). It has no concept of a plugin owning the keyboard, so while a plugin view is active it shows stale argus hints whose keys are (correctly) being surrendered to the plugin and will not do what the bar claims.

The substrate already has the seams needed to fix this cleanly. The WebSocket connector (`internal/tui/views/connector.go`) sends `resize` / `focus` / `blur` JSON control envelopes argus→plugin, and its read pump currently **discards** plugin→argus text frames ("reserves them for argus→plugin"). That discard path is the natural extension point for plugin→argus control envelopes.

## Goals / Non-Goals

**Goals:**

- Formalize a "who has the ball" model: while a plugin view is focused, the plugin owns the keyboard and argus forwards every key faithfully, including Ctrl/Shift/Alt-arrow, function keys, and Ctrl-combos.
- Fix the encoder so modified keys are encoded with standard xterm control sequences instead of being dropped or flattened — via a single shared encoder used by all three current call sites.
- Let the plugin hand control back to argus cleanly (plugin-initiated release) and guarantee argus can always reclaim the keyboard even if the plugin misbehaves (failsafe).
- Formalize a plugin **hotkey dictionary** the plugin pushes to argus: the full set of the plugin's hotkeys, each flagged for whether it appears in the bottom bar. Argus renders the bar-flagged subset in a context-sensitive bottom bar (with a non-overridable "return to argus" hint always present) and renders the full set in argus's `?` help overlay when the plugin asks for it.

**Non-Goals:**

- Cmd-specific decoding. macOS terminals do not send a raw "Command" modifier to TTY apps, but the operator's iTerm2 is configured to **remap Cmd+arrow into standard xterm CSI sequences** — `⌘→` emits `\x1b[1;7C`, `⌘←` → `\x1b[1;7D`, `⌘↑` → `\x1b[1;7A`, `⌘↓` → `\x1b[1;7B` (modifier param `7` = `1 + Alt(2) + Ctrl(4)`, i.e. Ctrl+Alt). tcell decodes those to `KeyRight/Left/Up/Down` with `ModCtrl|ModAlt`, which is exactly what argus's agent-view nav binds today (`app.go:1829`). So Cmd+arrow already reaches argus — as a Ctrl+Alt+arrow event — and this change supports it the same way it supports any other modified arrow: **the shared encoder must faithfully round-trip the `\x1b[1;7<final>]` form** so hera receives the identical bytes it would have received directly. Argus adds no Command-specific handling; correct Ctrl/Shift/Alt encoding is sufficient, and the terminal-side remap is the operator's responsibility (documented, not owned by argus).
- Hiding or restyling argus's top tab header in plugin mode. The header's number-key hints (1/2/3) are also surrendered while a plugin has the ball; whether to dim or hide the header is left as an open question, out of scope for this change.
- Plugin process lifecycle, supervision, or restart. Unchanged — that remains the plugin author's problem.
- Remote-TUI (`--remote`) plugin views. Plugin views already no-op cleanly in remote mode (`loadPluginViews` returns early when `a.db` is an `apistore.Store`); this change does not add remote support.
- Changing how the settings-stream section (`streampane`) gains/releases focus. It benefits from the shared encoder fix, but the ball/release/bottom-bar model applies to full-screen plugin views only.

## Decisions

### Decision 1: Full surrender — argus reserves no key for its own nav while a plugin has the ball

While `a.mode == modePluginView`, argus forwards every key to the plugin: Esc, Ctrl+Q, Ctrl/Shift/Alt-arrow, all of it. Argus stops intercepting Esc.

Rationale: the operator requires Esc to reach the plugin so the Claude PTY inside hera can be cancelled with Esc. And because argus mounts the plugin as a single pane with no knowledge of the plugin's internal panes, argus cannot meaningfully implement the layered "Ctrl+Q pops to the rail, then Ctrl+Q/Esc returns to argus" UX — that layering is the plugin's own behavior. Argus's job is to forward faithfully and to listen for the plugin's request to hand control back.

This replaces the current `modePluginView` branch, which intercepts Esc as the exit key.

**Alternatives considered:** (a) Esc always returns to argus — rejected because it steals Esc from the plugin's PTY (can't cancel Claude). (b) Double-Esc returns — rejected for the same reason plus timing-window fragility. (c) Per-view opt-in for Esc ownership — rejected as unnecessary surface area once we adopt plugin-initiated release.

### Decision 2: Plugin-initiated release via a new plugin→argus control envelope

The plugin signals "give the ball back" by sending a JSON text frame `{"type":"release"}` over the existing WebSocket. On receipt, argus tears down the view (the current `deactivatePluginView`: blur → close → back to the task list).

The connector grows a control-frame callback (today text frames plugin→argus are dropped in `readPump`). The callback parses the envelope and dispatches; `release` is wired to `deactivatePluginView` via `QueueUpdateDraw` (the callback runs on the read-pump goroutine, so it must not touch tview directly).

hera implements the operator's desired UX entirely on its side: deep focus + Ctrl+Q pops to its rail; at the rail, Ctrl+Q or Esc sends `release`.

**Alternatives considered:** Argus-side Ctrl+Q counting (first press = forward a "pop" hint, second = exit) — rejected because argus would have to model the plugin's internal focus depth, which it cannot observe, and it couldn't honor "Esc at rail returns to argus" without the plugin's cooperation anyway.

### Decision 3: Failsafe — fast double Ctrl+Q force-returns to argus

Argus forwards every Ctrl+Q to the plugin (so hera can use it for its internal pop). Argus also timestamps Ctrl+Q presses while in `modePluginView`; if two arrive within a short window (≈400 ms), argus treats the second as a failsafe, intercepts it (does not forward), and force-returns to argus regardless of plugin cooperation.

Rationale: with full surrender plus plugin-initiated release, a hung or buggy plugin that never sends `release` would otherwise trap the keyboard. The failsafe is the one thing argus does unilaterally. The "fast double" shape matches the operator's "ctrl+q … ctrl+q again" mental model: a deliberate, paced sequence lets hera handle the intermediate pop (each press forwarded normally), while a rapid double-tap escapes.

The window is argus-side state (a `lastCtrlQ time.Time` on `App`, reset on view activation). The first Ctrl+Q is always forwarded; only a second within the window is intercepted.

**Trade-off:** a user who fast-double-taps Ctrl+Q intending in-plugin navigation will be yanked back to argus. Acceptable for a failsafe and tunable. Key autorepeat (holding Ctrl+Q) will also trip it — equivalent to "hold to escape," which is benign.

**Alternatives considered:** (a) a distinct never-forwarded chord (e.g. Ctrl+\) — rejected as a third key to remember and undiscoverable. (b) no failsafe — rejected as a lockup hazard.

### Decision 4: One shared key encoder

Extract a single complete key encoder (new package, e.g. `internal/tui/keyenc`) that maps a `tcell.EventKey` to the bytes a PTY/terminal app expects, using standard xterm encoding. Route all three current call sites through it: `terminalpane.eventBytes`, `streampane.eventBytes`, and `app.go:tcellKeyToBytes`.

Coverage:

- Runes, with Alt → ESC-prefixed.
- Enter (and Shift/Alt+Enter → `ESC CR`), Tab, Backtab, Backspace, Delete, Escape.
- Arrows, Home, End, PgUp, PgDn — unmodified.
- Modified arrows / Home / End using the xterm CSI form `CSI 1 ; <mod> <final>`, where `<mod>` is `1 + (Shift=1) + (Alt=2) + (Ctrl=4)`. So `Ctrl+Right` → `\x1b[1;5C`, `Shift+Right` → `\x1b[1;2C`, `Alt+Right` → `\x1b[1;3C`, `Ctrl+Shift+Right` → `\x1b[1;6C`, and `Ctrl+Alt+Right` → `\x1b[1;7C`. The last one is the load-bearing case for the operator's setup: iTerm2 remaps Cmd+arrow to the `\x1b[1;7<final>]` (Ctrl+Alt) form, so faithfully re-emitting mod-`7` sequences is what makes hera's Cmd+arrow focus-switching work end to end.
- Ctrl+letter → the existing C0 control bytes.

Rationale: the bug is literally a divergence between three encoders. Consolidating fixes Ctrl-arrow everywhere (including a latent gap in the agent PTY) and removes the footgun of three implementations drifting. Existing mapped sequences must stay byte-identical so the agent view and existing plugins do not regress.

**Alternatives considered:** (a) fix only the two `eventBytes` copies — rejected; leaves three encoders and the agent gap. (b) fix plugin panes now, agent encoder later — rejected; the operator chose the full consolidation, and doing it once under one test suite is safer than twice.

### Decision 5: Plugin hotkey dictionary drives both the bottom bar and the `?` overlay

The plugin pushes a **hotkey dictionary** to argus over the existing WebSocket: the full set of its currently-active hotkeys, each entry carrying a key glyph, a label, and a `bar` flag for whether it should appear in the bottom bar.

```json
{"type":"hotkeys","items":[
  {"key":"^F","label":"next pane","bar":true},
  {"key":"^B","label":"prev pane","bar":true},
  {"key":"esc","label":"cancel agent","bar":false},
  {"key":"r","label":"refresh","bar":false}
]}
```

Argus stores the latest dictionary on the active mount and uses it two ways, both context-sensitive on `modePluginView`:

- **Bottom bar.** Render the `bar:true` subset, refreshing live as hera changes focus and re-pushes. Argus **always** renders a reserved, non-overridable "return to argus" hint (the failsafe: `^Q^Q argus`) — the plugin's items can never occupy or suppress that segment, and it is drawn last so it can never be pushed off-screen. With no dictionary yet, the bar shows only the reserved exit hint plus a "▶ <plugin title> has the keyboard" affordance.
- **`?` help overlay.** Argus renders the **full** dictionary (every item, `bar` flag ignored) in its existing help modal, styled like argus's own help so the look is normalized across plugins. While a plugin has the ball, the overlay shows only the plugin's hotkeys — not argus's bindings.

**The `?` overlay is plugin-triggered, not argus-reserved.** Because argus fully surrenders every key (including `?`), the plugin owns `?`. When the user presses `?` inside hera, hera sends a `{"type":"help"}` control envelope and argus pops the overlay from the stored dictionary. This keeps the full-surrender contract intact (argus reserves nothing but the double-Ctrl+Q failsafe) while still normalizing the *rendering* in argus. The overlay is dismissed by argus on the next key or Esc-equivalent and does not itself capture the keyboard beyond dismissal.

Rationale: the plugin owns the keyboard and is the only authority on what its keys do, and those bindings change with hera's internal focus — so static or registration-time hints would frequently be wrong. Argus already holds the dictionary for the bar, so rendering the `?` overlay from the same data is nearly free and consistent. The reserved exit hint guarantees the escape affordance is always visible regardless of what (or whether) the plugin pushes.

**Alternatives considered:** (a) argus reserves `?` and renders the overlay itself — rejected; it carves `?` out of the full-surrender contract for marginal gain, and the always-on exit hint already covers the must-not-lose affordance. (b) plugin renders its own help inside its surface — rejected; every plugin would reinvent help rendering and argus already holds the dictionary, so the look would be inconsistent for no benefit. (c) static registration-time hints — rejected; can't track focus changes. (d) blank bar — rejected; loses the "who has the ball" affordance.

### Decision 6: "Who has the ball" state stays where it already lives

The authoritative signal is the existing `a.mode == modePluginView` plus `a.activePlugin *pluginViewMount`. No new state machine. The status bar reads this (the app pushes plugin-mode on activate / clears on deactivate). The hotkey dictionary and the Ctrl+Q timestamp hang off the active mount / App, set on activate and cleared on deactivate, so a stale plugin's dictionary never bleeds into the next.

## Protocol summary

hera is the only plugin with a UX today, so these envelopes are treated as the contract hera implements, not as optional add-ons constrained by legacy compatibility. They are nonetheless additive to the documented v1 surface (new event/envelope types), so nothing existing breaks. New control envelopes, all JSON text frames:

| Direction      | Envelope                                             | Effect                                                        |
| -------------- | ---------------------------------------------------- | ------------------------------------------------------------- |
| argus → plugin | `{"type":"resize","cols":N,"rows":M}` (existing)     | unchanged                                                     |
| argus → plugin | `{"type":"focus"}` / `{"type":"blur"}` (existing)    | unchanged                                                     |
| plugin → argus | `{"type":"release"}` (**new**)                       | argus tears down the view and returns the ball                |
| plugin → argus | `{"type":"hotkeys","items":[{"key":"…","label":"…","bar":bool}]}` (**new**) | argus stores the dictionary; renders `bar:true` items in the bottom bar (exit hint reserved) and the full set in the `?` overlay |
| plugin → argus | `{"type":"help"}` (**new**)                          | argus pops the `?` help overlay rendered from the stored hotkey dictionary |

Unknown plugin→argus envelope types are ignored. A plugin that sends none of these still renders (encoder change is transparent) and is exited via the double-Ctrl+Q failsafe.

## Risks / Trade-offs

- **Shared encoder touches the agent PTY input path (highest risk)** → A regression here breaks typing into live Claude/Codex sessions. Mitigation: the encoder is a pure function; pin every currently-emitted sequence with table tests (port the existing `terminalpane_test`, `streampane_test`, and `app_test` cases into the new package and assert byte-identical output), then add the new modified-key cases. Stage 1 of the plan writes these as failing tests first.
- **Full surrender means Ctrl+C no longer quits argus while a plugin has the ball** → This is intended (Ctrl+C must reach the plugin), but it is a behavior change from "Ctrl+C quits" in other modes. Mitigation: the reserved exit hint documents the way out; the double-Ctrl+Q failsafe is always available.
- **Failsafe false-positive** → A fast double Ctrl+Q intended for in-plugin nav exits to argus. Mitigation: the window is tunable; document the behavior; deliberate (paced) Ctrl+Q sequences are unaffected.
- **Hotkey-dictionary render path is argus-owned** → A plugin pushing many/oversized items could bloat the bar or overlay. Mitigation: cap item count and total rendered width; truncate; the reserved exit segment is always drawn last so it can never be pushed off-screen.
- **Control-frame parsing on the read pump** → Malformed JSON from the plugin must not panic the pump. Mitigation: parse defensively, ignore unparseable/unknown frames, keep binary ANSI frames on the existing fast path.

## Migration Plan

- Additive only; no schema or data migration. The `plugin_views` table is unchanged.
- hera is the only plugin, and it will implement `release`, `hotkeys`, and `help`; there is no fleet of existing plugins to preserve, so backwards compatibility is not a design constraint. The encoder change is transparent regardless (more keys simply arrive), and a plugin that implements none of the new envelopes still renders and is exited via the failsafe.
- Rollback is a straight revert of the change; no persisted state is introduced.
- Docs: update `docs/plugins.md` to document full surrender, the `release` / `hotkeys` / `help` envelopes, and the double-Ctrl+Q failsafe. Add the non-obvious gotchas to `context/knowledge/gotchas/` (encoder consolidation invariant, control-frame-on-read-pump threading, failsafe window, the iTerm2 Cmd→Ctrl+Alt remap round-trip).

## Open Questions

- **Top header in plugin mode.** The tab header's number-key hints (1 tasks / 2 DAG / 3 settings) are also surrendered while a plugin has the ball. Do we dim/hide the header for a cleaner "plugin owns the screen" feel, or leave it? Deferred — out of scope here; flag for a follow-up.
- **Failsafe window value.** 400 ms is a starting point. May want to tune after dogfooding with hera, or make it configurable. Treated as a constant for now.
- **Should `release` optionally carry a reason** (e.g. `{"type":"release","reason":"user_exit"}`) for uxlog/debugging? Cheap to add later as an optional field; omitted for v1 minimalism.

## Discovery findings

- `internal/tui/app.go:handleGlobalKey` — single `SetInputCapture` handler; the `modePluginView` branch (≈1517) is the surrender seam.
- `internal/tui/app.go:tcellKeyToBytes` (≈2141) — agent PTY encoder; handles Alt-arrow but not Ctrl-arrow.
- `internal/tui/terminalpane/terminalpane.go:eventBytes` (289) and `internal/tui/streampane/streampane.go:eventBytes` (266) — the two minimal plugin-view encoders; both drop arrows and all modifier combos.
- `internal/tui/views/connector.go` — WebSocket protocol; `resize`/`focus`/`blur` argus→plugin envelopes; `readPump` (≈178) drops plugin→argus text frames today (the seam for `release`/`hints`).
- `internal/tui/plugin_views.go` — `activatePluginView` / `deactivatePluginView` / `pluginViewMount`; where ball activation and teardown live, and where hints + the Ctrl+Q timestamp will hang.
- `internal/tui/widget/statusbar.go` — `Draw` selects hints by `activeTab`; needs a plugin-view branch and a `SetPluginMode`-style setter.

## Acceptance criteria

Captured per behavioral section; these map to scenarios in the deltas.

**Key surrender / encoder (capability: `plugin-views`)**

- it should forward Ctrl+Right to the active plugin as `\x1b[1;5C`
- it should forward Shift/Alt/Ctrl+Shift modified arrows using the xterm `CSI 1;<mod><final>` form
- it should forward a Ctrl+Alt+arrow event as the mod-`7` form (e.g. `\x1b[1;7C`), round-tripping the sequence iTerm2 emits for Cmd+arrow
- it should forward Esc to the active plugin (not intercept it) so the plugin's PTY receives it
- it should forward Ctrl+C to the active plugin instead of quitting argus while a plugin has the ball
- it should forward plain arrows, Home, End, PgUp, PgDn to the active plugin
- it should encode every previously-mapped key to a byte-identical sequence (no regression) via the shared encoder

**Release / failsafe (capability: `plugin-views`)**

- it should tear down the active plugin view and return focus to the task list when the plugin sends `{"type":"release"}`
- it should force-return to argus when two Ctrl+Q presses arrive within the failsafe window
- it should forward a single Ctrl+Q to the plugin (no force-return) when no second press arrives within the window
- it should ignore an unknown or malformed plugin→argus control frame without disrupting the byte stream

**Bottom bar + hotkey dictionary (capability: `plugin-views`)**

- it should render the `bar:true` subset of the plugin's pushed hotkey dictionary in the bottom bar while a plugin has the ball
- it should always render a reserved "return to argus" exit hint that the plugin's items cannot suppress or displace
- it should update the bottom bar live when the plugin re-pushes its hotkey dictionary
- it should show argus's own tab hints (not plugin hotkeys) once the plugin releases the ball
- it should fall back to a "<plugin> has the keyboard" affordance plus the exit hint when the plugin has pushed no dictionary

**Help overlay (capability: `plugin-views`)**

- it should pop the `?` help overlay rendered from the full pushed dictionary when the plugin sends `{"type":"help"}`
- it should show only the plugin's hotkeys (not argus's bindings) in the overlay while a plugin has the ball
- it should NOT reserve `?` itself — `?` is forwarded to the plugin like any other key
