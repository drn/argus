# Task Linking

## Purpose

This capability owns two user-facing surfaces and explicitly does NOT own the dependency-graph engine (that is `task-orchestration`).

First, it owns the **REST/HTTP contract** for managing task-to-task dependency links: creating a link, removing a link, and inspecting a task's immediate dependencies. It specifies the contract as it is observable over HTTP — request/response shapes, status codes (including the cycle→409 rejection), and the guarantee that dependency state is left unchanged when an operation fails. It deliberately does not restate the graph algorithm (cycle detection, neighbour computation, unlink semantics); those engine invariants live in `task-orchestration`. The HTTP surface is reachable from both the master token and per-device tokens so the mobile client can manage dependencies.

Second, it owns the **terminal-output URL feature**: extracting followable URLs from agent terminal output, rejecting malformed ones, opening only http/https schemes, and the link-picker selection UI. The URL picker is a terminal-side affordance for following links an agent has printed.

## Requirements

### Requirement: Create a dependency link between two tasks

The system SHALL attach a parent task to a child task via the child's dependency list. On success it SHALL persist the parent's id in the child's `DependsOn` set and report success. The endpoint SHALL be reachable without master privileges (same tier as archive/rename) so device tokens can manage dependencies.

#### Scenario: Linking a child to an existing parent

- **WHEN** a request supplies a valid parent id for an existing child task
- **THEN** the parent id is added to the child's `DependsOn` list and the response reports a linked status

#### Scenario: Linking to a non-existent parent

- **WHEN** the supplied parent id does not correspond to any task
- **THEN** the request is rejected as not found and the child's dependency list is unchanged

#### Scenario: Malformed link request body

- **WHEN** the request body is not valid JSON
- **THEN** the request is rejected as a bad request and no dependency is created

### Requirement: Reject cycle-forming links over HTTP with a 409

When the orchestration engine reports that a link would form a cycle, the HTTP endpoint SHALL translate that into a conflict response whose body carries the cycle path the engine reported, so the client can render the offending chain inline. The endpoint SHALL leave the response indicating that no dependency state changed. (The graph-level definition of "cycle" and the path-ordering semantics are owned by `task-orchestration`; this requirement only fixes how that outcome is surfaced over HTTP.)

#### Scenario: Linking would close a cycle

- **WHEN** a link request is one the engine rejects as cycle-forming
- **THEN** the request is answered with a conflict status and a response body containing a non-empty cycle path field, and the response indicates no dependency was added

### Requirement: Remove a dependency link over HTTP

The unlink endpoint SHALL identify both the child and the parent from the request path so it works with no request body (browser-friendly DELETE). On a successful unlink it SHALL report an unlinked status. The endpoint SHALL be reachable without master privileges. (The graph effect of removal — and its no-op-when-absent behaviour — is owned by `task-orchestration`.)

#### Scenario: Unlinking via a no-body DELETE

- **WHEN** a DELETE request names a child and a parent entirely in its path with no body
- **THEN** the request succeeds and the response reports an unlinked status

### Requirement: Expose a task's immediate dependencies over HTTP

The dependency-inspection endpoint SHALL return, in its response body, the one-hop upstream and downstream neighbours the engine computes for a task. This read SHALL be available to device tokens so the dependency view can render on mobile. (The definition of "one-hop neighbour" is owned by `task-orchestration`; this requirement only fixes that the read is exposed over HTTP and reachable by device tokens.)

#### Scenario: Reading neighbours over HTTP

- **WHEN** a device-token request inspects a task that two others declare as a parent and that itself has no parents
- **THEN** the response succeeds and its body reports zero upstream neighbours and two downstream neighbours

### Requirement: Map dependency and graph errors to HTTP statuses

The system SHALL translate dependency operation failures to stable HTTP statuses: an empty/invalid task id SHALL be a bad request, an unknown task SHALL be not found, a cycle SHALL be a conflict, and any unrecognised failure SHALL be an internal server error so unexpected conditions surface rather than masquerading as client errors.

