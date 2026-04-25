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

test.describe('virtual key row', () => {
  test('vkey row is shown when task is running', async ({ page }) => {
    await login(page);
    await expect(page.locator('#vkey-row')).toBeVisible();
    await expect(page.locator('.vkey[data-key="esc"]')).toBeVisible();
    await expect(page.locator('.vkey[data-key="ctrl"]')).toBeVisible();
    await expect(page.locator('.vkey[data-key="up"]')).toBeVisible();
    await expect(page.locator('.vkey[data-key="enter"]')).toBeVisible();
  });

  test('Enter button sends \\r', async ({ page }) => {
    await login(page);

    const requests = [];
    page.on('request', req => {
      if (req.url().includes('/input')) requests.push({ url: req.url(), body: req.postData() });
    });
    await page.locator('.vkey[data-key="enter"]').click();
    await page.waitForTimeout(200);
    expect(requests.some(r => r.body === '\r')).toBe(true);
  });

  test('Up arrow sends ESC[A', async ({ page }) => {
    await login(page);
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('.vkey[data-key="up"]').click();
    const req = await inputReq;
    expect(req.postData()).toBe('\x1b[A');
  });

  test('Ctrl-C button sends \\x03', async ({ page }) => {
    await login(page);
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('.vkey[data-key="ctrl-c"]').click();
    const req = await inputReq;
    expect(req.postData()).toBe('\x03');
  });

  test('sticky Ctrl: tap → next typed key is Ctrl-modified', async ({ page }) => {
    await login(page);

    // Arm Ctrl by tapping it.
    await page.locator('.vkey[data-key="ctrl"]').click();
    await expect(page.locator('#vkey-ctrl')).toHaveClass(/sticky/);

    // Now type 'c' on terminal — should send \x03 (Ctrl+C).
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#term').click();
    await page.keyboard.press('c');
    const req = await inputReq;
    expect(req.postData()).toBe('\x03');

    // Ctrl should auto-clear after one keystroke.
    await expect(page.locator('#vkey-ctrl')).not.toHaveClass(/sticky/);
  });

  test('font size controls persist and update term', async ({ page }) => {
    await login(page);

    const initial = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);
    await page.locator('.vkey[data-key="font-up"]').click();
    await page.waitForTimeout(50);
    const bigger = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);
    expect(bigger).toBe(initial + 1);

    await page.locator('.vkey[data-key="font-down"]').click();
    await page.locator('.vkey[data-key="font-down"]').click();
    await page.waitForTimeout(50);
    const smaller = await page.evaluate(() => (window as any).term?.options?.fontSize ?? 0);
    expect(smaller).toBe(initial - 1);

    const persisted = await page.evaluate(() => localStorage.getItem('argus-font-size'));
    expect(Number(persisted)).toBe(smaller);
  });
});
