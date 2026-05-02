import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
}

test.describe('settings tab', () => {
  test('settings tab is visible and shows token + push sections', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.getByRole('heading', { name: 'API tokens' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Notifications' })).toBeVisible();
  });

  test('mint a device token via UI and reveal it', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await page.locator('#new-token-label').fill('test-iphone');
    await page.locator('button[onclick="mintToken()"]').click();
    await expect(page.locator('#token-mint-result')).toBeVisible();
    await expect(page.locator('#token-mint-result')).toContainText('Save this token now');
    // Token should be 64 hex chars somewhere in the result.
    const text = await page.locator('#token-mint-result').textContent();
    expect(text).toMatch(/[0-9a-f]{64}/);
    // Listed token row appears with last4 of the same token.
    await expect(page.locator('#token-list')).toContainText('test-iphone');
  });

  test('iOS share section pre-fills the Shortcut URL with current origin', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await expect(page.getByRole('heading', { name: 'iOS share sheet' })).toBeVisible();
    const input = page.locator('#ios-share-url');
    await expect(input).toBeVisible();
    const value = await input.inputValue();
    // URL must contain origin + /share?text=[Shortcut Input] placeholder.
    const origin = await page.evaluate(() => window.location.origin);
    expect(value).toBe(`${origin}/share?text=[Shortcut Input]`);
    // readonly so the user can't edit it but can still select+copy.
    await expect(input).toHaveAttribute('readonly', '');
  });

  test('iOS share help (?) icon toggles instructions including Shortcut Input variable', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    const help = page.locator('#ios-share-help');
    const toggle = page.locator('#ios-share-help-toggle');
    // Hidden by default.
    await expect(help).toBeHidden();
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    // Click reveals; instructions mention the Shortcut Input magic variable.
    await toggle.click();
    await expect(help).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(help).toContainText('Shortcut Input');
    await expect(help).toContainText('Show in Share Sheet');
    // Click again hides.
    await toggle.click();
    await expect(help).toBeHidden();
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });

  test('revoke a token from settings UI', async ({ page }) => {
    await login(page);
    await page.locator('.tab[data-tab="settings"]').click();
    await page.locator('#new-token-label').fill('soon-revoked');
    await page.locator('button[onclick="mintToken()"]').click();
    await expect(page.locator('#token-list')).toContainText('soon-revoked');

    page.on('dialog', d => d.accept());
    await page.locator('#token-list button.danger:has-text("Revoke")').first().click();
    await expect(page.locator('#token-list')).toContainText('revoked', { timeout: 3000 });
  });
});

test.describe('list toolbar', () => {
  test('Active and Archived segments switch the list', async ({ page }) => {
    await login(page);
    // Click the seed task → archive it.
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept());
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-archive').click();
    await expect(page.locator('#tasks-view')).toBeVisible();
    // Active list is empty.
    await expect(page.locator('.task-item')).toHaveCount(0);
    // Archived list has the task.
    await page.locator('.list-toolbar .seg[data-filter="archived"]').click();
    await expect(page.locator('.task-item')).toHaveCount(1);
  });
});