#### Scenario: Operation references an unknown task

- **WHEN** a dependency operation targets a task id that does not exist
- **THEN** the request is rejected as not found

#### Scenario: Unexpected backend failure

- **WHEN** a dependency operation fails for a reason that is neither a missing task, an empty id, nor a cycle
- **THEN** the request is rejected as an internal server error

### Requirement: Extract followable URLs from terminal output

The system SHALL extract unique http and https URLs from raw terminal output that may contain ANSI escape sequences, markdown links, or OSC 8 hyperlinks. Markdown links SHALL contribute their display text as the label; bare URLs SHALL use the URL itself as the label. The extractor SHALL strip terminal formatting before matching, deduplicate identical URLs, and present the distinct schemes and forms uniformly.

#### Scenario: Markdown link and bare URL together

- **WHEN** output contains a markdown link and a separate bare URL
- **THEN** both are returned, the markdown link uses its display text as its label, and the bare URL uses itself as its label

#### Scenario: Same URL appears as both a markdown link and a bare URL

- **WHEN** the same URL appears once in markdown form and once bare
- **THEN** only one link is returned, keeping the markdown display text as its label

#### Scenario: ANSI formatting embedded in or around a URL

- **WHEN** color codes appear in the middle of a URL and cursor-movement sequences follow it
- **THEN** the color codes are removed so the URL stays intact, and the cursor movement prevents unrelated trailing text from merging into the URL

#### Scenario: OSC 8 hyperlink with separate display text

- **WHEN** output contains an OSC 8 hyperlink whose target URL differs from its visible display text
- **THEN** the embedded target URL is extracted and the display text is not spliced into it

### Requirement: Reject incomplete or malformed extracted URLs

The system SHALL exclude URLs that are still being typed or are otherwise not safely openable: truncated URLs, URLs with no host, URLs whose host has no valid top-level domain, and hosts with a leading or trailing dot. The system SHALL accept IPv4 literals, the literal `localhost`, multi-label TLDs, and IDN punycode TLDs. Trailing punctuation that is not part of the URL SHALL be stripped.

#### Scenario: Truncated URL is excluded

- **WHEN** a URL ends in an ellipsis (unicode `…` or a trailing `...`)
- **THEN** that URL is not returned, while other complete URLs in the same content still are

#### Scenario: Host without a valid TLD is excluded

- **WHEN** a URL has no dot in its host, a one-character TLD, a numeric-only TLD, or no host at all
- **THEN** that URL is not returned

#### Scenario: Special hosts and trailing punctuation

- **WHEN** content contains a `localhost` URL, an IPv4-literal URL, or a URL followed by trailing punctuation
- **THEN** the localhost and IPv4 URLs are returned and the trailing punctuation is stripped from the returned URL

### Requirement: Open only http and https URLs from the link UI

The system SHALL open a chosen URL in the user's browser only when its scheme is http or https, and SHALL silently refuse any other scheme (such as file, javascript, or empty input) without launching anything.

#### Scenario: Non-http scheme is refused

- **WHEN** the user attempts to open a URL whose scheme is not http or https, or an empty string
- **THEN** no browser is launched and the attempt is logged as rejected

### Requirement: Choose among multiple extracted links

The system SHALL present a selection modal when multiple links are available, supporting cursor navigation with arrow keys and j/k, confirmation, and cancellation. Navigation SHALL clamp at the first and last entries. The modal SHALL report whether the user selected a link or cancelled, and SHALL expose the currently highlighted link.

#### Scenario: Navigating and selecting a link

- **WHEN** the user moves the cursor down and confirms
- **THEN** the modal reports that a link was selected and exposes the highlighted link as the chosen one

#### Scenario: Cancelling the picker

- **WHEN** the user dismisses the modal with escape or the detach key
- **THEN** the modal reports that it was cancelled and reports that no link was selected

#### Scenario: Navigation clamps at the ends

- **WHEN** the user presses up while at the first entry or down while at the last entry
- **THEN** the highlighted entry does not move past that boundary
