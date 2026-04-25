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
