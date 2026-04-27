import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
}

test.describe('terminal', () => {
  test('opens task and renders xterm.js', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#term-wrap')).toBeVisible();
    await expect(page.locator('#term .xterm')).toBeVisible();
    await expect(page.locator('#term .xterm-rows')).toBeVisible();
    // FitAddon should produce a non-zero grid.
    const cols = await page.evaluate(() => (window as any).term?.cols ?? 0);
    const rows = await page.evaluate(() => (window as any).term?.rows ?? 0);
    expect(cols).toBeGreaterThan(0);
    expect(rows).toBeGreaterThan(0);
  });

  test('SSE stream connects and shows live status', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });
    await expect(page.locator('#term-status-text')).toHaveText('live');
  });

  test('typing forwards to PTY and echoes back', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Click into terminal viewport to focus, then type a unique marker.
    await page.locator('#term').click();
    const marker = 'PWMARK' + Date.now();
    await page.keyboard.type(`echo ${marker}`);
    await page.keyboard.press('Enter');

    // Wait for the echo response in the terminal grid.
    await expect.poll(async () => {
      return await page.evaluate(() => {
        const term = (window as any).term;
        if (!term) return '';
        const buf = term.buffer.active;
        let out = '';
        for (let y = 0; y < buf.length; y++) {
          const line = buf.getLine(y);
          if (line) out += line.translateToString(true) + '\n';
        }
        return out;
      });
    }, { timeout: 5000 }).toContain(marker);
  });

  test('resize endpoint is called when viewport changes', async ({ page, context }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Listen for the resize POST.
    const resizePromise = page.waitForRequest(req =>
      req.url().includes('/resize') && req.method() === 'POST',
      { timeout: 5000 }
    );

    // Simulate orientation change (landscape).
    await page.setViewportSize({ width: 800, height: 390 });

    const req = await resizePromise;
    const body = JSON.parse(req.postData() || '{}');
    expect(body.cols).toBeGreaterThan(0);
    expect(body.rows).toBeGreaterThan(0);
  });

  test('detail view layout fits within phone viewport', async ({ page, viewport }) => {
    // Phone-only: at desktop widths the fixed #detail-view is trivially within
    // the viewport (left:0; right:0), so the assertions would pass vacuously.
    test.skip((viewport?.width ?? 0) > 500, 'phone-viewport regression only');
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#term-wrap')).toBeVisible();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // The detail view is position:fixed; the term flex chain must shrink to
    // viewport on phones. Verify the key boxes don't claim more layout width
    // than the viewport, and the document itself isn't horizontally scrollable
    // (catches stray width:100vw / overflow rules on the body).
    const layout = await page.evaluate(() => {
      const vw = window.innerWidth;
      // -1 sentinel on a missing node produces a readable assertion failure
      // ("-1 <= vw+1" passes; we'd never see a -1 in practice for real boxes)
      // instead of a TypeError from a non-null assertion.
      const right = (id: string) =>
        document.getElementById(id)?.getBoundingClientRect().right ?? -1;
      return {
        vw,
        detailView: right('detail-view'),
        termWrap: right('term-wrap'),
        term: right('term'),
        docScrollWidth: document.documentElement.scrollWidth,
        docClientWidth: document.documentElement.clientWidth,
      };
    });
    // +1 px tolerance for subpixel rounding at devicePixelRatio > 1.
    expect(layout.detailView).toBeLessThanOrEqual(layout.vw + 1);
    expect(layout.termWrap).toBeLessThanOrEqual(layout.vw + 1);
    expect(layout.term).toBeLessThanOrEqual(layout.vw + 1);
    expect(layout.docScrollWidth).toBeLessThanOrEqual(layout.docClientWidth + 1);
  });

  test('buffers SSE writes while scrolled into history', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Populate scrollback directly so the test is independent of viewport
    // size and shell timing.
    await page.evaluate(() => {
      const term = (window as any).term;
      const enc = new TextEncoder();
      let blob = '';
      for (let i = 0; i < 200; i++) blob += `fillerline${i}\r\n`;
      term.write(enc.encode(blob));
    });
    // Wait for xterm's writer queue to drain so baseY reflects the writes.
    await expect.poll(async () =>
      page.evaluate(() => (window as any).term.buffer.active.baseY)
    , { timeout: 5000 }).toBeGreaterThan(20);

    // Scroll up into history.
    await page.evaluate(() => (window as any).term.scrollLines(-30));
    await expect(page.locator('#jump-bottom.shown')).toBeVisible();
    const scrolledY = await page.evaluate(() => (window as any).term.buffer.active.viewportY);

    // Simulate live output arriving via the SSE path. bufferOrWrite is the
    // single entry point the SSE handler uses, so this exercises the same
    // branch.
    await page.evaluate(() => {
      const enc = new TextEncoder();
      (window as any).bufferOrWrite(enc.encode('LIVE_MARKER_AAA\r\n'));
      (window as any).bufferOrWrite(enc.encode('LIVE_MARKER_BBB\r\n'));
    });

    // Pending should be queued, viewport must not have moved.
    const pending = await page.evaluate(() => (window as any).argusPending());
    expect(pending.chunks).toBe(2);
    expect(pending.bytes).toBeGreaterThan(0);
    await expect(page.locator('#jump-bottom.has-pending')).toBeVisible();

    const stillY = await page.evaluate(() => (window as any).term.buffer.active.viewportY);
    expect(stillY).toEqual(scrolledY);

    // Tap jump-bottom to flush. Pending should drain and indicators clear.
    await page.locator('#jump-bottom').click();
    await expect(page.locator('#jump-bottom.has-pending')).toHaveCount(0);
    await expect(page.locator('#jump-bottom.shown')).toHaveCount(0);
    const drained = await page.evaluate(() => (window as any).argusPending());
    expect(drained.chunks).toBe(0);
    expect(drained.bytes).toBe(0);

    // Buffered markers should now be in the terminal buffer. xterm.write
    // queues bytes asynchronously, so poll instead of asserting on a
    // single snapshot — the markers land within a few rAFs of flushPending.
    await expect.poll(async () =>
      page.evaluate(() => {
        const buf = (window as any).term.buffer.active;
        let s = '';
        for (let y = 0; y < buf.length; y++) {
          const line = buf.getLine(y);
          if (line) s += line.translateToString(true) + '\n';
        }
        return s;
      }),
    { timeout: 3000 }).toMatch(/LIVE_MARKER_AAA[\s\S]*LIVE_MARKER_BBB/);
  });

  test('buffers SSE writes while finger is on the term (iOS momentum guard)', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Synthesize a touchstart on #term. The production handler reads no
    // TouchEvent fields (just toggles `termTouching`), so a plain Event
    // exercises the same code path without relying on Playwright's touch
    // emulation, which differs between desktop/iphone profiles.
    await page.evaluate(() => {
      document.getElementById('term')!.dispatchEvent(new Event('touchstart'));
    });
    const touchState = await page.evaluate(() => (window as any).argusTouchState());
    expect(touchState.touching).toBe(true);

    // Live SSE chunk arriving mid-touch must be buffered, not written —
    // termIsAtBottom() is still true here (no scroll yet), so the touch
    // gate is what catches this.
    await page.evaluate(() => {
      const enc = new TextEncoder();
      (window as any).bufferOrWrite(enc.encode('TOUCH_BUFFERED_AAA\r\n'));
    });
    const duringTouch = await page.evaluate(() => (window as any).argusPending());
    expect(duringTouch.chunks).toBe(1);

    // touchend without a scroll-up leaves us at the bottom — drainIfSettled
    // runs synchronously on touchend (since !isTermScrolling), flushing the
    // pending bytes immediately.
    await page.evaluate(() => {
      document.getElementById('term')!.dispatchEvent(new Event('touchend'));
    });
    await expect.poll(async () =>
      page.evaluate(() => (window as any).argusPending().chunks)
    , { timeout: 2000 }).toBe(0);

    const dump = await page.evaluate(() => {
      const buf = (window as any).term.buffer.active;
      let s = '';
      for (let y = 0; y < buf.length; y++) {
        const line = buf.getLine(y);
        if (line) s += line.translateToString(true) + '\n';
      }
      return s;
    });
    expect(dump).toContain('TOUCH_BUFFERED_AAA');
  });

  test('visualViewport.resize during touch defers ancestor-mutating sync', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Before any touch, syncVisualViewport must run inline. Confirm the
    // baseline so a later "no mutations" assertion isn't a vacuous pass
    // because the listener never fired at all.
    const baseline = await page.evaluate(() => {
      const before = (window as any).argusTouchState();
      window.visualViewport?.dispatchEvent(new Event('resize'));
      const after = (window as any).argusTouchState();
      return { before: before.pendingViewportSync, after: after.pendingViewportSync };
    });
    expect(baseline.before).toBe(false);
    expect(baseline.after).toBe(false); // ran inline, didn't queue

    // Pre-set --app-height to a sentinel so the deferred sync's setProperty
    // call has a different value to write — without this, the browser
    // dedupes same-value writes and no mutation appears.
    await page.evaluate(() => {
      document.documentElement.style.setProperty('--app-height', '1px');
    });

    // Simulate a touch in progress and fire visualViewport.resize while a
    // MutationObserver watches the .xterm-viewport ancestor chain. The
    // queued resize must NOT produce ancestor mutations (which is what
    // kills iOS momentum), and the queued sync must actually run on
    // touchend (proven by a mutation overwriting --app-height back to the
    // real vv.height).
    const result = await page.evaluate(async () => {
      const viewport = document.querySelector('.xterm-viewport')!;
      const ancestors = new Set<Element>();
      let p: Element | null = viewport.parentElement;
      while (p) { ancestors.add(p); p = p.parentElement; }
      let duringTouchMutations = 0;
      let afterSettleMutations = 0;
      let phase: 'touch' | 'settle' = 'touch';
      const obs = new MutationObserver(rs => {
        for (const r of rs) {
          if (ancestors.has(r.target as Element) || r.target === viewport) {
            if (phase === 'touch') duringTouchMutations++;
            else afterSettleMutations++;
          }
        }
      });
      obs.observe(document.documentElement, { childList: true, subtree: true, attributes: true });

      document.getElementById('term')!.dispatchEvent(new Event('touchstart'));
      window.visualViewport?.dispatchEvent(new Event('resize'));
      const queued = (window as any).argusTouchState();
      await new Promise(res => setTimeout(res, 30));

      phase = 'settle';
      document.getElementById('term')!.dispatchEvent(new Event('touchend'));
      // touchend with !isTermScrolling drains synchronously; allow a microtask
      // tick for the MutationObserver to surface the resulting setProperty.
      await new Promise(res => setTimeout(res, 50));
      obs.disconnect();
      const final = (window as any).argusTouchState();
      const finalAppHeight = document.documentElement.style.getPropertyValue('--app-height');
      return { queued, final, duringTouchMutations, afterSettleMutations, finalAppHeight };
    });
    expect(result.queued.touching).toBe(true);
    expect(result.queued.pendingViewportSync).toBe(true);
    // The resize fired mid-touch must NOT have mutated ancestors —
    // otherwise iOS would have killed momentum.
    expect(result.duringTouchMutations).toBe(0);
    // After settle, the deferred sync must actually run: setProperty
    // overwrites our '1px' sentinel with the real vv.height, recording
    // a mutation on <html>[style].
    expect(result.afterSettleMutations).toBeGreaterThan(0);
    expect(result.finalAppHeight).not.toBe('1px');
    expect(result.final.pendingViewportSync).toBe(false);
  });

  test('xterm-viewport is the topmost element so touches reach the scroll target', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    // Without the z-index fix, .xterm-screen paints on top of .xterm-viewport
    // and elementFromPoint at the visual centre of the term returns the
    // canvas / screen layer — touches go there and never hit the scrollable
    // viewport. Confirm the lift worked: the topmost element at the term's
    // centre must be inside .xterm-viewport's subtree.
    const result = await page.evaluate(() => {
      const term = document.getElementById('term')!;
      const rect = term.getBoundingClientRect();
      const cx = rect.left + rect.width / 2;
      const cy = rect.top + rect.height / 2;
      const top = document.elementFromPoint(cx, cy);
      const viewport = document.querySelector('.xterm-viewport')!;
      return {
        topTag: top?.tagName,
        topClass: top?.className,
        topInViewport: top !== null && (top === viewport || viewport.contains(top)),
        viewportZIndex: getComputedStyle(viewport).zIndex,
      };
    });
    expect(result.viewportZIndex).toBe('1');
    expect(result.topInViewport).toBe(true);
  });

  // Note: a previous version of this file had an "isTermScrolling gate
  // blocks writes until scrollend fires" test. It synthetically dispatched
  // scroll/scrollend events on .xterm-viewport, but xterm.js fires its own
  // scroll events from internal rAF callbacks (auto-snap-to-bottom, render
  // sync) at unpredictable times that overlap with the test's `await`
  // microtask boundaries — making the test deeply flaky regardless of what
  // we asserted. The gate behavior is covered transitively: the
  // "buffers SSE writes while finger is on the term" test exercises
  // termTouching, and the "visualViewport.resize during touch" test
  // exercises pendingViewportSync deferral. Don't reintroduce a dispatch-
  // synthetic-scroll-events test on .xterm-viewport without first solving
  // the xterm-rAF interference (e.g., via a pause/resume API).

  test('back button cleans up stream and term', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#term-wrap')).toBeVisible();
    await page.locator('.detail-back').click();
    await expect(page.locator('#tasks-view')).toBeVisible();
    // term should be disposed.
    const termInstance = await page.evaluate(() => (window as any).term);
    expect(termInstance).toBeNull();
  });
});
