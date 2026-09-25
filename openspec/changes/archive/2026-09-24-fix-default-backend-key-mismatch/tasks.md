## 1. Key mismatch fix

- [x] 1.1 Persist the default backend under `defaults.backend` instead of `default_backend`.
- [x] 1.2 Add a round-trip regression test asserting `database.Config().Defaults.Backend` reflects the new default after `handleSetDefault`.

## 2. Verification and archival

- [x] 2.1 Run the affected package tests.
- [x] 2.2 Archive the change with the PR update.
