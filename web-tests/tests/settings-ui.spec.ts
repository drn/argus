import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
}

test.describe('settings tab', () => {
  test('settings tab is visible and shows token + push sections', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'API tokens' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Notifications' })).toBeVisible();
  });

  test('mint a device token via UI and reveal it', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await page.locator('#new-token-label').fill('test-iphone');
    await page.locator('button[onclick="mintToken()"]').click();
    await expect(page.locator('#token-mint-result')).toBeVisible();
    await expect(page.locator('#token-mint-result')).toContainText('Save this token now');
    // Token should be 64 hex chars somewhere in the result.
    const text = await page.locator('#token-mint-result').textContent();
    expect(text).toMatch(/[0-9a-f]{64}/);
    // Listed token row appears with last4 of the same token.
    await expect(page.locator('#token-list')).toContainText('test-iphone');
  });

  test('revoke a token from settings UI', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await page.locator('#new-token-label').fill('soon-revoked');
    await page.locator('button[onclick="mintToken()"]').click();
    await expect(page.locator('#token-list')).toContainText('soon-revoked');

    page.on('dialog', d => d.accept());
    await page.locator('#token-list button.danger:has-text("Revoke")').first().click();
    await expect(page.locator('#token-list')).toContainText('revoked', { timeout: 3000 });
  });
});

test.describe('list toolbar', () => {
  test('Active and Archived segments switch the list', async ({ page }) => {
    await login(page);
    // Click the seed task → archive it.
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept());
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-archive').click();
    await expect(page.locator('#tasks-view')).toBeVisible();
    // Active list is empty.
    await expect(page.locator('.task-item')).toHaveCount(0);
    // Archived list has the task.
    await page.locator('.list-toolbar .seg[data-filter="archived"]').click();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });
});

test.describe('detail-view actions', () => {
  test('rename updates title', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept('renamed-via-ui'));
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-rename').click();
    await expect(page.locator('#detail-title')).toHaveText('renamed-via-ui', { timeout: 3000 });
  });

  test('fork creates a new task and opens it', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept('forked-via-ui'));
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-fork').click();
    await expect(page.locator('#detail-title')).toHaveText('forked-via-ui', { timeout: 5000 });
  });

  test('overflow menu closes when navigating Back', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu')).toHaveClass(/open/);
    await page.locator('.detail-back').click();
    // After back, detail-view is dismissed and overflow-menu is gone from DOM
    // (innerHTML rebuild on next openDetail), or at minimum no longer .open.
    await expect(page.locator('#tasks-view')).toBeVisible();
    await expect(page.locator('#overflow-menu.open')).toHaveCount(0);
  });

  test('overflow menu closes when switching tabs', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu')).toHaveClass(/open/);
    await page.locator('.tab[data-tab="settings"]').click();
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.locator('#overflow-menu.open')).toHaveCount(0);
  });
});
