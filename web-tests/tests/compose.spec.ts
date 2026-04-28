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

// IS_TOUCH is computed at script load from `'ontouchstart' in window` and
// `(pointer: coarse)`. On the iphone device profile both are true, on desktop
// neither — so the same #compose-bar element flips visible/hidden by project.
test.describe('compose bar', () => {
  test('visible on iphone, hidden on desktop while task is running', async ({ page }, testInfo) => {
    await login(page);
    const isTouch = testInfo.project.name === 'iphone';
    if (isTouch) {
      await expect(page.locator('#compose-bar')).toBeVisible();
      await expect(page.locator('#compose-input')).toBeVisible();
      await expect(page.locator('#compose-send')).toBeVisible();
    } else {
      await expect(page.locator('#compose-bar')).toBeHidden();
    }
  });

  test('Send button forwards value + newline to /input and clears textarea', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-send').click();
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\n');

    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  test('Enter sends, Shift+Enter inserts a newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    const ci = page.locator('#compose-input');
    await ci.click();
    await page.keyboard.type('one');
    await page.keyboard.down('Shift');
    await page.keyboard.press('Enter');
    await page.keyboard.up('Shift');
    await page.keyboard.type('two');

    // Shift+Enter must not have sent.
    await expect(ci).toHaveValue('one\ntwo');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.keyboard.press('Enter');
    const req = await inputReq;
    expect(req.postData()).toBe('one\ntwo\n');

    await expect(ci).toHaveValue('');
  });

  test('oversize input toasts and does not POST', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // 64KB + 1 byte — above COMPOSE_MAX_BYTES.
    const tooLong = 'a'.repeat(64 * 1024 + 1);
    await page.locator('#compose-input').fill(tooLong);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-send').click();

    await expect(page.locator('.toast')).toBeVisible();
    await expect(page.locator('.toast')).toContainText('Input too long');
    expect(posted).toBe(false);
  });

  test('compose bar hidden after closing the detail view', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await expect(page.locator('#compose-bar')).toBeVisible();
    await page.locator('#compose-input').fill('partial');
    // Invoke closeDetail() directly — the .detail-back link is hidden in
    // compact mode (which is on by default on the iphone viewport because
    // window.innerHeight - visualViewport.height starts > 100).
    await page.evaluate(() => (window as any).closeDetail());
    await expect(page.locator('#detail-view')).not.toHaveClass(/open/);
    // destroyTerm() centralizes the hide so switchTab / showOffline get it
    // for free; closeDetail also clears the textarea so the next opened task
    // sees a fresh field.
    await expect(page.locator('#compose-bar')).toBeHidden();
    await expect(page.locator('#compose-input')).toHaveValue('');
  });
});
