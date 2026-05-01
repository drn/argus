import { test, expect, APIRequestContext } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

const TOKEN = 'test-token';
const headers = { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' };

async function getFirstTaskID(req: APIRequestContext) {
  const r = await req.get('/api/tasks', { headers });
  expect(r.status()).toBe(200);
  const j = await r.json();
  return j.tasks[0].id;
}

test.describe('agent-staged clipboard API', () => {
  test('GET on empty returns 204', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.get(`/api/tasks/${id}/clipboard`, { headers });
    expect(r.status()).toBe(204);
  });

  test('POST then GET round-trip', async ({ request }) => {
    const id = await getFirstTaskID(request);
    let r = await request.post(`/api/tasks/${id}/clipboard`, {
      headers, data: { text: 'hello from test' },
    });
    expect(r.status()).toBe(200);

    r = await request.get(`/api/tasks/${id}/clipboard`, { headers });
    expect(r.status()).toBe(200);
    const j = await r.json();
    expect(j.text).toBe('hello from test');
  });

  test('DELETE clears the payload', async ({ request }) => {
    const id = await getFirstTaskID(request);
    let r = await request.post(`/api/tasks/${id}/clipboard`, {
      headers, data: { text: 'x' },
    });
    expect(r.status()).toBe(200);

    r = await request.delete(`/api/tasks/${id}/clipboard`, { headers });
    expect(r.status()).toBe(200);

    r = await request.get(`/api/tasks/${id}/clipboard`, { headers });
    expect(r.status()).toBe(204);
  });

  test('rejects oversize payload', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const big = 'a'.repeat((1 << 20) + 1);
    const r = await request.post(`/api/tasks/${id}/clipboard`, {
      headers, data: { text: big },
    });
    expect(r.status()).toBe(400);
  });

  test('unknown task ID returns 404', async ({ request }) => {
    const r = await request.get('/api/tasks/does-not-exist/clipboard', { headers });
    expect(r.status()).toBe(404);
  });

  test('per-task isolation', async ({ request }) => {
    const id1 = await getFirstTaskID(request);
    // Create a second task to isolate against.
    const created = await request.post('/api/tasks', {
      headers, data: { name: 'sibling-' + Date.now(), prompt: '#', project: 'echo' },
    });
    const id2 = (await created.json()).id;

    await request.post(`/api/tasks/${id1}/clipboard`, { headers, data: { text: 'one' } });
    await request.post(`/api/tasks/${id2}/clipboard`, { headers, data: { text: 'two' } });

    const r1 = await (await request.get(`/api/tasks/${id1}/clipboard`, { headers })).json();
    const r2 = await (await request.get(`/api/tasks/${id2}/clipboard`, { headers })).json();
    expect(r1.text).toBe('one');
    expect(r2.text).toBe('two');
  });
});

test.describe('PWA Copy button', () => {
  test('button appears on payload, hides on copy', async ({ page, request, context }) => {
    // Grant clipboard permission to the test browser.
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);

    await page.addInitScript((tok) => {
      try { localStorage.setItem('argus-token', tok); } catch (e) {}
    }, TOKEN);
    await page.goto('/');
    // Wait for the task list to populate.
    await page.locator('.task-item').first().waitFor();

    const id = await getFirstTaskID(request);

    // Open the task detail view by tapping the first task.
    await page.locator('.task-item').first().click();
    await page.locator('#detail-view.open').waitFor();

    // Initially no copy button.
    await expect(page.locator('#btn-copy')).not.toHaveClass(/visible/);

    // Stage text via API — SSE should push the event and reveal the button.
    const stageRes = await request.post(`/api/tasks/${id}/clipboard`, {
      headers, data: { text: 'staged-content' },
    });
    expect(stageRes.status()).toBe(200);

    await expect(page.locator('#btn-copy')).toHaveClass(/visible/);

    // Tap the button — should write to clipboard, then DELETE clears server.
    await page.locator('#btn-copy').click();

    // Button hides locally immediately.
    await expect(page.locator('#btn-copy')).not.toHaveClass(/visible/);

    // Clipboard contains the staged text. We read via the page's clipboard API.
    const clipText = await page.evaluate(async () => {
      return await navigator.clipboard.readText();
    });
    expect(clipText).toBe('staged-content');

    // Server-side state cleared (DELETE was fired).
    // Wait briefly for the async DELETE to land.
    await page.waitForTimeout(200);
    const r = await request.get(`/api/tasks/${id}/clipboard`, { headers });
    expect(r.status()).toBe(204);
  });

  test('button shows on initial load when payload pre-existed', async ({ page, request }) => {
    const id = await getFirstTaskID(request);
    // Stage BEFORE the page mounts the EventSource.
    await request.post(`/api/tasks/${id}/clipboard`, {
      headers, data: { text: 'pre-existing' },
    });

    await page.addInitScript((tok) => {
      try { localStorage.setItem('argus-token', tok); } catch (e) {}
    }, TOKEN);
    await page.goto('/');
    await page.locator('.task-item').first().waitFor();
    await page.locator('.task-item').first().click();
    await page.locator('#detail-view.open').waitFor();

    // The initial fetch in openDetail should reveal the button even though
    // the SSE event was emitted before the page subscribed.
    await expect(page.locator('#btn-copy')).toHaveClass(/visible/);
  });
});