test.describe('detail-view actions', () => {
  test('rename updates title', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept('renamed-via-ui'));
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-rename').click();
    await expect(page.locator('#detail-title')).toHaveText('renamed-via-ui', { timeout: 3000 });
  });

  test('fork creates a new task and opens it', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    page.on('dialog', d => d.accept('forked-via-ui'));
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-fork').click();
    await expect(page.locator('#detail-title')).toHaveText('forked-via-ui', { timeout: 5000 });
  });

  test('overflow menu closes when navigating Back', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu')).toHaveClass(/open/);
    // `.detail-back` matches both #detail-view's and #files-view's back link;
    // scope to detail-view's so strict-mode resolves to a single element.
    await page.locator('#detail-view .detail-back').click();
    // After back, detail-view is dismissed and overflow-menu is gone from DOM
    // (innerHTML rebuild on next openDetail), or at minimum no longer .open.
    await expect(page.locator('#tasks-view')).toBeVisible();
    await expect(page.locator('#overflow-menu.open')).toHaveCount(0);
  });

  test('overflow menu closes when switching tabs', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await expect(page.locator('#overflow-menu')).toHaveClass(/open/);
    // #detail-view.open is fixed/z-index:50 and covers the tab bar — a real
    // pointer click on `.tab[data-tab="settings"]` is intercepted by
    // detail-subtitle (by design, the user must close detail before
    // switching tabs). Drive the production handler directly so the test
    // exercises the menu-close behavior, not the layered click path.
    await page.evaluate(() => (window as any).switchTab('settings'));
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.locator('#overflow-menu.open')).toHaveCount(0);
  });

  test('view prompt opens modal with the seeded prompt text', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-prompt').click();

    // Modal becomes visible, body shows the seeded prompt verbatim.
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);
    await expect(page.locator('#prompt-modal-body')).toHaveText(
      'Investigate flaky CI runs and add retry logic.',
    );
    await expect(page.locator('#prompt-modal-body.empty')).toHaveCount(0);

    // Close button hides it.
    await page.locator('#prompt-modal button.primary').click();
    await expect(page.locator('#prompt-modal.open')).toHaveCount(0);
  });

  test('view prompt body is set via textContent (no HTML injection)', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();

    // Mutate the in-memory currentTask to contain HTML, then re-open the modal
    // — verifying that openPromptModal renders the angle brackets verbatim
    // (textContent), not as live DOM. A regression to innerHTML would render
    // the <img> and execute the onerror handler.
    await page.evaluate(() => {
      (window as any).currentTask.prompt = '<img src=x onerror="window.__pwn=1">';
      (window as any).openPromptModal();
    });
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);
    await expect(page.locator('#prompt-modal-body img')).toHaveCount(0);
    const pwn = await page.evaluate(() => (window as any).__pwn);
    expect(pwn).toBeUndefined();
    await expect(page.locator('#prompt-modal-body')).toContainText('<img');
  });

  test('prompt modal copy button writes prompt body to clipboard', async ({
    page,
    context,
  }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-prompt').click();
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);

    // Copy button sits to the left of Close inside .modal-actions.
    const actions = page.locator('#prompt-modal .modal-actions button');
    await expect(actions).toHaveCount(2);
    await expect(actions.nth(0)).toHaveText('Copy');
    await expect(actions.nth(1)).toHaveText('Close');

    await actions.nth(0).click();
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toBe('Investigate flaky CI runs and add retry logic.');

    // Modal stays open after copy — only Close dismisses it.
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);
  });

  test('view prompt placeholder when prompt is empty', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();
    await page.evaluate(() => {
      (window as any).currentTask.prompt = '';
      (window as any).openPromptModal();
    });
    await expect(page.locator('#prompt-modal-body.empty')).toBeVisible();
    await expect(page.locator('#prompt-modal-body')).toContainText('no prompt');
  });

  test('prompt modal copy skips empty-prompt placeholder', async ({
    page,
    context,
  }) => {
    // Pre-seed the clipboard with a sentinel; the empty-prompt copy path
    // must not overwrite it.
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();
    await page.evaluate(async () => {
      await navigator.clipboard.writeText('SENTINEL');
      (window as any).currentTask.prompt = '';
      (window as any).openPromptModal();
    });
    await expect(page.locator('#prompt-modal-body.empty')).toBeVisible();

    // Click Copy — guard should short-circuit; clipboard must be unchanged.
    await page.locator('#prompt-modal .modal-actions button').nth(0).click();
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toBe('SENTINEL');
  });

  test('prompt modal closes when detail view closes', async ({ page }) => {
    // Regression: closeDetail() must call closePromptModal() so the modal
    // doesn't stack over the task list after backing out. Drive closeDetail
    // directly because the modal overlay (z-index 300) covers the back link
    // when open — the same reason the existing "switching tabs" test below
    // calls switchTab() rather than clicking the tab.
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-prompt').click();
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);

    await page.evaluate(() => (window as any).closeDetail());
    await expect(page.locator('#tasks-view')).toBeVisible();
    await expect(page.locator('#prompt-modal.open')).toHaveCount(0);
  });

  test('prompt modal closes when switching tabs', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-prompt').click();
    await expect(page.locator('#prompt-modal')).toHaveClass(/open/);

    await page.evaluate(() => (window as any).switchTab('settings'));
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.locator('#prompt-modal.open')).toHaveCount(0);
  });

  test('input history modal lists original prompt and submitted lines', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();

    // Drive sendInputBytes the same way the terminal does — keystrokes plus
    // CR. Mixes plain text, an arrow-key ESC sequence (must be filtered),
    // a backspace (must pop), and bracket-paste markers (markers stripped,
    // inner content kept).
    await page.evaluate(() => {
      const send = (window as any).sendInputBytes;
      send('hello');
      send('\x1b[A');           // up-arrow — filtered
      send(' world');
      send('X\x7f');             // backspace — pops X
      send('\r');                // flush -> "hello world"
      send('\x1b[200~pasted line\x1b[201~\r'); // bracket paste -> "pasted line"
    });

    await page.locator('#btn-overflow').click();
    await page.locator('#btn-inputs').click();
    await expect(page.locator('#inputs-modal')).toHaveClass(/open/);

    // First entry is the seeded original prompt; subsequent entries are the
    // two submitted lines, in order.
    const bodies = page.locator('#inputs-modal-body .prompt-body');
    await expect(bodies).toHaveCount(3);
    await expect(bodies.nth(0)).toHaveText(
      'Investigate flaky CI runs and add retry logic.',
    );
    await expect(bodies.nth(1)).toHaveText('hello world');
    await expect(bodies.nth(2)).toHaveText('pasted line');

    // Original-prompt entry uses the literal label; submitted entries get
    // a timestamp label (any non-empty string).
    const labels = page.locator('#inputs-modal-body .inputs-label');
    await expect(labels.nth(0)).toHaveText('Original prompt');
    await expect(labels.nth(1)).not.toHaveText('');

    await page.locator('#inputs-modal button.primary').click();
    await expect(page.locator('#inputs-modal.open')).toHaveCount(0);
  });

  test('input history bodies are set via textContent (no HTML injection)', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();

    // Inject HTML into an entry, then re-render via the production opener.
    // A regression to innerHTML would mount the <img> and fire onerror.
    await page.evaluate(() => {
      const send = (window as any).sendInputBytes;
      send('<img src=x onerror="window.__pwn=1">');
      send('\r');
      (window as any).openInputsModal();
    });
    await expect(page.locator('#inputs-modal')).toHaveClass(/open/);
    await expect(page.locator('#inputs-modal-body img')).toHaveCount(0);
    const pwn = await page.evaluate(() => (window as any).__pwn);
    expect(pwn).toBeUndefined();
    await expect(page.locator('#inputs-modal-body')).toContainText('<img');
  });

  test('input history shows placeholder when nothing has been typed', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();

    // Empty original prompt + clean history -> only the placeholder renders.
    await page.evaluate(() => {
      (window as any).currentTask.prompt = '';
      const id = (window as any).currentTask.id;
      localStorage.removeItem('argus.inputs.v1.' + id);
      (window as any).openInputsModal();
    });
    await expect(page.locator('#inputs-modal-body .prompt-body.empty')).toBeVisible();
    await expect(page.locator('#inputs-modal-body')).toContainText('no inputs yet');
  });

  test('input history Clear wipes persisted entries', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await expect(page.locator('#detail-view.open')).toBeVisible();

    await page.evaluate(() => {
      const send = (window as any).sendInputBytes;
      send('first\r');
      send('second\r');
      // Clear the original prompt so only the two submissions remain — that
      // way the Clear button leaves the modal in the empty-placeholder
      // state and we don't have to depend on it staying open.
      (window as any).currentTask.prompt = '';
      (window as any).openInputsModal();
    });
    await expect(page.locator('#inputs-modal-body .prompt-body')).toHaveCount(2);

    await page.locator('#inputs-modal-clear').click();
    await expect(page.locator('#inputs-modal-body .prompt-body.empty')).toBeVisible();

    // localStorage entry is gone too — survives a re-open.
    const remaining = await page.evaluate(() => {
      const id = (window as any).currentTask.id;
      return localStorage.getItem('argus.inputs.v1.' + id);
    });
    expect(remaining).toBeNull();
  });

  test('input history modal closes when detail view closes', async ({ page }) => {
    // Same teardown contract as the prompt modal: closeDetail() must call
    // closeInputsModal() so the overlay doesn't stack over the task list
    // after backing out.
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-inputs').click();
    await expect(page.locator('#inputs-modal')).toHaveClass(/open/);

    await page.evaluate(() => (window as any).closeDetail());
    await expect(page.locator('#tasks-view')).toBeVisible();
    await expect(page.locator('#inputs-modal.open')).toHaveCount(0);
  });

  test('input history modal closes when switching tabs', async ({ page }) => {
    await login(page);
    await page.locator('.task-item').first().click();
    await page.locator('#btn-overflow').click();
    await page.locator('#btn-inputs').click();
    await expect(page.locator('#inputs-modal')).toHaveClass(/open/);

    await page.evaluate(() => (window as any).switchTab('settings'));
    await expect(page.locator('#settings-view')).toBeVisible();
    await expect(page.locator('#inputs-modal.open')).toHaveCount(0);
  });
});
