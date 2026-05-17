import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

test.describe('hotkeys', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('n switches to the + New tab', async ({ page }) => {
    await page.locator('body').press('n');
    await expect(page.locator('.tab[data-tab="create"]')).toHaveClass(/active/);
    await expect(page.locator('#create-view')).toBeVisible();
  });

  test('t / g / , navigate to Tasks / DAG / Settings tabs', async ({ page }) => {
    await page.locator('body').press('g');
    await expect(page.locator('.tab[data-tab="dag"]')).toHaveClass(/active/);
    await page.locator('body').press(',');
    await expect(page.locator('.tab[data-tab="settings"]')).toHaveClass(/active/);
    await page.locator('body').press('t');
    await expect(page.locator('.tab[data-tab="tasks"]')).toHaveClass(/active/);
  });

  test('/ focuses the task search input', async ({ page }) => {
    await page.locator('body').press('/');
    await expect(page.locator('#task-search-input')).toBeFocused();
    // Slash must not leak into the input.
    await expect(page.locator('#task-search-input')).toHaveValue('');
  });

  test('a toggles the Active / Archived filter', async ({ page }) => {
    await expect(page.locator('.list-toolbar .seg[data-filter="active"]')).toHaveClass(/active/);
    await page.locator('body').press('a');
    await expect(page.locator('.list-toolbar .seg[data-filter="archived"]')).toHaveClass(/active/);
    await page.locator('body').press('a');
    await expect(page.locator('.list-toolbar .seg[data-filter="active"]')).toHaveClass(/active/);
  });

  test('? opens the hotkeys help modal and Esc closes it', async ({ page }) => {
    await page.locator('body').press('?');
    await expect(page.locator('#hotkeys-modal')).toHaveClass(/open/);
    await page.locator('body').press('Escape');
    await expect(page.locator('#hotkeys-modal')).not.toHaveClass(/open/);
  });

  test('hotkeys do not fire while typing in the search input', async ({ page }) => {
    const input = page.locator('#task-search-input');
    await input.focus();
    await input.type('nasty');
    await expect(input).toHaveValue('nasty');
    // Tab bar stays on Tasks — `n` did not jump to New.
    await expect(page.locator('.tab[data-tab="tasks"]')).toHaveClass(/active/);
  });

  test('hotkeys are suppressed when the detail view is open', async ({ page }) => {
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view')).toHaveClass(/open/);
    await page.locator('#detail-view').press('n');
    // Still in detail view; tab did not change underneath.
    await expect(page.locator('#detail-view')).toHaveClass(/open/);
    await expect(page.locator('.tab[data-tab="tasks"]')).toHaveClass(/active/);
  });
});
