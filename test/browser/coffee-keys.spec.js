import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';
const b64 = (s) => Buffer.from(s).toString('base64');

// The menu's mail and payments keys (docs/coffee-api-keys-design.md).
//
// Whoever holds voter-audience-coffee-keys may patch coffee-mail and
// coffee-payments, and nothing else: no reading them, no other Secret.
// voter-coffee-keys decides what a patch may change -- the value, to something
// new, and nothing around it. There is no rule on the value itself, so any
// length will do. Sent by hand through /k8s, as a terminal would; there is no
// screen for it yet.
test('the key holder can set a new key, and nothing else', async ({ browser }) => {
  const displayName = `keys-${Date.now()}`;
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  try {
    await page.goto(`${APP}/`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/`);

    const secret = (name, method, body, query = '') => page.evaluate(async ({ name, method, body, query }) => {
      const s = await (await fetch('/auth/session')).json();
      const res = await fetch(`/k8s/api/v1/namespaces/voter/secrets/${name}${query}`, {
        method, headers: { 'content-type': 'application/merge-patch+json', [s.csrfHeader]: s.csrfToken },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const json = await res.json();
      return { status: res.status, message: json.message ?? '', data: json.kind === 'Secret' ? json.data : undefined };
    }, { name, method, body, query });
    const setMail = (apiKey) => secret('coffee-mail', 'PATCH', { stringData: { apiKey } });

    // Without the grant, RBAC says no before admission is asked.
    expect((await setMail('taart')).status).toBe(403);

    // The test stands in for the operator, who binds it to one person from /room.
    kube('create', 'rolebinding', 'voter-audience-coffee-keys', '--role=voter-audience-coffee-keys', '--group=demo:voter-audience');
    await expect.poll(async () => (await setMail('taart')).status).toBe(200);

    // The response carries the whole object, which is only what was just sent.
    const again = await setMail('koek');
    expect(again.status).toBe(200);
    expect(again.data).toEqual({ apiKey: b64('koek') });

    // Sending the stored value, or nothing, would answer with the previous
    // person's key: refused, and so is a dry run, which would test a guess.
    const same = await setMail('koek');
    expect(same.status).toBe(422);
    expect(same.message).toContain('That is the mail key already set: saving it has to change it.');
    expect((await secret('coffee-mail', 'PATCH', {})).status).toBe(422);
    const dryRun = await secret('coffee-mail', 'PATCH', { stringData: { apiKey: 'guess' } }, '?dryRun=All');
    expect(dryRun.status).toBe(403);
    expect(dryRun.message).toContain('A new mail key is set for real, not as a dry run.');

    // Any length: one character, or something the size of a real key.
    expect((await setMail('x')).status).toBe(200);
    expect((await setMail(`SG.${'a'.repeat(22)}.${'b'.repeat(43)}`)).status).toBe(200);

    // One entry, and nothing but the value.
    const second = await secret('coffee-mail', 'PATCH', { stringData: { other: 'x' } });
    expect(second.status).toBe(422);
    expect(second.message).toContain('The mail key holds one entry, apiKey, and nothing else.');
    // Each with a new value too, so that is not what refuses it.
    for (const sneaky of [{ metadata: { annotations: { note: 'x' } } }, { metadata: { labels: { sneaky: 'yes' } } }, { immutable: true }]) {
      const refused = await secret('coffee-mail', 'PATCH', { stringData: { apiKey: 'other' }, ...sneaky });
      expect(refused.status).toBe(403);
      expect(refused.message).toContain('Only the value of the mail key can be changed here');
    }

    // The payments key is its own Secret, under the same grant.
    expect((await secret('coffee-payments', 'PATCH', { stringData: { apiKey: 'bon1' } })).status).toBe(200);

    // Never read, and no other Secret.
    expect((await secret('coffee-mail', 'GET')).status).toBe(403);
    const other = await secret('krm-foyer-session-keys', 'PATCH', { stringData: { x: 'y' } });
    expect(other.status).toBe(403);
    expect(other.message).toContain('cannot patch resource "secrets"');

    const stored = JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json'));
    expect(stored.data).toEqual({ apiKey: b64(`SG.${'a'.repeat(22)}.${'b'.repeat(43)}`) });
    expect(stored.metadata.labels).toEqual({ 'voter.configbutler.ai/coffee-settings': 'true' });
    expect(stored.immutable).toBeUndefined();
  } finally {
    await context.close();
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys', '--ignore-not-found');
    // As the fixture's admin, a system: user the policy leaves alone.
    for (const name of ['coffee-mail', 'coffee-payments']) kube('patch', 'secret', name, '--type=merge', '-p', '{"data":{"apiKey":""}}');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});

// Rules about the data, so they hold for everyone, the fixture's admin
// included. Server-side dry runs: through admission, and nothing is stored.
test('a key reference names its own Secret, and only those two Secrets are published', () => {
  const dryRun = (object) => kube('create', '-f', '-', '--dry-run=server', { input: JSON.stringify(object), stdio: 'pipe' });
  const menu = (extra) => ({
    apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'CoffeeConfig',
    metadata: { name: `keys-probe-${Date.now()}`, namespace: 'voter' },
    spec: { shopName: 'Probe', currency: 'EUR', products: [{ sku: 'espresso', name: 'Espresso', priceCents: 250 }], ...extra },
  });

  // The CRD pins each reference to its one Secret.
  expect(() => dryRun(menu({ mail: { provider: 'sendgrid', apiKeySecretRef: { name: 'k8s-trail-git', key: 'ssh-privatekey' } } })))
    .toThrow(/spec\.mail\.apiKeySecretRef\.name: Unsupported value: "k8s-trail-git": supported values: "coffee-mail"/);
  expect(() => dryRun(menu({ mail: { provider: 'sendgrid', apiKeySecretRef: { name: 'coffee-payments', key: 'apiKey' } } })))
    .toThrow(/Unsupported value: "coffee-payments": supported values: "coffee-mail"/);
  expect(() => dryRun(menu({ payments: { provider: 'stripe' } })))
    .toThrow(/Payments need a provider and its key reference together, or neither/);
  expect(dryRun(menu({
    mail: { provider: 'sendgrid', apiKeySecretRef: { name: 'coffee-mail', key: 'apiKey' } },
    payments: { provider: 'stripe', apiKeySecretRef: { name: 'coffee-payments', key: 'apiKey' } },
  }))).toContain('created (server dry run)');

  // The label that publishes a Secret to coffee-settings, on any other Secret.
  expect(() => dryRun({ apiVersion: 'v1', kind: 'Secret', metadata: { name: 'coffee-settings-age', namespace: 'voter', labels: { 'voter.configbutler.ai/coffee-settings': 'true' } }, data: {} }))
    .toThrow(/coffee-settings-age cannot carry voter\.configbutler\.ai\/coffee-settings/);
});

// Demo beat 2 from the room's side: the operator hands the keys to one person
// on /room, that person sets a new key from the menu editor, as a change saved
// with the rest, and the operator takes the grant back. Nobody reloads
// anything: both pages follow the bindings. Without the grant the buttons are
// there but greyed out, and "Try anyway" gets Kubernetes' own refusal.
test('the operator gives one person the keys from /room, and takes them back', async ({ browser }) => {
  const displayName = `keys-holder-${Date.now()}`;
  const contexts = [];
  const open = async () => {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    contexts.push(context);
    const page = await context.newPage();
    page.on('pageerror', error => { throw error; });
    return page;
  };
  try {
    const holder = await open();
    await holder.goto(`${APP}/`);
    await holder.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await holder.getByLabel('Display name').fill(displayName);
    await holder.getByRole('radio', { name: 'Vue' }).check();
    await holder.getByRole('button', { name: 'Continue', exact: true }).click();
    await holder.waitForURL(`${APP}/`);
    const participant = JSON.parse(kube('get', 'participants', '-o', 'json')).items.find(p => p.spec.displayName === displayName);

    const room = await open();
    await room.goto(`${APP}/login?next=%2Froom&connector=github`);
    await room.waitForURL(`${APP}/room`);
    const panel = room.getByTestId('pick-keys');
    await expect(panel).toContainText('Hand one participant');

    // A pick: a binding to the keys Role naming one User, and the menu's own
    // one-person grant left alone. Picked at random, and the fixture's room
    // may hold other people, so who it lands on is not this test's to choose.
    await panel.getByRole('button', { name: 'Pick someone', exact: true }).click();
    const picked = () => JSON.parse(kube('get', 'rolebinding', 'voter-audience-coffee-keys-person', '-o', 'json'));
    await expect.poll(() => kube('get', 'rolebinding', 'voter-audience-coffee-keys-person', '--ignore-not-found', '-o', 'name').trim()).not.toBe('');
    expect(picked().roleRef.name).toBe('voter-audience-coffee-keys');
    expect(picked().subjects).toHaveLength(1);
    expect(picked().subjects[0].kind).toBe('User');
    await expect(panel.getByTestId('picked-name')).toHaveText(picked().metadata.annotations['voter.configbutler.ai/display-name']);
    expect(kube('get', 'rolebinding', 'voter-audience-coffee-admin-person', '--ignore-not-found', '-o', 'name').trim()).toBe('');

    // So the test stands in for the operator's luck: the same binding, naming
    // this test's participant (their username is what the session reports).
    const { subject } = await holder.evaluate(async () => (await fetch('/auth/session')).json());
    const binding = picked();
    binding.subjects[0].name = `demo:${subject}`;
    binding.metadata.annotations['voter.configbutler.ai/display-name'] = displayName;
    binding.metadata.annotations['voter.configbutler.ai/participant'] = participant.metadata.name;
    delete binding.metadata.resourceVersion;
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys-person');
    kube('create', '-f', '-', { input: JSON.stringify(binding) });
    await expect(panel.getByTestId('picked-name')).toHaveText(displayName);

    // The holder's menu page grows a "New mail API key" button. A key typed
    // there is a pending change like any other: discarded with Revert, and
    // only written by Save.
    await holder.goto(`${APP}/admin`);
    const newMailKey = holder.getByRole('button', { name: 'New mail API key' });
    const dialog = holder.getByRole('dialog');
    const summary = holder.locator('.save-summary');
    const enterKey = async (value, button) => {
      await newMailKey.click();
      await dialog.getByLabel('Mail API key').fill(value);
      await dialog.getByRole('button', { name: button }).click();
      await expect(dialog).toBeHidden();
    };
    await expect(holder.getByRole('button', { name: 'New payments API key' })).toBeVisible();

    await enterKey('nope', 'Cancel');
    await expect(summary).toContainText('No pending changes');

    await enterKey('taart', 'OK');
    await expect(summary).toContainText('1 change(s) ready to save');
    await expect(summary).toContainText('Mail API key');
    await expect(summary).not.toContainText('taart');
    await summary.getByRole('button', { name: 'Revert' }).click();
    await expect(summary).toContainText('No pending changes');
    expect(JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: '' });

    // Saved with a menu edit, the menu goes first: this holder may not edit
    // the menu, so RBAC refuses it, and the key is not written either.
    const banner = holder.getByLabel('Banner text');
    const bannerBefore = await banner.inputValue();
    await banner.fill(`${bannerBefore} (refused)`);
    await enterKey('taart', 'OK');
    await summary.getByRole('button', { name: 'Save 2 Changes' }).click();
    await expect(summary.getByRole('alert')).toContainText('cannot patch resource "coffeeconfigs"');
    await expect(summary).toContainText('2 change(s) ready to save');
    expect(JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: '' });
    await summary.locator('li', { hasText: 'bannerText' }).getByRole('button', { name: 'Revert' }).click();
    await expect(summary).toContainText('1 change(s) ready to save');
    await expect(banner).toHaveValue(bannerBefore);

    // The key on its own is saved.
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect.poll(() => JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: b64('taart') });
    await expect(summary).toContainText('No pending changes');

    // The same value again is admission's refusal, in its own words, and the
    // key stays pending.
    await enterKey('taart', 'OK');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(summary.getByRole('alert')).toContainText('That is the mail key already set: saving it has to change it.');
    await expect(summary).toContainText('1 change(s) ready to save');

    // Taken back on /room: the binding goes, and the buttons grey out.
    await summary.getByRole('button', { name: 'Revert' }).click();
    await panel.getByRole('button', { name: 'Take it back' }).click();
    await expect.poll(() => kube('get', 'rolebinding', 'voter-audience-coffee-keys-person', '--ignore-not-found', '-o', 'name').trim()).toBe('');
    await expect(panel.getByTestId('picked-name')).toHaveCount(0);
    await expect(newMailKey).toBeDisabled();

    // "Try anyway" lifts the page's check, not Kubernetes': the key is refused
    // on Save, by RBAC, and nothing is written.
    await holder.getByRole('button', { name: 'Try anyway' }).click();
    await enterKey('koek', 'OK');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(summary.getByRole('alert')).toContainText('cannot patch resource "secrets"');
    expect(JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: b64('taart') });
  } finally {
    for (const c of contexts) await c.close();
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys-person', '--ignore-not-found');
    for (const name of ['coffee-mail', 'coffee-payments']) kube('patch', 'secret', name, '--type=merge', '-p', '{"data":{"apiKey":""}}');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
