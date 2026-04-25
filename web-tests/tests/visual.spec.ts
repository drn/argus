import { test } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

test('capture terminal screenshot for review', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await page.locator('.task-item').first().click();
  await page.waitForSelector('.term-status.live', { timeout: 5000 });
  // Type something so the terminal has visible content.
  await page.locator('#term').click();
  await page.keyboard.type('echo hello from playwright');
  await page.keyboard.press('Enter');
  await page.waitForTimeout(500);
  await page.screenshot({ path: 'screenshots/terminal-iphone.png', fullPage: true });
});

test('capture task list screenshot', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await page.waitForSelector('.task-item', { timeout: 5000 });
  await page.screenshot({ path: 'screenshots/tasks-iphone.png', fullPage: true });
});

test('capture settings tab screenshot', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await page.waitForSelector('.tab[data-tab="settings"]');
  await page.locator('.tab[data-tab="settings"]').click();
  await page.locator('#new-token-label').fill('My iPhone');
  await page.locator('button[onclick="mintToken()"]').click();
  await page.waitForSelector('#token-mint-result');
  await page.screenshot({ path: 'screenshots/settings-iphone.png', fullPage: true });
});
