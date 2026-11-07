## 1. Tests first

- [x] 1.1 Pane test: a forwarded click reports on press and release; unfocused, dead-session and no-session clicks report nothing; wheel reports nothing; an unwired pane does not panic.
- [x] 1.2 App test: a click through a pump-wired pane opens the echo window; wheel does not.

## 2. Implement

- [x] 2.1 `reportUserInput` helper; call it from `forwardClickPress`, `forwardClickRelease` and the paste path.

## 3. Ship

- [x] 3.1 Gotcha note, gate steps, both review lenses applied, archive, PR.
