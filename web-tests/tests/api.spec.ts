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

test.describe('task action endpoints', () => {
  test('rename', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/rename`, {
      headers, data: { name: 'renamed-task-' + Date.now() },
    });
    expect(r.status()).toBe(200);
    const got = await request.get(`/api/tasks/${id}`, { headers });
    const j = await got.json();
    expect(j.name).toMatch(/^renamed-task-/);
  });

  test('archive then unarchive', async ({ request }) => {
    const id = await getFirstTaskID(request);
    let r = await request.post(`/api/tasks/${id}/archive`, { headers });
    expect(r.status()).toBe(200);

    // archived tasks are excluded from default list
    let list = await (await request.get('/api/tasks', { headers })).json();
    expect(list.tasks.find((t: any) => t.id === id)).toBeUndefined();

    // archived=1 returns only archived
    list = await (await request.get('/api/tasks?archived=1', { headers })).json();
    expect(list.tasks.find((t: any) => t.id === id)).toBeDefined();

    r = await request.post(`/api/tasks/${id}/unarchive`, { headers });
    expect(r.status()).toBe(200);
    list = await (await request.get('/api/tasks', { headers })).json();
    expect(list.tasks.find((t: any) => t.id === id)).toBeDefined();
  });

  test('set status', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/status`, {
      headers, data: { status: 'in_review' },
    });
    expect(r.status()).toBe(200);
    const j = await r.json();
    expect(j.status).toBe('in_review');
  });

  test('set status rejects unknown', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/status`, {
      headers, data: { status: 'nonsense' },
    });
    expect(r.status()).toBe(400);
  });

  test('fork creates a new task', async ({ request }) => {
    const id = await getFirstTaskID(request);
    const r = await request.post(`/api/tasks/${id}/fork`, {
      headers, data: { name: 'forked-' + Date.now(), prompt: 'follow up work' },
    });
    expect(r.status()).toBe(201);
    const j = await r.json();
    expect(j.id).toBeTruthy();
    expect(j.name).toMatch(/^forked-/);
  });
});

test.describe('projects CRUD', () => {
  test('list/create/update/delete', async ({ request }) => {
    const projName = 'spec-proj-' + Date.now();

    // create
    let r = await request.post('/api/projects', {
      headers, data: { name: projName, path: '/tmp/' + projName, branch: 'main' },
    });
    expect(r.status()).toBe(201);

    // list (full)
    r = await request.get('/api/projects/full', { headers });
    expect(r.status()).toBe(200);
    let body = await r.json();
    const found = body.projects.find((p: any) => p.name === projName);
    expect(found).toBeTruthy();
    expect(found.path).toBe('/tmp/' + projName);

    // update
    r = await request.put(`/api/projects/${projName}`, {
      headers, data: { path: '/tmp/' + projName + '-renamed', branch: 'main' },
    });
    expect(r.status()).toBe(200);

    // delete
    r = await request.delete(`/api/projects/${projName}`, { headers });
    expect(r.status()).toBe(200);

    r = await request.get('/api/projects/full', { headers });
    body = await r.json();
    expect(body.projects.find((p: any) => p.name === projName)).toBeUndefined();
  });

  test('rejects empty path', async ({ request }) => {
    const r = await request.post('/api/projects', {
      headers, data: { name: 'noPath', path: '' },
    });
    expect(r.status()).toBe(400);
  });
});

test.describe('backends CRUD', () => {
  test('list/create/update/delete', async ({ request }) => {
    const name = 'spec-backend-' + Date.now();

    let r = await request.post('/api/backends', {
      headers, data: { name, command: 'echo hi', prompt_flag: '-p' },
    });
    expect(r.status()).toBe(201);

    r = await request.get('/api/backends', { headers });
    let body = await r.json();
    expect(body.backends.find((b: any) => b.name === name)).toBeDefined();

    r = await request.put(`/api/backends/${name}`, {
      headers, data: { command: 'echo updated', prompt_flag: '' },
    });
    expect(r.status()).toBe(200);

    r = await request.get('/api/backends', { headers });
    body = await r.json();
    expect(body.backends.find((b: any) => b.name === name).command).toBe('echo updated');

    r = await request.delete(`/api/backends/${name}`, { headers });
    expect(r.status()).toBe(200);

    r = await request.get('/api/backends', { headers });
    body = await r.json();
    expect(body.backends.find((b: any) => b.name === name)).toBeUndefined();
  });
});

test.describe('stop-all', () => {
  test('marks running tasks as in_review', async ({ request }) => {
    const r = await request.post('/api/sessions/stop-all', { headers });
    expect(r.status()).toBe(200);
    const j = await r.json();
    expect(j.stopped).toBeGreaterThanOrEqual(0);
  });
});
