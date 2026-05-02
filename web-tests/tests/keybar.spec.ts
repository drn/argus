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

    // mousedown.preventDefault on the key bar buttons must keep focus on the
    // textarea — otherwise iOS dismisses the soft keyboard between key taps.
    expect(await page.evaluate(() => document.activeElement?.id)).toBe('compose-input');
    await expect(ci).toHaveValue('hello');
  });
});
