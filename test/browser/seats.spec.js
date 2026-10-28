import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => {
  const options = typeof args.at(-1) === 'object' ? args.pop() : {};
  return execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8', ...options });
};
const APP = 'https://app.voter.test:19443';
const SHOTS = process.env.SEAT_SHOTS;

// The fixture carries no SeatingPlan (test/e2e/voter.yaml says why), so this
// spec makes the one voter-seat-in-plan's binding names, and removes it.
const plan = (rows, columns) => JSON.stringify({
  apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'SeatingPlan',
  metadata: { name: 'mission2', namespace: 'voter' },
  spec: { title: 'Mission 2', rows, columns },
});

test('a participant takes a seat once, with a why, and changes its look with another', async ({ browser }) => {
  const run = `seat-${Date.now()}`;
  const contexts = [];
  kube('apply', '-f', '-', { input: plan(8, 12) });

  async function join(label) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true });
    contexts.push(context);
    const page = await context.newPage();
    page.on('pageerror', error => { throw error; });
    await page.goto(`${APP}/`);
    await page.getByLabel('Room code').fill(JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code);
    await page.getByLabel('Display name').fill(`${run}-${label}`);
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    return page;
  }
  // The overview map; the zoomed row under it carries the same labels.
  const seat = (page, label) => page.getByLabel(label, { exact: true }).first();

  try {
    // Joining lands on the seat page, not the quizzes: taking a seat is first.
    const alice = await join('alice');
    await alice.waitForURL(`${APP}/seat`);
    await expect(alice.getByRole('heading', { name: 'Where are you sitting?' })).toBeVisible();
    await expect(seat(alice, 'Row 8, seat 12, free')).toBeVisible();
    await expect(seat(alice, 'Row 9, seat 1, free')).toHaveCount(0);
    // A phone's width, and the page does not scroll sideways.
    expect(await alice.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

    // Nothing is sent without a seat and a why.
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();
    await expect(alice.getByText('Tap your seat on the map first.')).toBeVisible();
    await expect(alice.getByText('Tell us why. It goes with your choice.')).toBeVisible();

    await seat(alice, 'Row 3, seat 7, free').click();
    await expect(alice.getByText('Row 3, seat 7', { exact: true })).toBeVisible();
    await alice.getByRole('button', { name: 'Teal' }).click();
    await alice.getByRole('button', { name: '🦊' }).click();
    await alice.getByLabel('Why this seat, this colour and this emoji?').fill('ok');
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();
    await expect(alice.getByText('A few more words, please: at least 3 characters.')).toBeVisible();
    if (SHOTS) await alice.screenshot({ path: `${SHOTS}/claim.png`, fullPage: true });
    await alice.getByLabel('Why this seat, this colour and this emoji?').fill('  Near the door, foxes are clever ');
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();

    await alice.waitForURL(`${APP}/seat/mine?claimed=1`);
    await expect(alice.getByRole('heading', { name: 'Row 3, seat 7' })).toBeVisible();
    await expect(alice.getByText("You're in.")).toBeVisible();
    const claimed = JSON.parse(kube('get', 'seat', 'mission2-3-7', '-o', 'json'));
    expect(claimed.spec).toMatchObject({ seatingPlanRef: { name: 'mission2' }, row: 3, column: 7, displayName: `${run}-alice`, color: '#2ec4b6', emoji: '🦊' });
    expect(claimed.metadata.labels).toMatchObject({ 'voter.configbutler.ai/row': '03', 'voter.configbutler.ai/column': '07' });

    // Bob sees Alice's seat taken, and a seat taken under his finger is let go
    // of with the reason, before he presses anything.
    const bob = await join('bob');
    await bob.waitForURL(`${APP}/seat`);
    await expect(seat(bob, 'Row 3, seat 7, taken')).toBeVisible();
    await seat(bob, 'Row 3, seat 8, free').click();
    kube('create', '-f', '-', { input: JSON.stringify({
      apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'Seat',
      metadata: { name: 'mission2-3-8', namespace: 'voter', labels: { 'voter.configbutler.ai/submitter': `${run}-carol` } },
      spec: { seatingPlanRef: { name: 'mission2' }, row: 3, column: 8, displayName: `${run}-carol`, color: '#e4572e', emoji: '🐙' },
    }) });
    await expect(bob.getByText('Someone just took that seat. Pick one close by.')).toBeVisible();
    await expect(seat(bob, 'Row 3, seat 8, taken')).toBeVisible();

    // Alice changes her emoji: a why again, and nothing else of the seat moves.
    await alice.getByRole('button', { name: 'Change', exact: true }).click();
    await expect(alice.getByText('Pick a new colour or emoji first.')).toBeVisible();
    await alice.getByRole('button', { name: '🐙' }).click();
    await alice.getByRole('button', { name: 'Change', exact: true }).click();
    await expect(alice.getByText('Tell us why. It goes with your choice.')).toBeVisible();
    await alice.getByLabel('Why the change?').fill('Octopus, eight arms for eight questions');
    await alice.getByRole('button', { name: 'Change', exact: true }).click();
    await expect(alice.getByText('Changed.', { exact: true })).toBeVisible();
    expect(JSON.parse(kube('get', 'seat', 'mission2-3-7', '-o', 'json')).spec).toMatchObject({ emoji: '🐙', color: '#2ec4b6', row: 3, column: 7 });
    // On Bob's open map, without a reload.
    await expect(seat(bob, 'Row 3, seat 7, taken')).toHaveText('🐙');
    if (SHOTS) await alice.screenshot({ path: `${SHOTS}/mine.png`, fullPage: true });

    // The room grows in "Git": both phones redraw.
    kube('patch', 'seatingplan', 'mission2', '--type=merge', '-p', '{"spec":{"rows":10,"columns":14}}');
    await expect(seat(bob, 'Row 10, seat 14, free')).toBeVisible();
    await expect(seat(alice, 'Row 10, seat 14, free')).toBeVisible();

    // Offered once: Bob may go to the quizzes without a seat, and Alice, with
    // one, is never sent back.
    await bob.getByRole('link', { name: 'Quizzes' }).click();
    await bob.waitForURL(`${APP}/`);
    await alice.goto(`${APP}/`);
    await expect(alice).toHaveURL(`${APP}/`);
    await alice.goto(`${APP}/seat`);
    await alice.waitForURL(`${APP}/seat/mine`);
  } finally {
    for (const context of contexts) await context.close();
    kube('delete', 'seats', '--all', '--ignore-not-found');
    kube('delete', 'seatingplan', 'mission2', '--ignore-not-found');
  }
});
