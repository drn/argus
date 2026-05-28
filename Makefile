.PHONY: build vet test test-watch test-cover test-cover-gate test-pkg lint-pr fmt fmt-check vuln pre-pr

build:
	go build ./...

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

test:
	go test -race -count=1 ./...

test-watch:
	@command -v gotestsum >/dev/null 2>&1 || { echo "Install gotestsum: go install gotest.tools/gotestsum@latest"; exit 1; }
	gotestsum --watch ./...

test-cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	@echo "--- raw ---"
	@go tool cover -func=coverage.out | tail -1
	@echo "--- filtered (per coverage-ignore.txt) ---"
	@go run ./scripts/coverfilter -in coverage.out -out coverage.filtered.out

# CI gate. Fails if filtered coverage drops below the current floor.
# Ratchets up over time toward the 95% target.
test-cover-gate:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go run ./scripts/coverfilter -in coverage.out -out coverage.filtered.out -min 88

test-pkg:
	@test -n "$(PKG)" || { echo "Usage: make test-pkg PKG=./internal/db/"; exit 1; }
	go test -race -count=1 -v $(PKG)
