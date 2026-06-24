## ADDED Requirements

### Requirement: System metrics endpoint

The API SHALL expose a read-only `GET /api/system-metrics` endpoint, authenticated
like other API routes, that returns a snapshot of host-system load: overall CPU
utilization percent, the 1/5/15-minute load average, total/used/available memory
with percent, total/used swap with percent, total/used/free disk for the filesystem
holding the Argus data directory (`~/.argus`) along with that path, the Argus
process resident memory, host uptime, and the count of active and idle agent
sessions. Each metric SHALL carry an availability indicator so a metric the host
platform cannot supply is reported as unavailable rather than failing the whole
response. Metrics SHALL be sampled by a background collector on its own interval and
served from cache so requests return promptly and CPU deltas are accurate; the
session counts SHALL be read live at request time.

#### Scenario: Authenticated request returns a metrics snapshot
- **WHEN** an authenticated `GET /api/system-metrics` request is made
- **THEN** the response is 200 OK with a JSON body containing CPU, memory, swap, disk, process, uptime, and session-count fields

#### Scenario: Unauthenticated request is rejected
- **WHEN** a `GET /api/system-metrics` request is made without a valid token
- **THEN** the response is 401 Unauthorized

#### Scenario: Unavailable metric degrades gracefully
- **WHEN** the host platform cannot supply a particular metric (e.g. load average)
- **THEN** that field is marked unavailable in the response and the remaining metrics are still returned with 200 OK

#### Scenario: Session counts are read live
- **WHEN** the snapshot is served
- **THEN** the active/idle session counts reflect the runner's current state at request time, not the cached sample time
