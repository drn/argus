## Why

The user dictates with Wispr Flow (hold a push-to-talk key, speak, release). Turning a spoken thought into a running Argus task currently means switching to Argus, opening the new-task form, and pasting. A single held global hotkey should capture speech and start a task in the background, with no external hotkey tool and no macOS app dependency.

## What Changes

- New `voice-capture` capability: the Argus daemon owns a global modifier-only hotkey tap (default Right Option, pure Go via purego, no cgo). Holding it tells the Wispr Flow app to start recording (`open -g wispr-flow://start-hands-free`); releasing it sends `stop-hands-free`. The daemon then reads the finished transcript from Wispr's local SQLite history DB, resolves the project, creates and starts an auto-named task, and posts a macOS notification.
- New `internal/wispr` driver (URL-scheme commands plus a read-only poll of `flow.sqlite`), `internal/hotkey` (combo parser, pure tracker state machine, darwin event tap), `internal/voice` (daemon service), and `internal/voicetask` (parser, Jev classifier, shared launcher).
- Project resolution order: spoken "in project X" phrase, then the Jev typed-decision classifier (optional, above a confidence floor), then `defaults.voice_project`.
- New daemon RPCs `VoiceStart`/`VoiceStop`/`VoiceStatus` and CLI `argus voice start|stop|status`.
- New `[voice]` config table (config.toml only): `enabled`, `hotkey`, `wispr_db`, `transcript_timeout_seconds`, and `[voice.jev]` (`api_key`, `endpoint`, `model`, `min_confidence`).
- `POST /api/voice-tasks` now delegates to the shared launcher; `project_source` may also be `jev`.
- New setting `defaults.voice_project` (config, REST settings, web settings UI).
- TUI task list: a `✦` new-task indicator on the task row and on its project header, a one-time status-bar notice, and clear-on-open.

## Impact

- Specs: voice-capture (new), rest-api, config-management, task-list-view.
- Code: `internal/voicetask`, `internal/wispr`, `internal/hotkey`, `internal/voice`, `internal/daemon`, `internal/daemon/client`, `cmd/argus`, `internal/api`, `internal/config`, `internal/db`, `internal/tui`, `internal/apiclient`, `internal/apistore`, web SPA.

## Non-Goals / Named Follow-ups

- No macOS app (`macos/`) integration; the app neither hosts the hotkey nor shows capture UI.
- Wispr Flow also pastes the transcript into the focused app; this cannot be disabled and is accepted.
- Wispr's URL commands and `flow.sqlite` schema are undocumented (observed on v1.6.1034); the driver is best-effort and fails loudly in logs.
- `[voice]` is config.toml-only: no DB keys, REST settings, or TUI/web settings UI. Changes take effect on daemon restart.
- Web and macOS "new" badge in task lists, a REST way to clear `ui.new`, and a remote-TUI (`--remote`) `ui.new` indicator are named follow-ups (the REST task DTO does not expose `ui.new`; apistore reconstructs only the `pr` namespace).
- Linux/Windows hotkey hosts (the tap is darwin-only; other platforms get a stub returning `ErrUnsupported`; `argus voice start|stop` still works anywhere Wispr does).
- No new TUI keybindings.
