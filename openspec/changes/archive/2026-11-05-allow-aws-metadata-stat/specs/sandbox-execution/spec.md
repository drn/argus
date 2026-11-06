## ADDED Requirements

### Requirement: AWS credential directory metadata is visible

The generated profile SHALL allow `file-read-metadata` (stat) on `~/.aws` after the `~/.aws` read deny, so tools that probe for credential files by existence do not fail with EPERM, while content reads of `~/.aws` remain denied.

#### Scenario: stat on an AWS credentials file succeeds

- **WHEN** a sandboxed command runs `stat ~/.aws/credentials` and the file exists
- **THEN** the stat succeeds

#### Scenario: AWS credential contents remain unreadable

- **WHEN** a sandboxed command tries to read the contents of a file under `~/.aws`
- **THEN** the read is blocked
