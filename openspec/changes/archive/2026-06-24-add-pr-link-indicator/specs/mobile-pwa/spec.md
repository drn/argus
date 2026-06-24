## ADDED Requirements

### Requirement: Open Link modal marks pull-request links

The web client's Open Link modal SHALL render a "PR" badge before any link row
whose URL is a pull request or merge request, and SHALL render no badge for any
other link. The PR judgement SHALL come from the `isPR` field on each link
returned by `GET /api/tasks/{id}/links` (server-classified by the shared `links`
package), so the client does not re-derive it. The badge SHALL be inserted using
the DOM API as a separate element, never via `innerHTML`, preserving the
existing invariant that agent-controlled label/URL text is only ever set through
`textContent`.

#### Scenario: PR link shows a badge

- **WHEN** the links payload contains a link with `isPR: true`
- **THEN** its row in the Open Link modal SHALL show a "PR" badge before the link text

#### Scenario: Non-PR link shows no badge

- **WHEN** the links payload contains a link with a falsy `isPR`
- **THEN** its row SHALL show no PR badge
