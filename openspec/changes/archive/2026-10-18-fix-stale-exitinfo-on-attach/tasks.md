## 1. Fix

- [x] 1.1 Deterministic regression (real Recycle leaves a stale entry; attach must drop it; real stop yields fresh ExitInfo) — fails without the fix.
- [x] 1.2 `dropStaleExitInfo` on stream attach; bump SupervisorStreamSurface to 13 and re-record the digest.
- [x] 1.3 Update the daemon-rpc gotcha with the real mechanism.

## 2. Delivery

- [x] 2.1 `make pre-pr`; push and open PR; archive in the same PR.
