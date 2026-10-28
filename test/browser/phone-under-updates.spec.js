import { test, expect, devices } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';

// What a phone in the room goes through, and what the other specs never do to
// it: the room moves WHILE somebody has a field focused and half an answer
// typed. Ballots land and the tally rewrites the round's status, the operator
// flips who may vote, the menu is saved by somebody else, the phone goes into a
// pocket and comes back. Every one of those reaches the open page over the
// stream, and every one of them happened on 2026-10-07, where some people were
// shown an error starting with "invalid".
//
// Each test asserts the two things a person would notice: the field they were
// typing in still holds what they typed, and pressing the button afterwards
// works -- never an error they could not have caused.

const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';
const EVERYONE = 'voter-audience-ballot-voter-audience';
const everyoneBinding = JSON.stringify({
  apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding',
  metadata: { name: EVERYONE, namespace: 'voter' },
  subjects: [{ kind: 'Group', name: 'demo:voter-audience', apiGroup: 'rbac.authorization.k8s.io' }],
  roleRef: { kind: 'Role', name: 'voter-audience-ballot', apiGroup: 'rbac.authorization.k8s.io' },
});
const menuGrant = JSON.stringify({
  apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding',
  metadata: { name: 'voter-audience-coffee-admin', namespace: 'voter' },
  subjects: [{ kind: 'Group', apiGroup: 'rbac.authorization.k8s.io', name: 'demo:voter-audience' }],
  roleRef: { apiGroup: 'rbac.authorization.k8s.io', kind: 'Role', name: 'voter-audience-coffee-admin' },
});

// A phone: touch, a mobile user agent and a phone's viewport. Chromium, as the
// rest of the suite, because the fixture's resolver rules are Chromium flags.
const { defaultBrowserType: _, ...pixel } = devices['Pixel 7'];

async function join(browser, contexts, names, displayName, path = '/') {
  const context = await browser.newContext({ ...pixel, ignoreHTTPSErrors: true });
  contexts.push(context);
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  await page.goto(`${APP}${path}`);
  await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
  await page.getByLabel('Display name').fill(displayName);
  await page.getByRole('radio', { name: 'Vue' }).check();
  await page.getByRole('button', { name: 'Continue', exact: true }).tap();
  await page.waitForURL(`${APP}${path}`);
  names.push(displayName);
  return page;
}

async function round(name, questions) {
  kube('create', '-f', '-', { input: JSON.stringify({
    apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name, namespace: 'voter' },
    spec: { title: name, state: 'live', questions },
  }) });
  // The page waits for the digest on its own; waiting here keeps that wait out
  // of the timings below.
  await expect.poll(() => JSON.parse(kube('get', 'quizsession', name, '-o', 'json')).status?.questionsDigest ?? '').toMatch(/^sha256:/);
}

/** Somebody else's ballot, written past the page as the voting spec does. It
 *  moves the tally, so the round's status changes on every phone's stream. */
let others = 0;
function anotherBallot(roundName, answers) {
  kube('create', '-f', '-', { input: JSON.stringify({
    apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSubmission',
    metadata: { name: `${roundName}-other-${++others}`, namespace: 'voter', labels: { 'voter.configbutler.ai/round': roundName } },
    spec: { sessionRef: { group: 'examples.configbutler.ai', kind: 'QuizSession', name: roundName },
      submittedAt: new Date().toISOString().replace(/\.\d{3}Z$/, 'Z'), answers },
  }) });
}

const filed = roundName => JSON.parse(kube('get', 'quizsession', roundName, '-o', 'json')).status?.filed ?? 0;

function cleanUp(names, roundName) {
  if (roundName) {
    kube('delete', 'quizsubmissions', '-l', `voter.configbutler.ai/round=${roundName}`, '--ignore-not-found');
    kube('delete', 'quizsession', roundName, '--ignore-not-found');
  }
  const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
  for (const p of participants) if (names.includes(p.spec.displayName)) kube('delete', 'participant', p.metadata.name, '--ignore-not-found');
}

const QUESTIONS = [
  { id: 'framework', type: 'singleChoice', title: 'Which framework?', required: true, choices: ['React', 'Vue', 'Angular'] },
  { id: 'clusters', type: 'number', title: 'How many clusters?', min: 0, max: 1000 },
  { id: 'why', type: 'freeText', title: 'Why?' },
];

