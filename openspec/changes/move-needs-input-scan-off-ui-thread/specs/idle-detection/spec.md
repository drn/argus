## ADDED Requirements

### Requirement: The needs-input scan does not run on the UI goroutine

In the running TUI, the needs-input and content-idle scan, including its log reads, terminal emulation and blocked-role auto-clear, SHALL run on a background goroutine and SHALL NOT block the UI goroutine or hold the App mutex while it runs. At most one scan SHALL run at a time. A tick that arrives while a scan is running SHALL replace any waiting input, so scans never queue up.

#### Scenario: Keys are handled while a scan runs

- **WHEN** a scan is in progress and a key is pressed
- **THEN** the key is handled and a frame can be drawn before the scan finishes

#### Scenario: A slow scan does not accumulate work

- **WHEN** a scan takes longer than several ticks
- **THEN** only one further scan runs after it, using the newest input

#### Scenario: A failed scan keeps the last result

- **WHEN** a scan panics
- **THEN** the failure is logged, the TUI keeps running with the last published result, and later ticks scan again

### Requirement: Scan inputs share no mutable state with the UI goroutine

A background scan SHALL work from a snapshot containing only values taken on the UI goroutine, and SHALL NOT read `*model.Task` values or the App's task list. The scan's persistent per-session state SHALL be accessed only by the scan, and the UI goroutine SHALL NOT read it while a scan may be running.

#### Scenario: Task changes during a scan do not race

- **WHEN** task records are changed on the UI goroutine while a scan is running
- **THEN** no data race occurs, and the scan uses the archived state from its snapshot

#### Scenario: A runner swap during a scan does not race

- **WHEN** the daemon client is replaced on the UI goroutine while a scan is running
- **THEN** the scan keeps using the runner captured in its snapshot, with no data race

### Requirement: Scan results are published on the UI goroutine

The TUI SHALL apply a background scan's results to the task list, the Hera rail and the attention bar only on the UI goroutine, applying the bridge state current at that moment. Sticky carry-forward for the next scan SHALL use the scan's own previous result, so a flag cleared by a scan cannot be restored by an input snapshot taken before that scan finished.

#### Scenario: Results reach the views

- **WHEN** a background scan completes
- **THEN** the task list, the Hera rail and the attention bar show its results after the next frame

#### Scenario: A cleared flag is not resurrected

- **WHEN** a scan clears a session's flag while the next scan's input is already waiting
- **THEN** the next scan does not carry the cleared flag forward

### Requirement: Only the tick starts a background scan

In the running TUI, only the periodic tick SHALL start a needs-input scan. Any other task refresh SHALL apply the last published scan result on the UI goroutine without reading logs or running the emulator, and SHALL NOT advance the scan's tick counters.

#### Scenario: A task action does not scan

- **WHEN** a refresh runs on the UI goroutine because of a task action
- **THEN** no log read or terminal emulation happens on the UI goroutine, and the last published result is applied

#### Scenario: Extra refreshes do not advance counters

- **WHEN** several refreshes occur between two ticks
- **THEN** the scan's consecutive-tick counters advance only when a scan runs

### Requirement: The scan stays callable synchronously

The scan logic SHALL remain callable synchronously with results identical to a background run over the same inputs. An App without a running event loop SHALL scan inline during a refresh, so the first frame and event-loop-free callers see current results.

#### Scenario: A synchronous call returns the same result

- **WHEN** the scan is called synchronously with the same inputs and state as a background run
- **THEN** it returns the same needs-input set

#### Scenario: Startup fills the first frame

- **WHEN** the App is constructed and has not yet started its event loop
- **THEN** a refresh scans inline, so the first frame shows needs-input state
