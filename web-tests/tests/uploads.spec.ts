import { test, expect, APIRequestContext } from '@playwright/test';
import { resetServer } from './_helpers';

const TOKEN = 'test-token';
const headers = { Authorization: `Bearer ${TOKEN}` };

test.beforeEach(async () => { await resetServer(); });

async function getFirstTaskID(req: APIRequestContext) {
  const r = await req.get('/api/tasks', { headers: { ...headers, 'Content-Type': 'application/json' } });
  expect(r.status()).toBe(200);
  const j = await r.json();
  return j.tasks[0].id;
}

test.describe('file uploads — REST API', () => {
  // Playwright's APIRequestContext under webkit (the iphone profile) does not
  // emit a valid multipart/form-data body — the server receives no POST and
  // returns 405. The Go unit tests in internal/api/uploads_test.go cover the
  // same parser behavior with a real multipart writer, so this gap is fine.
  // Scoped to this describe (not file-wide) since the browser-UI describe
  // below doesn't send real multipart bodies and isn't affected.
  test.skip(({ browserName }) => browserName === 'webkit',
    'webkit APIRequestContext mishandles multipart; covered by Go tests');

  test('mid-session upload writes to .context/ and returns paths', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/upload`, {
      headers,
      multipart: {
        files: { name: 'screenshot.png', mimeType: 'image/png', buffer: Buffer.from('PNG-bytes') },
      },
    });
    expect(r.status()).toBe(200);
    const j = await r.json();
    expect(Array.isArray(j.paths)).toBe(true);
    expect(j.paths[0]).toMatch(/^\.\/\.context\/screenshot\.png$/);
  });

  test('mid-session upload rejects empty body', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/upload`, {
      headers,
      multipart: {},
    });
    expect(r.status()).toBe(400);
  });

  test('mid-session upload returns 404 for unknown task', async ({ request }) => {
    const r = await request.post(`/api/tasks/does-not-exist/upload`, {
      headers,
      multipart: {
        files: { name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('hi') },
      },
    });
    expect(r.status()).toBe(404);
  });

  test('multipart create-task accepts files and creates a task', async ({ request }) => {
    const r = await request.post('/api/tasks', {
      headers,
      multipart: {
        name: 'multipart-task',
        prompt: 'look at this',
        project: 'test-proj',
        files: { name: 'note.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') },
      },
    });
    // The test-server's TaskCreator (used for JSON path) ignores attachments,
    // so multipart goes through agent.CreateAndStart directly. Project
    // "test-proj" is configured, so the task should be created.
    expect([200, 201]).toContain(r.status());
    const j = await r.json();
    expect(j.id).toBeTruthy();
  });

  test('multipart create-task without project returns 400', async ({ request }) => {
    const r = await request.post('/api/tasks', {
      headers,
      multipart: {
        prompt: 'p',
        files: { name: 'a.txt', mimeType: 'text/plain', buffer: Buffer.from('a') },
      },
    });
    expect(r.status()).toBe(400);
  });
});

// Mid-session upload inherits api()'s longer FormData timeout (280s), which
// makes it more likely than the JSON path to legitimately outlast a slow
// connection — its catch block must treat a client-side AbortError as
// "maybe completed" rather than a hard failure, same as createTask()'s New
// Task form fix. No real multipart body is sent here (api() itself is
// stubbed), so this doesn't hit the webkit APIRequestContext gap above.
test.describe('file uploads — browser UI abort handling', () => {
  test('onUploadFilesPicked() treats an aborted upload as "maybe completed" instead of a hard error', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('#main-app')).toBeVisible();
    await page.locator('.task-item').first().click();
    await expect.poll(() => page.evaluate(() => !!(window as any).currentTask)).toBe(true);

    // Stub api() so the /api/tasks/:id/upload POST rejects with a
    // client-side AbortError, simulating the fetch timing out while the
    // server keeps writing the attachment.
    await page.evaluate(() => {
      const real = (window as any).api;
      (window as any).api = (path: string, opts?: any) => {
        if (path.includes('/upload') && opts?.method === 'POST') {
          return Promise.reject(new DOMException('The operation was aborted.', 'AbortError'));
        }
        return real(path, opts);
      };
    });

    await page.evaluate(() => {
      const file = new File(['PNG-bytes'], 'screenshot.png', { type: 'image/png' });
      const dt = new DataTransfer();
      dt.items.add(file);
      const input = document.createElement('input');
      input.type = 'file';
      input.files = dt.files;
      (window as any).onUploadFilesPicked({ target: input });
    });

    await expect(page.locator('.toast')).toContainText('may have completed anyway');
  });
});
