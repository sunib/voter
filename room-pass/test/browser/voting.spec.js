import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'room-pass', ...args], { encoding: 'utf8' });
const APP = 'https://app.roompass.test:18443';

test('two participants vote, see durable results, and cannot vote twice or after closure', async ({ browser }) => {
  const name = `vote-test-${Date.now()}`;
  const names = [];
  const contexts = [];
  const round = { apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name, namespace: 'room-pass' }, spec: {
    title: name, state: 'live', questions: [
      { id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] },
      { id: 'text', type: 'freeText', title: 'Why?' },
    ],
  }};
  execFileSync('kubectl', ['--kubeconfig', kubeconfig, 'create', '-f', '-'], { input: JSON.stringify(round) });
  const uid = JSON.parse(kube('get', 'quizsession', name, '-o', 'json')).metadata.uid;
  async function signIn(label) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    contexts.push(context);
    const page = await context.newPage();
    page.on('pageerror', error => { throw error; });
    await page.goto(`${APP}/vote`);
    const displayName = `${name}-${label}`;
    names.push(displayName);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/vote`);
    const card = page.getByRole('article').filter({ has: page.getByRole('heading', { name, exact: true }) });
    await card.getByRole('link', { name: 'Answer questions' }).click();
    await expect(page.getByRole('heading', { name: 'Which approach?' })).toBeVisible();
    return page;
  }
  try {
    const alice = await signIn('alice');
    await alice.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(alice.getByText('answer required: Which approach?')).toBeVisible();
    await alice.getByRole('button', { name: 'GitOps', exact: true }).click();
    await alice.getByLabel('Why?').fill('Changes are reviewable.');
    await alice.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(alice.getByText('Your vote is recorded. Thank you!')).toBeVisible();
    await expect(alice.getByTestId('vote-total')).toHaveText('1 votes recorded');
    await expect(alice.getByText('Changes are reviewable.')).toBeVisible();

    const bob = await signIn('bob');
    await bob.getByRole('button', { name: 'Manual changes', exact: true }).click();
    await bob.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(bob.getByTestId('vote-total')).toHaveText('2 votes recorded');
    await alice.getByRole('button', { name: 'Refresh results', exact: true }).click();
    await expect(alice.getByTestId('vote-total')).toHaveText('2 votes recorded');
    await expect(alice.getByRole('progressbar', { name: 'GitOps', exact: true })).toHaveAttribute('value', '1');
    await expect(alice.getByRole('progressbar', { name: 'Manual changes', exact: true })).toHaveAttribute('value', '1');
    await alice.reload();
    await expect(alice.getByTestId('vote-total')).toHaveText('2 votes recorded');

    await alice.goto(`${APP}/answer/${name}`);
    await alice.getByRole('button', { name: 'GitOps', exact: true }).click();
    await alice.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(alice.getByText('You have already voted in this round.')).toBeVisible();
    await expect(alice.getByTestId('vote-total')).toHaveText('2 votes recorded');

    await bob.goto(`${APP}/answer/${name}`);
    await bob.getByRole('button', { name: 'GitOps', exact: true }).click();
    kube('patch', 'quizsession', name, '--type=merge', '-p', '{"spec":{"state":"closed"}}');
    await bob.getByRole('button', { name: 'Submit', exact: true }).click();
    await expect(bob.getByText('This round is not open for voting.')).toBeVisible();
    await bob.reload();
    await expect(bob.getByRole('button', { name: 'Submit', exact: true })).toBeDisabled();
    const quizSubmissions = JSON.parse(kube('get', 'quizsubmissions', '-l', `voter.configbutler.ai/round-uid=${uid}`, '-o', 'json')).items;
    expect(quizSubmissions).toHaveLength(2);
  } finally {
    for (const context of contexts) await context.close();
    kube('delete', 'quizsubmissions', '-l', `voter.configbutler.ai/round-uid=${uid}`);
    kube('delete', 'quizsession', name);
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (names.includes(p.spec.displayName)) kube('delete', 'participant', p.metadata.name);
  }
});
