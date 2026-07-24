## Context

`ctrl+g` (add-hera-jump-question) already cycles the Hera rail to the next role needing input, force-expanding folded ancestor coordinators so a closed fold never swallows the target (BUG-007). That mechanism is correct and unchanged by this design — the gap is purely "what happens after the operator has fixed the problem(s)": there is no way back to the fold state they had before ctrl+g started rearranging their view, short of manually re-folding by hand.

This design was worked out and approved by Aaron in a coordination conversation before implementation started; it is recorded here for the archived record, not re-derived.

## Goals / Non-Goals

**Goals:**
- Automatically remember the rail's fold/selection state at the moment the operator is first interrupted by a needs-input signal.
- Let `ctrl+g` restore that state once every problem is resolved, with zero extra keystrokes beyond what the operator already presses today.
- Give the operator an explicit, unconditional "put it back now" key (`ctrl+b`) for when they want to end the excursion early, before every problem is resolved.
- Never hide the existence of remaining problems: restoring fold state must never suppress a coordinator header's own needs-input glyph.

**Non-Goals:**
- No persistence across restarts — the snapshot is pure in-memory rail state, deliberately excluded from the `hera.rail_view_state` DB blob (BUG-002) the rest of the fold state persists through.
- No wall-clock or idle-time heuristics of any kind. The state machine is driven purely by the whole-rail needs-input count and whether a snapshot is currently held.
- No change to the jump/cycle mechanics themselves (`Rail.NextNeedsInputTaskID`, `HeraPage.JumpToTask`, the ancestor-expand-on-jump behavior) — this only adds a snapshot/restore layer around the existing feature.

## Decisions

**D1 — Capture at the 0→≥1 transition, never at keypress time.** Capturing when the operator presses `ctrl+g` would capture whatever they've already started doing to the rail in reaction to the problem, not their true pre-interruption layout. Capturing at the transition (detected on every rail rebuild, `Rail.SetModel`) captures the state a tick before the operator could possibly have reacted. This is the single most important invariant of the whole design.

**D2 — A snapshot already held is never replaced while count stays >=1 ("a 3rd ? firing is the same excursion").** Only two conditions trigger a capture: the 0→≥1 edge, or `excursion == nil` while count is already >=1 (reachable only right after an explicit `ctrl+b` restore mid-excursion). This means a SECOND unrelated problem appearing mid-excursion does not overwrite the snapshot with the now-more-fussed-with layout — the operator's original layout survives until they explicitly discharge it.

**D3 — Fold-independent whole-rail count, not the rendered row count.** `Rail.rows` reflects only the currently-focused kanban group and expanded folds, so counting THOSE rows would make the excursion state machine's behavior depend on what happens to be visible at that instant — wrong, since folding/unfolding must never itself arm or disarm an excursion. `Model.NeedsInputTotalCount()` walks the whole model (every orchestrator's roles, including coordinator-kind, across Pinned/Active/Archived, plus Freelance) directly, independent of rail rendering state.

  Note: the original design assumed a still-live "folded coordinator's rolled-up subtree (?) glyph" contributing to this count. A later, independent change (`remove-needs-input-rollup-glyph`) retired that rollup glyph entirely — a coordinator header now shows only its OWN needs-input signal, never a descendant's rollup. `NeedsInputTotalCount` was written against CURRENT code: it sums each role's own `needsInputOwn()` signal (which is exactly what drives the rail's `(?)` glyphs and the existing `ctrl+g` candidate ring), so the count and the visible `(?)` marks agree by construction, with or without the retired rollup glyph.

**D4 — `ctrl+g`'s two branches share one dispatch, keyed on the live count at keypress time.** count>=1 behaves exactly as the shipped feature always has (peek → tear down agent view if needed → switch to Hera tab → jump), with one addition: it ensures a snapshot is armed (belt-and-suspenders — normally already true via D1) before jumping. count==0 never switches tabs or tears down the agent view — restoring fold state is a background fix with nothing that needs watching happen; the operator can switch to the Projects tab themselves to see the result. This mirrors the existing "no-op must not yank the operator out of their agent view" rule (the original BUG caught in review for the plain jump case).

**D5 — `ctrl+b` is unconditional and never discharges the candidate ring.** It calls the identical `Rail.RestoreExcursion()` primitive `ctrl+g`'s count==0 branch calls, but skips the count check entirely — "restore rail" always means restore, regardless of how many problems remain. Restoring only re-applies fold/selection state; it never marks anything as resolved, so a subsequent `ctrl+g` still reaches whatever is still outstanding.

**D6 — Key substitution: `ctrl+b` in place of the originally-approved `ctrl+shift+g`.** See proposal.md's "Deviation" note. `ctrl+<letter>` is a C0 control byte (`ctrl+g` is always `0x07`) with no bit for Shift at the wire protocol level — virtually no terminal (and this app's tcell setup, which does not implement the Kitty keyboard protocol or xterm's `modifyOtherKeys`) can send a byte sequence distinguishing `ctrl+g` from "`ctrl+shift+g`". `internal/tui/keymap`'s `Parse` independently documents and rejects the combination outright (`shift` is grammar-restricted to arrow/nav keys only). `ctrl+b` was chosen as the closest available substitute: unused across every context that participates in the global unconditional-dispatch bucket (`CtxGlobal`, `CtxAgent`, `CtxHeraRail`, `CtxTaskList`), and — unlike `ctrl+i`/`ctrl+m`, which tcell aliases to `Tab`/`Enter` — carries no structural-key collision risk.

## Risks / Trade-offs

- **[Risk]** The rail's background rebuild (`ScheduleRefresh`) only runs while the Hera tab is active (`internal/tui/app.go`'s per-tick gate); off-tab, the model only refreshes on tab entry. The count/snapshot bookkeeping lives inside `Rail.SetModel`, so it inherits this exact same freshness characteristic as the pre-existing `ctrl+g` candidate-peek logic — no better, no worse. → Not a regression introduced by this change; out of scope to fix rail staleness here.
- **[Risk]** A snapshot surviving a fully-resolved excursion (count back to 0, but no explicit `ctrl+g`/`ctrl+b` yet) is deliberate (D2's sibling rule: discharge is never automatic) but means a LONG-idle rail could carry a stale snapshot from an excursion the operator forgot about. → Accepted: pressing `ctrl+g` or `ctrl+b` at any later point still does the right, predictable thing (restores the last captured pre-interruption layout), and a stale restore is always reversible by the operator's own subsequent fold/unfold actions.

## Migration Plan

None — pure additive TUI behavior, no schema, no persisted state, no daemon RPC. Ships and rolls back with the binary.
