# Mobile PWA

## Purpose

The mobile PWA is the installable, offline-aware web client that lets a user manage Argus agent sessions from a phone. It ships as a single-page app shell (HTML, vendored xterm.js, manifest, icons) served by the REST API and cached by a service worker, an on-screen virtual key bar for keys iOS soft keyboards omit, a full-screen offline view when the daemon is unreachable, and OS integration via web share target and push-notification deep links.
## Requirements
### Requirement: Installable web app manifest

The app SHALL provide a web app manifest declaring a standalone, installable PWA with an app name, theme/background colors, and icon set so the OS can add it to the home screen and launch it chromeless.

#### Scenario: Manifest declares standalone display

- **WHEN** the OS reads `/manifest.webmanifest`
- **THEN** `display` is `standalone`, `start_url` is `/`, and both `theme_color` and `background_color` are set

#### Scenario: Icon set covers required sizes and purposes

- **WHEN** the OS selects an app icon
- **THEN** 192x192 and 512x512 PNG icons are available, including a `maskable` 512x512 variant

### Requirement: Service worker caches the app shell

The service worker SHALL cache the app shell (root document, manifest, vendored xterm.js/CSS, fit addon, icons) cache-first, serving cached assets immediately and refreshing them from the network in the background, so the SPA boots without a live connection.

#### Scenario: Shell asset served from cache

- **WHEN** a GET request targets a cached same-origin shell asset
- **THEN** the cached response is returned immediately and a background fetch refreshes the cache entry when the network response is OK

#### Scenario: Uncached GET falls back to network

- **WHEN** a GET request for a same-origin, non-`/api/` asset is not in the cache
- **THEN** the service worker fetches it from the network and caches a basic, OK response

#### Scenario: Cross-origin requests are not intercepted

- **WHEN** a request targets a different origin
- **THEN** the service worker does not intercept it and the browser handles it normally

### Requirement: API requests bypass the cache

The service worker SHALL treat `/api/*` requests as network-only and never cache them, because they carry authentication and dynamic data.

#### Scenario: API path is not cached

- **WHEN** a request path starts with `/api/`
- **THEN** the service worker does not intercept or cache the request and lets the browser fetch it from the network

### Requirement: Cache versioning busts stale shells

The service worker SHALL key its cache by a version string (`SW_VERSION`) and, on activation, delete every cache whose key differs from the current version, so bumping the version after any shell asset change replaces the stale cached shell on every installed device.

#### Scenario: Old cache versions purged on activation

- **WHEN** the service worker activates with a new `SW_VERSION`
- **THEN** all caches keyed by a different version are deleted and the worker claims existing clients

#### Scenario: New worker takes control immediately

- **WHEN** the service worker installs
- **THEN** it precaches the shell asset list and skips waiting so the updated worker activates without requiring all tabs to close

### Requirement: Offline view on repeated connection failure

The SPA SHALL show a full-screen offline view when the daemon becomes unreachable, requiring two consecutive `/api` poll failures before flipping (so a single transient hiccup does not eject the user), while the browser `offline` event flips immediately.

#### Scenario: Two consecutive failures trigger offline view

- **WHEN** two consecutive `/api` connection attempts fail
- **THEN** the offline screen is shown and the terminal stream, detail view, and open modals are torn down

#### Scenario: Browser offline event flips immediately

- **WHEN** the browser fires an `offline` event and a token is present
- **THEN** the offline screen is shown without waiting for the failure threshold

#### Scenario: Successful connection hides the offline view

- **WHEN** a connection attempt succeeds
- **THEN** the failure counter resets, the offline screen is hidden, and the user is returned to the main app (or the auth screen when no token is present)

#### Scenario: Retry from offline view

- **WHEN** the user taps Retry on the offline view with a saved token
- **THEN** the SPA re-attempts the connection and, on success, hides the offline view

### Requirement: Reconnect on browser online event

When the browser regains connectivity, the SPA SHALL re-attempt the connection if offline, and otherwise refresh the task list and re-attach the terminal stream for an in-progress task.

#### Scenario: Online event while offline view shown

- **WHEN** the browser fires an `online` event and the offline view is shown
- **THEN** the SPA retries the connection

#### Scenario: Online event re-attaches in-progress terminal

