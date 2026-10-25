## Why

The band requirement says omitting the engine's empty `AbovePrompt` component removes the blank row between the band and the prompt box, and names a scenario "No blank row above the prompt box". Live replays of 12 sessions show that row is Claude Code's own fixed spacer between the transcript and the prompt box: sessions that never drew a band have it too, and the 2.1.310 mod API exposes nothing that controls it. The spec promises something the mod cannot deliver.

## What changes

- Reword the band requirement: the engine's empty component is still omitted (behavior unchanged), and the blank row below the band is named as the engine's spacer, outside the mod's tree.
- Rename the scenario to what it checks: the band's tree holds only its own line.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: wording of the cached composable Hera band requirement only.

## Impact

Spec text and `gotchas/bridge.md` only. No code or behavior change.
