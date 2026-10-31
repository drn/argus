# voice-capture Specification

## Purpose

Hold-to-talk voice task capture owned by the daemon: a global modifier-only hotkey drives the Wispr Flow app's recording, the finished transcript is read from Wispr's local history DB, the project is resolved (spoken phrase, then optional Jev classifier, then the default project), and an auto-named task is created and started with a macOS notification. Also reachable by `argus voice start|stop|status` and by `POST /api/voice-tasks` (see `rest-api`). Wispr's URL commands and database schema are undocumented, so the driver is best-effort.

## Requirements

### Requirement: Daemon-owned global hold-to-talk hotkey

When `[voice] enabled = true` on macOS, the daemon SHALL install a listen-only global event tap for a modifier-only combo (default `right_option`, configurable via `[voice] hotkey`). Holding the combo for 200 ms with no other key SHALL begin recording; releasing it SHALL end recording. A regular keystroke or an additional modifier while the combo is armed SHALL cancel silently, and a release before 200 ms SHALL do nothing. If Input Monitoring is not granted, the daemon SHALL request it, log, and notify once instead of starting the tap. The tap SHALL re-enable itself when macOS disables it for timeout or user input. On non-darwin platforms the tap SHALL be unsupported and the daemon SHALL continue without it.

#### Scenario: Hold and release

- **WHEN** the user holds Right Option for longer than 200 ms and releases it
- **THEN** the service starts recording on the hold and stops it on the release

#### Scenario: Shortcut use does not record

- **WHEN** the user presses Right Option together with another key
- **THEN** recording never starts and nothing is launched

#### Scenario: Input Monitoring missing

- **WHEN** the combo is enabled but Input Monitoring has not been granted
- **THEN** the daemon requests access, logs it, posts one notification telling the user to grant it and restart the daemon, and does not start the tap

### Requirement: Wispr Flow driver

The system SHALL start and stop Wispr Flow recording by running `open -g wispr-flow://start-hands-free` and `open -g wispr-flow://stop-hands-free`, and SHALL read the resulting transcript from Wispr's local SQLite `History` table (default `~/Library/Application Support/Wispr Flow/flow.sqlite`, overridable via `[voice] wispr_db`) opened read-only. It SHALL return the first row at or after the recording start whose status is final with non-empty text (`formatted` uses `formattedText` falling back to `asrText`; `raw_transcript` uses `asrText`), keep waiting while the status is `processing` or empty, and fail on `dismissed`/`error`, on timeout (`[voice] transcript_timeout_seconds`, default 30), or when the database file is missing. Transcript text SHALL NOT be logged; only its length.

#### Scenario: Formatted transcript

- **WHEN** a `History` row with status `formatted` appears after the recording started
- **THEN** its `formattedText` is returned

#### Scenario: Dismissed recording

- **WHEN** the matching row has status `dismissed`
- **THEN** the wait fails with a dismissed error and no task is created

#### Scenario: Transcript never arrives

- **WHEN** no final row appears within the timeout
- **THEN** the wait fails with a timeout error

### Requirement: Voice service lifecycle

The daemon SHALL run a voice service with `Start`, `Stop`, and `Status`. `Start` while already recording SHALL be a no-op. `Stop` while idle SHALL be a no-op. A hold shorter than 300 ms SHALL be discarded without reading a transcript. A recording SHALL auto-stop after 5 minutes. Transcript processing SHALL run independently of recording, so a new recording may begin while a previous transcript is still processing. `Status` SHALL report whether recording is active, when it started, whether the hotkey is enabled, the hotkey, whether the tap is active, whether Input Monitoring is granted, and the last result, task ID, and time.

#### Scenario: Short hold discarded

- **WHEN** recording is stopped 100 ms after it started
- **THEN** no transcript is read, no task is created, and the discard is logged

#### Scenario: Overlapping captures

- **WHEN** a second recording starts while the first transcript is still being processed
- **THEN** both complete independently

### Requirement: Project resolution order

The launcher SHALL resolve a transcript's project in this order: (1) a spoken "in project <name>" phrase matching a known project, with the phrase stripped from the prompt (`project_source` `spoken`); (2) when a Jev API key is configured, the Jev typed-decision classifier over the project names plus `none`, accepted only when the chosen project is a known project and its probability is at least `[voice.jev] min_confidence` (default 0.7) (`project_source` `jev`); (3) `defaults.voice_project` when it names a known project (`project_source` `default`). A classifier error SHALL be logged and resolution SHALL fall through to the default. When no project resolves the launcher SHALL fail with a no-project error, and an empty transcript or empty remaining prompt SHALL fail with an empty-transcript error. A stamping failure SHALL be logged and SHALL NOT fail the launch.

#### Scenario: Spoken wins over classifier

- **WHEN** the transcript says "in project argus, fix login" and a classifier is configured
- **THEN** the project is `argus`, the classifier is not called, and `project_source` is `spoken`

#### Scenario: Classifier above floor

- **WHEN** the transcript names no project and Jev answers `forge` with probability 0.93
- **THEN** the task is created in `forge` with the transcript verbatim and `project_source` `jev`

#### Scenario: Classifier below floor or none

- **WHEN** Jev answers `none` or a probability under `min_confidence`
- **THEN** the launcher uses `defaults.voice_project`

#### Scenario: Classifier failure

- **WHEN** the Jev endpoint returns a non-2xx response
- **THEN** the failure is logged without the API key and the default project is used

### Requirement: Jev classifier request

The Jev classifier SHALL POST a typed-decision request to `[voice.jev] endpoint` (default `https://api.typesafe.ai/v1/systemone`) with `Authorization: Bearer <key>`, a `state` holding the transcript, and one `project` question of type `choice` whose criteria are each project name plus `none`. The `model` field SHALL be sent only when `[voice.jev] model` is non-empty. The API key SHALL be a secret source resolved through the secrets resolver (for example an `op://` reference) and SHALL NOT appear in errors or logs. The request SHALL time out after 3 seconds. An empty key SHALL disable the classifier.

#### Scenario: Model omitted

- **WHEN** `[voice.jev] model` is empty
- **THEN** the request body has no `model` field

### Requirement: Task launch notifications

After a capture finishes the daemon SHALL post a macOS notification via `osascript`, passing title and body as `argv` items and never interpolating text into the script. On success the title SHALL be "Argus: task launched" and the body SHALL contain the task name and project, a `(Jev)` or `(default)` hint when the project was not spoken, and a note naming an unrecognised spoken project. On failure the title SHALL be "Argus: voice task failed" and the body SHALL state the reason (no transcript, dismissed, no project, or create error).

#### Scenario: Default project with unknown spoken name

- **WHEN** the user said "in project zebra" and no such project exists, so the default is used
- **THEN** the notification names the task and project, hints `(default)`, and notes the unknown project

### Requirement: Voice RPC and CLI

The daemon RPC service SHALL expose `VoiceStart`, `VoiceStop`, and `VoiceStatus`, mirrored by the daemon client. The CLI SHALL provide `argus voice start`, `argus voice stop`, and `argus voice status`; `status` SHALL print the status fields and, when Input Monitoring is not granted, how to grant it.

#### Scenario: Manual trigger

- **WHEN** the user runs `argus voice start` then `argus voice stop`
- **THEN** the daemon records and processes the capture exactly as a hotkey hold would

### Requirement: Voice configuration is restart-scoped and config.toml-only

The `[voice]` table (`enabled`, `hotkey`, `wispr_db`, `transcript_timeout_seconds`, and `[voice.jev]`) SHALL be read from config.toml only; changes SHALL take effect on daemon restart.