- **WHEN** the browser fires an `online` event while the main app is visible, a token is present, and the open task is in progress with no active stream
- **THEN** the SPA refreshes and re-attaches the terminal so disk-log replay runs on reconnect

### Requirement: Virtual key bar for missing soft-keyboard keys

The SPA SHALL provide a toggleable on-screen key bar that sends raw escape sequences for keys iOS soft keyboards omit (Esc, Tab, Shift+Tab, Enter, arrows) to the PTY, with its visibility preference persisted and the bar mounted only while the compose bar is visible.

#### Scenario: Key bar sends raw byte sequences

- **WHEN** the user taps a virtual key (e.g. Esc, Tab, an arrow)
- **THEN** the corresponding raw escape sequence is sent to the PTY over the same input path as typed text

#### Scenario: Visibility preference persists

- **WHEN** the user toggles the key bar
- **THEN** the preference is saved to local storage and restored on the next load

#### Scenario: Bar only mounts with the compose bar

- **WHEN** the key bar is enabled but the compose bar is not visible
- **THEN** the key bar is not displayed

### Requirement: Web share target prefills a new task

The SPA SHALL register a web share target at `/share`; when content is shared to the app it SHALL capture the shared fields, switch to the create-task view, and prefill the prompt with the combined content. The service worker SHALL serve the cached app shell for `/share` so the SPA boots even offline.

#### Scenario: Shared content prefills the create prompt

- **WHEN** the app is opened via a share to `/share` carrying title/text/url params
- **THEN** the SPA switches to the create tab and prefills the prompt with the non-empty shared fields combined

#### Scenario: Share fields are length-capped

- **WHEN** a shared field exceeds the per-field maximum
- **THEN** the field is truncated before being stored

#### Scenario: Share URL is stripped after capture

- **WHEN** the share landing is captured
- **THEN** the URL is rewritten to `/` so a refresh does not re-fire the share and the token does not linger in history

#### Scenario: Share target served offline

- **WHEN** a GET request hits `/share`
- **THEN** the service worker responds with the cached app shell

### Requirement: Push notification click deep-links to a task

A push notification carrying a task id SHALL, on click, focus an existing app window and open the deep-linked task without a reload when one exists, and otherwise open a fresh window deep-linked to the task; the SPA SHALL open the task once tasks have loaded.

#### Scenario: Existing window opens task without reload

- **WHEN** a notification with a task id is clicked and a same-origin window already exists
- **THEN** the service worker focuses that window and posts a message instructing the SPA to open the task, without navigating or reloading

#### Scenario: No existing window opens deep link

- **WHEN** a notification with a task id is clicked and no app window exists
- **THEN** the service worker opens a new window at `/?task=<id>`

#### Scenario: SPA opens deep-linked task

- **WHEN** the SPA receives the open-task message or boots with a `?task=` parameter
- **THEN** it opens the matching task, refreshing and falling back to the archived filter to locate it, and silently no-ops if the task no longer exists

### Requirement: Push notification rendering

On a push event the service worker SHALL display a notification using the payload title and body (with safe defaults when the payload is missing or malformed), tagged by task id so notifications for the same task collapse.

#### Scenario: Push displays notification with defaults

- **WHEN** a push event arrives with no data or unparseable data
- **THEN** a notification is shown with a default title and body

#### Scenario: Push notification tagged by task

- **WHEN** a push event carries a task id
- **THEN** the notification is tagged by that task id so repeat notifications replace rather than stack

### Requirement: Web new-task model select

