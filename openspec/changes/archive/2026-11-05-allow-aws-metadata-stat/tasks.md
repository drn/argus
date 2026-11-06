## 1. Implementation

- [x] 1.1 Add the metadata allow after the `~/.aws` deny in `internal/agent/sandbox.go`
- [x] 1.2 Profile-text test asserting the allow follows the deny; live sandbox test (stat ok, read blocked) when sandbox-exec is functional
- [x] 1.3 Note the deny-covers-stat gotcha in `context/knowledge/gotchas/sandbox.md`
- [x] 1.4 Archive the change in the same PR; `make pre-pr`
