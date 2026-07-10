## MODIFIED Requirements

### Requirement: Viewable artifact registration

`artifact_register` SHALL require a source `path`, resolve the owning task by id/cwd, sanitize the destination basename (rejecting path separators and `..`), determine the artifact type from an explicit valid value (html, markdown, pdf, image, text, audio, or video) or infer it from the extension, copy the source file into durable per-task storage under a size cap, and persist a manifest row (last-write-wins per filename). The size cap SHALL be type-dependent: audio and video artifacts (streamed and scrubbed via HTTP Range requests rather than loaded whole) SHALL have a substantially larger cap than the inline-rendered types (html, markdown, pdf, image, text). When the manifest write fails after the copy, the copied bytes MUST be removed so no unreferenced file is left behind.

#### Scenario: Path required

- **WHEN** `artifact_register` is called without a `path`
- **THEN** the response is a tool error reporting path is required

#### Scenario: Invalid explicit type rejected

- **WHEN** `artifact_register` is called with a `type` that is not a recognized artifact type
- **THEN** the response is a tool error listing the valid types (html, markdown, pdf, image, text, audio, video)

#### Scenario: Oversized artifact rejected against its type's cap

- **WHEN** the source file exceeds the size cap for its resolved artifact type
- **THEN** the response is a tool error reporting the cap and no manifest row is created

#### Scenario: Audio/video registration under the larger media cap

- **WHEN** an audio or video file is registered whose size is under the media cap but over the default (html/markdown/pdf/image/text) cap
- **THEN** registration succeeds and the manifest row records the audio/video type
