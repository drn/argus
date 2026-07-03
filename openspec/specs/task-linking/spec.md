# Task Linking

## Purpose

This capability owns the **terminal-output URL feature**: extracting followable URLs from agent terminal output, rejecting malformed ones, opening only http/https schemes, and the link-picker selection UI. The URL picker is a terminal-side affordance for following links an agent has printed.

> **Retired:** this capability previously also owned a **REST/HTTP dependency-linking contract** (create/remove a `depends_on` link, inspect a task's neighbours, cycle→409 mapping). That surface — and the `task-orchestration` graph engine beneath it — was removed when the `depends_on` DAG was retired in favor of Hera (coordinator-driven worker spawning). See `task-orchestration` (retired) for the full removal note.
## Requirements
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

### Requirement: Flag only github.com pull-request URLs as PRs

The system SHALL mark an extracted link as a pull request only when its host is
`github.com` and its path has the form `/<owner>/<repo>/pull/<number>`, optionally
followed by a further path segment (such as `/files`). URLs on any other host —
including GitHub Enterprise hosts and GitLab merge-request URLs — SHALL NOT be
flagged as pull requests, even when their path contains `/pull/<n>` or
`/merge_requests/<n>`. The PR flag is the single source of truth for the PR
indicator on every link surface; clients SHALL NOT re-derive it.

#### Scenario: github.com pull request is flagged

- **WHEN** a link is `https://github.com/org/repo/pull/123` (with or without a
  trailing sub-path like `/files`)
- **THEN** it is flagged as a pull request

#### Scenario: Non-PR github.com paths are not flagged

- **WHEN** a github.com link points at a repo root, an issue, or a compare range
- **THEN** it is not flagged as a pull request

#### Scenario: Non-github.com hosts are never flagged

- **WHEN** a link is a GitHub Enterprise pull URL, a GitLab merge-request URL, or
  any other host whose path contains `/pull/<n>`
- **THEN** it is not flagged as a pull request

### Requirement: List pull-request links first

The system SHALL order extracted links so that pull-request links appear before
non-pull-request links. Ordering SHALL be stable: the relative order of links
within the PR group and within the non-PR group is preserved from extraction
order. This ordering SHALL be produced by the shared extractor so every consuming
surface (the terminal link pickers, the web link modal, and the REST links
endpoint) presents pull requests first without re-sorting.

#### Scenario: A PR link printed after a non-PR link still leads

- **WHEN** terminal output contains a non-PR URL followed by a github.com PR URL
- **THEN** the extracted list returns the PR link before the non-PR link

#### Scenario: Order within each group is preserved

- **WHEN** terminal output contains several non-PR URLs and several PR URLs
  interleaved
- **THEN** all PR links appear first in their original relative order, followed by
  all non-PR links in their original relative order

