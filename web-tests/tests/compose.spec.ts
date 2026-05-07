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

  // Simulates iOS Safari's soft-keyboard Send key when predictive text /
  // dictation hijacks the keydown. iOS surfaces keyCode 229 / key
  // 'Unidentified' (or no keydown at all) instead of a real Enter, while
  // still firing `beforeinput` with inputType: insertLineBreak. The pure-
  // beforeinput dispatch below mirrors the keydown-less worst case;
  // without the beforeinput listener the prompt sat in the textarea
  // un-submitted.
  test('soft-keyboard Send (beforeinput insertLineBreak) sends without keydown', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertLineBreak',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Regression: when iOS predictive text / autocorrect has an active
  // composition, the soft-keyboard Send key dispatches `keydown` with
  // key='Enter' AND isComposing=true, immediately followed by `beforeinput`
  // with inputType='insertLineBreak'. The previous keydown handler always
  // updated `lastEnterKeydownAt` regardless of isComposing, so the beforeinput
  // handler treated the line break as "Shift+Enter following a real Enter
  // keydown" and returned early without sending — the browser then inserted a
  // literal `\n` and the prompt sat in the textarea unsubmitted. Symptom:
  // tapping Send sometimes drops a newline instead of POSTing.
  test('soft-keyboard Send during IME composition still sends', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // Enter with isComposing=true — the keydown handler must NOT claim the
      // timestamp, so the beforeinput insertLineBreak that follows is treated
      // as a fresh soft-keyboard Send.
      el.dispatchEvent(new KeyboardEvent('keydown', {
        key: 'Enter',
        isComposing: true,
        cancelable: true,
        bubbles: true,
      }));
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertLineBreak',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  test('Send while scrolled in history snaps viewport back to bottom', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Populate scrollback and scroll up so the at-bottom gate is closed. Without
    // the snap-on-send, an SSE response from the agent would buffer into
    // pendingChunks and the user would see no feedback after pressing Send.
    // term.write is async (microtask parser); the callback signals "buffer
    // updated" so the subsequent scrollLines actually moves baseY.
    await page.evaluate(async () => {
      const t = (window as any).term;
      let s = '';
      for (let i = 0; i < 200; i++) s += `line ${i}\r\n`;
      await new Promise<void>(resolve => t.write(s, () => resolve()));
      t.scrollLines(-50);
    });
    const beforeAtBottom = await page.evaluate(() => {
      const t = (window as any).term;
      return t.buffer.active.viewportY === t.buffer.active.baseY;
    });
    expect(beforeAtBottom).toBe(false);

    await page.locator('#compose-input').fill('reply from scrollback');
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-send').click();
    await inputReq;

    // After Send, viewport must be back at the bottom so any agent reply
    // arriving through bufferOrWrite drains immediately into xterm.
    const afterAtBottom = await page.evaluate(() => {
      const t = (window as any).term;
      return t.buffer.active.viewportY === t.buffer.active.baseY;
    });
    expect(afterAtBottom).toBe(true);

    // The viewport check above proves we *moved* to the bottom; this proves the
    // bufferOrWrite gate is now open by feeding a synthetic SSE chunk through
    // the same path xterm receives real agent replies on. If the gate were
    // still closed (scrollToBottom didn't sync viewportY in time), the chunk
    // would land in pendingChunks instead of being written. argusPending is
    // exposed on window for exactly this kind of internal-state assertion.
    const pending = await page.evaluate(() => {
      (window as any).bufferOrWrite(new TextEncoder().encode('agent reply\r\n'));
      return (window as any).argusPending();
    });
    expect(pending.chunks).toBe(0);
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
    // Blur the currently-focused element. login leaves compose-input focused
    // (setupTerm's rAF calls focusInputOrTerm), and a bare body.focus() is a
    // no-op on a non-tabindex body — so the focus assertion would pass even
    // when the handler did nothing. Explicit blur is the only reliable way
    // to drop focus.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());

    // Synthesize a tap (touchstart + touchend with no movement) on #term —
    // a real click on iPhone fires touch events, not mouse events.
    await page.locator('#term').dispatchEvent('touchstart', { touches: [{ clientX: 100, clientY: 100 }] });
    await page.locator('#term').dispatchEvent('touchend', { changedTouches: [{ clientX: 100, clientY: 100 }] });

    // After a tap, focus must land on the compose textarea so iOS dictation,
    // third-party keyboards, and Wispr Flow have a real visible target.
    const focusedId = await page.evaluate(() => document.activeElement?.id);
    expect(focusedId).toBe('compose-input');
  });

  test('tap from scrollback still focuses compose-input', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Push enough content into the terminal to populate scrollback, then
    // scroll the viewport up so the user is reading history. The previous
    // implementation gated tap-to-focus on `termIsAtBottom()`, which silently
    // dropped this tap and left the user with no input target. The new
    // implementation must focus on every deliberate tap, scroll position
    // notwithstanding.
    //
    // term.write is async (the parser runs on a microtask); the callback
    // signals "data has been parsed and emitted to the buffer". Without it
    // the subsequent scrollLines call lands before the buffer has the new
    // rows, so baseY hasn't advanced and the scroll is a no-op.
    await page.evaluate(async () => {
      const t = (window as any).term;
      let s = '';
      for (let i = 0; i < 200; i++) s += `line ${i}\r\n`;
      await new Promise<void>(resolve => t.write(s, () => resolve()));
      t.scrollLines(-50);
    });
    // Sanity-check we are not at bottom (otherwise the test would also pass
    // under the old bottom-gated behavior and prove nothing).
    const atBottom = await page.evaluate(() => {
      const t = (window as any).term;
      return t.buffer.active.viewportY === t.buffer.active.baseY;
    });
    expect(atBottom).toBe(false);

    // Call the touch handlers directly with plain objects matching the
    // TouchEvent shape. We can't go through dispatchEvent: WebKit forbids
    // `new Touch()` (Illegal constructor), and dispatching a TouchEvent with
    // plain-object `touches` results in an empty TouchList — the handler
    // can't capture coords and the tap-vs-swipe discriminator collapses to
    // wasTap=true. Calling the handler directly exercises the gate logic
    // itself without fighting the browser's Touch interface.
    //
    // Blur explicitly (a bare body.focus() is a no-op on a non-tabindex body,
    // so the assertion would silently pass even if the handler did nothing).
    // Also fire onTermScrollEnd to clear isTermScrolling — `term.scrollLines`
    // dispatches a `scroll` event that sets the gate to true, and our handler
    // would otherwise reject the tap as a swipe.
    const focusedId = await page.evaluate(async () => {
      const w = window as any;
      await new Promise(r => requestAnimationFrame(r));
      if (w.onTermScrollEnd) w.onTermScrollEnd();
      (document.activeElement as HTMLElement | null)?.blur();
      w.onTermTouchStart({ touches: [{ clientX: 100, clientY: 100 }] });
      w.onTermTouchEnd({ changedTouches: [{ clientX: 100, clientY: 100 }] });
      return (document.activeElement as HTMLElement | null)?.id ?? '';
    });
    expect(focusedId).toBe('compose-input');
  });

  test('swipe on terminal does NOT focus compose-input', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Blur the currently-focused element. login leaves compose-input focused
    // (setupTerm's rAF calls focusInputOrTerm), and a bare body.focus() is a
    // no-op on a non-tabindex body — so the focus assertion would pass even
    // when the handler did nothing. Explicit blur is the only reliable way
    // to drop focus.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());

    // touchstart at (100,100), touchend at (100,250) — 150px vertical move,
    // well past the 10px tap threshold. Without the coord-delta gate, this
    // would be misclassified as a tap (isTermScrolling stays false on a
    // synthesized event with no real scroll dispatch) and pop the keyboard.
    const focusedId = await page.evaluate(async () => {
      const w = window as any;
      // Wait one frame so any pending setupTerm rAF (which calls
      // focusInputOrTerm) fires before the blur.
      await new Promise(r => requestAnimationFrame(r));
      (document.activeElement as HTMLElement | null)?.blur();
      w.onTermTouchStart({ touches: [{ clientX: 100, clientY: 100 }] });
      w.onTermTouchEnd({ changedTouches: [{ clientX: 100, clientY: 250 }] });
      return (document.activeElement as HTMLElement | null)?.id ?? '';
    });
    expect(focusedId).not.toBe('compose-input');
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
