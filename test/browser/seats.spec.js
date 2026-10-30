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
const plan = (rows, columns, aisles = []) => JSON.stringify({
  apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'SeatingPlan',
  metadata: { name: 'mission2', namespace: 'voter' },
  spec: { title: 'Mission 2', rows, columns, aisles },
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
  const seat = (page, label) => page.getByLabel(label, { exact: true });

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
    await expect(alice.getByText("No problem if it's not exact, it is what it is now.")).toBeVisible();
    await alice.getByRole('button', { name: 'Teal' }).click();
    await alice.getByRole('button', { name: '🦊' }).click();
    await alice.getByLabel('Why this seat, this colour and this emoji?').fill('ok');
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();
    await expect(alice.getByText('A few more words, please: at least 3 characters.')).toBeVisible();
    if (SHOTS) await alice.screenshot({ path: `${SHOTS}/claim.png`, fullPage: true });
    await alice.getByLabel('Why this seat, this colour and this emoji?').fill('  Near the door, foxes are clever ');
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();

    // Seated, the next stop is coffee.
    await alice.waitForURL(`${APP}/coffee`);
    await alice.goto(`${APP}/seat/mine`);
    await expect(alice.getByRole('heading', { name: 'Row 3, seat 7' })).toBeVisible();
    const claimed = JSON.parse(kube('get', 'seat', 'mission2-3-7', '-o', 'json'));
    // Claimed as the Kubernetes identity, which admission checked against the
    // token, with the chosen name beside it for the map.
    const aliceUser = await alice.evaluate(async () => (await (await fetch('/auth/whoami')).json()).userInfo.username);
    expect(aliceUser).toMatch(/^demo:/);
    expect(claimed.spec).toMatchObject({ seatingPlanRef: { name: 'mission2' }, row: 3, column: 7, claimedBy: { displayName: `${run}-alice`, username: aliceUser }, color: '#2ec4b6', emoji: '🦊' });
    expect(claimed.metadata.labels).toMatchObject({ 'voter.configbutler.ai/row': '03', 'voter.configbutler.ai/column': '07' });
    // And the API server finds it by that identity, as the page does.
    expect(kube('get', 'seats', '--field-selector', `spec.claimedBy.username=${aliceUser}`, '-o', 'name').trim()).toBe('seat.examples.configbutler.ai/mission2-3-7');

    // Bob sees Alice's seat taken, and a seat taken under his finger is let go
    // of with the reason, before he presses anything.
    const bob = await join('bob');
    await bob.waitForURL(`${APP}/seat`);
    await expect(seat(bob, 'Row 3, seat 7, taken')).toBeVisible();
    await seat(bob, 'Row 3, seat 8, free').click();
    kube('create', '-f', '-', { input: JSON.stringify({
      apiVersion: 'examples.configbutler.ai/v1alpha1', kind: 'Seat',
      metadata: { name: 'mission2-3-8', namespace: 'voter' },
      spec: { seatingPlanRef: { name: 'mission2' }, row: 3, column: 8, claimedBy: { displayName: `${run}-carol`, username: `demo:${run}-carol` }, color: '#e4572e', emoji: '🐙' },
    }) });
    await expect(bob.getByText('Someone just took that seat. Pick one close by.')).toBeVisible();
    await expect(seat(bob, 'Row 3, seat 8, taken')).toBeVisible();

    // RBAC lets Bob patch any seat; admission lets him change only his own.
    // Straight at /k8s, as anyone with the page's session could.
    const steal = await bob.evaluate(async () => {
      const s = await (await fetch('/auth/session')).json();
      const res = await fetch('/k8s/apis/examples.configbutler.ai/v1alpha1/namespaces/voter/seats/mission2-3-7', {
        method: 'PATCH',
        headers: { 'content-type': 'application/merge-patch+json', [s.csrfHeader]: s.csrfToken },
        body: JSON.stringify({ spec: { emoji: '🔥' } }),
      });
      return { status: res.status, message: (await res.json()).message };
    });
    expect(steal.status).toBe(403);
    expect(steal.message).toContain("This is someone else's seat.");
    expect(JSON.parse(kube('get', 'seat', 'mission2-3-7', '-o', 'json')).spec.emoji).toBe('🦊');

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

    // The room grows in "Git" to Mission 2's two blocks of 8: both phones
    // redraw, with a walkway between the blocks.
    kube('patch', 'seatingplan', 'mission2', '--type=merge', '-p', '{"spec":{"rows":10,"columns":16,"aisles":[8]}}');
    await expect(seat(bob, 'Row 10, seat 16, free')).toBeAttached();
    await expect(seat(alice, 'Row 10, seat 16, free')).toBeAttached();
    await expect(bob.locator('.seat-map__aisle')).toHaveCount(10);
    // Wider than the phone: the map scrolls, the page does not.
    expect(await bob.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await seat(bob, 'Row 10, seat 16, free').scrollIntoViewIfNeeded();
    await expect(seat(bob, 'Row 10, seat 16, free')).toBeInViewport();
    if (SHOTS) await bob.screenshot({ path: `${SHOTS}/hall.png`, fullPage: true });

    // Offered once: Bob may go to the quizzes without a seat, and Alice, with
    // one, is never sent back.
    // Quizzes are not on today's programme: under "Not today", not a tab.
    await expect(bob.getByRole('link', { name: 'Quizzes' })).toBeHidden();
    await bob.getByText('Not today', { exact: true }).click();
    await bob.getByRole('link', { name: 'Quizzes' }).click();
    await bob.waitForURL(`${APP}/`);
    // The smallest phones in the room: the bar still fits.
    await bob.setViewportSize({ width: 375, height: 812 });
    expect(await bob.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await alice.goto(`${APP}/`);
    await expect(alice).toHaveURL(`${APP}/`);
    await alice.goto(`${APP}/seat`);
    await alice.waitForURL(`${APP}/seat/mine`);

    // The operator, signed in with GitHub as "Kilgore Trout": a display name no
    // label can carry, which once made the Seat tab a 400. Their seat is found
    // by their username, and they may take one like anybody.
    const operatorContext = await browser.newContext({ ignoreHTTPSErrors: true });
    contexts.push(operatorContext);
    const operator = await operatorContext.newPage();
    operator.on('pageerror', error => { throw error; });
    await operator.goto(`${APP}/login?next=%2Fseat%2Fmine&connector=github`);
    await operator.waitForURL(`${APP}/seat`);
    await expect(operator.getByRole('heading', { name: 'Where are you sitting?' })).toBeVisible();
    await seat(operator, 'Row 1, seat 1, free').click();
    await operator.getByLabel('Why this seat, this colour and this emoji?').fill('Front row, to keep an eye on things');
    await operator.getByRole('button', { name: 'Claim', exact: true }).click();
    await operator.waitForURL(`${APP}/coffee`);
    await operator.goto(`${APP}/seat/mine`);
    await expect(operator.getByRole('heading', { name: 'Row 1, seat 1' })).toBeVisible();
    expect(JSON.parse(kube('get', 'seat', 'mission2-1-1', '-o', 'json')).spec.claimedBy)
      .toEqual({ displayName: 'Kilgore Trout', username: 'github:kilgore@kilgore.trout' });
  } finally {
    for (const context of contexts) await context.close();
    kube('delete', 'seats', '--all', '--ignore-not-found');
    kube('delete', 'seatingplan', 'mission2', '--ignore-not-found');
  }
});

// The two seat switches on /room: each a RoleBinding to its own Role, so the
// map can close to newcomers and freeze the seated separately. The fixture
// starts with both bound; this takes them away and puts them back.
const seatBinding = (role) => JSON.stringify({
  apiVersion: 'rbac.authorization.k8s.io/v1', kind: 'RoleBinding',
  metadata: { name: role, namespace: 'voter' },
  subjects: [{ kind: 'Group', name: 'demo:voter-audience', apiGroup: 'rbac.authorization.k8s.io' }],
  roleRef: { kind: 'Role', name: role, apiGroup: 'rbac.authorization.k8s.io' },
});

test('the operator closes and freezes the seats, and every phone rests its button', async ({ browser }) => {
  const run = `seat-lock-${Date.now()}`;
  const contexts = [];
  kube('apply', '-f', '-', { input: plan(8, 12) });
  const bound = (role) => kube('get', 'rolebinding', role, '--ignore-not-found', '-o', 'name').trim() !== '';

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
  const seat = (page, label) => page.getByLabel(label, { exact: true });

  try {
    const alice = await join('alice');
    await alice.waitForURL(`${APP}/seat`);
    await seat(alice, 'Row 2, seat 2, free').click();
    await alice.getByLabel('Why this seat, this colour and this emoji?').fill('Close to the front');
    await alice.getByRole('button', { name: 'Claim', exact: true }).click();
    await alice.waitForURL(`${APP}/coffee`);
    await alice.goto(`${APP}/seat/mine`);
    await expect(alice.getByRole('heading', { name: 'Row 2, seat 2' })).toBeVisible();

    const operatorContext = await browser.newContext({ ignoreHTTPSErrors: true });
    contexts.push(operatorContext);
    const operator = await operatorContext.newPage();
    operator.on('pageerror', error => { throw error; });
    await operator.goto(`${APP}/login?next=%2Froom&connector=github`);
    await operator.waitForURL(`${APP}/room`);
    const claim = operator.getByRole('checkbox', { name: 'Let the room take a seat' });
    const edit = operator.getByRole('checkbox', { name: 'Let the room edit their seat' });
    await expect(claim).toBeChecked();
    await expect(edit).toBeChecked();
    await expect(edit).toBeEnabled();

    // Frozen: Alice's Change rests, with one quiet line and nothing about who
    // decided. The map is still hers to look at.
    const change = alice.getByRole('button', { name: 'Change', exact: true });
    await expect(change).toBeEnabled();
    await edit.uncheck();
    await expect.poll(() => bound('voter-audience-seat-edit')).toBe(false);
    await expect(alice.getByText("Editing your seat isn't possible at the moment.")).toBeVisible();
    await expect(change).toBeDisabled();
    await expect(seat(alice, 'Row 2, seat 3, free')).toBeVisible();

    // Closed: a newcomer sees the map, and Claim rests.
    await claim.uncheck();
    await expect.poll(() => bound('voter-audience-seat-claim')).toBe(false);
    await expect(operator.getByTestId('seat-switches').getByText('locked', { exact: true })).toBeVisible();
    const bob = await join('bob');
    await bob.waitForURL(`${APP}/seat`);
    await expect(bob.getByText("Taking a seat isn't possible at the moment.")).toBeVisible();
    const claimButton = bob.getByRole('button', { name: 'Claim', exact: true });
    await expect(claimButton).toBeDisabled();
    await seat(bob, 'Row 4, seat 4, free').click();
    await bob.getByLabel('Why this seat, this colour and this emoji?').fill('Middle of the room');

    // Open again: both buttons wake without a reload, and the claim lands.
    await claim.check();
    await edit.check();
    await expect.poll(() => bound('voter-audience-seat-claim') && bound('voter-audience-seat-edit')).toBe(true);
    const claimBinding = JSON.parse(kube('get', 'rolebinding', 'voter-audience-seat-claim', '-o', 'json'));
    expect(claimBinding.subjects).toEqual([{ kind: 'Group', apiGroup: 'rbac.authorization.k8s.io', name: 'demo:voter-audience' }]);
    expect(claimBinding.metadata.labels['voter.configbutler.ai/grant']).toBe('seat-claim');
    await expect(change).toBeEnabled();
    await expect(bob.getByText("Taking a seat isn't possible at the moment.")).toHaveCount(0);
    await claimButton.click();
    await bob.waitForURL(`${APP}/coffee`);
  } finally {
    for (const context of contexts) await context.close();
    kube('apply', '-f', '-', { input: seatBinding('voter-audience-seat-claim') });
    kube('apply', '-f', '-', { input: seatBinding('voter-audience-seat-edit') });
    kube('delete', 'seats', '--all', '--ignore-not-found');
    kube('delete', 'seatingplan', 'mission2', '--ignore-not-found');
  }
});
