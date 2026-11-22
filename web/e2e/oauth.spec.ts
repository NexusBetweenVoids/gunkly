import { createHash, randomBytes } from 'node:crypto';
import { createServer } from 'node:http';
import type { AddressInfo } from 'node:net';
import { request } from '@playwright/test';
import { axe, expect, nextIP, test } from './helpers';

// OAuth sign-in for MCP clients (ADR 0016): off by default; an admin turns it on; a client registers,
// a user approves it on the server-rendered consent page, the app shows under Connected apps and is
// disconnected there. Register and token calls are made as the (non-browser) client would.
test('oauth: admin enables it, a user approves an app, sees it under Connected apps and disconnects it', async ({ world, baseURL }) => {
  const client = await request.newContext({ baseURL, extraHTTPHeaders: { 'X-Forwarded-For': nextIP() } });
  // The client's redirect target: a loopback listener on a free port, like a native app's.
  const cb = createServer((_, res) => res.end('client')).listen(0, '127.0.0.1');
  await new Promise((r) => cb.once('listening', r));
  const hostPort = `127.0.0.1:${(cb.address() as AddressInfo).port}`;
  const redirect = `http://${hostPort}/callback`;
  try {
    // Off by default: every endpoint is a plain 404 and nothing is advertised.
    expect((await client.get('/.well-known/oauth-authorization-server')).status()).toBe(404);
    expect((await client.post('/oauth/register', { data: { redirect_uris: [redirect] } })).status()).toBe(404);
    expect((await client.post('/mcp', { data: {} })).headers()['www-authenticate']).toBe('Bearer');

    // The admin turns it on in Administration.
    const admin = await world.newUser({ admin: true });
    const ap = (await admin.open()).page;
    await ap.goto('/admin');
    const settings = ap.getByRole('region', { name: 'Instance settings' });
    const box = settings.getByLabel('Let apps such as ChatGPT sign in with Gunkly (OAuth)');
    await expect(box).not.toBeChecked();
    await expect(box).toBeEnabled(); // the e2e base URL is http on loopback, which is allowed for development
    await box.check();
    await settings.getByRole('button', { name: 'Save' }).click();
    const confirm = ap.getByRole('dialog', { name: "Confirm it's you" });
    await confirm.waitFor();
    await confirm.getByLabel('Password').fill(admin.password);
    await confirm.getByRole('button', { name: 'Confirm' }).click();
    await expect(ap.getByText('Saved.')).toBeVisible();
    const meta = await client.get('/.well-known/oauth-authorization-server');
    expect(meta.status()).toBe(200);
    expect(await meta.json()).toMatchObject({ issuer: baseURL, code_challenge_methods_supported: ['S256'] });
    expect((await client.post('/mcp', { data: {} })).headers()['www-authenticate']).toContain('resource_metadata=');

    // A user with a household; a client that registers itself with a hostile name.
    const u = await world.newUser();
    const h = await world.household(u);
    const name = 'Test <b>App</b> & "co"';
    const reg = await client.post('/oauth/register', { data: { client_name: name, redirect_uris: [redirect] } });
    expect(reg.status()).toBe(201);
    const clientId = (await reg.json()).client_id as string;
    const verifier = randomBytes(32).toString('base64url');
    const challenge = createHash('sha256').update(verifier).digest('base64url');
    const authorize =
      '/oauth/authorize?' +
      new URLSearchParams({ response_type: 'code', client_id: clientId, redirect_uri: redirect, code_challenge: challenge, code_challenge_method: 'S256', state: 'xyz', resource: `${baseURL}/mcp` });

    // Signed out: the sign-in page, then straight back to the consent page.
    const ctx = await world.newContext();
    const page = await ctx.newPage();
    await page.goto(authorize);
    await expect(page).toHaveURL(/\/signin\?next=%2Foauth%2Fauthorize/);
    await page.getByLabel('Email or username').fill(u.username);
    await page.getByLabel('Password', { exact: true }).fill(u.password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.getByRole('heading', { name: new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} wants to connect`) })).toBeVisible();
    await expect(page.getByText('Unverified app.')).toBeVisible();
    await expect(page.getByText(hostPort)).toBeVisible();
    await expect(page.locator('main b')).toHaveCount(0); // the name is text, never markup
    await expect(page.getByLabel(h.name)).toBeChecked();
    await expect(page.getByLabel('Mark chores done, snooze, skip and pause them')).toBeChecked();
    await expect(page.getByLabel('Create chores')).not.toBeChecked();
    await axe(page, 'oauth consent page');

    // Allow: the browser follows the 303 to the client (the page's CSP names that origin in form-action).
    await page.getByRole('button', { name: 'Allow' }).click();
    await page.waitForURL(`http://${hostPort}/callback?**`);
    const back = new URL(page.url());
    expect(back.searchParams.get('state')).toBe('xyz');
    expect(back.searchParams.get('iss')).toBe(baseURL);
    const code = back.searchParams.get('code')!;

    // The client redeems the code with its PKCE verifier and uses the token on /mcp only.
    const tok = await client.post('/oauth/token', {
      form: { grant_type: 'authorization_code', client_id: clientId, code, redirect_uri: redirect, code_verifier: verifier },
    });
    expect(tok.status()).toBe(200);
    const pair = await tok.json();
    expect(pair.access_token).toMatch(/^gko_/);
    expect(pair.scope).toBe('chores:read chores:complete');
    const auth = { Authorization: `Bearer ${pair.access_token}` };
    const list = await client.post('/mcp', {
      headers: { ...auth, Accept: 'application/json, text/event-stream' },
      data: { jsonrpc: '2.0', id: 1, method: 'tools/list' },
    });
    expect(list.status()).toBe(200);
    expect(JSON.stringify(await list.json())).toContain('complete_chore');
    expect((await client.get('/api/v1/households', { headers: auth })).status()).toBe(401);

    // Connected apps (Account, Integrations) lists it; Disconnect ends it at once.
    const { page: acct } = await u.open();
    await acct.goto('/account');
    const apps = acct.getByRole('region', { name: 'Connected apps' });
    const row = apps.getByRole('listitem').filter({ hasText: name });
    await expect(row).toContainText(h.name);
    await axe(acct, 'connected apps');
    await row.getByRole('button', { name: 'Disconnect' }).click();
    await acct.getByRole('dialog').getByRole('button', { name: 'Disconnect' }).click();
    await expect(acct.getByText('Disconnected.')).toBeVisible();
    await expect(apps.getByText('No apps are connected.')).toBeVisible();
    expect((await client.post('/mcp', { headers: { ...auth, Accept: 'application/json, text/event-stream' }, data: { jsonrpc: '2.0', id: 2, method: 'tools/list' } })).status()).toBe(401);
    const refresh = await client.post('/oauth/token', { form: { grant_type: 'refresh_token', client_id: clientId, refresh_token: pair.refresh_token } });
    expect(refresh.status()).toBe(400);

    // The assistant guide offers the connector options and just shows the address to paste.
    await acct.reload();
    await acct.getByRole('button', { name: 'Connect an assistant' }).click();
    const guide = acct.getByRole('dialog', { name: 'Connect an AI assistant' });
    await guide.getByLabel('ChatGPT').check();
    await expect(guide.getByText(`${baseURL}/mcp`)).toBeVisible();
    await expect(guide.getByText(/You will sign in to Gunkly/)).toBeVisible();
  } finally {
    await world.admin.call('PATCH', '/admin/settings', { oauthEnabled: false }); // leave the shared instance as it was
    await client.dispose();
    cb.close();
  }
});
