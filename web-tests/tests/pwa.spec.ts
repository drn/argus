import { test, expect } from '@playwright/test';

test.describe('PWA', () => {
  test('manifest.webmanifest is served with correct content', async ({ request }) => {
    const r = await request.get('/manifest.webmanifest');
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type']).toContain('manifest');
    const body = await r.json();
    expect(body.name).toBe('Argus Remote');
    expect(body.display).toBe('standalone');
    expect(body.icons).toContainEqual(
      expect.objectContaining({ sizes: '192x192', type: 'image/png' })
    );
  });

  test('icons are reachable', async ({ request }) => {
    for (const path of ['/icon-192.png', '/icon-512.png', '/apple-touch-icon.png']) {
      const r = await request.get(path);
      expect(r.status(), `${path}`).toBe(200);
      expect(r.headers()['content-type'], `${path}`).toBe('image/png');
      const buf = await r.body();
      // PNG magic bytes: 89 50 4E 47 0D 0A 1A 0A
      expect(buf[0]).toBe(0x89);
      expect(buf[1]).toBe(0x50);
      expect(buf[2]).toBe(0x4e);
      expect(buf[3]).toBe(0x47);
    }
  });

  test('service worker registers and is reachable', async ({ page, request }) => {
    const swResp = await request.get('/sw.js');
    expect(swResp.status()).toBe(200);
    expect(swResp.headers()['content-type']).toContain('javascript');
    expect((await swResp.text()).length).toBeGreaterThan(100);

    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    // Service workers are async; give it a beat.
    await page.waitForFunction(
      () => navigator.serviceWorker?.controller !== null || navigator.serviceWorker?.ready,
      { timeout: 5000 }
    );
    const reg = await page.evaluate(() => navigator.serviceWorker.getRegistration());
    expect(reg).toBeTruthy();
  });

  test('manifest link is present on dashboard', async ({ page }) => {
    await page.goto('/');
    const href = await page.locator('link[rel="manifest"]').getAttribute('href');
    expect(href).toBe('/manifest.webmanifest');
    const apple = await page.locator('link[rel="apple-touch-icon"]').getAttribute('href');
    expect(apple).toBe('/apple-touch-icon.png');
  });

  test('PWA assets do not require auth', async ({ request }) => {
    // No Authorization header — these must still be reachable so iOS/Chromium
    // can fetch them on install.
    for (const path of ['/manifest.webmanifest', '/sw.js', '/icon-192.png', '/apple-touch-icon.png']) {
      const r = await request.get(path);
      expect(r.status(), path).toBe(200);
    }
  });
});
