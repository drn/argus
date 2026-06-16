import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

// The test-server seeds one "demo-orch" orchestrator with a coordinator role
// bound to the running echo-bash task (see cmd/argus-test-server/seedHera), so
// these specs exercise the populated-roster render + drill-in, not just the
// empty state covered in hotkeys.spec.ts.
test.describe('Hera tab', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('renders the seeded orchestrator and its coordinator role', async ({ page }) => {
    await page.locator('body').press('g');
    await expect(page.locator('.tab[data-tab="hera"]')).toHaveClass(/active/);
    await expect(page.locator('#hera-view')).toBeVisible();

    // Orchestrator card + coordinator role row with the bound task name.
    await expect(page.locator('.hera-card-name', { hasText: 'demo-orch' })).toBeVisible();
    const role = page.locator('.hera-role.live', { hasText: 'coordinator' });
    await expect(role).toBeVisible();
    await expect(role.locator('.hera-task')).toHaveText('echo-bash');
    // Status bar reflects the count rather than the empty placeholder.
    await expect(page.locator('.hera-empty')).toHaveCount(0);
    await expect(page.locator('#hera-status')).toContainText('orchestrator');
  });

  test('clicking a live role drills into that task detail', async ({ page }) => {
    await page.locator('body').press('g');
    await page.locator('.hera-role.live', { hasText: 'coordinator' }).click();
    // openTaskById opens the shared detail overlay on top of the Hera tab.
    await expect(page.locator('#detail-view')).toHaveClass(/open/);
  });
});
