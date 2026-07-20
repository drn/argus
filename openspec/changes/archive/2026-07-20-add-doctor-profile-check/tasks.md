## 1. internal/doctor: profile-library status

- [x] 1.1 Add `internal/doctor/profilelib.go` with a `ProfileLibraryStatus` enum (`ProfileLibraryFound` / `ProfileLibraryNone` / `ProfileLibraryUnknown`), mirroring `StopHookStatus` in `stophook.go`
- [x] 1.2 Add `DiagnoseProfileLibrary(names []string, validNames []string, listErr error) ProfileLibraryStatus` (or equivalent signature) that degrades to Unknown on a listing error, else Found when at least one name validates, else None — shipped as `DiagnoseProfileLibrary(validNames []string, dirMissing bool, listErr error)`
- [x] 1.3 Add `RenderProfileLibrary(status ProfileLibraryStatus) string` printed after `RenderStopHook`'s output, following its formatting (status line + remediation snippet on None, note on Unknown)
- [x] 1.4 Unit tests in `internal/doctor` covering all three states plus the empty-vs-all-invalid None-found sub-cases

## 2. cmd/argus: wire the gather step

- [x] 2.1 Add `gatherProfileLibraryStatus()` in `cmd/argus/doctor.go` using `internal/profiles.Loader{LibraryDir: filepath.Join(db.DataDir(), "profiles")}.Discover()` + `ValidateName` per discovered name to classify Found/None, surfacing a listing error (e.g. permission denied) as Unknown — shipped via a direct `os.ReadDir` + `diagnoseProfileLibraryAt(dir, cfg)` helper instead of `Discover()` (which swallows listing errors, losing the None-vs-Unknown distinction)
- [x] 2.2 Call it from `runDoctor()` and print via `doctor.RenderProfileLibrary`, after the existing `doctor.RenderStopHook` call
- [x] 2.3 Confirm `runDoctor()`'s exit-code logic is untouched — still gated solely on `doctor.Diagnose(actors).Verdict`
- [x] 2.4 Tests for the gather step (temp `~/.argus/profiles/`-equivalent dir via `t.Setenv("HOME", ...)`, covering found/none/unknown), following the existing `gatherStopHookStatus` test shape — shipped as `diagnoseProfileLibraryAt(dir, cfg)` unit tests against a plain `t.TempDir()`, no `$HOME` override needed since the dir is an explicit param

## 3. Docs

- [x] 3.1 Add one sentence to README's "Diagnosing binary skew (`argus doctor`)" section documenting the new section, mirroring the existing Stop-hook sentence
- [x] 3.2 Add a short gotcha bullet noting doctor's profile check is library-existence-only, not per-project binding — landed in `gotchas/daemon-rpc.md` next to the existing Stop-hook gotcha (not `misc.md`) to match precedent, with a one-line cross-ref left in `misc.md`'s diligence-profiles section

## 4. Verify and archive

- [x] 4.1 `make pre-pr` clean (vuln gate advisory-only per documented toolchain-CVE exception; remaining gates green including test-cover-gate at 88.6%)
- [x] 4.2 Manual check: `argus doctor` against an empty `~/.argus/profiles/`, a populated one, and an unreadable one (or reasoned equivalent) to confirm all three states render correctly
- [x] 4.3 Archive `add-doctor-profile-check`: merge the `binary-coherence` delta into `openspec/specs/binary-coherence/spec.md` and move the change folder to `openspec/changes/archive/<date>-add-doctor-profile-check/`, in the same PR before merge
