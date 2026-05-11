import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

// New Task form — project dropdown recovery. The boot-time loadProjects()
// silently swallows failures (`if (!r.ok) return` + `catch(e) {}`), so a
// transient hiccup during initial connect leaves the dropdown permanently
// blank. applyCreateDefaults() re-fetches when the cached dropdown is empty
// — mirrors the existing recovery path for the backend select.

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

  test('re-fetches when applyCreateDefaults sees an empty dropdown', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    // Wait for the initial population so we know the network is working.
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);

    // Simulate the silent-failure aftermath: empty options. (This mirrors what
    // the user actually saw when their boot-time fetch failed transiently.)
    await page.evaluate(() => {
      const sel = document.getElementById('create-project') as HTMLSelectElement;
      sel.replaceChildren();
    });

    // Switching to the create tab must trigger a re-fetch.
    await page.evaluate(() => (window as any).switchTab('create'));
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);
  });
});
