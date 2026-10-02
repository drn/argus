## 1. Detection

- [x] 1.1 Capture the real ghost-suggestion bytes and reproduce the leak through `ScreenRenderer`.
- [x] 1.2 Move the OSC filter to `internal/oscfilter` and apply it in `ScreenRenderer.render`.

## 2. Wording

- [x] 2.1 Make `abandonedDraftAnnotation` conditional and keep `injectedNoticeDraft` matching it.

## 3. Verification

- [x] 3.1 Regression fixture, composer-shape table tests, and notify-level annotation tests.
- [x] 3.2 Gotcha entry, full pre-PR gate, archive with the implementation.
