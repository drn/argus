import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

// These specs cover the New Task form's slash-skill autocomplete:
//   - mid-prompt trigger (works after a space, not just at position 0)
//   - case-insensitive substring filter (matches Claude Code's picker)
//
// `cachedSkills` is in module scope (closure-only), so we seed it by
// mocking /api/skills via page.route — the SW returns early for /api/*,
// so route() is not bypassed (see web-remote.md gotchas) — and firing the
// project select's `change` event to drive loadSkillsForProject.

test.beforeEach(async () => { await resetServer(); });

test.describe('New Task — slash-skill autocomplete', () => {
  test('opens dropdown when "/" is typed mid-prompt (after a space)', async ({ page }) => {
    // Mock /api/skills so loadSkillsForProject populates the closure cache.
    await page.route('**/api/skills**', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          skills: [
            { name: 'commit', description: 'Create a commit' },
            { name: 'review', description: 'Review PR' },
            { name: 'cortex:review', description: 'Plugin review' },
          ],
        }),
      });
    });

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await page.evaluate(() => (window as any).switchTab('create'));

    // Trigger the project change so loadSkillsForProject fires and caches.
    await page.evaluate(() => {
      const sel = document.getElementById('create-project') as HTMLSelectElement;
      sel.dispatchEvent(new Event('change'));
    });
    // Wait for the cache to populate.
    await page.waitForTimeout(200);

    const prompt = page.locator('#create-prompt');
    const drop = page.locator('#ac-dropdown');

    // Type some prefix text, a space, then the trigger. Old behavior would
    // close the dropdown because the prompt didn't START with "/".
    await prompt.click();
    await prompt.fill('fix bug ');
    await prompt.press('/');
    await expect(drop).toHaveClass(/open/);
    // All three skills are visible (filter is empty after the trigger).
    await expect(drop.locator('.ac-item')).toHaveCount(3);
  });

  test('substring match — "/rev" matches "review" AND "cortex:review"', async ({ page }) => {
    await page.route('**/api/skills**', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          skills: [
            { name: 'commit', description: 'Create a commit' },
            { name: 'review', description: 'Review PR' },
            { name: 'cortex:review', description: 'Plugin review' },
            { name: 'test', description: 'Run tests' },
          ],
        }),
      });
    });

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await page.evaluate(() => (window as any).switchTab('create'));
    await page.evaluate(() => {
      const sel = document.getElementById('create-project') as HTMLSelectElement;
      sel.dispatchEvent(new Event('change'));
    });
    await page.waitForTimeout(200);

    const prompt = page.locator('#create-prompt');
    const drop = page.locator('#ac-dropdown');

    await prompt.click();
    await prompt.fill('/rev');
    // Force input event in case fill doesn't dispatch one in this profile.
    await prompt.dispatchEvent('input');
    await expect(drop).toHaveClass(/open/);
    // "review" (prefix) AND "cortex:review" (substring) — but NOT "commit"/"test".
    const items = drop.locator('.ac-item');
    await expect(items).toHaveCount(2);
    const names = await items.locator('span').first().allTextContents();
    expect(names).toContain('review');
  });

  test('selecting AC mid-prompt replaces only the trigger token', async ({ page }) => {
    await page.route('**/api/skills**', (route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          skills: [{ name: 'commit', description: 'Create a commit' }],
        }),
      });
    });

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await page.evaluate(() => (window as any).switchTab('create'));
    await page.evaluate(() => {
      const sel = document.getElementById('create-project') as HTMLSelectElement;
      sel.dispatchEvent(new Event('change'));
    });
    await page.waitForTimeout(200);

    const prompt = page.locator('#create-prompt');

    await prompt.click();
    await prompt.fill('fix bug ');
    await prompt.press('/');
    await prompt.press('c');
    await prompt.press('o');
    // Accept with Enter.
    await prompt.press('Enter');
    // Leading text preserved; only "/co" replaced with "/commit ".
    await expect(prompt).toHaveValue('fix bug /commit ');
  });
});
