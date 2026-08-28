## Tasks

- [x] Add `ResolveRestartDaemon`/`ResolveRestartSupervisor` to `RestartDaemonModal`, rebuilding the button set instead of always finalizing the modal.
- [x] Update `App.handleRestartDaemonKey` to keep the modal open when a restart action remains after resolving the chosen one.
- [x] Unit tests for the modal's resolve behavior (both-stale and single-stale cases).
- [x] Smoke test for the app-level flow: restart daemon while both stale → modal stays open → restart supervisor → modal dismisses.
- [x] Archive this change into the base `binary-coherence` spec in the same PR.
