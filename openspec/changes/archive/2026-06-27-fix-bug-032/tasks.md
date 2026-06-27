## 1. Content fingerprint (agent package)

- [x] 1.1 Add `agent.ContentFingerprint(tail []byte) uint64` — strip ANSI, fold
  bare `\r` line breaks, drop blank + volatile-chrome lines, de-duplicate
  (first-occurrence) so repeated repaint frames collapse, keep the trailing
  `contentFingerprintLines` distinct lines, FNV-hash them.
- [x] 1.2 Add `fingerprintVolatileLine` — excludes spinner/timing decoration
  (`decorationLine`) and Claude's input/cursor line (leading `❯`) from the
  fingerprint so animation does not perturb it.

## 2. Daemon detector (internal/api/push.go)

- [x] 2.1 `computeNeedsInput` gains a content-stability pass: fingerprint every
  running session that shows the signature, flag it when the fingerprint equals
  the previous tick's. Returns the new fingerprint map; signature unchanged for
  the idle + sticky passes.
- [x] 2.2 `idleWatcherState.contentFP` carries fingerprints across ticks;
  `detectNeedsInputTick` threads prev→new.

## 3. TUI detector (internal/tui/app.go)

- [x] 3.1 `detectNeedsInputSticky` gains the same content-stability pass, reading
  the on-disk session log tail; fingerprints persist on `App.needsInputFP`.

## 4. Tests (both directions)

- [x] 4.1 `agent.TestContentFingerprint`: animation-only changes fingerprint
  identically (and both snapshots still detect needs-input); new transcript
  content changes it; repaint count does not destabilize it.
- [x] 4.2 `api.TestComputeNeedsInput` + `TestComputeNeedsInput_StabilityAcrossTicks`:
  a never-idle blocked session is flagged once its content is stable across ticks;
  a session whose content shifts is not (streaming guard); existing idle + sticky
  cases still hold.
- [x] 4.3 `tui.TestDetectNeedsInputSticky_ContentStability`: a never-idle worker
  parked at a prompt is flagged on the second (stable) tick; a streaming session
  is never flagged.
- [x] 4.4 Existing needs-input tests (BUG-023 / BUG-028) still pass.

## 5. Docs

- [x] 5.1 Add a gotcha bullet (content-stability flags a parked-but-never-idle
  session; the fingerprint-instability streaming guard) and bump the index count.
