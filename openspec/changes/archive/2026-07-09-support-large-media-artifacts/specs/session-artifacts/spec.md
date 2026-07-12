## MODIFIED Requirements

### Requirement: Serve registered artifact bytes

The API SHALL serve the raw bytes of a single artifact selected by its on-disk filename, with the Content-Type that matches the artifact's recorded type. Range requests and HEAD handling SHALL be supported for the served content.

#### Scenario: Serve by artifact type

- **WHEN** a client requests a registered artifact whose type is HTML, markdown, PDF, image, text, audio, or video
- **THEN** the response is HTTP 200, the body is the exact stored bytes, and the Content-Type matches the artifact type (for example `text/html; charset=utf-8` for HTML, `application/pdf` for PDF, `image/png` for a PNG image, `audio/mpeg` for an mp3, `video/mp4` for an mp4)

#### Scenario: Range request against a large media artifact

- **WHEN** a client requests a byte range of a registered audio or video artifact via a `Range` header
- **THEN** the response serves the requested range (HTTP 206) so the client can scrub/seek without downloading the full file first