test('a phone keeps its focus and its words while the room votes around it', async ({ browser }) => {
  const name = `phone-${Date.now()}`;
  const contexts = [];
  const names = [];
  await round(name, QUESTIONS);
  try {
    const phone = await join(browser, contexts, names, `${name}-p`);
    await phone.goto(`${APP}/answer/${name}`);
    await phone.getByRole('button', { name: 'Vue', exact: true }).tap();

    const why = phone.getByLabel('Why?');
    await why.tap();
    await why.pressSequentially('Because the room');

    // The room votes while the keyboard is up. Each ballot rewrites the
    // round's status, which every open answer page receives.
    for (let i = 0; i < 3; i++) anotherBallot(name, [{ questionId: 'framework', singleChoice: 'React' }]);
    await expect.poll(() => filed(name)).toBe(3);
    // And the operator's switch, which rings the page's doorbell: it asks the
    // API server what it may do, mid-sentence.
    const asked = phone.waitForRequest(r => r.url().includes('selfsubjectrulesreviews'));
    kube('annotate', 'quizsession', name, '--overwrite', 'voter.configbutler.ai/ballot-groups=demo:voter-audience');
    await asked;

    await expect(why).toBeFocused();
    await expect(why).toHaveValue('Because the room');
    await why.pressSequentially(' keeps moving.');

    const clusters = phone.getByLabel('How many clusters?');
    await clusters.tap();
    await clusters.pressSequentially('1');
    anotherBallot(name, [{ questionId: 'framework', singleChoice: 'Angular' }]);
    await expect.poll(() => filed(name)).toBe(4);
    await clusters.pressSequentially('2');
    await expect(clusters).toBeFocused();
    await expect(clusters).toHaveValue('12');

    // Straight from the focused field to Submit, as a thumb does.
    await phone.getByRole('button', { name: 'Submit', exact: true }).tap();
    await expect(phone.getByText('Your vote is recorded. Thank you!')).toBeVisible();
    const mine = JSON.parse(kube('get', 'quizsubmission', `${name}-${name}-p`, '-o', 'json'));
    expect(mine.spec.answers).toEqual([
      { questionId: 'framework', singleChoice: 'Vue' },
      { questionId: 'clusters', number: 12 },
      { questionId: 'why', freeText: 'Because the room keeps moving.' },
    ]);
  } finally {
    for (const c of contexts) await c.close();
    cleanUp(names, name);
  }
});

// Demo step 3 from the phone's side: the operator takes the vote away while
// somebody is answering and gives it back. The form is swapped for the refusal
// and back again, which unmounts every field on it.
test('a vote taken away and given back mid-answer keeps what was typed', async ({ browser }) => {
  const name = `phone-${Date.now()}`;
  const contexts = [];
  const names = [];
  await round(name, QUESTIONS);
  try {
    const phone = await join(browser, contexts, names, `${name}-p`);
    await phone.goto(`${APP}/answer/${name}`);
    await phone.getByRole('button', { name: 'Angular', exact: true }).tap();
    const why = phone.getByLabel('Why?');
    await why.tap();
    await why.pressSequentially('Half a thought');

    kube('delete', 'rolebinding', EVERYONE);
    kube('annotate', 'quizsession', name, '--overwrite', 'voter.configbutler.ai/ballot-groups=');
    await expect(phone.getByTestId('ballot-refused')).toBeVisible();

    kube('apply', '-f', '-', { input: everyoneBinding });
    kube('annotate', 'quizsession', name, '--overwrite', 'voter.configbutler.ai/ballot-groups=demo:voter-audience');
    await expect(why).toHaveValue('Half a thought');
    await why.tap();
    await why.press('End');
    await why.pressSequentially(', finished.');

    await phone.getByRole('button', { name: 'Submit', exact: true }).tap();
    await expect(phone.getByText('Your vote is recorded. Thank you!')).toBeVisible();
    const mine = JSON.parse(kube('get', 'quizsubmission', `${name}-${name}-p`, '-o', 'json'));
    expect(mine.spec.answers).toEqual([
      { questionId: 'framework', singleChoice: 'Angular' },
      { questionId: 'why', freeText: 'Half a thought, finished.' },
    ]);
  } finally {
    for (const c of contexts) await c.close();
    kube('apply', '-f', '-', { input: everyoneBinding });
    cleanUp(names, name);
  }
});

// The phone is locked, the stream drops, the round is closed and reopened by the
// operator in the meantime, and the phone comes back.
test('a phone back from the pocket can still vote', async ({ browser }) => {
  const name = `phone-${Date.now()}`;
  const contexts = [];
  const names = [];
  await round(name, QUESTIONS);
  try {
    const phone = await join(browser, contexts, names, `${name}-p`);
    await phone.goto(`${APP}/answer/${name}`);
    await phone.getByRole('button', { name: 'React', exact: true }).tap();

    await phone.context().setOffline(true);
    kube('patch', 'quizsession', name, '--type=merge', '-p', '{"spec":{"state":"closed"}}');
    anotherBallot(name, [{ questionId: 'framework', singleChoice: 'Vue' }]);
    kube('patch', 'quizsession', name, '--type=merge', '-p', '{"spec":{"state":"live"}}');
    await phone.waitForTimeout(5000);
    await phone.context().setOffline(false);

    await expect(phone.getByRole('button', { name: 'Submit', exact: true })).toBeEnabled({ timeout: 30000 });
    await phone.getByRole('button', { name: 'Submit', exact: true }).tap();
    await expect(phone.getByText('Your vote is recorded. Thank you!')).toBeVisible();
  } finally {
    for (const c of contexts) await c.close();
    cleanUp(names, name);
  }
});

