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

    // Both integrations on the holder's menu page have an API key field. A key typed
    // there is a pending change like any other: discarded with Revert, and
    // only written by Save.
    await holder.goto(`${APP}/admin`);
    const pane = (name) => holder.getByRole('region', { name });
    const mailPane = pane('Mail integration');
    const mailKey = mailPane.getByLabel('API key');
    const paymentsKey = pane('Payment integration').getByLabel('API key');
    const summary = holder.locator('.save-summary');
    await expect(paymentsKey).toBeEditable();
    await expect(mailPane.getByText('Not set', { exact: true })).toBeVisible();

    // The eye shows only what is being typed: nothing to show until then.
    const eye = mailPane.getByRole('button', { name: 'Show what you typed' });
    await expect(eye).toBeDisabled();
    await expect(mailKey).toHaveAttribute('type', 'password');
    await mailKey.fill('nope');
    await expect(summary).toContainText('1 change(s) ready to save');
    await eye.click();
    await expect(mailKey).toHaveAttribute('type', 'text');
    await expect(mailKey).toHaveValue('nope');
    // Reverted in the field itself, and the eye closes with it.
    await mailPane.getByRole('button', { name: 'Revert' }).click();
    await expect(summary).toContainText('No pending changes');
    await expect(mailKey).toHaveValue('');
    await expect(mailKey).toHaveAttribute('type', 'password');

    await mailKey.fill('taart');
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
    await mailKey.fill('taart');
    await summary.getByRole('button', { name: 'Save 2 Changes' }).click();
    await expect(summary.getByRole('alert')).toContainText('cannot patch resource "coffeeconfigs"');
    await expect(summary).toContainText('2 change(s) ready to save');
    expect(JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: '' });
    await summary.locator('li', { hasText: 'bannerText' }).getByRole('button', { name: 'Revert' }).click();
    await expect(summary).toContainText('1 change(s) ready to save');
    await expect(banner).toHaveValue(bannerBefore);

    // The key on its own is saved, and the other Secret is not written at all:
    // its resourceVersion would move even for the same value.
    const paymentsSecret = () => JSON.parse(kube('get', 'secret', 'coffee-payments', '-o', 'json'));
    const paymentsVersion = paymentsSecret().metadata.resourceVersion;
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect.poll(() => JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: b64('taart') });
    await expect(summary).toContainText('No pending changes');
    await expect(holder.getByRole('status').filter({ hasText: /^Saved$/ })).toBeVisible();
    expect(paymentsSecret().metadata.resourceVersion).toBe(paymentsVersion);

    // Set, for the page and for whoever reads the menu: one bit, and the dots are
    // a placeholder over an empty input, so the value is nowhere in the page.
    await expect(mailPane.getByText('Set', { exact: true })).toBeVisible();
    await expect(mailKey).toHaveValue('');
    await expect(mailKey).toHaveAttribute('placeholder', /^•+$/);
    await expect(mailPane.getByRole('button', { name: 'Show what you typed' })).toBeDisabled();
    expect(await holder.content()).not.toContain('taart');
    expect(JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).status.keys).toEqual({ mail: { set: true }, payments: { set: false } });

    // The same value again is admission's refusal, in its own words, on that
    // key's row, and the key stays pending. The payments key saved with it is
    // written all the same: each key is refused on its own.
    await mailKey.fill('taart');
    await paymentsKey.fill('bon1');
    await summary.getByRole('button', { name: 'Save 2 Changes' }).click();
    await expect.poll(() => paymentsSecret().data).toEqual({ apiKey: b64('bon1') });
    await expect(summary.locator('li', { hasText: 'Mail API key' }).getByRole('alert')).toContainText('That is the mail key already set: saving it has to change it.');
    await expect(summary).toContainText('1 change(s) ready to save');
    await expect(holder.getByRole('status').filter({ hasText: 'Saved, except what is marked below' })).toBeVisible();

    // A new value answers the refusal, so the refusal goes.
    await mailKey.fill('koekje');
    await expect(summary.getByRole('alert')).toHaveCount(0);

    // Taken back on /room: the binding goes, and the buttons grey out.
    await summary.getByRole('button', { name: 'Revert' }).click();
    await panel.getByRole('button', { name: 'Take it back' }).click();
    await expect.poll(() => kube('get', 'rolebinding', 'voter-audience-coffee-keys-person', '--ignore-not-found', '-o', 'name').trim()).toBe('');
    await expect(panel.getByTestId('picked-name')).toHaveCount(0);
    await expect(mailKey).toBeDisabled();

    // "Try anyway" lifts the page's check, not Kubernetes': the key is refused
    // on Save, by RBAC, and nothing is written.
    await holder.getByRole('button', { name: 'Try anyway' }).click();
    await mailKey.fill('koek');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(summary.getByRole('alert')).toContainText('cannot patch resource "secrets"');
    expect(JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json')).data).toEqual({ apiKey: b64('taart') });

    // That refusal names this participant's username, one very long word. Wide or
    // narrow, with the whole answer open, it wraps and Save stays on screen.
    await summary.getByRole('button', { name: 'Show JSON' }).click();
    for (const width of [390, 1280]) {
      await holder.setViewportSize({ width, height: 800 });
      expect(await holder.locator('.admin-sidebar').evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
      expect(await holder.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    }
  } finally {
    for (const c of contexts) await c.close();
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys-person', '--ignore-not-found');
    for (const name of ['coffee-mail', 'coffee-payments']) kube('patch', 'secret', name, '--type=merge', '-p', '{"data":{"apiKey":""}}');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});

// A key going from empty to set moves the menu's resourceVersion, through its
// status. That is somebody else's change to the page of whoever is editing: their
// edit stays, and their save still lands.
test('a key being set does not disturb an edit of the menu', async ({ browser }) => {
  const displayName = `keys-editor-${Date.now()}`;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  const before = JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).spec.shopName;
  try {
    kube('create', 'rolebinding', 'voter-audience-coffee-admin', '--role=voter-audience-coffee-admin', '--group=demo:voter-audience');
    await page.goto(`${APP}/admin`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/admin`);

    const mailPane = page.getByRole('region', { name: 'Mail integration' });
    const shopField = page.locator('label', { hasText: 'Shop name' }).locator('input');
    const summary = page.locator('.save-summary');
    await expect(shopField).toBeEditable();
    await expect(mailPane.getByText('Not set', { exact: true })).toBeVisible();
    await shopField.fill(`${before} edited`);

    kube('patch', 'secret', 'coffee-mail', '--type=merge', '-p', `{"data":{"apiKey":"${b64('koek')}"}}`);
    await expect(mailPane.getByText('Set', { exact: true })).toBeVisible();
    await expect(shopField).toHaveValue(`${before} edited`);
    await expect(summary).toContainText('1 change(s) ready to save');

    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect.poll(() => JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).spec.shopName).toBe(`${before} edited`);
    await expect(summary).toContainText('No pending changes');
    await expect(summary.getByRole('alert')).toHaveCount(0);

    // And back: empty is "Not set" again, with no dots.
    kube('patch', 'secret', 'coffee-mail', '--type=merge', '-p', '{"data":{"apiKey":""}}');
    await expect(mailPane.getByText('Not set', { exact: true })).toBeVisible();
    await expect(mailPane.getByLabel('API key')).not.toHaveAttribute('placeholder', /•/);
  } finally {
    await context.close().catch(() => {});
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    kube('patch', 'coffeeconfig', 'demo-coffee', '--type=merge', '-p', JSON.stringify({ spec: { shopName: before } }));
    kube('patch', 'secret', 'coffee-mail', '--type=merge', '-p', '{"data":{"apiKey":""}}');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});

// Somebody deleted the Secret, and Flux has not seeded it again yet. Typing a key is
// what they came for, so the save brings it back: a server-side apply, the one
// `create` RBAC can limit by name, and admission holds it to what the seed would make.
test('setting a key whose Secret is gone brings the Secret back, and nothing else', async ({ browser }) => {
  const displayName = `keys-gone-${Date.now()}`;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  const label = { 'voter.configbutler.ai/coffee-settings': 'true' };
  const seed = { apiVersion: 'v1', kind: 'Secret', metadata: { name: 'coffee-mail', labels: label }, type: 'Opaque', stringData: { apiKey: 'taart' } };
  const write = (name, method, body, contentType = 'application/apply-patch+yaml', path = `/${name}?fieldManager=voter&force=true`) => page.evaluate(async ({ method, body, contentType, path }) => {
    const s = await (await fetch('/auth/session')).json();
    const res = await fetch(`/k8s/api/v1/namespaces/voter/secrets${path}`, {
      method, headers: { 'content-type': contentType, [s.csrfHeader]: s.csrfToken }, body: JSON.stringify(body),
    });
    return { status: res.status, message: (await res.json()).message ?? '' };
  }, { method, body, contentType, path });
  const gone = () => { kube('delete', 'secret', 'coffee-mail', '--ignore-not-found'); };
  try {
    kube('create', 'rolebinding', 'voter-audience-coffee-admin', '--role=voter-audience-coffee-admin', '--group=demo:voter-audience');
    kube('create', 'rolebinding', 'voter-audience-coffee-keys', '--role=voter-audience-coffee-keys', '--group=demo:voter-audience');
    await page.goto(`${APP}/admin`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/admin`);

    const mailPane = page.getByRole('region', { name: 'Mail integration' });
    const mailKey = mailPane.getByLabel('API key');
    const summary = page.locator('.save-summary');
    await expect(mailKey).toBeEditable();
    gone();

    // A plain POST has no name for RBAC to match, whatever it says it is.
    const post = await write('', 'POST', seed, 'application/json', '');
    expect(post.status).toBe(403);
    expect(post.message).toContain('cannot create resource "secrets"');
    // And an apply is only ever one of the two names.
    const otherName = await write('krm-foyer-session-keys2', 'PATCH', { ...seed, metadata: { ...seed.metadata, name: 'krm-foyer-session-keys2' } });
    expect(otherName.status).toBe(403);

    // Only what the seed would have made, or it is not published with the menu, or
    // something beside the key reaches a public repository in plain text.
    for (const [body, words] of [
      [{ ...seed, stringData: { apiKey: 'taart', other: 'x' } }, 'The mail key holds one entry, apiKey, and nothing else.'],
      [{ ...seed, metadata: { name: 'coffee-mail' } }, 'Only the value of the mail key can be chosen here'],
      [{ ...seed, metadata: { ...seed.metadata, labels: { ...label, sneaky: 'yes' } } }, 'Only the value of the mail key can be chosen here'],
      [{ ...seed, metadata: { ...seed.metadata, annotations: { note: 'x' } } }, 'Only the value of the mail key can be chosen here'],
      [{ ...seed, immutable: true }, 'Only the value of the mail key can be chosen here'],
      [{ ...seed, type: 'example.com/other' }, 'Only the value of the mail key can be chosen here'],
    ]) {
      const refused = await write('coffee-mail', 'PATCH', body);
      expect(refused.status, JSON.stringify(body)).toBeGreaterThanOrEqual(400);
      expect(refused.message).toContain(words);
    }
    expect(() => kube('get', 'secret', 'coffee-mail')).toThrow();

    // The save itself, as a person does it.
    await mailKey.fill('taart');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(summary).toContainText('No pending changes');
    await expect(summary.getByRole('alert')).toHaveCount(0);
    const made = JSON.parse(kube('get', 'secret', 'coffee-mail', '-o', 'json'));
    expect(made.data).toEqual({ apiKey: b64('taart') });
    expect(made.type).toBe('Opaque');
    expect(made.metadata.labels).toEqual(label);
    expect(made.metadata.annotations).toBeUndefined();
    await expect(mailPane.getByText('Set', { exact: true })).toBeVisible();
    await expect(mailKey).toHaveValue('');

    // From here it is an ordinary key: an ordinary patch, under the ordinary rules.
    const same = await write('coffee-mail', 'PATCH', { stringData: { apiKey: 'taart' } }, 'application/merge-patch+json', '/coffee-mail');
    expect(same.status).toBe(422);
    expect(same.message).toContain('That is the mail key already set');
    expect((await write('coffee-mail', 'PATCH', { stringData: { apiKey: 'koek' } }, 'application/merge-patch+json', '/coffee-mail')).status).toBe(200);
  } finally {
    await context.close().catch(() => {});
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys', '--ignore-not-found');
    // Back to the fixture's seed, whichever way the test ended.
    gone();
    kube('create', 'secret', 'generic', 'coffee-mail', '--from-literal=apiKey=');
    kube('label', 'secret', 'coffee-mail', 'voter.configbutler.ai/coffee-settings=true');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
