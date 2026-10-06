import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';
const GRANT = 'voter-audience-coffee-admin';

const closedRound = (name) => JSON.stringify({ apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name, namespace: 'voter' }, spec: {
  title: name, state: 'closed', questions: [
    { id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] },
  ],
}});

// /room holds no admin check. It opens the same watches any signed-in browser
// may ask for, carrying that browser's own token, and renders what Kubernetes
// returns -- so the only thing standing between a participant and the room's
// join code is RBAC. This test is what says that sentence is true: it drives a
// real enrolment through Room Pass and Dex, then checks the refusal is real and
// that no valid join code reached the page.
test('a participant is refused the operator page and never sees a join code', async ({ browser }) => {
  const name = `operator-test-${Date.now()}`;
  const displayName = `${name}-participant`;
  kube('create', '-f', '-', { input: closedRound(name) });

  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  try {
    await page.goto(`${APP}/`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    // The Room asks one question at the door; any answer will do here.
    await page.getByRole('radio', { name: 'Vue' }).check();
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

    // And the write behind those buttons refuses this identity on its own,
    // rather than relying on the page having hidden them. It is the same patch
    // the operator page sends, through krm-foyer's /k8s with this participant's
    // token; they hold get/list/watch on quizsessions and no patch, so this is
    // the API server talking.
    const refusal = await page.evaluate(async (roundName) => {
      const session = await (await fetch('/auth/session')).json();
      const res = await fetch(`/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions/${roundName}`, {
        method: 'PATCH',
        headers: { 'content-type': 'application/merge-patch+json', [session.csrfHeader]: session.csrfToken },
        body: JSON.stringify({ spec: { state: 'live' } }),
      });
      return { status: res.status, body: await res.json() };
    }, name);
    expect(refusal.status, `opening a round as a participant: ${JSON.stringify(refusal.body)}`).toBe(403);
    expect(refusal.body.reason).toBe('Forbidden');
    expect(refusal.body.message).toContain('cannot patch resource "quizsessions"');

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

// The operator's half, which the fixture can run since it gained an operator
// (dex.yaml's mockCallback under the github id, cluster-admin like the
// cluster's). Every action here is the browser's own write through /k8s.
test('the operator sees the join code, opens and closes a round, and grants the room the menu', async ({ browser }) => {
  const name = `operator-run-${Date.now()}`;
  kube('create', '-f', '-', { input: closedRound(name) });
  kube('delete', 'rolebinding', GRANT, '--ignore-not-found');

  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  try {
    // The operator's way in: the login screen with the GitHub connector, which
    // sends krm-foyer's login oidc.connector_id=github and comes back here. (A
    // signed-out visit to /room itself goes to the room's default, Room Pass.)
    await page.goto(`${APP}/login?next=%2Froom&connector=github`);
    await page.waitForURL(`${APP}/room`);
    const who = await page.evaluate(async () => (await (await fetch('/auth/whoami')).json()).userInfo.username);
    expect(who).toBe('github:kilgore@kilgore.trout');

    // The QR code, drawn from the Room's current code. What it encodes, the
    // /join-room URL, is join-and-logout.spec.js's to follow.
    await expect(page.locator('.room-qr__code svg')).toBeVisible();

    const card = page.locator('article', { has: page.getByRole('heading', { name, exact: true }) });
    await card.getByRole('button', { name: 'Open', exact: true }).click();
    await expect.poll(() => JSON.parse(kube('get', 'quizsession', name, '-o', 'json')).spec.state).toBe('live');
    await card.getByRole('button', { name: 'Close', exact: true }).click();
    await expect.poll(() => JSON.parse(kube('get', 'quizsession', name, '-o', 'json')).spec.state).toBe('closed');

    // The grant: a RoleBinding the page creates and deletes, naming the Room's
    // audience group, labelled as the app's and kept out of Git.
    const grant = page.getByRole('checkbox', { name: /Let the room edit the coffee menu/ });
    await expect(grant).toBeEnabled();
    await expect(grant).not.toBeChecked();
    await grant.check();
    await expect.poll(() => kube('get', 'rolebinding', GRANT, '--ignore-not-found', '-o', 'name').trim()).toBe(`rolebinding.rbac.authorization.k8s.io/${GRANT}`);
    const binding = JSON.parse(kube('get', 'rolebinding', GRANT, '-o', 'json'));
    expect(binding.subjects).toEqual([{ kind: 'Group', apiGroup: 'rbac.authorization.k8s.io', name: 'demo:voter-audience' }]);
    expect(binding.metadata.labels['voter.configbutler.ai/grant']).toBe('coffee-admin');
    await grant.uncheck();
    await expect.poll(() => kube('get', 'rolebinding', GRANT, '--ignore-not-found', '-o', 'name').trim()).toBe('');
  } finally {
    await context.close();
    kube('delete', 'quizsession', name, '--ignore-not-found');
    kube('delete', 'rolebinding', GRANT, '--ignore-not-found');
  }
});
