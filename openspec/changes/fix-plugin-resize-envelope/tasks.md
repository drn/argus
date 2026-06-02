# Tasks

## 1. Tests first (red)

- [ ] 1.1 Test: viewport size pre-layout falls back to screen-derived chrome math, never 13x8
- [ ] 1.2 Smoke test: first resize envelope after activation matches the real pane inner rect (78x20 on an 80x24 sim screen)
- [ ] 1.3 Test: reconciler re-sends when computed size differs from last-sent; dedupes when unchanged
- [ ] 1.4 Test: failed send leaves last-sent unchanged (retried next draw)
- [ ] 1.5 Test: re-activation re-sends the current size

## 2. Implementation (green)

- [ ] 2.1 Track last-sent cols/rows + conn-ready + laid-out state on `pluginViewMount`
- [ ] 2.2 Move envelope sending out of the dial goroutine into an afterDraw reconciler (single send path, tview goroutine only)
- [ ] 2.3 Harden `pluginViewportSize`: trust `pane.GetRect()` only post-layout; screen-minus-chrome fallback
- [ ] 2.4 `afterDraw` runs the reconciler on every draw (not just terminal resize)

## 3. Wrap-up

- [ ] 3.1 `openspec validate --all --strict` clean
- [ ] 3.2 `make pre-pr` clean
- [ ] 3.3 Gotcha documented in `context/knowledge/gotchas/keybindings.md` (plugin-view section)
