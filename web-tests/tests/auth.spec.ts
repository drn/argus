import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

test.describe('auth', () => {
  test('shows token prompt when no token saved', async ({ page, context }) => {
    await context.clearCookies();
    await page.addInitScript(() => localStorage.clear());
    await page.goto('/');
    await expect(page.locator('#auth-screen')).toBeVisible();
    await expect(page.locator('#token-input')).toBeVisible();
  });

  test('rejects bad token', async ({ page }) => {
    await page.addInitScript(() => localStorage.clear());
    await page.goto('/');
    await page.locator('#token-input').fill('wrong-token');
    await page.locator('#auth-screen button').click();
    await expect(page.locator('#auth-error')).toBeVisible();
  });

  test('accepts valid token and shows task list', async ({ page }) => {
    await page.addInitScript(() => localStorage.clear());
    await page.goto('/');
    await page.locator('#token-input').fill('test-token');
    await page.locator('#auth-screen button').click();
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('.task-item')).toHaveCount(1);
    await expect(page.locator('.task-name')).toContainText('echo-bash');
  });

  test('persists token across reloads', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
  });
});
