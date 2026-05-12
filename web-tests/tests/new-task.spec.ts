import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

// New Task form — project dropdown recovery. The boot-time loadProjects()
// fail-quiets: `if (!r.ok) return` skips non-2xx, `catch(e) {}` swallows
// network errors. Without recovery the dropdown stays blank until the user
// reloads or saves Settings. applyCreateDefaults() re-fetches when
// `projectsLoaded` is still false — the flag prevents looping refetches
// on every tab switch for a user with zero projects configured.

test.beforeEach(async () => { await resetServer(); });

test.describe('New Task — project dropdown', () => {
  test('populates from /api/projects on initial load', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    // Wait for the boot-time loadProjects() to populate the dropdown.
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);
    const names = await page.evaluate(() =>
      [...(document.getElementById('create-project') as HTMLSelectElement).options].map(o => o.value),
    );
    expect(names).toContain('test-proj');
  });

  test('re-fetches on tab switch after a failed boot-time load', async ({ page }) => {
    // Fail the first /api/projects so the boot-time loadProjects() returns
    // empty and `projectsLoaded` stays false — exactly the production silent-
    // failure path. Subsequent calls succeed.
    let projectsCalls = 0;
    await page.route('**/api/projects', (route) => {
      projectsCalls++;
      if (projectsCalls === 1) {
        return route.fulfill({ status: 503, contentType: 'application/json', body: '{"error":"unavailable"}' });
      }
      return route.continue();
    });

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    // After connect the dropdown is empty because the boot fetch 503'd.
    await expect.poll(async () => projectsCalls).toBeGreaterThanOrEqual(1);
    const initialCount = await page.evaluate(() =>
      (document.getElementById('create-project') as HTMLSelectElement).options.length,
    );
    expect(initialCount).toBe(0);

    // Switching to the create tab must trigger a successful recovery fetch.
    await page.evaluate(() => (window as any).switchTab('create'));
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);
  });

  test('does not refetch when projectsLoaded is true', async ({ page }) => {
    // After a successful boot-time fetch, opening + New should NOT issue
    // another /api/projects request — even if the user happens to have zero
    // projects configured, the flag prevents the refetch loop.
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    // Wait for the boot-time fetch to complete.
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);

    // Now count further /api/projects requests during a tab switch.
    let calls = 0;
    await page.route('**/api/projects', (route) => { calls++; return route.continue(); });
    await page.evaluate(() => (window as any).switchTab('create'));
    // Give applyCreateDefaults a tick to (mis)fire.
    await page.waitForTimeout(150);
    expect(calls).toBe(0);
  });
});