The web new-task form SHALL present the model field as a per-backend `<select>` rather than a free-text input. The select SHALL be populated from the backend roster the form already fetches, which SHALL include a per-backend `models` list. The options SHALL be, in order: a `default` option (labeled to surface the backend's configured default model when one exists), the backend's known models, and a `custom…` option that reveals a text input for a model not in the list. When the backend selector changes, the model select SHALL be repopulated for the newly selected backend and reset to `default`.

The value submitted with the create request SHALL be: empty when `default` is selected (so the backend/CLI default applies), the chosen model when a listed model is selected, or the trimmed typed value when `custom…` is selected — matching the value semantics of the prior free-text field.

#### Scenario: Model select populates from the selected backend

- **WHEN** the user opens the web new-task form and selects a backend
- **THEN** the model select offers that backend's `default`, its known models, and `custom…`

#### Scenario: Changing backend repopulates the model select

- **WHEN** the user changes the backend selector after choosing a model
- **THEN** the model select is repopulated for the new backend and reset to `default`

#### Scenario: Custom model via the web form

- **WHEN** the user selects `custom…` and types a model identifier
- **THEN** the create request carries that trimmed identifier as the model value

### Requirement: Hera orchestration tab

The PWA SHALL provide a read-only "Hera" tab (the second tab, reachable by the `g` hotkey and documented in the in-app help modal) that renders the orchestration roster from `GET /api/hera`. Orchestrators SHALL be grouped into Pinned, Active, and Archived sections, with a separate Freelance section for hoisted freelance roles. Each role row SHALL show a status dot keyed to the hera status, the role kind and name, and — when the role holds a live binding — the bound task's name and workflow badge plus a ready-to-close indicator when flagged. Tapping a role that has a live binding SHALL open that task's existing detail/terminal overlay; the roster itself SHALL remain read-only (no mutation controls). All orchestrator, role, and task names rendered into the DOM MUST be HTML-escaped.

#### Scenario: Switching to the Hera tab renders the roster
- **WHEN** the user presses `g` or taps the Hera tab
- **THEN** the Hera view is shown and the roster is fetched and rendered, with a status line summarizing the orchestrator and role counts

#### Scenario: Empty roster placeholder
- **WHEN** `/api/hera` returns no orchestrators and no freelance roles
- **THEN** the Hera view shows an empty-state placeholder rather than an error

#### Scenario: Drill into a live role
- **WHEN** the user taps a role row that has a live binding
- **THEN** the task detail overlay opens for that role's bound task

#### Scenario: Roster excluded from mutation
- **WHEN** the Hera tab is displayed
- **THEN** it exposes no controls that create, modify, or delete orchestrators or roles

### Requirement: Hera tab participates in the connection lifecycle

While the Hera tab is foregrounded, the periodic poll SHALL refresh the roster instead of the task list, and the roster fetch SHALL drive the same connection-tracking the task-list refresh does — updating the connection dot and the consecutive-failure count that promotes the offline view — so a daemon that becomes unreachable while the user is on the Hera tab is still detected. On reconnect (the retry action and the browser `online` event) the roster SHALL be refreshed when the Hera tab is the active tab.

#### Scenario: Offline detection on the Hera tab
- **WHEN** the daemon becomes unreachable while the Hera tab is foregrounded and the roster fetch fails repeatedly past the failure threshold
- **THEN** the connection dot turns to its error state and the offline view is shown, exactly as it would be from the task-list poll

#### Scenario: Successful fetch clears failure state
- **WHEN** a roster fetch succeeds
- **THEN** the consecutive-failure count is reset and the offline view is hidden if it was showing

#### Scenario: Roster refreshes on reconnect
- **WHEN** the browser fires the `online` event (or the user taps retry) while the Hera tab is active
- **THEN** the roster is refreshed rather than left stale until the next poll tick

### Requirement: Live system-metrics panel in Settings

The Settings view SHALL include a System panel that displays live host-load
metrics: CPU utilization percent and the 1/5/15-minute load average, memory and
swap usage (used/total with percent bars), disk usage of the Argus data-directory
filesystem (used/total/free with percent and the path), the Argus process resident
memory, host uptime, and the active/idle agent-session counts. The panel SHALL
fetch `GET /api/system-metrics` and refresh on a short interval (~2 seconds) only
while the Settings tab is visible, and SHALL stop refreshing when the user navigates
away so no background polling continues for a hidden tab. A metric reported as
unavailable SHALL be rendered as a placeholder (e.g. an em dash) rather than a
misleading zero.

#### Scenario: Panel populates on Settings open
- **WHEN** the user opens the Settings tab
- **THEN** the System panel fetches metrics immediately and renders CPU, load, memory, swap, disk, process memory, uptime, and session counts

#### Scenario: Panel refreshes live while visible
- **WHEN** the Settings tab remains open
- **THEN** the panel re-fetches and re-renders the metrics roughly every 2 seconds

#### Scenario: Polling stops when the tab is hidden
- **WHEN** the user navigates away from the Settings tab
- **THEN** the refresh interval is cleared and no further metric fetches occur until the tab is reopened

#### Scenario: Unavailable metric shows a placeholder
- **WHEN** a metric is reported unavailable by the API
- **THEN** that row renders a placeholder instead of a numeric value

