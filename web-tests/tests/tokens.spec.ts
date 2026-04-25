import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

const MASTER = 'test-token';

test.describe('per-device tokens', () => {
  test('master can mint, list, revoke device tokens', async ({ request }) => {
    const headers = { Authorization: `Bearer ${MASTER}`, 'Content-Type': 'application/json' };

    let r = await request.post('/api/tokens', { headers, data: { label: 'My iPhone' } });
    expect(r.status()).toBe(201);
    const minted = await r.json();
    expect(minted.token).toBeTruthy();
    expect(minted.token.length).toBeGreaterThan(40);
    expect(minted.label).toBe('My iPhone');
    const tokenID = minted.id;
    const tokenPlain = minted.token;

    // Device token can read tasks…
    r = await request.get('/api/tasks', {
      headers: { Authorization: `Bearer ${tokenPlain}` },
    });
    expect(r.status()).toBe(200);

    // …but cannot mint more tokens
    r = await request.post('/api/tokens', {
      headers: { Authorization: `Bearer ${tokenPlain}`, 'Content-Type': 'application/json' },
      data: { label: 'no' },
    });
    expect(r.status()).toBe(403);

    // List shows the device token (last4, no plaintext)
    r = await request.get('/api/tokens', { headers });
    const list = await r.json();
    const found = list.tokens.find((t: any) => t.id === tokenID);
    expect(found).toBeTruthy();
    expect(found.last4).toBe(tokenPlain.slice(-4));

    // Revoke
    r = await request.delete(`/api/tokens/${tokenID}`, { headers });
    expect(r.status()).toBe(200);

    // Token no longer works
    r = await request.get('/api/tasks', {
      headers: { Authorization: `Bearer ${tokenPlain}` },
    });
    expect(r.status()).toBe(401);
  });

  test('invalid bearer is rejected', async ({ request }) => {
    const r = await request.get('/api/tasks', {
      headers: { Authorization: 'Bearer wrong-token' },
    });
    expect(r.status()).toBe(401);
  });
});
