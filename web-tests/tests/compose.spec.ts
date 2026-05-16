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

  // Regression: iOS WebKit's predictive text / dictation sometimes commits
  // the soft-keyboard Send by dispatching `beforeinput` with `inputType:
  // 'insertText'` and `data: '\n'` instead of `insertLineBreak`. Catching
  // only `insertLineBreak` let this slip through the handler, the browser
  // inserted a literal `\n` into the textarea, and the prompt sat unsubmitted.
  // Symptom: tapping Send sometimes drops a newline instead of POSTing.
  test('soft-keyboard Send dispatched as insertText "\\n" still sends', async ({ page }, testInfo) => {
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
        inputType: 'insertText',
        data: '\n',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Regression: some iOS WebKit builds dispatch `beforeinput` with
  // `inputType: 'insertParagraph'` for the soft-keyboard Send on a textarea.
  // Catching only `insertLineBreak` left this falling through to the browser
  // default — same newline-in-textarea, prompt-not-sent failure mode.
  test('soft-keyboard Send dispatched as insertParagraph still sends', async ({ page }, testInfo) => {
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
        inputType: 'insertParagraph',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Guard against the broadened insertText matcher: a regular character like
  // 'a' must NOT trigger a send. Only data ending in a newline counts as a
  // line break.
  test('beforeinput insertText with regular character does NOT send', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello');

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertText',
        data: 'a',
        cancelable: true,
        bubbles: true,
      }));
    });
    // Give any inflight POST a moment to land — none should.
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    // Textarea unchanged because nothing was sent and we didn't preventDefault
    // (the synthetic dispatchEvent doesn't actually mutate the textarea — but
    // the value should at least still be the original 'hello').
    await expect(page.locator('#compose-input')).toHaveValue('hello');
  });

  // Regression: on long prompts, iOS WebKit's predictive text / dictation /
  // autocorrect bundles a still-composing trailing word with the Send line
  // break in a single `beforeinput insertText` event whose `data` is `'word\n'`.
  // The previous matcher required `data` to be exactly `'\n'` / `'\r'` /
  // `'\r\n'`, so this fell through to the browser default — the entire data
  // string (including the trailing newline) landed in the textarea and
  // sendCompose() never ran. Symptom users report: "Enter on synthetic
  // keyboard still does not always send and sometimes adds a newline,
  // especially with long prompts." The committed prefix must survive into
  // the POST so the user's last word isn't dropped.
  test('soft-keyboard Send dispatched as insertText "word\\n" still sends with prefix preserved', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello ');
    // Park the cursor at end so setRangeText appends — mirrors the typical
    // long-prompt-then-Send shape where the user has just dictated the last
    // word.
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    });

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertText',
        data: 'world\n',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    // The prefix ('world') must be merged into the textarea value before
    // sendCompose reads it, so the POST contains 'hello world\r' — without
    // the prefix-preservation the POST would be 'hello \r' and the user's
    // last word would silently vanish.
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Same bundling but via `insertReplacementText` — iOS uses this inputType
  // when committing an autocorrect replacement. With autocorrect=on on the
  // compose textarea, this path is now reachable on every Send that happens
  // mid-autocorrect.
  test('soft-keyboard Send dispatched as insertReplacementText "word\\n" still sends', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello ');
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
    });

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertReplacementText',
        data: 'world\n',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Verifies that for `insertReplacementText` (autocorrect commit), the
  // splice respects the IME's selection range — i.e. it REPLACES the
  // autocorrect target rather than appending at end-of-text. The previous
  // test set `selectionStart === selectionEnd === end`, exercising only the
  // append path. Real autocorrect events arrive with the selection covering
  // the misspelled word; this test simulates that shape.
  test('insertReplacementText with non-collapsed selection replaces autocorrect target', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Textarea content has the misspelled word `wrold` at positions 6..11.
    // The IME has selected that range to mark its autocorrect target.
    await page.locator('#compose-input').fill('hello wrold');
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.setSelectionRange(6, 11);
    });

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertReplacementText',
        data: 'world\n',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    // setRangeText replaces the IME target range (6..11 = "wrold") with the
    // corrected word "world", yielding "hello world\r" in the POST. If the
    // splice ignored the selection range and appended instead, the POST
    // would be "hello wroldworld\r".
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Defense-in-depth: any `input` event (post-mutation) that lands a trailing
  // newline in the textarea via an unrecognized inputType — a future iOS
  // variant, a third-party keyboard, Wispr Flow — must still submit. The
  // beforeinput matcher is intentionally narrow; this fallback observes the
  // side effect rather than the trigger and closes the open-ended whack-a-mole
  // surface area.
  test('input fallback: unknown inputType with trailing newline still sends', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // Simulate the browser default having applied — the textarea ends in
      // \n — and dispatch the post-mutation `input` event with an inputType
      // the beforeinput matcher does NOT know about. The fallback catches
      // it on the trailing-newline check.
      el.value = 'hello world\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromComposition',
        data: '\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Multi-line bundled data — the strict beforeinput regex (`[^\r\n]*`)
  // rejects data with embedded newlines, so it falls through to the input
  // fallback, which strips the trailing newlines. A looser regex variant
  // (e.g. `[\s\S]*?`) would have matched `'word\n\n'` with prefix `'word\n'`,
  // splicing that prefix via setRangeText and leaving an embedded `\n` in
  // the POST that Claude Code would treat as Shift+Enter — a multi-line
  // submission the user didn't compose. The strict-regex + fallback split
  // keeps embedded newlines out of the POST.
  test('input fallback: multi-line "word\\n\\n" data strips trailing newlines and sends only the word', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello ');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // Browser default would insert 'world\n\n' on `data: 'world\n\n'`.
      // Simulate that landing in the textarea, then fire the post-mutation
      // input event. The beforeinput matcher rejected this data (no match
      // because of the embedded newline gating in `[^\r\n]*`), so the
      // browser default applied. The fallback strips trailing newlines.
      el.value = 'hello world\n\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertText',
        data: 'world\n\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    // Trailing newlines stripped, no embedded `\n` smuggled into the POST.
    expect(req.postData()).toBe('hello world\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // No-double-fire guard: when the beforeinput handler successfully matches a
  // bundled-prefix variant (`insertText 'word\n'`), it preventDefaults and
  // calls sendCompose itself — but `setRangeText` fires a synchronous post-
  // mutation `input` event per HTML spec. The fallback must NOT also call
  // sendCompose on that event. The mechanism: setRangeText splices `'world'`
  // (no newline) into the textarea, so when its synchronous input event fires
  // the trailing-`\n` check (`/[\r\n]$/.test(v)`) bails — empty inputType is
  // no longer the gate (denylist passes it). The trailing-newline check is
  // load-bearing here. This test asserts exactly one POST fires.
  test('beforeinput-matched bundled prefix does NOT double-fire via input fallback', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello ');

    let postCount = 0;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') postCount++;
    });

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.setSelectionRange(el.value.length, el.value.length);
      // beforeinput matches → preventDefault + setRangeText('world', ...) +
      // sendCompose. setRangeText fires its own synchronous input event; the
      // fallback must skip it because `setRangeText` splices only the prefix
      // (no trailing newline), so the `/[\r\n]$/.test(v)` check bails. Empty
      // inputType is no longer the gate (denylist passes it).
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertText',
        data: 'world\n',
        cancelable: true,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello world\r');
    // Give a beat for any spurious second POST to land — none should.
    await page.waitForTimeout(200);
    expect(postCount).toBe(1);
  });

  // Regression: the variant-N+1 case — `keydown(Enter, isComposing=true)`
  // followed by a `beforeinput` inputType the matcher doesn't recognize.
  // Earlier the keydown unconditionally stamped `lastEnterAt`, which gated
  // the input fallback off and reproduced the original "newline lands, no
  // submit" symptom for any future iOS IME variant. Both stamps must skip
  // on `isComposing` so the fallback can fire.
  test('input fallback fires after isComposing keydown + unknown beforeinput inputType', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello world');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // 1) iOS dispatches keydown(Enter) with isComposing=true (active
      //    dictation/autocorrect). Both stamps must remain unset so the
      //    input fallback isn't gated off.
      el.dispatchEvent(new KeyboardEvent('keydown', {
        key: 'Enter',
        isComposing: true,
        cancelable: true,
        bubbles: true,
      }));
      // 2) iOS dispatches a beforeinput event the matcher doesn't recognize
      //    (simulating a future variant or third-party keyboard injection).
      //    Our handler doesn't preventDefault.
      el.dispatchEvent(new InputEvent('beforeinput', {
        inputType: 'insertSomeFutureVariant',
        data: 'final\n',
        cancelable: true,
        bubbles: true,
      }));
      // 3) Browser default would have inserted the data — simulate the
      //    post-mutation state and fire the input event. The fallback must
      //    catch the trailing newline and submit.
      el.value = 'hello worldfinal\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertSomeFutureVariant',
        data: 'final\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello worldfinal\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Guard rail: the input fallback MUST NOT fire on hardware Shift+Enter
  // (intentional newline) or on paste-with-trailing-newline (user action,
  // not a Send signal). Without proper gating, every Shift+Enter would
  // auto-submit and every paste of multi-line text would lose its content.
  test('input fallback does NOT fire on hardware Shift+Enter', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    const ci = page.locator('#compose-input');
    await ci.click();
    await page.keyboard.type('one');

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });

    await page.keyboard.down('Shift');
    await page.keyboard.press('Enter');
    await page.keyboard.up('Shift');

    // Shift+Enter sets `lastEnterAt`, so the input fallback (which fires from
    // the browser's default \n insertion) must bail. Give the runtime a beat
    // for any stray POST to land — none should.
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(ci).toHaveValue('one\n');
  });

  // Guard rail: paste of multi-line text must not auto-submit even though
  // the textarea now ends in `\n`. The fallback's denylist explicitly
  // includes `insertFromPaste` (alongside other paste/drop/yank types).
  test('input fallback does NOT fire on paste with trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // Simulate a paste of "hello\n" — browser default inserts the value
      // and fires `input` with inputType='insertFromPaste'. The fallback
      // must reject this even though the value now ends in \n.
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromPaste',
        data: 'hello\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('hello\n');
  });

  // Guard rail: drop of multi-line text must not auto-submit. The fallback's
  // denylist explicitly includes `insertFromDrop`. Pairs with the paste test
  // — both share the `PASTE_DROP_INPUT_TYPES.has()` branch but are exercised
  // individually so a future edit that removes `insertFromDrop` from the Set
  // is caught.
  test('input fallback does NOT fire on insertFromDrop with trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'dropped\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromDrop',
        data: 'dropped\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('dropped\n');
  });

  // Guard rail: paste-as-quotation must not auto-submit. Same denylist
  // entry class as paste/drop/yank. Closes the per-entry coverage gap so
  // every member of PASTE_DROP_INPUT_TYPES has its own regression test.
  test('input fallback does NOT fire on insertFromPasteAsQuotation with trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'quoted\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromPasteAsQuotation',
        data: 'quoted\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('quoted\n');
  });

  // Guard rail: Emacs-style yank (kill-buffer paste) must not auto-submit.
  // Same denylist entry as paste/drop.
  test('input fallback does NOT fire on insertFromYank with trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'yanked\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromYank',
        data: 'yanked\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('yanked\n');
  });

  // Regression: dictation tools (Wispr Flow, Voice Control, third-party
  // keyboards) often inject text by mutating `.value` directly — no
  // `beforeinput` and no `input` event fires for the trailing `\n`. Neither
  // defense layer ever runs. The user then taps the on-screen send button.
  // Without sendCompose's own trailing-`\n` strip, the POST would be
  // `"text\n\r"` — Claude Code interprets the leading `\n` as Shift+Enter,
  // leaving the prompt drafted with a newline below it, NOT submitted.
  test('sendCompose strips trailing newline (dictation injects \\n via .value, then tap send)', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      // Simulate Wispr-style injection: programmatic .value set fires no
      // input/beforeinput events at all.
      el.value = 'send this prompt to the agent\n';
    });

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-send').tap();
    const req = await inputReq;
    // Trailing \n must be stripped before \r is appended. Otherwise the
    // POST is "text\n\r" and Claude Code drafts-without-submitting.
    expect(req.postData()).toBe('send this prompt to the agent\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Regression: iOS WebKit / Wispr Flow / Voice Control sometimes dispatch
  // the line-break-bearing `input` event with `inputType: ''` (empty string)
  // when WebKit can't classify the source. Old allowlist (`startsWith
  // ('insert')`) bailed; denylist passes empty inputType, so the fallback's
  // trailing-newline check is the actual gate.
  test('input fallback: empty inputType with trailing newline still sends', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: '',
        data: '\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Regression: a future inputType outside the `insert*` namespace entirely
  // (e.g. `'beforeBreak'`, or anything iOS 19+ ships). Denylist passes it;
  // trailing-newline check submits.
  test('input fallback: non-insert inputType with trailing newline still sends', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    await page.locator('#compose-input').fill('hello');

    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'beforeBreak',
        data: '\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    const req = await inputReq;
    expect(req.postData()).toBe('hello\r');
    await expect(page.locator('#compose-input')).toHaveValue('');
  });

  // Guard rail: backspacing into a multi-line draft leaves a trailing `\n`
  // that the user is editing — must NOT auto-submit. The denylist
  // explicitly excludes `delete*` inputTypes.
  test('input fallback does NOT fire on deleteContentBackward leaving trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      // User had "hello\nworld", deletes "world" → "hello\n". Fallback must
      // bail (user is editing, not sending).
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'deleteContentBackward',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('hello\n');
  });

  // Guard rail: undo/redo (history*) that lands a trailing `\n` (e.g. undoing
  // back to a draft state that previously ended in a newline) must NOT
  // auto-submit. The denylist's `startsWith('history')` branch.
  test('input fallback does NOT fire on historyUndo leaving trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'historyUndo',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('hello\n');
  });

  // Guard rail: format edits (bold/italic/etc.) that happen to leave a
  // trailing `\n` must NOT auto-submit. The denylist's `startsWith('format')`
  // branch. Real-world likelihood near-zero for a plain textarea, but the
  // branch should be locked in by a test.
  test('input fallback does NOT fire on formatBold leaving trailing newline', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    let posted = false;
    page.on('request', req => {
      if (req.url().includes('/input') && req.method() === 'POST') posted = true;
    });
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'hello\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'formatBold',
        cancelable: false,
        bubbles: true,
      }));
    });
    await page.waitForTimeout(200);
    expect(posted).toBe(false);
    await expect(page.locator('#compose-input')).toHaveValue('hello\n');
  });

  // Regression: when layer 2 (input fallback) correctly bails on a
  // denylisted event but leaves a trailing `\n` in the textarea, the user
  // may then tap Send manually — layer 3 (sendCompose strip) must catch
  // that `\n` before it reaches the POST. Without layer 3, the POST is
  // `"text\n\r"` and Claude Code drafts the prompt with an embedded newline
  // instead of submitting. Pairs with the dictation `.value`-injection test
  // (which exercises layer 3 via a no-event path) to lock in layer-3 as
  // both the dictation belt AND the post-deny safety net.
  test('layer 3 sendCompose strip catches trailing \\n after denylisted input event + tap send', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'iphone', 'compose bar is touch-gated');
    await login(page);

    // Step 1: simulate a denylisted input event (paste) that leaves a
    // trailing newline. Layer 2's input fallback bails (paste is denied),
    // so the `\n` stays in the textarea.
    await page.locator('#compose-input').evaluate((el: HTMLTextAreaElement) => {
      el.focus();
      el.value = 'pasted text\n';
      el.dispatchEvent(new InputEvent('input', {
        inputType: 'insertFromPaste',
        data: 'pasted text\n',
        cancelable: false,
        bubbles: true,
      }));
    });
    await expect(page.locator('#compose-input')).toHaveValue('pasted text\n');

    // Step 2: user taps send. Layer 3 in sendCompose must strip the trailing
    // `\n` so the POST is `"pasted text\r"` not `"pasted text\n\r"`.
    const inputReq = page.waitForRequest(req =>
      req.url().includes('/input') && req.method() === 'POST',
      { timeout: 3000 }
    );
    await page.locator('#compose-send').tap();
    const req = await inputReq;
    expect(req.postData()).toBe('pasted text\r');
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
