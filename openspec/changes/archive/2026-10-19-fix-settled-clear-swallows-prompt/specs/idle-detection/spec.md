## ADDED Requirements

### Requirement: A settlement clear does not suppress a later, different prompt

A needs-input flag cleared by demonstrated settlement records a cleared marker that carries no content fingerprint, because the session's tail showed no needs-input signal when it settled. A later needs-input candidacy for the same session whose last-input timestamp has not advanced past that marker, and for which a content fingerprint is available, SHALL re-arm the flag and capture a fresh baseline. The signal having been absent from a genuinely idle tail for the full settlement run, any signal appearing afterward is a new prompt. A candidacy with no available fingerprint, and a marker that did not come from settlement, SHALL keep the timestamp-only suppression.

#### Scenario: Permission prompt after a settled clear surfaces (real-bytes regression)

- **WHEN** a session's first prompt is cleared by settlement, nobody types, and the session then parks on a new numbered permission prompt
- **THEN** the system flags the session as waiting for input

#### Scenario: Stale re-detection without a fingerprint stays suppressed

- **WHEN** a settlement-cleared session is re-presented as a candidate with no content fingerprint available and an unchanged last-input timestamp
- **THEN** the system does not re-flag it
