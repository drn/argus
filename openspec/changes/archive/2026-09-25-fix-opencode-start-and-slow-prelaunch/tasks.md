## Tasks

- [x] Update the built-in OpenCode command and adjust command-construction tests.
- [x] Upgrade the exact previous OpenCode backend default in existing databases, preserving customized commands.
- [x] Capture OpenCode v2 session IDs from `session_v2` without losing v1 and JSON compatibility.
- [x] Add a `StartSession` RPC deadline covering the bounded Pi prelaunch at both client hops, with a regression test for a start taking longer than the ordinary deadline.
- [x] Update relevant base specs, README Reference facts if affected, and gotcha files; archive this change on the branch before merge.
- [x] Run targeted tests, `make test`, and `make pre-pr` (including `test-cover-gate`, the same race coverage suite as `make test-cover`) before opening or updating a PR.
