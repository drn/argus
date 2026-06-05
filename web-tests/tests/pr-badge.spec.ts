import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

// The test harness seeds the echo-bash task with a cached PR review state of
// "awaiting-review" in task_meta namespace "pr" (the daemon poller's job in
// production). These specs assert the PWA renders the matching PR badge and
// that the badge axis is orthogonal to the status badge.
test.beforeEach(async () => { await resetServer(); });

test.describe('PR review badge', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('renders the awaiting-review PR badge for the seeded task', async ({ page }) => {
    const badge = page.locator('.task-item .badge-pr-awaiting');
    await expect(badge).toBeVisible();
    await expect(badge).toHaveText('PR');
    await expect(badge).toHaveAttribute('title', 'PR awaiting review');
  });

  test('PR badge coexists with the status badge (orthogonal axes)', async ({ page }) => {
    const item = page.locator('.task-item').first();
    // The status badge (Running) and the PR badge both render in the row.
    await expect(item.locator('.task-badge.badge-in_progress, .task-badge.badge-idle')).toHaveCount(1);
    await expect(item.locator('.task-badge.badge-pr-awaiting')).toHaveCount(1);
  });

  test('the API exposes pr_state without shelling out to gh', async ({ request }) => {
    const r = await request.get('/api/tasks', {
      headers: { Authorization: 'Bearer test-token' },
    });
    expect(r.ok()).toBeTruthy();
    const body = await r.json();
    const task = body.tasks.find((t: any) => t.name === 'echo-bash');
    expect(task).toBeTruthy();
    expect(task.pr_state).toBe('awaiting-review');
  });
});
