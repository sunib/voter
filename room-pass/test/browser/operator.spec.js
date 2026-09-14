import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'room-pass', ...args], { encoding: 'utf8' });
const APP = 'https://app.roompass.test:18443';

// /room holds no admin check. It opens the same watches any signed-in browser
// may ask for, carrying that browser's own token, and renders what Kubernetes
// returns -- so the only thing standing between a participant and the room's
// join code is RBAC. This test is what says that sentence is true: it drives a
// real enrolment through Room Pass and Dex, then checks the refusal is real and
// that no valid join code reached the page.
test('a participant is refused the operator page and never sees a join code', async ({ browser }) => {
  const name = `operator-test-${Date.now()}`;
  const displayName = `${name}-participant`;
  const round = { apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name, namespace: 'room-pass' }, spec: {
    title: name, state: 'closed', questions: [
      { id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] },
    ],
  }};
  execFileSync('kubectl', ['--kubeconfig', kubeconfig, 'create', '-f', '-'], { input: JSON.stringify(round) });

  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  try {
    await page.goto(`${APP}/`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/`);

    // The session works: this identity can see the round it is allowed to see.
    await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();

    await page.goto(`${APP}/room`);
    await expect(page.getByRole('heading', { name: 'You cannot run this room' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Sign in with GitHub' })).toBeVisible();

    // The refusal is not cosmetic: nothing that could be scanned is on the page.
    // Every currently valid code is checked, not just the newest, because the
    // room keeps a window of them and any one of them would let somebody join.
    const status = JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status;
    const codes = (status.validJoinCodes ?? []).map(c => c.code).concat(status.joinCode?.code ?? []);
    expect(codes.length).toBeGreaterThan(0);
    const body = await page.locator('body').innerText();
    const html = await page.content();
    for (const code of codes) {
      expect(body, `join code ${code} rendered on the page`).not.toContain(code);
      expect(html, `join code ${code} present in the markup`).not.toContain(code);
    }
    await expect(page.locator('.room-qr__code')).toHaveCount(0);

    // No round controls either, so there is nothing to click by mistake.
    await expect(page.getByRole('button', { name: 'Open', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Close', exact: true })).toHaveCount(0);

    // And the endpoint behind those buttons refuses this identity on its own,
    // rather than relying on the page having hidden them. A participant holds
    // get/list/watch on quizsessions and no patch, so this is the API server
    // talking, not the application.
    const refusal = await page.evaluate(async (roundName) => {
      const session = await (await fetch('/auth/session', { credentials: 'include' })).json();
      const res = await fetch(`/public/rounds/${roundName}/state`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'content-type': 'application/json', 'x-csrf-token': session.csrfToken },
        body: JSON.stringify({ state: 'live' }),
      });
      return { status: res.status, body: await res.text() };
    }, name);
    expect(refusal.status, `opening a round as a participant: ${refusal.body}`).toBe(403);
    expect(refusal.body).toContain('forbidden');

    // The round really did not move.
    const after = JSON.parse(kube('get', 'quizsession', name, '-o', 'json'));
    expect(after.spec.state).toBe('closed');
  } finally {
    await context.close();
    kube('delete', 'quizsession', name, '--ignore-not-found');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
