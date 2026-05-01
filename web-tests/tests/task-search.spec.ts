import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

test.describe('task search', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('search input is rendered in the toolbar', async ({ page }) => {
    await expect(page.locator('#task-search-input')).toBeVisible();
    await expect(page.locator('#task-search-clear')).toBeHidden();
  });

  test('matching query keeps the task visible', async ({ page }) => {
    await page.locator('#task-search-input').fill('echo');
    await expect(page.locator('.task-item')).toHaveCount(1);
    await expect(page.locator('.task-name')).toContainText('echo-bash');
    await expect(page.locator('#task-search-clear')).toBeVisible();
  });

  test('matching by project name keeps the task visible', async ({ page }) => {
    await page.locator('#task-search-input').fill('test-proj');
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('non-matching query shows empty state', async ({ page }) => {
    await page.locator('#task-search-input').fill('zzznomatch');
    await expect(page.locator('.task-item')).toHaveCount(0);
    await expect(page.locator('.task-empty-search')).toContainText('zzznomatch');
  });

  test('clear button restores the full list', async ({ page }) => {
    const input = page.locator('#task-search-input');
    await input.fill('zzznomatch');
    await expect(page.locator('.task-item')).toHaveCount(0);
    await page.locator('#task-search-clear').click();
    await expect(input).toHaveValue('');
    await expect(page.locator('.task-item')).toHaveCount(1);
    await expect(page.locator('#task-search-clear')).toBeHidden();
  });

  test('switching Active ↔ Archived clears the search', async ({ page }) => {
    const input = page.locator('#task-search-input');
    await input.fill('zzznomatch');
    await expect(page.locator('.task-item')).toHaveCount(0);
    await page.locator('.list-toolbar .seg[data-filter="archived"]').click();
    await expect(input).toHaveValue('');
    await expect(page.locator('#task-search-clear')).toBeHidden();
    await page.locator('.list-toolbar .seg[data-filter="active"]').click();
    await expect(input).toHaveValue('');
    await expect(page.locator('.task-item')).toHaveCount(1);
  });
});
