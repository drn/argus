import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
  await page.locator('.task-item').first().click();
  await expect(page.locator('.term-status.live')).toBeVisible({ timeout: 5000 });
}

// IS_TOUCH is computed at script load from `'ontouchstart' in window` and
// `(pointer: coarse)`. On the iphone device profile both are true, on desktop
// neither — so the same #compose-bar element flips visible/hidden by project.
test.describe('compose bar', () => {
  test('visible on iphone, hidden on desktop while task is running', async ({ page }, testInfo) => {
    await login(page);
    const isTouch = testInfo.project.name === 'iphone';
    if (isTouch) {
      await expect(page.locator('#compose-bar')).toBeVisible();
      await expect(page.locator('#compose-input')).toBeVisible();
      await expect(page.locator('#compose-send')).toBeVisible();
    } else {
      await expect(page.locator('#compose-bar')).toBeHidden();
    }
  });

  test('Send button forwards value + CR to /input and clears textarea', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-send').click();
    const req = await inputReq;
    // \r (CR), not \n — raw-terminal Enter key. \n is interpreted as an
    // embedded newline by Claude Code and would not submit the prompt.
    expect(req.postData()).toBe('hello world\r');

    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  test('Enter sends, Shift+Enter inserts a newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    const ci = page.locator('#compose-input');
    await ci.click();
    await page.keyboard.type('one');
    await page.keyboard.down('Shift');
    await page.keyboard.press('Enter');
    await page.keyboard.up('Shift');
    await page.keyboard.type('two');

    // Shift+Enter must not have sent.
    await expect(ci).toHaveValue('one\ntwo');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.keyboard.press('Enter');
    const req = await inputReq;
    expect(req.postData()).toBe('one\ntwo\r');

    await expect(ci).toHaveValue('');
  });

  test('oversize input toasts and does not POST', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // 64KB + 1 byte — above COMPOSE_MAX_BYTES.
    const tooLong = 'a'.repeat(64 * 1024 + 1);
    await page.locator('#compose-input').fill(tooLong);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-send').click();

    await expect(page.locator('.toast')).toBeVisible();
    await expect(page.locator('.toast')).toContainText('Input too long');
    expect(posted).toBe(false);
  });

  test('skill autocomplete: / opens dropdown, Enter inserts without sending', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');

    // The test server seeds an empty `~/.claude/skills/` (HOME is a tempdir) so
    // /api/skills returns []. Stub the endpoint with a couple of fake skills so
    // the AC has something to render. route() runs before the page makes its
    // first /api/skills request, which fires the moment the agent view shows
    // the compose bar.
    await page.route('**/api/skills**', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        skills: [
          { name: 'review', description: 'Review a pull request' },
          { name: 'rereview', description: 'Re-review with fresh eyes' },
          { name: 'test', description: 'Run tests' },
        ],
      }),
    }));

    await login(page);

    const ci = page.locator('#compose-input');
    const dd = page.locator('#compose-ac-dropdown');

    await ci.click();
    await page.keyboard.type('/');
    // First character of `/` opens the dropdown with all three skills.
    await expect(dd).toHaveClass(/open/);
    await expect(dd.locator('.ac-item')).toHaveCount(3);

    // `re` filters to review + rereview (case-insensitive substring match).
    await page.keyboard.type('re');
    await expect(dd.locator('.ac-item')).toHaveCount(2);

    // ArrowDown moves selection from review → rereview.
    await page.keyboard.press('ArrowDown');
    await expect(dd.locator('.ac-item.selected')).toContainText('rereview');

    // Enter while AC is open must SELECT the item — not POST to /input.
    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.keyboard.press('Enter');
    await expect(ci).toHaveValue('/rereview ');
    await expect(dd).not.toHaveClass(/open/);
    expect(posted).toBe(false);

    // Now Enter on a non-slash value sends normally.
    await ci.fill('hello');
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await ci.press('Enter');
    const req = await inputReq;
    expect(req.postData()).toBe('hello\r');
  });

  test('skill autocomplete: tapping a dropdown item inserts and closes', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');

    await page.route('**/api/skills**', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        skills: [
          { name: 'review' },
          { name: 'rereview' },
        ],
      }),
    }));

    await login(page);

    const ci = page.locator('#compose-input');
    const dd = page.locator('#compose-ac-dropdown');

    await ci.click();
    await page.keyboard.type('/re');
    await expect(dd).toHaveClass(/open/);

    // Tapping an item must NOT POST to /input — only insert + close.
    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await dd.locator('.ac-item', { hasText: 'rereview' }).click();
    await expect(ci).toHaveValue('/rereview ');
    await expect(dd).not.toHaveClass(/open/);
    expect(posted).toBe(false);
  });

  test('skill autocomplete: Escape closes dropdown without inserting', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');

    await page.route('**/api/skills**', route => route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ skills: [{ name: 'review' }] }),
    }));

    await login(page);

    const ci = page.locator('#compose-input');
    const dd = page.locator('#compose-ac-dropdown');

    await ci.click();
    await page.keyboard.type('/r');
    await expect(dd).toHaveClass(/open/);

    await page.keyboard.press('Escape');
    await expect(dd).not.toHaveClass(/open/);
    // Escape must not clear the typed prefix — the user can keep editing.
    await expect(ci).toHaveValue('/r');
  });

  test('tap on terminal focuses compose-input (not xterm helper textarea)', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Start with focus elsewhere so the assertion is meaningful — login leaves
    // focus on the helper textarea (term.focus() runs in setupTerm's rAF).
    await page.locator('body').evaluate((el: HTMLElement) => el.focus());

    // Synthesize a tap (touchstart + touchend with no movement) on #term —
    // a real click on iPhone fires touch events, not mouse events.
    await page.locator('#term').dispatchEvent('touchstart', { touches: [{ clientX: 100, clientY: 100 }] });
    await page.locator('#term').dispatchEvent('touchend', { changedTouches: [{ clientX: 100, clientY: 100 }] });

    // After a tap, focus must land on the compose textarea so iOS dictation,
    // third-party keyboards, and Wispr Flow have a real visible target.
    const focusedId = await page.evaluate(() => document.activeElement?.id);
    expect(focusedId).toBe('compose-input');
  });

  test('compose bar hidden after closing the detail view', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await expect(page.locator('#compose-bar')).toBeVisible();
    await page.locator('#compose-input').fill('partial');
    // Invoke closeDetail() directly — the .detail-back link is hidden in
    // compact mode (which is on by default on the iphone viewport because
    // window.innerHeight - visualViewport.height starts > 100).
    await page.evaluate(() => (window as any).closeDetail());
    await expect(page.locator('#detail-view')).not.toHaveClass(/open/);
    // destroyTerm() centralizes the hide so switchTab / showOffline get it
    // for free; closeDetail also clears the textarea so the next opened task
    // sees a fresh field.
    await expect(page.locator('#compose-bar')).toBeHidden();
    await expect(page.locator('#compose-input')).toHaveValue('');
  });
});
