import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';
const EVERYONE = 'voter-audience-ballot-voter-audience';

// The fixture's whole-room ballot binding (voter.yaml), put back after a spec
// takes it away.
const everyoneBinding = JSON.stringify({
  apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding',
  metadata: { name: EVERYONE, namespace: 'voter' },
  subjects: [{ kind: 'Group', name: 'demo:voter-audience', apiGroup: 'rbac.authorization.k8s.io' }],
  roleRef: { kind: 'Role', name: 'voter-audience-ballot', apiGroup: 'rbac.authorization.k8s.io' },
});

// `kubectl auth can-i` exits 1 on "no", which execFileSync reports as a throw.
function canI(...args) {
  try {
    return kube('auth', 'can-i', ...args, { stdio: ['pipe', 'pipe', 'ignore'] }).trim();
  } catch (error) {
    return String(error.stdout ?? '').trim();
  }
}

// Dex's subject for an authproxy login, computed here independently of the
// page's own roomGrants.ts: base64url of IDTokenSubject{1: user id, 2: connector}.
function dexSubject(userID, connector = 'room-pass') {
  const field = (n, s) => { const b = Buffer.from(s); return Buffer.concat([Buffer.from([(n << 3) | 2, b.length]), b]); };
  return Buffer.concat([field(1, userID), field(2, connector)]).toString('base64url');
}

async function join(browser, contexts, displayName, answer) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  contexts.push(context);
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  await page.goto(`${APP}/`);
  await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
  await page.getByLabel('Display name').fill(displayName);
  await page.getByRole('radio', { name: answer }).check();
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await page.waitForURL(`${APP}/`);
  return page;
}

async function operator(browser, contexts) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  contexts.push(context);
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  await page.goto(`${APP}/login?next=%2Froom&connector=github`);
  await page.waitForURL(`${APP}/room`);
  return page;
}

function cleanUp(names, round) {
  if (round) kube('delete', 'quizsession', round, '--ignore-not-found');
  if (round) kube('delete', 'quizsubmissions', '-l', `voter.configbutler.ai/round=${round}`, '--ignore-not-found');
  const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
  for (const p of participants) if (names.includes(p.spec.displayName)) kube('delete', 'participant', p.metadata.name, '--ignore-not-found');
}

// Demo step 3: most of the room may not vote, is told so plainly, and the
// operator lets one group in from /room. Their refusal turns into the ballot
// with nobody reloading, because the page keeps asking the API server.
test('the operator opens a round to one answer group, and only that group may vote', async ({ browser }) => {
  const stamp = Date.now();
  const round = `groups-${stamp}`;
  const svelteName = `${round}-svelte`;
  const vueName = `${round}-vue`;
  const contexts = [];
  kube('create', '-f', '-', { input: JSON.stringify({
    apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name: round, namespace: 'voter' },
    spec: { title: round, state: 'live', questions: [{ id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] }] },
  }) });
  kube('delete', 'rolebinding', EVERYONE, '--ignore-not-found');
  try {
    const svelte = await join(browser, contexts, svelteName, 'Svelte');
    const vue = await join(browser, contexts, vueName, 'Vue');
    for (const page of [svelte, vue]) {
      await page.goto(`${APP}/answer/${round}`);
      await expect(page.getByRole('heading', { name: /This round isn’t open to you/ })).toBeVisible();
      await expect(page.getByRole('button', { name: 'Submit', exact: true })).toHaveCount(0);
    }
    await expect(svelte.getByTestId('ballot-refused')).toContainText('demo:frontend-svelte');

    const room = await operator(browser, contexts);
    const everyone = room.getByRole('checkbox', { name: 'Let Everyone in the room vote' });
    const svelteSwitch = room.getByRole('checkbox', { name: 'Let Svelte vote' });
    await expect(svelteSwitch).toBeEnabled();
    await expect(everyone).not.toBeChecked();
    await svelteSwitch.check();
    await expect.poll(() => JSON.parse(kube('get', 'rolebinding', 'voter-audience-ballot-frontend-svelte', '-o', 'json')).subjects)
      .toEqual([{ kind: 'Group', apiGroup: 'rbac.authorization.k8s.io', name: 'demo:frontend-svelte' }]);

    // No reload: the refusal becomes the ballot on its own.
    await expect(svelte.getByRole('heading', { name: 'Which approach?' })).toBeVisible();
    await svelte.getByRole('button', { name: 'GitOps', exact: true }).click();
    await svelte.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(svelte.getByText('Your vote is recorded. Thank you!')).toBeVisible();

    // Vue is still out, and RBAC says so too, not only the page.
    await expect(vue.getByTestId('ballot-refused')).toBeVisible();

    await svelteSwitch.uncheck();
    await expect.poll(() => kube('get', 'rolebinding', 'voter-audience-ballot-frontend-svelte', '--ignore-not-found', '-o', 'name').trim()).toBe('');

    // Everyone in: the Vue participant's page opens too.
    await everyone.check();
    await expect(vue.getByRole('heading', { name: 'Which approach?' })).toBeVisible();
  } finally {
    for (const c of contexts) await c.close();
    kube('delete', 'rolebinding', 'voter-audience-ballot-frontend-svelte', '--ignore-not-found');
    kube('apply', '-f', '-', { input: everyoneBinding });
    cleanUp([svelteName, vueName], round);
  }
});

