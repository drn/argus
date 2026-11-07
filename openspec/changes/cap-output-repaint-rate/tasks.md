## 1. Baseline

- [x] 1.1 Add the opt-in synthetic workload (`ARGUS_PAINT_WORKLOAD=1`) through the real pump, tview draw and SimulationScreen; record before numbers in `measurements.md`.

## 2. Tests first

- [ ] 2.1 synctest: output-only frames are at least 40ms apart; an echo (key within 150ms) draws at the 8ms cap; the first byte after quiet draws immediately.
- [ ] 2.2 synctest: a key during a 40ms throttle shortens it to the 8ms interval; a key while idle changes nothing.
- [ ] 2.3 synctest: the final write of a burst paints, and a write during a draw re-wakes.
- [ ] 2.4 Counters: echo and output frames classified at draw time and logged in `[repaint]`.
- [ ] 2.5 Smoke: `handleGlobalKey` stamps the pump (with and without the typing log), including with a nil pump.

## 3. Implement

- [ ] 3.1 `repaintPump`: `lastKey` atomic, `noteKey`, `keys` channel, per-draw interval selection, interruptible throttle, counters.
- [ ] 3.2 Call `noteKey` at the top of `handleGlobalKey`.
- [ ] 3.3 uxlog `[repaint]` line with `echo_frames` and `output_frames`.

## 4. Prove and document

- [ ] 4.1 Run the workload after the change; fill the before/after table in `measurements.md`.
- [ ] 4.2 Gotchas in `context/knowledge/gotchas/ui-threading.md` and the index row.
- [ ] 4.3 `make pre-pr`, `/hera-review`, `/hera-review-test-adversary`, archive the change, push, open PR.
