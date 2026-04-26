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

    // Buffered markers should now be in the terminal buffer.
    const dump = await page.evaluate(() => {
      const buf = (window as any).term.buffer.active;
      let s = '';
      for (let y = 0; y < buf.length; y++) {
        const line = buf.getLine(y);
        if (line) s += line.translateToString(true) + '\n';
      }
      return s;
    });
    expect(dump).toContain('LIVE_MARKER_AAA');
    expect(dump).toContain('LIVE_MARKER_BBB');
  });

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
