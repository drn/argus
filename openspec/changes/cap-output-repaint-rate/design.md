## Context

PR 1 measured (see the archived `repaint-panes-on-output/measurements.md`): echo median fell from 52ms to 9ms for the coordinator, but a busy pane repaints at 38–47fps. Most of the extra CPU was the per-`Show()` terminal size ioctl, since cached for 1s, leaving draw/diff/flush cost that still scales with frame count.

Synthetic baseline through the real pump, a real tview draw and a SimulationScreen (`repaint_workload_test.go`, 200x50, one pane, 50 writes/s for 4s): **201 frames (50.2fps), about 10.8% of one core, echo 1.7–2.9ms**. The simulation has no ioctl or terminal write, so this isolates the draw/diff cost the cap can remove.

## Decisions

### 1. Rule

Let `lastKey` be the time of the most recent key event seen by `handleGlobalKey`.

- A draw is an *echo frame* when `now - lastKey < 150ms`, otherwise an *output frame*.
- Minimum gap between draws (measured from the previous draw's completion, as today): 8ms for echo frames, 40ms for output frames.
- The first wake after a gap longer than the applicable interval draws immediately (unchanged leading edge).

Why 150ms: Claude Code echoes a key within tens of ms even under load; 150ms covers slow echoes and also covers a held key, since each key renews the window. Why 40ms: 25fps, inside the 20–30fps target, and the largest gap a human reads as smooth for streaming text.

### 2. A key must shorten a throttle already in progress

The pump may be sleeping out a 40ms output throttle when a key arrives. Without help the echo, which arrives a few ms later, would wait for that timer. `noteKey` therefore also sends on a size-1 `keys` channel; the throttle wait selects on it and recomputes the remaining delay using the 8ms interval. If the pump is idle, the key needs no wake: the echo's own wake finds the window open.

### 3. Where the key is stamped

`handleGlobalKey` is the single `SetInputCapture` for the TUI, so stamping there covers the Tasks agent view, both Hera panes and the plugin view without touching the many `typing.forward` sites. It runs whether or not `ARGUS_TYPING_LOG` is on, so the default path does not depend on the tracker. Keys that are not forwarded (list navigation) also open the window; that is harmless, since those screens repaint through their own paths.

Bracketed paste bypasses `InputCapture` (see AGENTS.md). Its echo paints within 40ms. Accepted and documented; hooking paste is a named follow-up.

### 4. No lost final frame

The wake channel is a single slot: output during a throttle leaves one pending token, and the pump draws once the throttle ends, after draining and before drawing. A write during the draw re-arms the token. So the final output of a burst paints at most one output interval later. A test pins this with a synctest burst and a trailing write.

### 5. Typing tracker

Untouched. It still measures key press to `lazyScreen.Show` and now doubles as the proof that echo stays fast.

### 6. Counters

`frames` splits into `echo_frames` and `output_frames` (classified at draw time), logged in the existing 30s `[repaint]` line only when diagnostics are on.

## Expected effect

Frames on a 50-writes/s stream fall from about 50fps to about 25fps, so draw CPU falls by about half (synthetic: ~10.8% to ~5.5% of one core). On the live system the saving is smaller in absolute terms and bounded by the ioctl floor, so expect roughly 40–50% of the post-PR-1 draw cost, not of total TUI CPU. Echo latency is unchanged: 8ms cap, same as today.

## Non-goals

- No change to the 8ms echo cap, the size cache, the spinner or the tick.
- No supervisor or wire change, so no `ProtocolVersion` bump and `TestSupervisorSurfaceDigest` is unchanged.
- No paste hook.
