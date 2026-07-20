# fix-worker-context-window-denominator

Corrects the Hera rail's context-pressure indicator to scale against a worker's real context
window (1M tokens), not the coordinator's much smaller recycle-nudge policy budget (200k).
