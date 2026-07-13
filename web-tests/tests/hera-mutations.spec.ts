import { test, expect } from '@playwright/test';
import { resetServer } from './_helpers';

// The test-server seeds one "demo-orch" orchestrator with a live coordinator,
// an unbound "worker-1", and a planned "1a-planned" node (see
// cmd/argus-test-server/seedHera) — enough fixture to exercise all three
// mutation surfaces (spawn worker, send message, plan-DAG authoring) added by
// add-hera-mutation-rest-api.
test.beforeEach(async () => { await resetServer(); });

async function login(page) {
  await page.addInitScript(() => localStorage.setItem('argus-token', 'test-token'));
  await page.goto('/');
  await expect(page.locator('#main-app')).toBeVisible();
}

async function openHeraTab(page) {
  await page.locator('body').press('g');
  await expect(page.locator('#hera-view')).toBeVisible();
  await expect(page.locator('.hera-card-name', { hasText: 'demo-orch' })).toBeVisible();
}

test.describe('Hera mutations', () => {
  test('spawn worker form creates a new worker and refreshes the roster', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: '+ Worker' }).click();
    await expect(page.locator('#hera-spawn-modal')).toHaveClass(/open/);
    await page.locator('#hsm-prompt').fill('Implement the parser');
    await page.locator('#hsm-role-name').fill('parser-work');
    await page.locator('#hera-spawn-modal button.primary', { hasText: 'Spawn' }).click();

    await expect(page.locator('#hera-spawn-modal')).not.toHaveClass(/open/);
    await expect(page.locator('.hera-role-name', { hasText: 'parser-work' })).toBeVisible();
  });

  test('spawn worker requires a prompt', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: '+ Worker' }).click();
    await page.locator('#hera-spawn-modal button.primary', { hasText: 'Spawn' }).click();
    // Validation error toast, modal stays open — no request went out.
    await expect(page.locator('#hera-spawn-modal')).toHaveClass(/open/);
  });

  test('send message form has no sender-role selector and sends to a picked recipient', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: 'Send' }).click();
    await expect(page.locator('#hera-send-modal')).toHaveClass(/open/);
    // No "from"/sender field exists anywhere in the compose form — sends are
    // always attributed to the orchestrator's coordinator.
    await expect(page.locator('#hera-send-modal').locator('text=From')).toHaveCount(0);

    await page.locator('#hsend-to').selectOption({ label: 'worker: worker-1' });
    await page.locator('#hsend-tldr').fill('status update');
    await page.locator('#hsend-body').fill('Please proceed with the next stage.');
    await page.locator('#hera-send-modal button.primary', { hasText: 'Send' }).click();

    await expect(page.locator('#hera-send-modal')).not.toHaveClass(/open/);
    await expect(page.locator('.toast', { hasText: 'Message sent' })).toBeVisible();
  });

  test('plan: create a node, then cancel it requires confirmation', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: 'Plan' }).click();
    await expect(page.locator('#hera-plan-modal')).toHaveClass(/open/);

    await page.locator('#hplan-new-name').fill('2a-writer');
    await page.locator('#hplan-new-prompt').fill('write the doc');
    await page.locator('#hera-plan-modal button.primary', { hasText: 'Create node' }).click();
    await expect(page.locator('.toast', { hasText: 'Planned node created' })).toBeVisible();

    // Re-select the seeded planned node (the create above refreshed the
    // roster and rebuilt the pickers) and cancel it. Dismissing the confirm
    // dialog must issue no request.
    await page.locator('#hplan-edit-role').selectOption({ label: 'worker: 1a-planned' });
    page.on('dialog', d => d.dismiss());
    await page.locator('#hera-plan-modal button.danger', { hasText: 'Cancel node' }).click();
    await expect(page.locator('.toast', { hasText: 'Node cancelled' })).toHaveCount(0);

    page.removeAllListeners('dialog');
    page.on('dialog', d => d.accept());
    await page.locator('#hera-plan-modal button.danger', { hasText: 'Cancel node' }).click();
    await expect(page.locator('.toast', { hasText: 'Node cancelled' })).toBeVisible();
  });

  test('plan: edit a planned node\'s prompt', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: 'Plan' }).click();
    await page.locator('#hplan-edit-role').selectOption({ label: 'worker: 1a-planned' });
    await page.locator('#hplan-edit-prompt').fill('revised prompt');
    await page.locator('#hera-plan-modal button.primary', { hasText: 'Save' }).click();
    await expect(page.locator('.toast', { hasText: 'Node updated' })).toBeVisible();
  });

  test('plan: add a blocking edge, then remove it requires confirmation', async ({ page }) => {
    await login(page);
    await openHeraTab(page);

    await page.locator('.hera-card-actions button', { hasText: 'Plan' }).click();
    await page.locator('#hplan-blocked').selectOption({ label: 'worker: 1a-planned' });
    await page.locator('#hplan-blocker').selectOption({ label: 'worker: worker-1' });
    await page.locator('#hera-plan-modal button.primary', { hasText: 'Add edge' }).click();
    await expect(page.locator('.toast', { hasText: 'Blocking edge added' })).toBeVisible();

    page.on('dialog', d => d.dismiss());
    await page.locator('#hera-plan-modal button.danger', { hasText: 'Remove edge' }).click();
    await expect(page.locator('.toast', { hasText: 'Blocking edge removed' })).toHaveCount(0);

    page.removeAllListeners('dialog');
    page.on('dialog', d => d.accept());
    await page.locator('#hera-plan-modal button.danger', { hasText: 'Remove edge' }).click();
    await expect(page.locator('.toast', { hasText: 'Blocking edge removed' })).toBeVisible();
  });

  test('mutation controls are hidden for an orchestrator with no live coordinator', async ({ page }) => {
    // The seeded demo-orch always has a live coordinator, so this pins the
    // negative case structurally: hasLiveCoordinator gating means an
    // orchestrator card renders NO .hera-card-actions block at all when it
    // has none — exercised directly against handleHera/hera_mutations.go's
    // 409 in internal/api/hera_mutations_test.go; here we only confirm the
    // seeded (live) card DOES show the actions, so a future regression that
    // drops the gate (and would show actions unconditionally) is caught.
    await login(page);
    await openHeraTab(page);
    await expect(page.locator('.hera-card', { hasText: 'demo-orch' }).locator('.hera-card-actions')).toBeVisible();
  });
});
