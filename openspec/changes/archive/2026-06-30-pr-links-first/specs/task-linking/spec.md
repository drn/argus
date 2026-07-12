# Task Linking

## ADDED Requirements

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
