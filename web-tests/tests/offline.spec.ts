import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

test.describe('offline view', () => {
  test('hides main app and shows Tailscale reminder when triggered', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    // showOffline() is the same entry point both the offline browser event and
    // the consecutive-failure path call into. Driving it directly lets us
    // verify the UI without coupling the test to flaky network mocking.
    await page.evaluate(() => (window as any).showOffline());

    await expect(page.locator('#offline-screen')).toBeVisible();
    await expect(page.locator('#offline-screen')).toContainText('Cannot reach Argus');
    await expect(page.locator('#offline-screen')).toContainText('Tailscale');
    await expect(page.locator('.banner-line')).toHaveCount(5);
    await expect(page.locator('#main-app')).toBeHidden();

    // Retry button calls refresh() which succeeds against the real test server,
    // so the offline screen clears and the main app reappears.
    await page.locator('#retry-btn').click();
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('#offline-screen')).toBeHidden();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('does not show on 401 — auth-error path is preserved', async ({ page }) => {
    await page.addInitScript(() => localStorage.clear());
    await page.goto('/');
    await page.locator('#token-input').fill('wrong-token');
    await page.locator('#auth-screen button').click();
    await expect(page.locator('#auth-error')).toBeVisible();
    await expect(page.locator('#offline-screen')).toBeHidden();
  });

  test('renders the gradient banner with TUI palette', async ({ page }) => {
    // Sanity-check the banner survives the markup pass: 5 lines, monospace,
    // each colored with the matching TUI ANSI 256 gradient stop (87 → 212).
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await page.evaluate(() => (window as any).showOffline());
    await expect(page.locator('#offline-screen')).toBeVisible();

    const lines = page.locator('.banner-line');
    await expect(lines).toHaveCount(5);
    const firstColor = await lines.first().evaluate((el) => getComputedStyle(el).color);
    const lastColor = await lines.last().evaluate((el) => getComputedStyle(el).color);
    expect(firstColor).toBe('rgb(95, 255, 255)');
    expect(lastColor).toBe('rgb(255, 135, 215)');
  });

  test('browser offline event flips to the offline view when token is set', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    await page.evaluate(() => window.dispatchEvent(new Event('offline')));
    await expect(page.locator('#offline-screen')).toBeVisible();
  });

  test('two consecutive refresh failures cross the threshold', async ({ page }) => {
    // Simulate the steady-state path by calling onConnectFailure() directly.
    // The threshold is OFFLINE_FAIL_THRESHOLD=2, so two calls flip the screen.
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    await page.evaluate(() => {
      (window as any).onConnectFailure();
      (window as any).onConnectFailure();
    });
    await expect(page.locator('#offline-screen')).toBeVisible();
  });

  test('retry with no token bounces back to the auth screen', async ({ page }) => {
    // Edge case: the offline screen is reachable without a saved token (e.g.,
    // user hit it via showOffline() before authenticating). Retry should send
    // them to the auth screen, not loop on /api/status with an empty Bearer.
    await page.addInitScript(() => localStorage.clear());
    await page.goto('/');
    await expect(page.locator('#auth-screen')).toBeVisible();
    await page.evaluate(() => (window as any).showOffline());
    await expect(page.locator('#offline-screen')).toBeVisible();
    await page.locator('#retry-btn').click();
    await expect(page.locator('#auth-screen')).toBeVisible();
    await expect(page.locator('#offline-screen')).toBeHidden();
  });

  test('logout while connected stops the poll loop', async ({ page }) => {
    // Regression for review W1: setInterval used to keep firing after logout,
    // which 401s with the empty token and tripped the offline screen on top
    // of the auth screen. Verify the timer is cleared on logout.
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    await page.evaluate(() => (window as any).logout());
    await expect(page.locator('#auth-screen')).toBeVisible();

    const timerCleared = await page.evaluate(() => (window as any).__argusRefreshTimer() === null);
    expect(timerCleared).toBe(true);
  });
});
