# sandbox-execution

## MODIFIED Requirements

### Requirement: Scoped tool, cache, and browser write access

The generated profile SHALL grant scoped write access to the GitHub CLI config (`~/.config/gh`), build-tool caches (including `~/.argus/cache`, where `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` are redirected), the macOS Keychains directory, and the Google Chrome support directory, without broadening to their parent directories. In particular the gh allow rule SHALL NOT undo the gcloud deny-read, the Chrome allow rule SHALL NOT broaden to all of Application Support, and the `~/.argus/cache` allow rule SHALL NOT broaden to all of `~/.argus` (which also holds the sqlite database and daemon socket).

#### Scenario: gh config is writable but gcloud read stays denied

- **WHEN** a sandboxed command writes a file under `~/.config/gh` and separately tries to read `~/.config/gcloud`
- **THEN** the gh write succeeds and the gcloud read remains blocked

#### Scenario: Chrome crashpad support file is writable

- **WHEN** a sandboxed command writes the Chrome crashpad `settings.dat` under `~/Library/Application Support/Google/Chrome`
- **THEN** the write succeeds, allowing Chrome to launch for browser automation

#### Scenario: sibling Application Support directories stay denied

- **WHEN** a sandboxed command writes to an unrelated `~/Library/Application Support/OtherApp` path
- **THEN** the write is denied, confirming the Chrome rule is not over-broad

#### Scenario: Keychains directory is writable

- **WHEN** a sandboxed command writes a file under `~/Library/Keychains`
- **THEN** the write succeeds so the agent can store API keys via the macOS Keychain

#### Scenario: ~/.argus/cache is writable for the GOCACHE/PLAYWRIGHT_BROWSERS_PATH redirect

- **WHEN** a sandboxed command creates and writes under `~/.argus/cache/go-build` (or `~/.argus/cache/ms-playwright`)
- **THEN** the write succeeds, so `go build`/`go test`/Playwright's browser install work inside a sandboxed agent
