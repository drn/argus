import { test, expect, type Page } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page: Page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
  await page.locator('.task-item').first().click();
  await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });
}

// Key bar: virtual keys (Esc, Tab, ⇧Tab, arrows) the iOS soft keyboard
// doesn't expose. Touch-gated like the compose bar — same IS_TOUCH check —
// so the toggle button is iphone-only.
test.describe('key bar', () => {
  test('toggle button visible on iphone, hidden on desktop', async ({ page }, testInfo) => {
    await login(page);
    if (testInfo.project.name === 'iphone') {
      await expect(page.locator('#compose-keybar-toggle')).toBeVisible();
    } else {
      await expect(page.locator('#compose-bar')).toBeHidden();
      await expect(page.locator('#compose-keybar-toggle')).toBeHidden();
    }
  });

  test('hidden by default; toggle shows it and persists preference', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);

    const bar = page.locator('#key-bar');
    const toggle = page.locator('#compose-keybar-toggle');

    // Default off — class .show drives display.
    await expect(bar).not.toHaveClass(/show/);
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');

    await toggle.click();
    await expect(bar).toHaveClass(/show/);
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    expect(await page.evaluate(() => localStorage.getItem('argus-keybar-visible'))).toBe('1');

    await toggle.click();
    await expect(bar).not.toHaveClass(/show/);
    expect(await page.evaluate(() => localStorage.getItem('argus-keybar-visible'))).toBe('0');
  });

  test('preference restored on reload', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').click();
    await expect(page.locator('#key-bar')).toHaveClass(/show/);

    await page.reload();
    await expect(page.locator('#main-app')).toBeVisible();
    await page.locator('.task-item').first().click();
    await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });

    await expect(page.locator('#key-bar')).toHaveClass(/show/);
    await expect(page.locator('#compose-keybar-toggle')).toHaveAttribute('aria-pressed', 'true');
  });

  test.describe('key bytes', () => {
    const cases: Array<{ key: string; bytes: string }> = [
      { key: 'esc',       bytes: '\x1b'    },
      { key: 'tab',       bytes: '\t'      },
      { key: 'shift-tab', bytes: '\x1b[Z'  },
      { key: 'up',        bytes: '\x1b[A'  },
      { key: 'down',      bytes: '\x1b[B'  },
      { key: 'left',      bytes: '\x1b[D'  },
      { key: 'right',     bytes: '\x1b[C'  },
    ];
    for (const c of cases) {
      test(`${c.key} POSTs the correct escape sequence`, async ({ page }, testInfo) => {
        test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
        await login(page);
        await page.locator('#compose-keybar-toggle').click();

        const inputReq = page.waitForRequest(req =>
          req.url().includes('/input') && req.method() === 'POST',
          { timeout: 3000 }
        );
        await page.locator(`#key-bar button[data-keybar="${c.key}"]`).click();
        const req = await inputReq;
        expect(req.postData()).toBe(c.bytes);
      });
    }
  });

  test('hidden when compose bar is torn down', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').click();
    await expect(page.locator('#key-bar')).toHaveClass(/show/);

    // closeDetail destroys term + compose; key bar must follow.
    await page.evaluate(() => (window as any).closeDetail());
    await expect(page.locator('#compose-bar')).toBeHidden();
    await expect(page.locator('#key-bar')).not.toHaveClass(/show/);
  });

  test('keeps textarea focus when tapping a key (no keyboard dismiss)', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').click();

    const ci = page.locator('#compose-input');
    await ci.click();
    await ci.fill('hello');
    expect(await page.evaluate(() => document.activeElement?.id)).toBe('compose-input');

    await page.locator('#key-bar button[data-keybar="up"]').click();

    // touchstart/mousedown preventDefault on the key bar buttons must keep
    // focus on the textarea — otherwise iOS dismisses the soft keyboard
    // between key taps.
    expect(await page.evaluate(() => document.activeElement?.id)).toBe('compose-input');
    await expect(ci).toHaveValue('hello');
  });

  // `.click()` synthesizes a mouse event and bypasses the touchstart handler.
  // `.tap()` dispatches a real touchstart → touchend pair (the synthetic click
  // is suppressed by our `preventDefault()` on touchend). Per the Touch Events
  // spec, calling `preventDefault()` on touchstart ALSO suppresses the
  // synthetic click, so any handler that relies on `click` after
  // `touchstart.preventDefault()` is dead on real iOS. These specs lock in the
  // touchend-driven path; if the action ever moves back to `click`, they fail.
  test('TAP on toggle reveals key bar (real touch sequence)', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    const bar = page.locator('#key-bar');
    await expect(bar).not.toHaveClass(/show/);
    await page.locator('#compose-keybar-toggle').tap();
    await expect(bar).toHaveClass(/show/);
  });

  test('TAP on a key sends bytes (real touch sequence)', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').tap();
    await expect(page.locator('#key-bar')).toHaveClass(/show/);

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#key-bar button[data-keybar="esc"]').tap();
    const req = await inputReq;
    expect(req.postData()).toBe('\x1b');
  });

  test('stopped task: key tap toasts and does not POST', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').click();

    // Force the cached currentTask off in_progress so the click guard fires.
    // The compose bar normally tears down in this state via destroyTerm, but
    // we want the key-bar click handler reachable to verify its own guard —
    // mutating currentTask in place is the cleanest way to isolate it.
    await page.evaluate(() => { (window as any).currentTask.status = 'complete'; });

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#key-bar button[data-keybar="esc"]').click();

    await expect(page.locator('.toast')).toBeVisible();
    await expect(page.locator('.toast')).toContainText('Agent not running');
    expect(posted).toBe(false);
  });

  // The guard lives inside the shared `fn` closure, so click and touchend
  // paths both go through it — but a `.tap()` variant locks that in. If the
  // guard ever moves to a path-specific spot, this spec fails.
  test('stopped task: key TAP also toasts and does not POST', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'key bar is touch-gated');
    await login(page);
    await page.locator('#compose-keybar-toggle').tap();
    await page.evaluate(() => { (window as any).currentTask.status = 'complete'; });

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#key-bar button[data-keybar="esc"]').tap();

    await expect(page.locator('.toast')).toBeVisible();
    await expect(page.locator('.toast')).toContainText('Agent not running');
    expect(posted).toBe(false);
  });
});
