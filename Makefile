.PHONY: build vet test test-watch test-cover test-cover-gate test-pkg lint-pr fmt fmt-check vuln pre-pr plugin-smoke dogfood-install

build:
	go build ./...

# Build + install the live argusd binary the local daemon runs, self-signing
# it with a STABLE code-signing identity when one is available so macOS TCC
# privacy grants persist across rebuilds. This is the command .iris.toml runs
# on every dogfood deploy.
#
# WHY: Go's toolchain ad-hoc-signs binaries with a content-derived cdhash that
# changes on every build. macOS TCC keys a privacy grant ("argus would like to
# access data from other apps" — triggered when a spawned agent or tool touches
# ~/Library/{Application Support,Containers,Caches}) to that cdhash, so an
# unsigned/ad-hoc binary re-prompts after every rebuild. A stable signing
# identity gives TCC a constant designated requirement, so one grant sticks.
#
# OPT-IN (macOS, per developer — fully optional):
#   1. Keychain Access -> Certificate Assistant -> Create a Certificate:
#        Name: "Argus Dogfood"  (or set ARGUS_SIGN_IDENTITY to your own name)
#        Identity Type: Self Signed Root
#        Certificate Type: Code Signing
#      (No need to mark the cert "trusted" — codesign and TCC both work with an
#       untrusted self-signed identity; locally-built binaries aren't quarantined.)
#   2. Run a dogfood deploy (or `make dogfood-install`) — the binary is now
#      signed with a stable identity.
#   3. Approve the macOS prompt once, OR grant the binary Full Disk Access
#      (System Settings -> Privacy & Security -> Full Disk Access). It persists.
#
# Machines without that identity (other devs, CI, Linux) fall back to a plain
# `go install` — byte-identical to before — so this target is always safe.
ARGUS_SIGN_IDENTITY ?= Argus Dogfood

dogfood-install:
	go install ./cmd/argus
	@bin="$$(go env GOBIN)"; [ -n "$$bin" ] || bin="$$(go env GOPATH)/bin"; bin="$$bin/argus"; \
	id=$$(security find-identity -p codesigning 2>/dev/null | grep -F "$(ARGUS_SIGN_IDENTITY)" | head -1 | awk '{print $$2}'); \
	if [ -n "$$id" ]; then \
		codesign --force --identifier com.drn.argus --sign "$$id" "$$bin" \
			&& echo "[dogfood] signed $$bin with stable identity '$(ARGUS_SIGN_IDENTITY)' ($$id) — TCC grant will persist"; \
	else \
		echo "[dogfood] no '$(ARGUS_SIGN_IDENTITY)' code-signing identity found — left ad-hoc (macOS may re-prompt for file access). To opt in, see the dogfood-install comment in the Makefile."; \
	fi

# Full pre-PR gate — mirrors .github/workflows/ci.yml in order. Run this
# (and get a clean pass) before opening or updating a PR. test-cover-gate
# runs the race suite + coverage floor, so it subsumes `make test`.
# Note: `vuln` is `continue-on-error` in CI (advisory only — stdlib CVEs
# need a Go bump), but is fatal here so local runs surface them early.
# A red `make pre-pr` whose ONLY failure is `vuln` will still pass CI.
pre-pr: build vet fmt-check lint-pr vuln test-cover-gate
	@echo "✓ pre-pr checks passed — safe to open/update the PR"

vet:
	go vet ./...

# Format the entire tree with goimports (a superset of gofmt).
fmt:
	@command -v goimports >/dev/null 2>&1 || { echo "Install: go install golang.org/x/tools/cmd/goimports@latest"; exit 1; }
	goimports -w .

# Fail if any file is not goimports-clean. Mirrors the CI check.
fmt-check:
	@command -v goimports >/dev/null 2>&1 || { echo "Install: go install golang.org/x/tools/cmd/goimports@latest"; exit 1; }
	@out=$$(goimports -l .); if [ -n "$$out" ]; then echo "Files not formatted:"; echo "$$out"; exit 1; fi

# Scan for known vulnerabilities in stdlib and dependencies.
vuln:
	@command -v govulncheck >/dev/null 2>&1 || { echo "Install: go install golang.org/x/vuln/cmd/govulncheck@latest"; exit 1; }
	govulncheck ./...

# Run golangci-lint the same way CI does — only flag issues introduced by
# this branch's diff vs origin/master. Use before pushing to catch lint
# failures locally instead of via a CI round-trip.
lint-pr:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "Install: brew install golangci-lint OR go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; exit 1; }
	@git fetch origin master >/dev/null 2>&1 || true
	golangci-lint run --new-from-rev=origin/master ./...

# -timeout 120s bounds a hung suite while the machine is AWAKE. It is a weak
# guard, not the real backstop: Go's test timeout uses the runtime-monotonic
# clock, which macOS suspends during sleep, so an overnight-slept run never
# accumulates the wall-clock minutes to trip it. The orphaned-test reaper
# (script/reap-orphaned-tests.sh) is the sleep-proof backstop.
test:
	go test -race -count=1 -timeout 120s ./...

test-watch:
	@command -v gotestsum >/dev/null 2>&1 || { echo "Install gotestsum: go install gotest.tools/gotestsum@latest"; exit 1; }
	gotestsum --watch ./...

test-cover:
	go test -race -count=1 -timeout 120s -coverprofile=coverage.out ./...
	@echo "--- raw ---"
	@go tool cover -func=coverage.out | tail -1
	@echo "--- filtered (per coverage-ignore.txt) ---"
	@go run ./scripts/coverfilter -in coverage.out -out coverage.filtered.out

# CI gate. Fails if filtered coverage drops below the current floor.
# Ratchets up over time toward the 95% target.
test-cover-gate:
	go test -race -count=1 -timeout 120s -coverprofile=coverage.out ./...
	go run ./scripts/coverfilter -in coverage.out -out coverage.filtered.out -min 88

test-pkg:
	@test -n "$(PKG)" || { echo "Usage: make test-pkg PKG=./internal/db/"; exit 1; }
	go test -race -count=1 -timeout 120s -v $(PKG)

# Black-box smoke test the plugin substrate against the locally running
# daemon. Mints nothing — see cmd/argus-plugin-smoke for the one-time setup
# (`argus token mint --scope smoke` → ~/.argus/smoke-token). Cleans up
# transient backends/tasks/views/sections/MCP tools on exit.
plugin-smoke:
	go run ./cmd/argus-plugin-smoke -verbose
