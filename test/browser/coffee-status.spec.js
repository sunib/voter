import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8' });
const APP = 'https://app.voter.test:19443';
const menu = () => JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json'));
const condition = (type) => menu().status?.conditions?.find((c) => c.type === type);
const setLimit = (vouchers, limit) => kube('patch', 'coffeeconfig', 'demo-coffee', '--type=merge', '-p', JSON.stringify({
  spec: { vouchers: vouchers.map((v) => (v.code === 'TESTNET' ? { ...v, maximumUsage: limit } : v)) },
}));

// The menu's status (docs/coffee-status-design.md), told in the order the talk tells
// it: open and untouched, the first order, a voucher that runs out before anyone is
// refused, somebody raising its limit, and Voter having looked. All of it while
// somebody has an unsaved edit open, whose save must still land.
test('the menu says how it is, live, without disturbing whoever is editing it', async ({ browser }) => {
  // A rollout and the edge catching up come out of this test's budget.
  test.setTimeout(180000);
  const displayName = `status-${Date.now()}`;
  const before = menu().spec;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  try {
    // What an earlier run left behind. A start clears it, along with every
    // redemption, so a restart is a clean slate for the next talk.
    setLimit(before.vouchers, 1);
    kube('patch', 'coffeeconfig', 'demo-coffee', '--subresource=status', '--type=merge', '-p', '{"status":{"firstOrderTime":"2026-11-05T08:00:00Z"}}');
    kube('rollout', 'restart', 'deployment/voter');
    kube('rollout', 'status', 'deployment/voter', '--timeout=120s');
    await expect.poll(() => menu().status?.firstOrderTime).toBeUndefined();
    await expect.poll(() => condition('Ready')?.status).toBe('True');
    await expect.poll(() => menu().status.observedGeneration).toBe(menu().metadata.generation);

    kube('create', 'rolebinding', 'voter-audience-coffee-admin', '--role=voter-audience-coffee-admin', '--group=demo:voter-audience');
    // For a moment after a restart the edge still has the old pod among its servers,
    // so /admin can answer 200 and its script 502, which is a blank page and no retry.
    // Only the form itself says the page works. Short tries, so that a hung one does
    // not use up the test.
    await expect(async () => {
      await page.goto(`${APP}/admin`, { timeout: 5000 });
      await expect(page.getByLabel('Room code')).toBeVisible({ timeout: 5000 });
    }).toPass({ timeout: 120000 });
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/admin`);

    const panel = page.getByRole('article', { name: 'Menu status' });
    const shopField = page.locator('label', { hasText: 'Shop name' }).locator('input');
    const summary = page.locator('.save-summary');
    const order = (voucherCode) => page.evaluate(async (voucherCode) => {
      const session = await (await fetch('/auth/session')).json();
      const res = await fetch('/public/orders', {
        method: 'POST',
        headers: { 'content-type': 'application/json', [session.csrfHeader]: session.csrfToken },
        body: JSON.stringify({ voucherCode, items: [{ sku: 'coffee-espresso', quantity: 1 }] }),
      });
      return await res.json();
    }, voucherCode);

    await expect(panel.getByText('Ready', { exact: true })).toBeVisible();
    await expect(panel).toContainText('None yet');
    await expect(shopField).toBeEditable();
    await shopField.fill(`${before.shopName} edited`);

    // The first order, and the last of the voucher's allowance: the status knows
    // before anybody has been refused.
    expect((await order('TESTNET')).status).toBe('placed');
    await expect(panel.getByText('Not ready', { exact: true })).toBeVisible();
    await expect(panel).toContainText('Voucher TESTNET has reached its maximumUsage of 1.');
    await expect(panel).toContainText('VoucherDepleted');
    await expect(panel).not.toContainText('None yet');
    await expect(shopField).toHaveValue(`${before.shopName} edited`);

    const ready = condition('Ready');
    expect(ready).toMatchObject({ status: 'False', reason: 'VoucherDepleted' });
    const firstOrder = menu().status.firstOrderTime;
    expect(firstOrder).toBeTruthy();
    expect(JSON.stringify(menu().status)).not.toMatch(/used[^U]/i);

    // The next person walks into it, and a refused order is no first order.
    expect((await order('TESTNET')).status).toBe('rejected');
    expect((await order('')).status).toBe('placed');
    await page.waitForTimeout(2000);
    expect(menu().status.firstOrderTime).toBe(firstOrder);

    // Somebody raises the limit: Voter looks at the new version, and it is ready.
    const edited = menu().metadata.generation;
    setLimit(before.vouchers, 5);
    await expect.poll(() => menu().status.observedGeneration).toBe(edited + 1);
    await expect(panel.getByText('Ready', { exact: true })).toBeVisible();
    expect(condition('Ready').reason).toBe('ShopReady');

    // kubectl says the same, for the stage.
    const row = kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'wide');
    for (const column of ['READY', 'REASON', 'GEN', 'OBSERVED', 'FIRST ORDER', 'MESSAGE', 'ShopReady']) expect(row).toContain(column);

    // The edit is still there through all of those writes, and it lands.
    await expect(shopField).toHaveValue(`${before.shopName} edited`);
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect.poll(() => menu().spec.shopName).toBe(`${before.shopName} edited`);
    await expect(summary).toContainText('No pending changes');
    await expect(summary.getByRole('alert')).toHaveCount(0);
    await expect.poll(() => menu().status.observedGeneration).toBe(menu().metadata.generation);
  } finally {
    await context.close().catch(() => {});
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    kube('patch', 'coffeeconfig', 'demo-coffee', '--type=merge', '-p', JSON.stringify({ spec: { shopName: before.shopName, vouchers: before.vouchers } }));
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
