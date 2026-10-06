import { test, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
const kubeconfig = resolve('../../.local/kubeconfig');
const kube = (...args) => execFileSync('kubectl', ['--kubeconfig', kubeconfig, '-n', 'voter', ...args], { encoding: 'utf8' });
const APP = 'https://app.voter.test:19443';

// The QR journey, the way a phone takes it, and the way out again.
//
// A scanner hands the QR URL to the browser as a navigation from somewhere
// else -- which is what decides whether Room Pass's SameSite=Lax join cookie
// survives the redirects through the issuer. A URL typed into the address bar,
// or a test driver's own goto, carries even a Strict cookie through, so it
// would pass with the mistake Room Pass warns about (krm-foyer's room-pass.md).
// So the QR link is followed from a page on another origin.
//
// Then logout, which is two programs: krm-foyer's POST /auth/logout ends its
// session, and Room Pass's own sign-out is a form on its /join page.
test('a scanned code signs a participant in, and logout ends both sessions', async ({ browser }) => {
  const displayName = `join-${Date.now()}`;
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  page.on('pageerror', error => { throw error; });
  try {
    const code = JSON.parse(kube('get', 'room', 'demo', '-o', 'json')).status.joinCode.code;
    // about:blank is another origin as far as the cookie is concerned.
    await page.setContent(`<a href="${APP}/join-room?code=${code}">Scan</a>`);
    await page.getByRole('link', { name: 'Scan' }).click();

    // Room Pass's join page, on the application's host, with the code taken
    // from the cookie: there is no code field to fill in.
    await expect(page.getByLabel('Display name')).toBeVisible();
    expect(new URL(page.url()).host).toBe('app.voter.test:19443');
    await expect(page.getByLabel('Room code')).toHaveCount(0);
    // And the code is in no URL past the QR code's own.
    expect(page.url()).not.toContain(code);

    await page.getByLabel('Display name').fill(displayName);
    // The Room asks one question at the door; any answer will do here.
    await page.getByRole('radio', { name: 'Vue' }).check();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.waitForURL(`${APP}/`);
    const session = await page.evaluate(async () => (await fetch('/auth/session')).json());
    expect(session.connector).toBe('room-pass');
    expect(session.displayName).toBe(displayName);
    // The answer's group arrives beside the room's: Dex split Room Pass's header.
    expect(session.groups).toEqual(expect.arrayContaining(['demo:voter-audience', 'demo:frontend-vue']));

    // Sign out from the identity page: krm-foyer's session ends, and the
    // participant lands on Room Pass's page with its own button.
    await page.goto(`${APP}/me`);
    await page.getByRole('button', { name: 'Sign out anyway' }).click();
    await page.waitForURL(`${APP}/join`);
    await expect(page.getByText(`You’re already enrolled as ${displayName}`)).toBeVisible();
    const cookies = (await context.cookies(APP)).map((c) => c.name);
    expect(cookies.some((name) => name.includes('foyer'))).toBe(false);

    // Room Pass's sign-out, which only its own form can post.
    await page.getByRole('button', { name: 'Sign out of this browser' }).click();
    await expect(page.getByLabel('Room code')).toBeVisible();
    expect((await context.cookies(APP)).map((c) => c.name)).not.toContain('__Host-rp-session');
  } finally {
    await context.close();
    const participants = JSON.parse(kube('get', 'participants', '-o', 'json')).items;
    for (const p of participants) if (p.spec.displayName === displayName) kube('delete', 'participant', p.metadata.name);
  }
});
