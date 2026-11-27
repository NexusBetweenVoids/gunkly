import { createHash, randomBytes } from 'node:crypto';
import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import type { AddressInfo } from 'node:net';
import { request } from '@playwright/test';
import { axe, expect, headerItem, nextIP, test, uniq } from './helpers';
import { idpFile } from './global-setup';

// The OAuth consent page confirms with a passkey or the identity provider (ADR 0016, 0018): the page is a
// plain server-rendered form; one same-origin script adds the passkey button. The app (an MCP client)
// is registered and redeemed as in oauth.spec.ts, here only the consent step matters.
async function withClient<T>(
  world: { baseURL: string; admin: { call: (m: string, p: string, d?: unknown) => Promise<unknown> } },
  run: (c: { authorize: string; hostPort: string }) => Promise<T>,
): Promise<T> {
  const client = await request.newContext({ baseURL: world.baseURL, extraHTTPHeaders: { 'X-Forwarded-For': nextIP() } });
  const cb = createServer((_, res) => res.end('client')).listen(0, '127.0.0.1');
  await new Promise((r) => cb.once('listening', r));
  const hostPort = `127.0.0.1:${(cb.address() as AddressInfo).port}`;
  const redirect = `http://${hostPort}/callback`;
  await world.admin.call('PATCH', '/admin/settings', { oauthEnabled: true });
  try {
    const reg = await client.post('/oauth/register', { data: { client_name: 'Consent test app', redirect_uris: [redirect] } });
    expect(reg.status()).toBe(201);
    const verifier = randomBytes(32).toString('base64url');
    const authorize =
      '/oauth/authorize?' +
      new URLSearchParams({
        response_type: 'code',
        client_id: (await reg.json()).client_id as string,
        redirect_uri: redirect,
        code_challenge: createHash('sha256').update(verifier).digest('base64url'),
        code_challenge_method: 'S256',
        state: 'consent-state',
      });
    return await run({ authorize, hostPort });
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { oauthEnabled: false }); // leave the shared instance as it was
    await client.dispose();
    cb.close();
  }
}

test('oauth consent: a passkey confirms the app, no password typed', async ({ world, browserName }) => {
  test.skip(browserName !== 'chromium', 'virtual authenticators need Chromium');
  const u = await world.newUser();
  await world.household(u);
  const { context, page } = await u.open();
  const cdp = await context.newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  });
  // Register a passkey (this step-up is the password's), then sign in with it: a session that has not stepped up.
  await page.goto('/settings/security');
  const section = page.getByRole('region', { name: 'Passkeys' });
  await section.getByRole('button', { name: 'Add', exact: true }).click();
  await section.getByLabel('Name').fill('Consent key');
  await section.getByRole('button', { name: 'Create passkey' }).click();
  const confirm = page.getByRole('dialog', { name: "Confirm it's you" });
  await confirm.getByLabel('Password').fill(u.password);
  await confirm.getByRole('button', { name: 'Confirm' }).click();
  await section.getByRole('button', { name: "I've saved them" }).click();
  await expect(section.getByText('Consent key')).toBeVisible();
  await (await headerItem(page, 'button', 'Sign out')).click();
  await page.goto('/signin');
  await page.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await expect(page).not.toHaveURL(/signin/);

  await withClient(world, async ({ authorize, hostPort }) => {
    await page.goto(authorize);
    await expect(page.getByRole('heading', { name: /Consent test app wants to connect/ })).toBeVisible();
    const passkey = page.getByRole('button', { name: 'Confirm with a passkey' });
    await expect(passkey).toBeVisible(); // the script revealed it
    await axe(page, 'oauth consent page with a passkey');
    await passkey.click();
    await page.waitForURL(`http://${hostPort}/callback?**`);
    const back = new URL(page.url());
    expect(back.searchParams.get('state')).toBe('consent-state');
    expect(back.searchParams.get('code')).toBeTruthy();
  });
});

test('oauth consent: an account without a password confirms through the identity provider', async ({ world }) => {
  const idp = readFileSync(idpFile, 'utf8');
  const sub = uniq('consent-sub-');
  const username = uniq('ssoc');
  const asProviderUser = async (): Promise<void> => {
    const ctx = await request.newContext();
    expect((await ctx.post(`${idp}/_next`, { data: { sub, preferred_username: username, name: 'Sunny Sso' } })).ok()).toBe(true);
    await ctx.dispose();
  };
  await world.admin.call('PATCH', '/admin/settings', { ssoCreateAccounts: true });
  try {
    const ctx = await world.newContext();
    const page = await ctx.newPage();
    await asProviderUser();
    await page.goto('/signin');
    await page.getByRole('link', { name: 'Sign in with Test IdP' }).click();
    await expect(page.getByRole('heading', { name: 'Sign in' })).toHaveCount(0);
    const csrf = (await ctx.cookies()).find((c) => c.name === 'gunkly_csrf')?.value ?? '';
    const made = await ctx.request.post('/api/v1/households', { data: { name: uniq('Home '), timezone: 'Europe/Berlin' }, headers: { 'X-CSRF-Token': decodeURIComponent(csrf) } });
    expect(made.status()).toBe(201);

    await withClient(world, async ({ authorize, hostPort }) => {
      await page.goto(authorize);
      await expect(page.getByLabel('Password', { exact: true })).toHaveCount(0); // there is none to type
      const sso = page.getByRole('button', { name: 'Confirm with Test IdP' });
      await expect(sso).toBeVisible();
      await axe(page, 'oauth consent page with single sign-on');
      await asProviderUser();
      await sso.click(); // the provider re-authenticates the user and returns to the same consent request
      await expect(page.getByText('Confirmed.')).toBeVisible();
      await expect(page).toHaveURL(/\/oauth\/authorize\?.*client_id=/);
      await expect(page.getByRole('button', { name: 'Confirm with Test IdP' })).toHaveCount(0);
      await page.getByRole('button', { name: 'Allow' }).click();
      await page.waitForURL(`http://${hostPort}/callback?**`);
      expect(new URL(page.url()).searchParams.get('state')).toBe('consent-state');
    });
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { ssoCreateAccounts: false });
  }
});
