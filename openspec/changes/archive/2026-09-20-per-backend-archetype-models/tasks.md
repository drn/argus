## 1. Profile contract

- [x] 1.1 Replace the flat archetype model schema with backend-keyed choices and preserve field-level inheritance.
- [x] 1.2 Validate backend keys and each model against its specific backend.
- [x] 1.3 Convert and verify all embedded seed profiles.

## 2. Runtime resolution

- [x] 2.1 Select profile models using the resolved backend and preserve fail-open fallback.
- [x] 2.2 Validate explicit task model overrides before command construction.
- [x] 2.3 Make `profile_resolve` return backend-aware archetype model data.

## 3. Consumers and documentation

- [x] 3.1 Update profile-resolve consumers and embedded skill copies for the new JSON shape.
- [x] 3.2 Document the backend-keyed schema and explicit-override validation invariant.

## 4. Verification

- [x] 4.1 Add focused TDD coverage for parsing, inheritance, validation, resolution, MCP output, and Hera spawning.
- [x] 4.2 Run OpenSpec validation and `make pre-pr`.
