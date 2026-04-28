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

  test('manifest declares Web Share Target with title/text/url params', async ({ request }) => {
    const r = await request.get('/manifest.webmanifest');
    expect(r.status()).toBe(200);
    const body = await r.json();
    expect(body.share_target).toBeTruthy();
    expect(body.share_target.action).toBe('/share');
    expect(body.share_target.method).toBe('GET');
    expect(body.share_target.params).toEqual({ title: 'title', text: 'text', url: 'url' });
  });

  test('/share is reachable without auth and returns the dashboard shell', async ({ request }) => {
    // iOS hits /share with the share params before any auth has happened, so
    // the route must be unauthenticated like / itself.
    const r = await request.get('/share?title=hello&text=world&url=https%3A%2F%2Fexample.com');
    expect(r.status()).toBe(200);
    expect(r.headers()['content-type']).toContain('text/html');
    const html = await r.text();
    expect(html).toContain('id="main-app"');
  });

  test('/share?title=...&text=...&url=... prefills New Task prompt after auth', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/share?title=Hello&text=Some%20text&url=https%3A%2F%2Fexample.com');
    // After connecting, the create tab should be the visible view and the
    // prompt textarea should hold the joined share content.
    await expect(page.locator('#create-view')).toBeVisible();
    const prompt = await page.locator('#create-prompt').inputValue();
    expect(prompt).toContain('Hello');
    expect(prompt).toContain('Some text');
    expect(prompt).toContain('https://example.com');
    // URL should have been cleaned up so reload doesn't refire the share.
    expect(new URL(page.url()).pathname).toBe('/');
    // Pending share should be consumed.
    const pending = await page.evaluate(() => sessionStorage.getItem('argus-pending-share'));
    expect(pending).toBeNull();
  });
});
