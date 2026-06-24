# Tasks

## Diagnostics
- [x] On non-zero exit, fold captured stdout into the wrapped error (where
      `claude -p` writes runtime errors), alongside the existing stderr fold
- [x] Test: fake `claude` writes a runtime error to stdout + exits 1 → error
      includes the stdout reason (not a bare `exit status 1`)

## Budget cap
- [x] Raise `--max-budget-usd` from `0.01` to `0.05`
- [x] Correct the package + inline cost comments to the measured figures
- [x] Test: assert the budget flag value passed to the CLI is `0.05`

## Retry on transient failure
- [x] Retry the CLI call once with a short backoff on a non-zero exit before
      returning the error (skip retry for unavailable / empty-prompt)
- [x] Test: fake `claude` fails once then succeeds → GenerateName returns the
      name; fake that always fails → error after the retry

## Timeout
- [x] Raise `DefaultTimeout` from 30s to 45s

## Gate
- [x] `make pre-pr` passes clean
- [x] Archive this change (merge delta into base spec, move to archive/) in
      this PR before merge
