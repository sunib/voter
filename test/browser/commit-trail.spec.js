import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';

// A save on the menu editor reaches two repositories, and the page says so: one
// CommitRequest per GitTarget, one line per commit, under the Save button that
// was just pressed.
//
// The fixture has no ConfigButler, so this test is the operator: a CRD with only
// what the page reads, the grant the live Role holds, and a status written by
// hand. What gitops-reverser does with a request is not under test here.
const CRD = {
  apiVersion: 'apiextensions.k8s.io/v1', kind: 'CustomResourceDefinition',
  metadata: { name: 'commitrequests.configbutler.ai' },
  spec: {
    group: 'configbutler.ai', scope: 'Namespaced',
    names: { kind: 'CommitRequest', plural: 'commitrequests', singular: 'commitrequest', listKind: 'CommitRequestList' },
    versions: [{
      name: 'v1alpha3', served: true, storage: true, subresources: { status: {} },
      schema: { openAPIV3Schema: { type: 'object', 'x-kubernetes-preserve-unknown-fields': true } },
    }],
  },
};
const GRANT = [
  { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'Role', metadata: { name: 'commit-trail-test' },
    rules: [{ apiGroups: ['configbutler.ai'], resources: ['commitrequests'], verbs: ['create', 'get', 'list', 'watch'] }] },
  { apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding', metadata: { name: 'commit-trail-test' },
    subjects: [{ apiGroup: 'rbac.authorization.k8s.io', kind: 'Group', name: 'demo:voter-audience' }],
    roleRef: { apiGroup: 'rbac.authorization.k8s.io', kind: 'Role', name: 'commit-trail-test' } },
];
const apply = (object) => kube('apply', '-f', '-', { input: JSON.stringify(object), stdio: 'pipe' });
const requests = (target) =>
  JSON.parse(kube('get', 'commitrequests', '-o', 'json')).items
    .filter((r) => r.spec.gitTargetRef.name === target)
    .sort((a, b) => a.metadata.creationTimestamp.localeCompare(b.metadata.creationTimestamp));
const finish = (target, reason, commit) => {
  const status = { conditions: [{ type: 'Ready', status: 'True', reason, message: `${reason} on ${target}`, lastTransitionTime: new Date().toISOString() }], ...(commit ? { commit } : {}) };
  kube('patch', 'commitrequest', requests(target).at(-1).metadata.name, '--subresource=status', '--type=merge', '-p', JSON.stringify({ status }));
};

