import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';

// What a participant cannot do, even by hand.
//
// Under krm-foyer the browser holds a door to the API server (/k8s) with the
// participant's own token. The pages only ever send what the rules allow; this
// spec sends what they never would, from the same page and the same session, and
// checks each is refused by Kubernetes itself -- RBAC or one of Voter's
// admission policies (voter/config/admission/) -- with the policy's own words.
test('a participant cannot stuff the ballot box, relabel the menu, or pass as someone else', async ({ browser }) => {
  const round = `boundary-${Date.now()}`;
  const displayName = `${round}-mallory`;
  kube('create', '-f', '-', { input: JSON.stringify({ apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name: round, namespace: 'voter' }, spec: {
    title: round, state: 'live', questions: [{ id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] }],
  }}) });
  // The menu is editable in this test, so the refusal below is admission's and
  // not merely the missing grant.
  kube('create', 'rolebinding', 'voter-audience-coffee-admin', '--role=voter-audience-coffee-admin', '--group=demo:voter-audience');

  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  try {
    await expect.poll(() => kube('get', 'quizsession', round, '-o', 'jsonpath={.status.questionsDigest}')).toMatch(/^sha256:/);
    await page.goto(`${APP}/`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(displayName);
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/`);

    const k8s = (path, method, body, contentType = 'application/json') => page.evaluate(async ({ path, method, body, contentType }) => {
      const s = await (await fetch('/auth/session')).json();
      const res = await fetch(`/k8s/apis/${path}`, { method, headers: { 'content-type': contentType, [s.csrfHeader]: s.csrfToken }, body: JSON.stringify(body) });
      const status = await res.json();
      return { status: res.status, message: status.message ?? '' };
    }, { path, method, body, contentType });
    const r = JSON.parse(kube('get', 'quizsession', round, '-o', 'json'));
    const ballot = (name, submitter, extra = {}) => ({
      apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSubmission',
      metadata: { name, labels: { 'voter.configbutler.ai/round': round, 'voter.configbutler.ai/submitter': submitter } },
      spec: { sessionRef: { group: 'examples.configbutler.ai', kind: 'QuizSession', name: round },
        roundUID: r.metadata.uid, questionsDigest: r.status.questionsDigest, submittedAt: '2026-10-06T12:00:00Z',
        answers: [{ questionId: 'choice', singleChoice: 'GitOps' }], ...extra },
    });
    const submissions = 'examples.configbutler.ai/v1alpha1/namespaces/voter/quizsubmissions';

    // A ballot in somebody else's name.
    const forged = await k8s(submissions, 'POST', ballot(`${round}-alice`, 'Alice'));
    expect(forged.status).toBe(403);
    expect(forged.message).toContain(`A ballot is named after its round and your display name: ${round}-${displayName}.`);

    // A ballot with no pins: it could be for any questions.
    const unpinned = await k8s(submissions, 'POST', ballot(`${round}-${displayName}`, displayName, { roundUID: undefined, questionsDigest: undefined }));
    expect(unpinned.status).toBe(403);
    expect(unpinned.message).toContain('The round changed. Reload the questions before voting.');

    // Their own ballot is fine -- once.
    expect((await k8s(submissions, 'POST', ballot(`${round}-${displayName}`, displayName))).status).toBe(201);
    const twice = await k8s(submissions, 'POST', ballot(`${round}-${displayName}`, displayName));
    expect(twice.status).toBe(409);

    // A label on the menu: gitops-reverser would commit it under their name.
    const relabel = await k8s('examples.configbutler.ai/v1alpha1/namespaces/voter/coffeeconfigs/demo-coffee', 'PATCH',
      { metadata: { labels: { sneaky: 'yes' } } }, 'application/merge-patch+json');
    expect(relabel.status).toBe(403);
    expect(relabel.message).toContain('Only spec can be changed here');

    // Nothing landed.
    expect(kube('get', 'quizsubmissions', '-l', `voter.configbutler.ai/round=${round}`, '-o', 'name').trim().split('\n')).toEqual([
      `quizsubmission.examples.configbutler.ai/${round}-${displayName}`,
    ]);
    expect(JSON.parse(kube('get', 'coffeeconfig', 'demo-coffee', '-o', 'json')).metadata.labels?.sneaky).toBeUndefined();
  } finally {
    await context.close();
    kube('delete', 'rolebinding', 'voter-audience-coffee-admin', '--ignore-not-found');
    kube('delete', 'quizsubmissions', '-l', `voter.configbutler.ai/round=${round}`);
    kube('delete', 'quizsession', round, '--ignore-not-found');
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
