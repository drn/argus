# Web Push Notifications

## Purpose

Argus delivers Web Push notifications to registered PWA devices so the user can be alerted when an agent session needs attention without keeping the UI open. This capability owns VAPID key lifecycle, device-subscription registration, the fan-out that sends notifications, expired-subscription pruning, and the idle-watcher gating that decides when a busy→idle agent transition is worth a push.

## Requirements

### Requirement: VAPID key lifecycle

The system SHALL persist a single VAPID keypair across restarts, generating one on first use and reusing the stored keys thereafter. The public key SHALL be retrievable for the PWA to register subscriptions.

#### Scenario: Generate keypair on first run

- **WHEN** the push manager initializes and no VAPID public or private key is stored
- **THEN** a new VAPID keypair is generated, persisted, and the public key is non-empty

#### Scenario: Reuse stored keypair across restarts

- **WHEN** the push manager re-initializes over a store that already holds a VAPID keypair
- **THEN** the previously stored public key is returned verbatim and no new keypair is generated

#### Scenario: Public key endpoint unavailable without manager

- **WHEN** a client requests the VAPID public key and no push manager is configured
- **THEN** the request is rejected with HTTP 503

### Requirement: VAPID subject management

The system SHALL track the VAPID JWT subject claim, which MUST be a routable mailto: or https:// URL, persist changes, and clear a known-bad legacy default so push services do not keep rejecting deliveries.

#### Scenario: Set and persist a subject

- **WHEN** a valid https subject is supplied
- **THEN** the subject is stored, persisted, and returned on subsequent reads

#### Scenario: Empty or unchanged subject is a no-op

- **WHEN** an empty subject, or a subject equal to the current one, is supplied
- **THEN** the stored subject is left unchanged and no persistence write occurs

#### Scenario: Legacy bad default cleared on init

- **WHEN** the push manager initializes and the stored subject equals the known-bad legacy default
- **THEN** the stored subject is cleared to empty

### Requirement: Subscription registration

The system SHALL register a device push subscription from a W3C PushSubscription payload, requiring an endpoint and both encryption keys, and SHALL reject malformed or incomplete requests.

#### Scenario: Valid subscription is created

- **WHEN** a subscribe request supplies an endpoint, p256dh key, and auth key
- **THEN** the subscription is stored and HTTP 201 is returned with its new id

#### Scenario: Missing endpoint or keys rejected

- **WHEN** a subscribe request omits the endpoint, the p256dh key, or the auth key
- **THEN** the request is rejected with HTTP 400

#### Scenario: Invalid JSON rejected

- **WHEN** a subscribe request body is not valid JSON
- **THEN** the request is rejected with HTTP 400

### Requirement: Subscription listing and removal

The system SHALL list registered subscriptions with their endpoints masked, and SHALL allow removal of a subscription by id.

#### Scenario: List masks long endpoints

- **WHEN** subscriptions are listed and an endpoint exceeds the masking threshold
- **THEN** each returned endpoint is truncated to a masked form rather than exposed in full

#### Scenario: Delete by id

- **WHEN** an unsubscribe request supplies a valid subscription id
- **THEN** that subscription is removed and the deleted id is returned

#### Scenario: Invalid id rejected

- **WHEN** an unsubscribe request supplies a non-numeric id
- **THEN** the request is rejected with HTTP 400

### Requirement: Notification fan-out

The system SHALL send a notification carrying title, body, and an optional task id to every registered subscription, and SHALL skip the send entirely when no subscriptions are registered. A nil manager SHALL be safe to call.

#### Scenario: Fan-out to all subscriptions

- **WHEN** a notification is sent and at least one subscription is registered and a VAPID subject is set
- **THEN** a push request is delivered to each subscription's endpoint

#### Scenario: No subscriptions skips send

- **WHEN** a notification is sent and no subscriptions are registered
- **THEN** no push request is made

#### Scenario: Send skipped when subject unset

- **WHEN** a notification is sent and no VAPID subject has been established
- **THEN** no push request is delivered and the subscription is preserved

### Requirement: Per-key throttling

The system SHALL throttle repeated notifications sharing a non-empty throttle key to at most one send per throttle window, SHALL disable throttling when the key is empty, and SHALL NOT record a throttle entry when there are no subscriptions.

#### Scenario: Repeat within window suppressed

- **WHEN** a second notification with the same non-empty throttle key is sent within the throttle window
- **THEN** the second send is suppressed and the recorded send time is unchanged

#### Scenario: Empty key disables throttling

- **WHEN** notifications are sent with an empty throttle key
- **THEN** no throttle entry is recorded and sends are not suppressed

#### Scenario: No-subscription state does not poison throttle

- **WHEN** a notification with a non-empty throttle key is sent while no subscriptions exist
- **THEN** no throttle entry is recorded, so a later send after the user subscribes is not suppressed

### Requirement: Expired-subscription pruning

The system SHALL drop a subscription when the push service reports it as permanently gone (HTTP 410 or 404), and SHALL preserve the subscription on transient or other non-success responses and on send errors.

#### Scenario: Drop on Gone or Not Found

- **WHEN** a push delivery to a subscription returns HTTP 410 or HTTP 404
- **THEN** that subscription is removed from the store

#### Scenario: Keep on other failures

- **WHEN** a push delivery returns another non-success status (e.g. HTTP 500) or the send errors out
- **THEN** the subscription is preserved

#### Scenario: Keep on success

- **WHEN** a push delivery returns a success status
- **THEN** the subscription is preserved

### Requirement: Test notification endpoint

The system SHALL expose a master-only endpoint that sends a verification notification to all registered devices, rejecting non-master callers, so a device holder cannot spam every registered device.

#### Scenario: Master triggers test push

- **WHEN** a master-authenticated caller invokes the test-push endpoint with a push manager configured
- **THEN** a test notification is fanned out to all subscriptions and success is reported

#### Scenario: Non-master rejected

- **WHEN** a non-master caller invokes the test-push endpoint
- **THEN** the request is rejected and no notification is sent

### Requirement: Idle-watcher push gating

The system SHALL fire at most one idle push per agent work cycle, only on a busy→idle transition, only after the session has received user input, and SHALL re-arm only when fresh input arrives after the last push. The first observation of a session SHALL never fire, and per-task state SHALL be independent.

#### Scenario: Busy to idle fires once

- **WHEN** a session previously observed busy transitions to idle and has received user input since startup
- **THEN** exactly one idle push fires for that transition

#### Scenario: First observation suppressed

- **WHEN** a session is observed for the first time, whether idle or busy
- **THEN** no push fires

#### Scenario: No user input ever suppresses

- **WHEN** a session goes idle but has never received any user input
- **THEN** no push fires

#### Scenario: Idle blips after a push stay silent

- **WHEN** a session that has already pushed flips busy then idle again with no new user input in between
- **THEN** no further push fires

#### Scenario: Fresh input re-arms

- **WHEN** user input arrives strictly after the last push and the agent then goes idle again
- **THEN** a new idle push fires

#### Scenario: Per-task independence

- **WHEN** multiple sessions are watched concurrently
- **THEN** each session's fire decision is tracked independently by task id