test('a save names both GitTargets and shows both commits, and only what this press wrote', async ({ browser }) => {
  test.setTimeout(120000);
  const displayName = `trail-${Date.now()}`;
  const shopName = JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).spec.shopName;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  page.on('pageerror', (error) => { throw error; });
  try {
    apply(CRD);
    kube('wait', '--for=condition=Established', 'crd/commitrequests.configbutler.ai', '--timeout=30s');
    GRANT.forEach(apply);
    kube('create', 'rolebinding', 'voter-audience-coffee-admin', '--role=voter-audience-coffee-admin', '--group=demo:voter-audience');
    // The fixture's Voter names no target, so the page is told here. In the page
    // because only the browser can resolve the fixture's host.
    await page.addInitScript(() => {
      const fetch = window.fetch;
      window.fetch = async (input, init) => {
        const response = await fetch(input, init);
        if (String(input) !== '/config.json') return response;
        return Response.json({
          ...(await response.json()),
          gitTargetName: 'coffee', commitURLTemplate: 'https://git.example/k8s-trail/commit/{sha}',
          settingsGitTargetName: 'coffee-settings', settingsCommitURLTemplate: 'https://git.example/coffee-settings/commit/{sha}',
        });
      };
    });

    await page.goto(`${APP}/admin`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/admin`);

    const summary = page.locator('.save-summary');
    const trail = summary.locator('.commit-trail');
    const shopField = page.locator('label', { hasText: 'Shop name' }).locator('input');
    await expect(shopField).toBeEditable();

    // The menu: both targets are asked, with the same message.
    const shaMenu = 'a'.repeat(40);
    const shaSettings = 'b'.repeat(40);
    await shopField.fill(`${shopName} trail`);
    await summary.getByLabel('Why are you making this change?').fill('both of them');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(trail).toHaveText('Committing to coffee-settings and k8s-trail…');
    for (const target of ['coffee', 'coffee-settings']) {
      expect(requests(target)).toHaveLength(1);
      expect(requests(target)[0].spec.message).toBe('both of them');
    }
    finish('coffee', 'Committed', shaMenu);
    finish('coffee-settings', 'Committed', shaSettings);
    await expect(trail).toHaveText('Committed to coffee-settings and k8s-trail');
    await expect(trail.getByRole('link', { name: 'coffee-settings' })).toHaveAttribute('href', `https://git.example/coffee-settings/commit/${shaSettings}`);
    await expect(trail.getByRole('link', { name: 'k8s-trail' })).toHaveAttribute('href', `https://git.example/k8s-trail/commit/${shaMenu}`);
    // Where the person is looking: on a phone, with the Save button still there.
    await expect(trail).toBeInViewport();
    await expect(summary.getByRole('button', { name: /Save/ })).toBeInViewport();

    // A refused key wrote nothing, so there is no commit to show, not even the
    // last one, and no warning about one.
    const mailKey = page.getByRole('region', { name: 'Mail integration' }).getByLabel('API key');
    await page.getByRole('button', { name: 'Try anyway' }).click();
    await mailKey.fill('koek');
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(summary.getByRole('alert')).toContainText('cannot patch resource "secrets"');
    await expect(trail).toHaveCount(0);
    await expect(summary.locator('.panel--warning')).toHaveCount(0);
    expect(requests('coffee')).toHaveLength(1);
    await page.waitForTimeout(3100);

    // A key that lands is one commit, in the repository that holds it.
    kube('create', 'rolebinding', 'voter-audience-coffee-keys', '--role=voter-audience-coffee-keys', '--group=demo:voter-audience');
    await expect(async () => {
      await summary.getByRole('button', { name: 'Save 1 Change' }).click();
      await expect(summary.getByRole('alert')).toHaveCount(0, { timeout: 2000 });
    }).toPass({ timeout: 30000 });
    await expect(trail).toHaveText('Committing to coffee-settings…');
    expect(requests('coffee')).toHaveLength(1);
    finish('coffee-settings', 'Committed', 'c'.repeat(40));
    await expect(trail).toHaveText('Committed to coffee-settings');
    await expect(trail.getByRole('link')).toHaveAttribute('href', `https://git.example/coffee-settings/commit/${'c'.repeat(40)}`);

    // One target losing its commit does not cost the other its line, and the
    // warning says which.
    await shopField.fill(`${shopName} trail two`);
    await summary.getByRole('button', { name: 'Save 1 Change' }).click();
    await expect(trail).toHaveText('Committing to coffee-settings and k8s-trail…');
    // The first save's reason was that save's, and is not this one's message.
    expect(requests('coffee').at(-1).spec.message).toBeUndefined();
    finish('coffee', 'Committed', 'd'.repeat(40));
    await expect(trail).toHaveText('Committed to k8s-trail · committing to coffee-settings…');
    finish('coffee-settings', 'NoWindow');
    await expect(trail).toHaveText('Committed to k8s-trail');
    await expect(summary.locator('.panel--warning')).toContainText('coffee-settings: NoWindow on coffee-settings');
  } finally {
    await context.close().catch(() => {});
    kube('delete', 'rolebinding', 'voter-audience-coffee-keys', 'voter-audience-coffee-admin', 'commit-trail-test', '--ignore-not-found');
    kube('delete', 'role', 'commit-trail-test', '--ignore-not-found');
    kube('delete', 'crd', 'commitrequests.configbutler.ai', '--ignore-not-found');
    kube('patch', 'coffeeconfig', 'demo-coffee', '--type=merge', '-p', JSON.stringify({ spec: { shopName } }));
    kube('patch', 'secret', 'coffee-mail', '--type=merge', '-p', '{"data":{"apiKey":""}}');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
