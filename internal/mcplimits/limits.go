// Package mcplimits shares server wait limits with backend client configuration.
package mcplimits

const (
	// HeraInboxTimeoutSeconds caps a server-side inbox wait, matching task_ask's
	// two-minute envelope so requests cannot remain open indefinitely.
	HeraInboxTimeoutSeconds = 120
	// ClientTimeoutSeconds leaves time for the server to return an empty inbox
	// after its longest wait. HTTP/SSE clients otherwise commonly abort at 60s.
	ClientTimeoutSeconds = HeraInboxTimeoutSeconds + 30
	// ClientTimeoutMilliseconds is the same deadline for JSON/env consumers.
	ClientTimeoutMilliseconds = ClientTimeoutSeconds * 1000
)
