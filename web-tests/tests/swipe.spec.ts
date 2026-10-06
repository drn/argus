import { test, expect, Page } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

// Dispatch synthetic touch events on the first task row; the swipe handler
// listens on #task-list via event delegation.
async function swipe(page: Page, dx: number) {
  await page.evaluate(async (dx) => {
    const item = document.querySelector('.task-item') as HTMLElement;
    const r = item.getBoundingClientRect();
    const x0 = r.left + r.width / 2, y = r.top + r.height / 2;
    const mk = (type: string, x: number) => {
      const t = new Touch({ identifier: 1, target: item, clientX: x, clientY: y });
      item.dispatchEvent(new TouchEvent(type, {
        bubbles: true, cancelable: true,
        touches: type === 'touchend' ? [] : [t], changedTouches: [t],
      }));
    };
    mk('touchstart', x0);
    mk('touchmove', x0 + dx / 2);
    mk('touchmove', x0 + dx);
    mk('touchend', x0 + dx);
    item.click();
  }, dx);
}

async function status(request: any) {
  const r = await request.get('/api/tasks', { headers: { Authorization: 'Bearer test-token' } });
  return (await r.json()).tasks.find((t: any) => t.name === 'echo-bash').status;
}

test.describe('task row swipe', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
    await page.goto('/');
    await expect(page.locator('.task-item')).toHaveCount(1);
  });

  test('short swipe snaps back without a request or opening the task', async ({ page, request }) => {
    const before = await status(request);
    await swipe(page, -30);
    await expect(page.locator('#detail-view, #agent-view').first()).not.toBeVisible();
    expect(await status(request)).toBe(before);
  });

  test('swipe left completes, swipe right reopens', async ({ page, request }) => {
    await swipe(page, -150);
    await expect.poll(() => status(request)).toBe('complete');
    await expect(page.locator('.task-item .badge-complete')).toBeVisible();
    await swipe(page, 150);
    await expect.poll(() => status(request)).toBe('in_review');
  });

  test('swipe right on a non-complete task does nothing', async ({ page, request }) => {
    const before = await status(request);
    await swipe(page, 150);
    expect(await status(request)).toBe(before);
  });
});
