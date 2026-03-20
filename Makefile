.PHONY: build vet test test-watch test-cover test-pkg

build:
	go build ./...

vet:
	go vet ./...

test:
	go test -race -count=1 ./...

test-watch:
	@command -v gotestsum >/dev/null 2>&1 || { echo "Install gotestsum: go install gotest.tools/gotestsum@latest"; exit 1; }
	gotestsum --watch ./...

test-cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

test-pkg:
	@test -n "$(PKG)" || { echo "Usage: make test-pkg PKG=./internal/db/"; exit 1; }
	go test -race -count=1 -v $(PKG)
