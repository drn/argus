import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
  await page.locator('.task-item').first().click();
  await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });
}

test.describe('agent detail view chrome', () => {
  test('virtual key row is gone', async ({ page }) => {
    await login(page);
    await expect(page.locator('#vkey-row')).toHaveCount(0);
  });

  test('font size controls live in the overflow menu and persist', async ({ page }) => {
    await login(page);

    const initial = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);

    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu.open')).toBeVisible();

    await page.locator('.overflow-font-row button[data-key="font-up"]').click();
    await page.waitForTimeout(50);
    const bigger = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);
    expect(bigger).toBe(initial + 1);

    // Menu should stay open across consecutive font taps.
    await expect(page.locator('#overflow-menu.open')).toBeVisible();

    await page.locator('.overflow-font-row button[data-key="font-down"]').click();
    await page.locator('.overflow-font-row button[data-key="font-down"]').click();
    await page.waitForTimeout(50);
    const smaller = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);
    expect(smaller).toBe(initial - 1);

    const persisted = await page.evaluate(() => localStorage.getItem('argus-font-size'));
    expect(Number(persisted)).toBe(smaller);
  });

  test('header collapses to compact mode when keyboard reduces viewport', async ({ page }) => {
    await login(page);

    // Simulate the soft keyboard by shrinking visualViewport.height. The
    // syncVisualViewport handler keys off (innerHeight - vv.height) > 100.
    // This relies on the Playwright project viewport being tall enough that
    // window.innerHeight - 200 > 100 — the iPhone 14 Pro device profile and
    // the Desktop Chrome 1280x800 viewport both clear that easily. If a
    // future project sets a viewport with height ≤ 300, this test will
    // silently no-op rather than fail; raise vv.height in that case.
    await page.evaluate(() => {
      const vv: any = window.visualViewport;
      Object.defineProperty(vv, 'height', { configurable: true, get: () => 200 });
      vv.dispatchEvent(new Event('resize'));
    });

    await expect(page.locator('.detail-header.compact')).toBeVisible();
    await expect(page.locator('.detail-header.compact .detail-back')).toBeHidden();
    await expect(page.locator('.detail-header.compact .detail-subtitle')).toBeHidden();
  });

  // Esc lives in the key bar (see keybar.spec.ts) — the overflow menu Esc
  // entry was removed to declutter the dropdown.

  test('overflow menu has no Esc entry', async ({ page }) => {
    await login(page);
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu.open')).toBeVisible();
    await expect(page.locator('#btn-esc')).toHaveCount(0);
  });

  test('Toggle mode menu item sends Shift+Tab (CSI Z) and keeps menu open', async ({ page }) => {
    await login(page);

    await page.locator('#btn-overflow').click();
    await expect(page.locator('#btn-mode')).toBeVisible();

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#btn-mode').click();
    const req = await inputReq;
    expect(req.postData()).toBe('\x1b[Z');

    await expect(page.locator('#overflow-menu.open')).toBeVisible();
  });

  test('Stop button is gone from the menu', async ({ page }) => {
    await login(page);
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#btn-stop')).toHaveCount(0);
  });

  test('link picker opens, filters, and clicks invoke openExternalURL', async ({ page }) => {
    // Same SW-disable as the View PR test: the network-only /api/* SW handler
    // bypasses page.route() in WebKit unless register() is no-op'd here.
    await page.addInitScript(() => {
      const sw = navigator.serviceWorker;
      if (sw) {
        Object.defineProperty(sw, 'register', {
          configurable: true,
          value: () => Promise.reject(new Error('disabled in test')),
        });
      }
    });

    // Stub the links endpoint with a known set so the test isn't sensitive
    // to whatever the bash harness has emitted.
    await page.route('**/api/tasks/*/links*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          links: [
            { label: 'Example Docs', url: 'https://example.com/docs' },
            { label: 'https://github.com/foo/bar', url: 'https://github.com/foo/bar' },
          ],
        }),
      });
    });

    await login(page);

    // Capture window.open calls instead of letting the test browser navigate.
    await page.evaluate(() => {
      (window as any).__opens = [];
      window.open = (url: any) => {
        (window as any).__opens.push(String(url));
        return null;
      };
    });

    await page.locator('#btn-overflow').click();
    await page.locator('#btn-links').click();

    // Modal renders both rows.
    await expect(page.locator('#links-modal.open')).toBeVisible();
    const items = page.locator('#links-modal-body .links-item');
    await expect(items).toHaveCount(2);

    // Filter narrows the list.
    await page.locator('#links-modal-filter').fill('docs');
    await expect(items).toHaveCount(1);
    await expect(items.first()).toContainText('Example Docs');

    // Click → window.open invoked, modal closes.
    await items.first().click();
    await expect(page.locator('#links-modal.open')).toHaveCount(0);
    const opens = await page.evaluate(() => (window as any).__opens);
    expect(opens).toEqual(['https://example.com/docs']);
  });

  test('View PR appears without re-entering when pr_url becomes available', async ({ page }) => {
    // Disable the SPA's service worker before any script runs. The SW's
    // `/api/*` network-only handler re-issues fetches from the SW context,
    // which bypasses Playwright's `page.route()` in WebKit (the request
    // hits the network but the route never observes it). Patching
    // `serviceWorker.register` to no-op leaves the rest of the
    // `serviceWorker` API intact (push subscription code in the SPA reads
    // `ready`/`getRegistration`) while keeping every fetch on the
    // window-side path where `page.route()` works on every browser.
    await page.addInitScript(() => {
      const sw = navigator.serviceWorker;
      if (sw) {
        Object.defineProperty(sw, 'register', {
          configurable: true,
          value: () => Promise.reject(new Error('disabled in test')),
        });
      }
    });

    await login(page);

    // Initially no PR — button must not be in the menu.
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu.open')).toBeVisible();
    await expect(page.locator('#btn-pr')).toHaveCount(0);
    // Close so opening again picks up the rerendered DOM.
    await page.locator('#btn-overflow').click();

    // Stub the next /api/tasks response to add a pr_url, simulating the
    // agent opening a PR while the user is parked in the detail view.
    // Construct the body explicitly (status + body + content-type) — webkit
    // silently drops the overridden body in `response: r, json: body` form.
    await page.route('**/api/tasks*', async (route) => {
      const r = await route.fetch();
      const body = await r.json();
      for (const t of body.tasks || []) {
        t.pr_url = 'https://github.com/example/repo/pull/42';
      }
      await route.fulfill({
        status: r.status(),
        contentType: 'application/json',
        body: JSON.stringify(body),
      });
    });

    // Trigger a refresh immediately rather than waiting 5s for the poll.
    // refresh() returns a Promise; page.evaluate awaits it, so the
    // currentTask sync + renderOverflowMenu have completed before we check.
    await page.evaluate(() => (window as any).refresh());

    // Without the in-place currentTask sync, the button only appears after
    // closeDetail() + openDetail(). With it, the menu picks up pr_url on the
    // very next open.
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#btn-pr')).toBeVisible();
  });
});
