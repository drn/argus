import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

const TOKEN = 'test-token';
const headers = { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' };

test.describe('push notifications API', () => {
  test('VAPID public key endpoint returns a key', async ({ request }) => {
    const r = await request.get('/api/push/vapid-public-key', { headers });
    expect(r.status()).toBe(200);
    const j = await r.json();
    expect(j.public_key).toBeTruthy();
    expect(j.public_key.length).toBeGreaterThan(40);
  });

  test('subscribe → list → delete', async ({ request }) => {
    // Mock subscription shape (real push services would supply these via
    // PushManager.subscribe()).
    const fakeSub = {
      label: 'iPhone test',
      endpoint: 'https://fcm.googleapis.com/fcm/send/test-' + Date.now(),
      keys: {
        p256dh: 'BJG3v0SrEqJjrGMQyqZ_YKoY3QHc8CqYZTnfQqbJC1Ne_m0QuhWv7HXgBnCKM7nXkLSUJ8FQmCzTPL7QHfQS-AY',
        auth: 'tBHItJI5svbpez7KI4CCXg',
      },
    };

    let r = await request.post('/api/push/subscribe', { headers, data: fakeSub });
    expect(r.status()).toBe(201);
    const { id } = await r.json();
    expect(id).toBeGreaterThan(0);

    r = await request.get('/api/push/subscriptions', { headers });
    expect(r.status()).toBe(200);
    let body = await r.json();
    expect(body.subscriptions.find((s: any) => s.id === id)).toBeTruthy();
    // Endpoint is masked in list output
    expect(body.subscriptions.find((s: any) => s.id === id).endpoint_masked).toContain('…');

    r = await request.delete(`/api/push/subscribe/${id}`, { headers });
    expect(r.status()).toBe(200);

    r = await request.get('/api/push/subscriptions', { headers });
    body = await r.json();
    expect(body.subscriptions.find((s: any) => s.id === id)).toBeUndefined();
  });

  test('subscribe rejects empty endpoint', async ({ request }) => {
    const r = await request.post('/api/push/subscribe', {
      headers, data: { endpoint: '', keys: { p256dh: 'x', auth: 'y' } },
    });
    expect(r.status()).toBe(400);
  });
});
