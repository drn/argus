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

// Task creation runs git-worktree setup + hooks + agent start synchronously
// server-side before responding, which can outlast api()'s own client-side
// timeout on a slow connection — the server keeps going and the task gets
// created anyway. createTask() must treat that AbortError as "maybe
// succeeded" rather than reporting a hard failure (this was the root cause
// of the "Error: Fetch is aborted" bug users hit despite the task showing up
// in the list afterward).
test.describe('New Task — client-side abort handling', () => {
  test.beforeEach(async () => { await resetServer(); });

  test('api() aborts via AbortController once opts.timeoutMs elapses', async ({ page, browserName }) => {
    // On webkit, an unresolved page.route() handler (no continue/fulfill/
    // abort call) does not stay pending the way it does on chromium — the
    // request falls through to the real network almost immediately and
    // hits the server's "GET /" subtree catch-all (a real 200, not a
    // stall), so the client-side timer never gets a chance to fire. This
    // is a Playwright/webkit route-interception gap, not a claim about our
    // code — createTask()'s and onUploadFilesPicked()'s own AbortError
    // handling (below / in uploads.spec.ts) are stub-based and still
    // verified on webkit.
    test.skip(browserName === 'webkit', 'webkit does not hang an unresolved page.route() handler');

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();

    // A route that never resolves forces the client's own timer to fire —
    // this exercises the actual AbortController wiring, not a stub.
    await page.route('**/api/__never_responds__', () => {});

    const errName = await page.evaluate(async () => {
      try {
        // A generous-but-still-fast margin — webkit's route-interception IPC
        // has more overhead than chromium's, so a very tight timeoutMs (e.g.
        // 50ms) can race the route even being reached at all.
        await (window as any).api('/api/__never_responds__', { timeoutMs: 300 });
        return null;
      } catch (e: any) {
        return e.name;
      }
    });
    expect(errName).toBe('AbortError');
  });

  test('createTask() treats an aborted create as "maybe succeeded" instead of a hard error', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await expect.poll(async () =>
      page.evaluate(() => (document.getElementById('create-project') as HTMLSelectElement).options.length),
    ).toBeGreaterThan(0);

    await page.evaluate(() => (window as any).switchTab('create'));
    await page.locator('#create-project').selectOption('test-proj');
    await page.locator('#create-prompt').fill('investigate the flaky thing');

    // Stub api() so the /api/tasks POST rejects with a client-side
    // AbortError, simulating the fetch timing out while the server keeps
    // working — every other call (status/tasks polling inside refresh())
    // still goes through to the real implementation.
    await page.evaluate(() => {
      const real = (window as any).api;
      (window as any).api = (path: string, opts?: any) => {
        if (path === '/api/tasks' && opts?.method === 'POST') {
          return Promise.reject(new DOMException('The operation was aborted.', 'AbortError'));
        }
        return real(path, opts);
      };
    });

    await page.evaluate(() => (window as any).createTask());

    await expect(page.locator('.toast')).toContainText('may have started anyway');
    // The catch branch switches back to the task list instead of leaving
    // the user stuck on the create form with only a scary error.
    await expect(page.locator('#tasks-view')).toBeVisible();
  });
});
