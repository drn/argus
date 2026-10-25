## ADDED Requirements

### Requirement: Bridge doorbells retain inbox acknowledgment

Hera doorbells SHALL use the shared bridge delivery route when eligible and SHALL retain the hera: delivery-ID namespace, durable message storage and enqueue-time delivery-mode stamping. hera_inbox and hera_mark_read SHALL cancel queued or claimed delivery before submission exactly as they cancel PTY delivery. Bridge acknowledgment SHALL acknowledge notification transport only and SHALL NOT mark durable Hera messages read.

#### Scenario: Inbox cancels bridge claim

- **WHEN** hera_inbox returns a message whose doorbell is claimed but not authorized
- **THEN** the claim cannot subsequently authorize a prompt and the message is marked read