// What a person who changes their mind sends: a required question answered and
// then taken back. "Answer required" is the sentence they can act on; anything
// starting with "invalid" reads as the page being broken.
test('a required answer taken back reads as required, never as invalid', async ({ browser }) => {
  const name = `phone-${Date.now()}`;
  const contexts = [];
  const names = [];
  await round(name, [
    { id: 'tools', type: 'multiChoice', title: 'Which tools?', required: true, choices: ['kubectl', 'Helm', 'Flux'] },
    { id: 'say', type: 'freeText', title: 'Say something', required: true },
  ]);
  try {
    const phone = await join(browser, contexts, names, `${name}-p`);
    await phone.goto(`${APP}/answer/${name}`);
    const submit = phone.getByRole('button', { name: 'Submit', exact: true });

    await phone.getByText('Helm', { exact: true }).tap();
    await phone.getByText('Helm', { exact: true }).tap();
    await phone.getByLabel('Say something').fill('Fine.');
    await submit.tap();
    await expect(phone.getByText('answer required: Which tools?')).toBeVisible();

    await phone.getByText('Flux', { exact: true }).tap();
    await phone.getByLabel('Say something').fill('   ');
    await submit.tap();
    await expect(phone.getByText('answer required: Say something')).toBeVisible();
    await expect(phone.getByText(/^invalid/)).toHaveCount(0);

    await phone.getByLabel('Say something').fill('Fine after all.');
    await submit.tap();
    await expect(phone.getByText('Your vote is recorded. Thank you!')).toBeVisible();
  } finally {
    for (const c of contexts) await c.close();
    cleanUp(names, name);
  }
});

// The coffee menu, on a phone, while somebody else saves it. The editor
// re-renders on every stream event and every permission poll; a price field
// with the keyboard up must come through that with what was typed in it.
test('a price being typed on a phone survives somebody else saving the menu', async ({ browser }) => {
  const contexts = [];
  const names = [];
  const before = kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json');
  kube('apply', '-f', '-', { input: menuGrant });
  try {
    const stamp = Date.now();
    const phone = await join(browser, contexts, names, `menu-${stamp}-p`, '/admin');
    const other = await join(browser, contexts, names, `menu-${stamp}-o`, '/admin');
    const price = page => page.locator('label', { hasText: 'Base price (cents)' }).first().locator('input');
    const shopName = page => page.locator('label', { hasText: 'Shop name' }).locator('input');
    await expect(price(phone)).toBeVisible();

    await price(phone).tap();
    await price(phone).fill('');
    await price(phone).pressSequentially('25');

    // Somebody else saves a different field. Both the stream event and the
    // next permission poll re-render the phone's editor.
    await shopName(other).fill(`Shop ${stamp}`);
    await other.getByRole('button', { name: /^Save \d+ Change/ }).tap();
    await expect(shopName(phone)).toHaveValue(`Shop ${stamp}`);
    await phone.waitForTimeout(4500);

    await expect(price(phone)).toBeFocused();
    await expect(price(phone)).toHaveValue('25');
    await price(phone).pressSequentially('0');
    await expect(price(phone)).toHaveValue('250');
    await phone.getByRole('button', { name: /^Save \d+ Change/ }).tap();
    await expect.poll(() => JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).spec.products[0].priceCents).toBe(250);
    await expect(phone.getByText(/invalid/i)).toHaveCount(0);
  } finally {
    for (const c of contexts) await c.close();
    kube('replace', '-f', '-', { input: JSON.stringify((({ metadata: { resourceVersion, ...metadata }, ...rest }) => ({ ...rest, metadata }))(JSON.parse(before))) });
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    cleanUp(names);
  }
});

// A phone's numeric keyboard, and a label that says cents to somebody who
// thinks in euros: "2.5" is what gets typed. The CRD takes whole cents, and
// sent, it came back as the API server's 'is invalid: ... Invalid value:
// "number"'. The page has to say it first, in words, and send nothing.
test('a price typed in euros is answered in words, not a schema error', async ({ browser }) => {
  const contexts = [];
  const names = [];
  const before = kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json');
  const stored = () => JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).spec.products[0].priceCents;
  kube('apply', '-f', '-', { input: menuGrant });
  try {
    const phone = await join(browser, contexts, names, `menu-${Date.now()}-p`, '/admin');
    const price = phone.locator('label', { hasText: 'Base price (cents)' }).first().locator('input');
    const was = stored();
    await price.tap();
    await price.fill('');
    await price.pressSequentially('2.5');
    const save = phone.getByRole('button', { name: /^Save \d+ Change/ });
    await expect(phone.getByRole('alert').filter({ hasText: 'must be a whole number, not 2.5' })).toBeVisible();
    await expect(save).toBeDisabled();
    expect(stored()).toBe(was);

    await price.fill('250');
    await expect(phone.getByText('must be a whole number')).toHaveCount(0);
    await save.tap();
    await expect.poll(stored).toBe(250);
    await expect(phone.getByText(/Invalid value|is invalid|must be of type/)).toHaveCount(0);
  } finally {
    for (const c of contexts) await c.close();
    kube('replace', '-f', '-', { input: JSON.stringify((({ metadata: { resourceVersion, ...metadata }, ...rest }) => ({ ...rest, metadata }))(JSON.parse(before))) });
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    cleanUp(names);
  }
});
