## ADDED Requirements

### Requirement: Voice task creation endpoint

`POST /api/voice-tasks` SHALL accept `{"transcript": string, "backend"?: string}`, resolve the project through the shared voice launcher (a spoken "in project <name>" phrase, then the Jev classifier when configured, then `defaults.voice_project`), create and start an auto-named task, stamp task_meta `ui.new="true"` and `ui.source="voice"`, and return 201 with `id`, `name`, `status`, `project`, `prompt`, `project_source` (`spoken`, `jev`, or `default`), and `spoken_project` when an unrecognised project was spoken. An empty transcript or empty remaining prompt SHALL return 400; an unresolvable project SHALL return 422.

#### Scenario: Spoken project

- **WHEN** a client posts `{"transcript": "In project argus, fix the login bug."}` and project `argus` exists
- **THEN** a task is created in `argus` with prompt "fix the login bug." and `project_source` is `spoken`

#### Scenario: Classifier-chosen project

- **WHEN** the transcript names no project, a Jev key is configured, and Jev picks a known project at or above the confidence floor
- **THEN** the task is created in that project and `project_source` is `jev`

#### Scenario: Default project fallback

- **WHEN** the transcript names no project and `defaults.voice_project` is a known project
- **THEN** the task is created in the default project, `project_source` is `default`, and the transcript is kept verbatim as the prompt

#### Scenario: No project resolvable

- **WHEN** the transcript names no project and no valid default is configured
- **THEN** the response is 422 and no task is created

### Requirement: Voice project setting over REST

`GET`/`PUT /api/settings` SHALL expose `defaults.voice_project` (string) alongside `defaults.share_project`. The `[voice]` table is not exposed over REST.

#### Scenario: Updating the setting

- **WHEN** a client PUTs `defaults.voice_project` = `forge`
- **THEN** a subsequent GET returns `forge`
