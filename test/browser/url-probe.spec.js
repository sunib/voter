import { test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...a) => { const o = typeof a.at(-1) === 'object' ? a.pop() : {}; return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...a], { encoding: 'utf8', ...o }); };
const APP = 'https://app.voter.test:19443';
const OUT = process.env.OUT;
test('probe', async ({ browser }) => {
  test.setTimeout(120000);
  const round = 'probe1';
  kube('delete', 'quizsession', round, '--ignore-not-found');
  kube('create', '-f', '-', { input: JSON.stringify({ apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'QuizSession', metadata: { name: round, namespace: 'voter' }, spec: { title: round, state: 'live', questions: [{ id: 'choice', type: 'singleChoice', title: 'Which approach?', required: true, choices: ['GitOps', 'Manual changes'] }] } }) });
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, recordVideo: undefined });
  const page = await ctx.newPage();
  await page.goto(`${APP}/`);
  await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
  await page.getByLabel('Display name').fill('Probe');
  await page.getByRole('radio', { name: 'Svelte' }).check();
  await page.getByRole('button', { name: 'Continue', exact: true }).click();
  await page.waitForURL(`${APP}/`);
  await page.goto(`${APP}/answer/${round}`);
  await page.getByRole('button', { name: 'GitOps', exact: true }).click();
  await page.getByRole('button', { name: 'Submit', exact: true }).click();
  await page.getByText('Your vote is recorded').waitFor();
  const urls = {
    whoami: '/auth/whoami',
    session: '/auth/session',
    submission: `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsubmissions/${round}-probe`,
    round: `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions/${round}`,
    submissionsList: `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsubmissions`,
    room: `/k8s/apis/room-pass.koudijs.dev/v1alpha1/namespaces/voter/rooms/demo`,
    stream: `/stream/v1?group=examples.configbutler.ai&version=v1alpha1&resource=quizsessions&namespace=voter`,
    k8sWatch: `/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/quizsessions?watch=1`,
  };
  for (const [k, u] of Object.entries(urls)) {
    const p = await ctx.newPage();
    let status = '?', ct = '?';
    p.on('response', r => { if (r.url().endsWith(u.split('/').pop())) { status = r.status(); ct = r.headers()['content-type']; } });
    try { const r = await p.goto(APP + u, { timeout: 8000, waitUntil: 'commit' }); status = r?.status(); ct = r?.headers()['content-type']; } catch (e) { status += ' (' + e.message.split('\n')[0] + ')'; }
    await p.waitForTimeout(2500);
    const text = (await p.evaluate(() => document.body?.innerText ?? '').catch(() => '')).slice(0, 500);
    await p.screenshot({ path: `${OUT}/${k}.png` }).catch(() => {});
    console.log(`=== ${k} ${u}\n  status=${status} ct=${ct}\n  ${text.replace(/\n/g, '\n  ')}`);
    await p.close();
  }
  await ctx.close();
  kube('delete', 'quizsubmissions', '-l', `voter.configbutler.ai/round=${round}`);
  kube('delete', 'quizsession', round);
  for (const p of JSON.parse(kube('get', 'participants', '-o', 'json')).items) if (p.spec.displayName.toLowerCase() === 'probe') kube('delete', 'participant', p.metadata.name);
});
