# Measurements

Workload: `ARGUS_PAINT_WORKLOAD=1 go test ./internal/tui -run TestWorkload_OutputRepaintRate -v`.

- One Tasks agent pane on a 200x50 SimulationScreen, through the real repaint pump and a real tview draw.
- Synthetic producer: 50 writes/s of ~140-byte lines for 4s (about the 38–47fps a busy pane produced live in PR 1), 5 runs per side.
- A keystroke is simulated every 500ms (`noteKey`, then an echo write).
- `cpu` is process user+system time over the producer window; `draw_busy` is wall time inside the pump's draw, which is the UI goroutine's work.
- The simulation has no terminal write or size ioctl, so this isolates the draw/diff cost the cap removes. The live saving is bounded by costs the cap does not touch.

**Before** is the same binary with the output interval set back to 8ms (PR 1 behavior). **After** is this change.

| | Before (8ms for all frames) | After (8ms echo / 40ms output) |
| --- | --- | --- |
| frames over 4s | 201 (50.2 fps) in all 5 runs | 125 (31.2 fps) in all 5 runs |
| draw_busy, % of one core | 14.4 / 15.2 / 14.5 / 14.5 / 17.5 | 9.3 / 9.3 / 9.5 / 9.1 / 9.2 |
| cpu, % of one core | 15.2 / 15.4 / 15.2 / 15.1 / 17.3 | 9.7 / 9.7 / 9.7 / 9.5 / 9.6 |
| frames per echo window | n/a | echo frames keep the 8ms cap |
| echo ms (all 35 samples) | min 2.5, median 2.97, max 6.95 | min 2.6, median 3.9, max 7.4 |
| final burst write painted | yes, all runs | yes, all runs |

Reading the table:

- Frames fall 38% and CPU falls about 36% (median 15.2% to 9.7%). The 31fps After figure is 25fps of output frames plus the extra frames inside the 150ms echo windows.
- Echo stays bounded by the 8ms echo cap, the same bound as before. Its median moved from about 3.0ms to 3.9ms because with output frames now paced 40ms apart, an echo can land just after a draw and wait out the remainder of the 8ms gap. The worst case did not increase.
- An earlier baseline taken with a 1ms-polling harness gave 10.8% CPU at 50.2fps; it counted harness wake-up cost and is superseded by the table above.

## Not measured

- No live A/B with a human typing; the typing-latency tracker (`ARGUS_TYPING_LOG`) is unchanged and remains the live check.
- The size-ioctl and terminal-write costs are absent from the simulation. They scale with frame count too, so the live saving should be at least proportional, but that is not measured here.