// Demo step 4: one person on the coffee menu before the room. The binding
// names a username the page computed from the Participant, so this proves the
// computation against the subject Dex actually issued.
test('the operator hands the coffee menu to one participant picked at random', async ({ browser }) => {
  const name = `pick-${Date.now()}`;
  const contexts = [];
  try {
    const alice = await join(browser, contexts, name, 'Vue');
    const session = await alice.evaluate(async () => (await fetch('/auth/session')).json());
    const participant = JSON.parse(kube('get', 'participants', '-o', 'json')).items.find(p => p.spec.displayName === name);
    expect(session.subject).toBe(dexSubject(participant.metadata.name.replace(/^p-/, '')));

    const room = await operator(browser, contexts);
    const panel = room.getByTestId('pick-someone');
    await panel.getByRole('button', { name: 'Pick someone', exact: true }).click();
    await expect(panel.getByTestId('picked-name')).toBeVisible();

    const binding = JSON.parse(kube('get', 'rolebinding', 'voter-audience-coffee-admin-person', '-o', 'json'));
    expect(binding.roleRef.name).toBe('voter-audience-coffee-admin');
    const picked = binding.metadata.annotations['voter.configbutler.ai/participant'];
    const username = binding.subjects[0].name;
    expect(binding.subjects).toEqual([{ kind: 'User', apiGroup: 'rbac.authorization.k8s.io', name: `demo:${dexSubject(picked.replace(/^p-/, ''))}` }]);
    await expect(panel.getByTestId('picked-name')).toHaveText(binding.metadata.annotations['voter.configbutler.ai/display-name']);
    // That one user may now edit the menu, and the room as a whole still may not.
    expect(canI('patch', 'coffeeconfigs.examples.configbutler.ai', '--as', username, '--as-group', 'demo:voter-audience')).toBe('yes');
    expect(canI('patch', 'coffeeconfigs.examples.configbutler.ai', '--as', 'demo:somebody-else', '--as-group', 'demo:voter-audience')).toBe('no');

    await panel.getByRole('button', { name: 'Take it back' }).click();
    await expect.poll(() => kube('get', 'rolebinding', 'voter-audience-coffee-admin-person', '--ignore-not-found', '-o', 'name').trim()).toBe('');
    await expect(panel.getByTestId('picked-name')).toHaveCount(0);
  } finally {
    for (const c of contexts) await c.close();
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin-person', '--ignore-not-found');
    cleanUp([name]);
  }
});
